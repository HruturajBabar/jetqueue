package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/hruturajbabar/jetqueue/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type benchmarkConfig struct {
	addr          string
	queue         string
	jobType       string
	numJobs       int
	parallel      int
	sleepMS       int
	maxAttempts   int
	wait          bool
	pollInterval  time.Duration
	pollTimeout   time.Duration
	idemPrefix    string
	echoMessage   string
	reportLatency bool
}

type submitResult struct {
	jobID         string
	deduped       bool
	submittedAtMS int64
	err           error
}

func main() {
	cfg := parseFlags()

	if err := validateConfig(cfg); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	ctx := context.Background()

	conn, err := grpc.Dial(
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

	submitTimes := make(map[string]int64, len(results))
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
		submitTimes[r.jobID] = r.submittedAtMS
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
	summary, completedAt, err := waitForTerminalStates(ctx, client, cfg, jobIDs)
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

	if cfg.reportLatency {
		latenciesMS := make([]int64, 0, len(jobIDs))
		for _, jobID := range jobIDs {
			submittedAtMS, ok1 := submitTimes[jobID]
			completedAtMS, ok2 := completedAt[jobID]
			if !ok1 || !ok2 || completedAtMS < submittedAtMS {
				continue
			}
			latenciesMS = append(latenciesMS, completedAtMS-submittedAtMS)
		}

		ls := computeLatencySummary(latenciesMS)

		fmt.Println()
		fmt.Println("=== Latency Summary ===")
		fmt.Printf("count: %d\n", ls.count)
		fmt.Printf("min_ms: %d\n", ls.minMS)
		fmt.Printf("p50_ms: %d\n", ls.p50MS)
		fmt.Printf("p95_ms: %d\n", ls.p95MS)
		fmt.Printf("p99_ms: %d\n", ls.p99MS)
		fmt.Printf("max_ms: %d\n", ls.maxMS)
		fmt.Printf("avg_ms: %.2f\n", ls.avgMS)
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
	flag.BoolVar(&cfg.reportLatency, "report-latency", false, "report latency percentiles in wait mode")

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

				submittedAtMS := time.Now().UnixMilli()
				resp, err := client.SubmitJob(ctx, req)
				if err != nil {
					results[i] = submitResult{err: err}
					continue
				}

				results[i] = submitResult{
					jobID:         resp.GetJobId(),
					deduped:       resp.GetDeduped(),
					submittedAtMS: submittedAtMS,
					err:           nil,
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

type latencySummary struct {
	count int
	minMS int64
	p50MS int64
	p95MS int64
	p99MS int64
	maxMS int64
	avgMS float64
}

func (s completionSummary) completed() int64 {
	return s.succeeded + s.failed + s.dlq + s.otherTerminal
}

func waitForTerminalStates(
	ctx context.Context,
	client pb.JetQueueClient,
	cfg benchmarkConfig,
	jobIDs []string,
) (completionSummary, map[string]int64, error) {
	deadline := time.Now().Add(cfg.pollTimeout)

	type state struct {
		done          bool
		status        string
		completedAtMS int64
	}

	type pollResult struct {
		jobID         string
		status        string
		completedAtMS int64
		ok            bool
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
						jobID:         id,
						status:        status,
						completedAtMS: job.GetUpdatedAtUnixMs(),
						ok:            true,
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
				done:          true,
				status:        res.status,
				completedAtMS: res.completedAtMS,
			}
		}

		time.Sleep(cfg.pollInterval)
	}

	var summary completionSummary
	completedAt := make(map[string]int64, len(jobIDs))

	for jobID, st := range states {
		if !st.done {
			summary.pending++
			continue
		}

		completedAt[jobID] = st.completedAtMS

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

	return summary, completedAt, nil
}

func isTerminalStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "dlq":
		return true
	default:
		return false
	}
}

func computeLatencySummary(latenciesMS []int64) latencySummary {
	if len(latenciesMS) == 0 {
		return latencySummary{}
	}

	sort.Slice(latenciesMS, func(i, j int) bool {
		return latenciesMS[i] < latenciesMS[j]
	})

	var sum int64
	for _, v := range latenciesMS {
		sum += v
	}

	return latencySummary{
		count: len(latenciesMS),
		minMS: latenciesMS[0],
		p50MS: percentile(latenciesMS, 0.50),
		p95MS: percentile(latenciesMS, 0.95),
		p99MS: percentile(latenciesMS, 0.99),
		maxMS: latenciesMS[len(latenciesMS)-1],
		avgMS: float64(sum) / float64(len(latenciesMS)),
	}
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}

	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}
