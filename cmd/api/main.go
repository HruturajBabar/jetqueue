package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hruturajbabar/jetqueue/internal/config"
	"github.com/hruturajbabar/jetqueue/internal/metrics"
	"github.com/hruturajbabar/jetqueue/internal/queue"
	"github.com/hruturajbabar/jetqueue/internal/store"
	"github.com/hruturajbabar/jetqueue/internal/types"
	pb "github.com/hruturajbabar/jetqueue/proto"
)

type server struct {
	pb.UnimplementedJetQueueServer
	q   *queue.Client
	st  *store.Store
	met *metrics.Metrics
	cfg config.Config
}

func main() {
	cfg := config.Load()

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
	go serveMetrics("api", cfg.MetricsAddr, reg)

	// Outbox publisher loop (small but huge for correctness)
	go outboxPublisher(cfg, st, q)

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatal(err)
	}

	s := grpc.NewServer()
	pb.RegisterJetQueueServer(s, &server{
		q:   q,
		st:  st,
		met: met,
		cfg: cfg,
	})

	log.Printf("api: grpc listening on %s", cfg.GRPCAddr)
	if err := s.Serve(lis); err != nil {
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

func outboxPublisher(cfg config.Config, st *store.Store, q *queue.Client) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	ctx := context.Background()

	for range ticker.C {
		rows, err := st.FetchUnsentOutbox(ctx, 50)
		if err != nil {
			log.Printf("api: outbox fetch err: %v", err)
			continue
		}
		for _, r := range rows {
			// Publish outbox payload exactly as stored
			subject := r.Topic
			if err := q.Publish(ctx, subject, []byte(r.PayloadJSON)); err != nil {
				log.Printf("api: outbox publish err (id=%d): %v", r.ID, err)
				break
			}
			if err := st.MarkOutboxSent(ctx, r.ID); err != nil {
				log.Printf("api: outbox mark-sent err (id=%d): %v", r.ID, err)
			}
		}
	}
}

// ---- gRPC handlers ----

func (s *server) SubmitJob(ctx context.Context, req *pb.SubmitJobRequest) (*pb.SubmitJobResponse, error) {
	queueName := strings.TrimSpace(req.GetQueue())
	if queueName == "" {
		queueName = s.cfg.DefaultQueue
	}
	jobType := strings.TrimSpace(req.GetType())
	if jobType == "" {
		return nil, status.Error(codes.InvalidArgument, "type is required")
	}
	payload := req.GetPayloadJson()
	if payload == "" {
		payload = "{}"
	}

	maxAttempts := int(req.GetMaxAttempts())
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	jobID := uuid.NewString()
	now := time.Now().Unix()

	msg := types.JobMsg{
		JobID:       jobID,
		Queue:       queueName,
		Type:        jobType,
		PayloadJSON: payload,
		Attempt:     0,
		MaxAttempts: maxAttempts,
		CreatedAt:   now,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal msg: %v", err)
	}

	subject := "jobs." + queueName

	res, err := s.st.CreateJobWithIdempotencyAndOutbox(ctx, store.CreateJobInput{
		JobID:          jobID,
		Queue:          queueName,
		Type:           jobType,
		PayloadJSON:    payload,
		MaxAttempts:    maxAttempts,
		IdempotencyKey: strings.TrimSpace(req.GetIdempotencyKey()),
	}, subject, string(b))
	if err != nil {
		if err == store.ErrBadInput {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "create job: %v", err)
	}

	s.met.Submitted.WithLabelValues(queueName, jobType).Inc()

	return &pb.SubmitJobResponse{
		JobId:   res.JobID,
		Deduped: res.Deduped,
	}, nil
}

func (s *server) GetJob(ctx context.Context, req *pb.GetJobRequest) (*pb.GetJobResponse, error) {
	// Day 5
	return &pb.GetJobResponse{}, nil
}

func (s *server) ListJobs(ctx context.Context, req *pb.ListJobsRequest) (*pb.ListJobsResponse, error) {
	// Day 5
	return &pb.ListJobsResponse{}, nil
}
