package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hruturajbabar/jetqueue/internal/backoff"
	"github.com/hruturajbabar/jetqueue/internal/config"
	"github.com/hruturajbabar/jetqueue/internal/metrics"
	"github.com/hruturajbabar/jetqueue/internal/queue"
	"github.com/hruturajbabar/jetqueue/internal/store"
	"github.com/hruturajbabar/jetqueue/internal/types"
)

func main() {
	cfg := config.Load()

	workerConc, err := strconv.Atoi(cfg.WorkerConc)
	if err != nil || workerConc <= 0 {
		log.Fatalf("bad JETQUEUE_WORKER_CONCURRENCY=%q: %v", cfg.WorkerConc, err)
	}

	st, err := store.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatal(err)
	}

	q, err := queue.Connect(cfg.NatsURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := q.EnsureStream(); err != nil {
		log.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	met := metrics.New(reg)
	go serveMetrics("worker", cfg.MetricsAddr, reg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("worker: starting consumer loop")
	if err := runWorker(ctx, st, q, met, workerConc); err != nil {
		log.Fatal(err)
	}
}

func serveMetrics(name, addr string, reg *prometheus.Registry) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	log.Printf("%s: metrics on %s/metrics", name, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func runWorker(ctx context.Context, st *store.Store, q *queue.Client, met *metrics.Metrics, workerConc int) error {
	consumerName := "jetqueue-worker"
	sub, err := q.JS.PullSubscribe(
		"jobs.*",
		consumerName,
		nats.BindStream(queue.StreamName),
		nats.ManualAck(),
	)
	if err != nil {
		return err
	}

	sem := make(chan struct{}, workerConc)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			log.Printf("worker: shutdown signal received, draining in-flight jobs")
			wg.Wait()
			log.Printf("worker: drain complete, exiting")
			return nil
		case sem <- struct{}{}:
			// reserved one execution slot
		}
		msgs, err := sub.Fetch(1, nats.MaxWait(2*time.Second))
		if err != nil {
			<-sem // release reserved slot
			if errors.Is(err, nats.ErrTimeout) {
				continue // No messages, loop again
			}
			return err
		}
		for _, msg := range msgs {
			wg.Add(1)
			go func(m *nats.Msg) {
				defer wg.Done()
				defer func() { <-sem }()

				if err := handleMessage(ctx, st, q, natsWorkerMessage{msg: m}, met); err != nil {
					log.Printf("worker: handle message error: %v", err)
				}
			}(msg)
		}
	}
}

type natsWorkerMessage struct {
	msg *nats.Msg
}

func handleMessage(ctx context.Context, st *store.Store, q dlqPublisher, msg workerMessage, met *metrics.Metrics) error {
	if msg.SubjectName() == queue.DLQSubject {
		log.Printf("worker: skipping dlq message subject=%s", msg.SubjectName())
		if err := msg.Ack(); err != nil {
			return err
		}
		return nil
	}

	var job types.JobMsg
	if err := json.Unmarshal(msg.DataBytes(), &job); err != nil {
		log.Printf("worker: invalid message JSON: %v", err)

		// ACK bad messages so they don't poison the queue
		if err := msg.Ack(); err != nil {
			return err
		}
		return nil
	}

	log.Printf("worker: received job id=%s queue=%s type=%s", job.JobID, job.Queue, job.Type)

	attempt, err := msg.DeliveryAttempt()
	if err != nil {
		return err
	}

	processed, err := st.HasProcessedMessage(ctx, job.JobID)
	if err != nil {
		return err
	}
	if processed {
		log.Printf("worker: duplicate job %s, skipping", job.JobID)
		if err := msg.Ack(); err != nil {
			return err
		}
		return nil
	}

	labels := []string{job.Queue, job.Type}
	start := time.Now()

	if err := st.MarkJobRunning(ctx, job.JobID, attempt); err != nil {
		return err
	}
	met.Started.WithLabelValues(labels...).Inc()
	met.Inflight.WithLabelValues(labels...).Inc()

	defer func() {
		met.Duration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
	}()
	defer met.Inflight.WithLabelValues(labels...).Dec()

	if err := executeJob(ctx, job); err != nil {
		var te terminalError
		nextAttempt := attempt + 1
		if !errors.As(err, &te) {
			if nextAttempt < job.MaxAttempts {
				delay := backoff.Compute(attempt)

				if ferr := st.MarkJobRetryScheduled(ctx, job.JobID, nextAttempt, err.Error()); ferr != nil {
					return ferr
				}
				met.Retried.WithLabelValues(labels...).Inc()

				log.Printf(
					"worker: retrying job id=%s type=%s attempt=%d max_attempts=%d delay=%s error=%v",
					job.JobID,
					job.Type,
					nextAttempt,
					job.MaxAttempts,
					delay,
					err,
				)

				if err := msg.NakWithDelay(delay); err != nil {
					return err
				}

				return nil
			}
		}

		if ferr := st.MarkJobFailed(ctx, job.JobID, nextAttempt, err.Error()); ferr != nil {
			return ferr
		}
		met.Failed.WithLabelValues(labels...).Inc()

		dlqMsg := types.DLQMsg{
			JobID:           job.JobID,
			Queue:           job.Queue,
			Type:            job.Type,
			PayloadJSON:     job.PayloadJSON,
			Attempt:         nextAttempt,
			MaxAttempts:     job.MaxAttempts,
			FailedAtUnix:    time.Now().Unix(),
			LastError:       err.Error(),
			OriginalSubject: msg.SubjectName(),
		}

		if derr := q.PublishDLQ(ctx, dlqMsg); derr != nil {
			return derr
		}
		met.DLQ.WithLabelValues(labels...).Inc()

		log.Printf(
			"worker: job sent to DLQ id=%s type=%s attempt=%d max_attempts=%d error=%v",
			job.JobID,
			job.Type,
			nextAttempt,
			job.MaxAttempts,
			err,
		)

		if err := msg.Term(); err != nil {
			return err
		}

		return nil
	}

	if err := st.MarkJobSucceeded(ctx, job.JobID, attempt); err != nil {
		return err
	}
	met.Succeeded.WithLabelValues(labels...).Inc()

	if err := st.MarkMessageProcessed(ctx, job.JobID); err != nil {
		return err
	}

	log.Printf("worker: completed job id=%s type=%s", job.JobID, job.Type)

	if err := msg.Ack(); err != nil {
		return err
	}
	return nil
}

type workerMessage interface {
	SubjectName() string
	DataBytes() []byte
	DeliveryAttempt() (int, error)
	Ack() error
	NakWithDelay(time.Duration) error
	Term() error
}

func (m natsWorkerMessage) Ack() error {
	return m.msg.Ack()
}

func (m natsWorkerMessage) NakWithDelay(d time.Duration) error {
	return m.msg.NakWithDelay(d)
}

func (m natsWorkerMessage) Term() error {
	return m.msg.Term()
}

func (m natsWorkerMessage) SubjectName() string {
	return m.msg.Subject
}

func (m natsWorkerMessage) DataBytes() []byte {
	return m.msg.Data
}

func (m natsWorkerMessage) DeliveryAttempt() (int, error) {
	meta, err := m.msg.Metadata()
	if err != nil {
		return 0, err
	}

	attempt := int(meta.NumDelivered) - 1
	if attempt < 0 {
		attempt = 0
	}
	return attempt, nil
}

type dlqPublisher interface {
	PublishDLQ(context.Context, types.DLQMsg) error
}

func executeJob(ctx context.Context, job types.JobMsg) error {
	switch job.Type {
	case "echo":
		var payload types.EchoPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return terminalError{err: err}
		}
		log.Printf("worker: echo job id=%s message=%q", job.JobID, payload.Message)
		return nil
	case "sleep":
		var payload types.SleepPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return terminalError{err: err}
		}

		log.Printf("worker: sleep job id=%s duration_ms=%d", job.JobID, payload.DurationMS)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(payload.DurationMS) * time.Millisecond):
			return nil
		}
	case "webhook":
		var payload types.WebhookPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return terminalError{err: err}
		}
		if payload.URL == "" {
			return terminalError{err: ErrBadInput}
		}
		method := payload.Method
		if method == "" {
			method = "POST"
		}
		timeoutMS := payload.TimeoutMS
		if timeoutMS <= 0 {
			timeoutMS = 5000
		}

		ctx, cancel := context.WithTimeout(ctx, time.Millisecond*time.Duration(timeoutMS))
		defer cancel()

		var req *http.Request
		var err error
		if payload.Body == "" {
			req, err = http.NewRequestWithContext(ctx, method, payload.URL, nil)
		} else {
			req, err = http.NewRequestWithContext(ctx, method, payload.URL, strings.NewReader(payload.Body))
		}
		if err != nil {
			return terminalError{err: err}
		}

		if payload.Headers != nil {
			for key, val := range payload.Headers {
				req.Header.Set(key, val)
			}
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			return fmt.Errorf("webhook returned retryable status: %d", resp.StatusCode)
		}
		if resp.StatusCode >= 400 {
			switch resp.StatusCode {
			case 429:
				return fmt.Errorf("webhook returned retryable status: %d", resp.StatusCode)
			case 408:
				return fmt.Errorf("webhook returned retryable status: %d", resp.StatusCode)
			case 425:
				return fmt.Errorf("webhook returned retryable status: %d", resp.StatusCode)
			default:
				return terminalError{err: fmt.Errorf("webhook returned status: %d", resp.StatusCode)}
			}
		}
		return nil

	default:
		return terminalError{err: fmt.Errorf("unknown job type: %s", job.Type)}
	}
}

type terminalError struct {
	err error
}

var (
	ErrBadInput = errors.New("bad input")
)

func (e terminalError) Error() string {
	return e.err.Error()
}
