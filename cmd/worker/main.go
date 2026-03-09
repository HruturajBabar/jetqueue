package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
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

	if _, err := strconv.Atoi(cfg.WorkerConc); err != nil {
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
	_ = metrics.New(reg)
	go serveMetrics("worker", cfg.MetricsAddr, reg)

	log.Printf("worker: starting consumer loop")
	if err := runWorker(context.Background(), st, q); err != nil {
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

func runWorker(ctx context.Context, st *store.Store, q *queue.Client) error {
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

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Keep the worker running to process messages.
		}
		msgs, err := sub.Fetch(1, nats.MaxWait(2*time.Second))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue // No messages, loop again
			}
			return err
		}
		for _, msg := range msgs {
			if err := handleMessage(ctx, st, msg); err != nil {
				log.Printf("worker: handle message error: %v", err)
			}
		}
	}
}

func handleMessage(ctx context.Context, st *store.Store, msg *nats.Msg) error {
	var job types.JobMsg
	if err := json.Unmarshal(msg.Data, &job); err != nil {
		log.Printf("worker: invalid message JSON: %v", err)

		// ACK bad messages so they don't poison the queue
		if err := msg.Ack(); err != nil {
			return err
		}
		return nil
	}

	log.Printf("worker: received job id=%s queue=%s type=%s", job.JobID, job.Queue, job.Type)

	meta, err := msg.Metadata()
	if err != nil {
		return err
	}

	attempt := int(meta.NumDelivered) - 1
	if attempt < 0 {
		attempt = 0
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

	if err := st.MarkJobRunning(ctx, job.JobID, attempt); err != nil {
		return err
	}

	if err := executeJob(ctx, job); err != nil {
		nextAttempt := attempt + 1
		if nextAttempt < job.MaxAttempts {
			delay := backoff.Compute(attempt)

			if ferr := st.MarkJobRetryScheduled(ctx, job.JobID, nextAttempt, err.Error()); ferr != nil {
				return ferr
			}

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

		if ferr := st.MarkJobFailed(ctx, job.JobID, nextAttempt, err.Error()); ferr != nil {
			return ferr
		}

		log.Printf(
			"worker: job failed permanently id=%s type=%s attempt=%d max_attempts=%d error=%v",
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

	if err := st.MarkMessageProcessed(ctx, job.JobID); err != nil {
		return err
	}

	log.Printf("worker: completed job id=%s type=%s", job.JobID, job.Type)

	if err := msg.Ack(); err != nil {
		return err
	}
	return nil
}

func executeJob(ctx context.Context, job types.JobMsg) error {
	switch job.Type {
	case "echo":
		var payload types.EchoPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return err
		}
		log.Printf("worker: echo job id=%s message=%q", job.JobID, payload.Message)
		return nil
	case "sleep":
		var payload types.SleepPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return err
		}

		log.Printf("worker: sleep job id=%s duration_ms=%d", job.JobID, payload.DurationMS)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(payload.DurationMS) * time.Millisecond):
			return nil
		}
	default:
		return errors.New("unknown job type: " + job.Type)
	}
}
