package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	pb "github.com/hruturajbabar/jetqueue/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type benchmarkConfig struct {
	addr         string
	queue        string
	jobType      string
	numJobs      int
	parallel     int
	sleepMS      int
	maxAttempts  int
	wait         bool
	pollInterval time.Duration
	pollTimeout  time.Duration
	idemPrefix   string
	echoMessage  string
}

type submitResult struct {
	jobID   string
	deduped bool
	err     error
}

func main() {
	cfg := parseFlags()

	if err := validateConfig(cfg); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	ctx := context.Background()

	conn, err := grpc.NewClient(
		cfg.addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("failed to dial gRPC server: %v", err)
	}
	defer conn.Close()

	client := pb.NewJetQueueClient(conn)

	log.Printf(
		"benchmark: starting submit run addr=%s queue=%s type=%s jobs=%d parallel=%d wait=%v",
		cfg.addr, cfg.queue, cfg.jobType, cfg.numJobs, cfg.parallel, cfg.wait,
	)

	submitStart := time.Now()
	results := submitJobs(ctx, client, cfg)
	submitElapsed := time.Since(submitStart)

	var submitted int
	var deduped int
	var failed int
	jobIDs := make([]string, 0, len(results))

	for _, r := range results {
		if r.err != nil {
			failed++
			continue
		}
		submitted++
		if r.deduped {
			deduped++
		}
		jobIDs = append(jobIDs, r.jobID)
	}

	submitRate := 0.0
	if submitElapsed > 0 {
		submitRate = float64(submitted) / submitElapsed.Seconds()
	}

	fmt.Println("=== Submit Summary ===")
	fmt.Printf("submitted_ok: %d\n", submitted)
	fmt.Printf("submit_failed: %d\n", failed)
	fmt.Printf("deduped: %d\n", deduped)
	fmt.Printf("submit_duration: %s\n", submitElapsed)
	fmt.Printf("submit_rate_jobs_per_sec: %.2f\n", submitRate)

	if !cfg.wait {
		return
	}

	waitStart := time.Now()
	summary, err := waitForTerminalStates(ctx, client, cfg, jobIDs)
	waitElapsed := time.Since(waitStart)
	if err != nil {
		log.Fatalf("wait mode failed: %v", err)
	}

	fmt.Println()
	fmt.Println("=== Completion Summary ===")
	fmt.Printf("tracked_jobs: %d\n", len(jobIDs))
	fmt.Printf("wait_duration: %s\n", waitElapsed)
	fmt.Printf("succeeded: %d\n", summary.succeeded)
	fmt.Printf("failed: %d\n", summary.failed)
	fmt.Printf("dlq: %d\n", summary.dlq)
	fmt.Printf("other_terminal: %d\n", summary.otherTerminal)
	fmt.Printf("timed_out_or_non_terminal: %d\n", summary.pending)
	if waitElapsed > 0 {
		fmt.Printf("completion_rate_jobs_per_sec: %.2f\n", float64(summary.completed())/waitElapsed.Seconds())
	}
}

func parseFlags() benchmarkConfig {
	var cfg benchmarkConfig

	flag.StringVar(&cfg.addr, "addr", "localhost:50051", "JetQueue gRPC address")
	flag.StringVar(&cfg.queue, "queue", "default", "queue name")
	flag.StringVar(&cfg.jobType, "type", "sleep", "job type: echo|sleep")
	flag.IntVar(&cfg.numJobs, "n", 1000, "number of jobs to submit")
	flag.IntVar(&cfg.parallel, "parallel", 20, "number of concurrent submitters")
	flag.IntVar(&cfg.sleepMS, "sleep-ms", 50, "sleep job duration in milliseconds")
	flag.IntVar(&cfg.maxAttempts, "max-attempts", 5, "max attempts per job")
	flag.BoolVar(&cfg.wait, "wait", false, "wait for jobs to reach terminal states")
	flag.DurationVar(&cfg.pollInterval, "poll-interval", 500*time.Millisecond, "poll interval in wait mode")
	flag.DurationVar(&cfg.pollTimeout, "poll-timeout", 2*time.Minute, "max wait duration in wait mode")
	flag.StringVar(&cfg.idemPrefix, "idempotency-prefix", "", "optional idempotency key prefix")
	flag.StringVar(&cfg.echoMessage, "echo-message", "hello from benchmark", "echo job message")

	flag.Parse()
	return cfg
}

func validateConfig(cfg benchmarkConfig) error {
	if strings.TrimSpace(cfg.addr) == "" {
		return fmt.Errorf("addr is required")
	}
	if strings.TrimSpace(cfg.queue) == "" {
		return fmt.Errorf("queue is required")
	}
	if cfg.jobType != "echo" && cfg.jobType != "sleep" {
		return fmt.Errorf("type must be one of: echo, sleep")
	}
	if cfg.numJobs <= 0 {
		return fmt.Errorf("n must be > 0")
	}
	if cfg.parallel <= 0 {
		return fmt.Errorf("parallel must be > 0")
	}
	if cfg.maxAttempts <= 0 {
		return fmt.Errorf("max-attempts must be > 0")
	}
	if cfg.jobType == "sleep" && cfg.sleepMS < 0 {
		return fmt.Errorf("sleep-ms must be >= 0")
	}
	if cfg.wait {
		if cfg.pollInterval <= 0 {
			return fmt.Errorf("poll-interval must be > 0")
		}
		if cfg.pollTimeout <= 0 {
			return fmt.Errorf("poll-timeout must be > 0")
		}
	}
	return nil
}

func submitJobs(ctx context.Context, client pb.JetQueueClient, cfg benchmarkConfig) []submitResult {
	jobs := make(chan int)
	results := make([]submitResult, cfg.numJobs)

	var wg sync.WaitGroup

	for w := 0; w < cfg.parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				req, err := buildSubmitRequest(cfg, i)
				if err != nil {
					results[i] = submitResult{err: err}
					continue
				}

				resp, err := client.SubmitJob(ctx, req)
				if err != nil {
					results[i] = submitResult{err: err}
					continue
				}

				results[i] = submitResult{
					jobID:   resp.GetJobId(),
					deduped: resp.GetDeduped(),
					err:     nil,
				}
			}
		}()
	}

	for i := 0; i < cfg.numJobs; i++ {
		jobs <- i
	}
	close(jobs)

	wg.Wait()
	return results
}

func buildSubmitRequest(cfg benchmarkConfig, index int) (*pb.SubmitJobRequest, error) {
	payload, err := buildPayload(cfg, index)
	if err != nil {
		return nil, err
	}

	req := &pb.SubmitJobRequest{
		Queue:       cfg.queue,
		Type:        cfg.jobType,
		PayloadJson: payload,
		MaxAttempts: int32(cfg.maxAttempts),
	}

	if cfg.idemPrefix != "" {
		req.IdempotencyKey = fmt.Sprintf("%s-%d", cfg.idemPrefix, index)
	}

	return req, nil
}

func buildPayload(cfg benchmarkConfig, index int) (string, error) {
	switch cfg.jobType {
	case "echo":
		body := map[string]any{
			"message": fmt.Sprintf("%s #%d", cfg.echoMessage, index),
		}
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		return string(b), nil

	case "sleep":
		body := map[string]any{
			"duration_ms": cfg.sleepMS,
		}
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		return string(b), nil

	default:
		return "", fmt.Errorf("unsupported job type: %s", cfg.jobType)
	}
}

type completionSummary struct {
	succeeded     int64
	failed        int64
	dlq           int64
	otherTerminal int64
	pending       int64
}

func (s completionSummary) completed() int64 {
	return s.succeeded + s.failed + s.dlq + s.otherTerminal
}

func waitForTerminalStates(
	ctx context.Context,
	client pb.JetQueueClient,
	cfg benchmarkConfig,
	jobIDs []string,
) (completionSummary, error) {
	deadline := time.Now().Add(cfg.pollTimeout)

	type state struct {
		done   bool
		status string
	}

	type pollResult struct {
		jobID  string
		status string
		ok     bool
	}

	states := make(map[string]state, len(jobIDs))
	for _, id := range jobIDs {
		states[id] = state{}
	}

	for time.Now().Before(deadline) {
		remaining := make([]string, 0, len(jobIDs))
		for _, id := range jobIDs {
			if !states[id].done {
				remaining = append(remaining, id)
			}
		}

		if len(remaining) == 0 {
			break
		}

		sem := make(chan struct{}, cfg.parallel)
		resultsCh := make(chan pollResult, len(remaining))
		var wg sync.WaitGroup

		for _, jobID := range remaining {
			wg.Add(1)
			sem <- struct{}{}

			go func(id string) {
				defer wg.Done()
				defer func() { <-sem }()

				resp, err := client.GetJob(ctx, &pb.GetJobRequest{JobId: id})
				if err != nil {
					resultsCh <- pollResult{jobID: id, ok: false}
					return
				}

				job := resp.GetJob()
				if job == nil {
					resultsCh <- pollResult{jobID: id, ok: false}
					return
				}

				status := strings.TrimSpace(job.GetStatus())
				if isTerminalStatus(status) {
					resultsCh <- pollResult{
						jobID:  id,
						status: status,
						ok:     true,
					}
					return
				}

				resultsCh <- pollResult{jobID: id, ok: false}
			}(jobID)
		}

		wg.Wait()
		close(resultsCh)

		for res := range resultsCh {
			if !res.ok {
				continue
			}
			states[res.jobID] = state{
				done:   true,
				status: res.status,
			}
		}

		time.Sleep(cfg.pollInterval)
	}

	var summary completionSummary
	for _, st := range states {
		if !st.done {
			summary.pending++
			continue
		}

		switch st.status {
		case "succeeded":
			summary.succeeded++
		case "failed":
			summary.failed++
		case "dlq":
			summary.dlq++
		default:
			summary.otherTerminal++
		}
	}

	return summary, nil
}

func isTerminalStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "dlq":
		return true
	default:
		return false
	}
}
