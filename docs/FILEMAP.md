# File/Folder Map

## cmd/
Entry points (binaries).
- cmd/api/main.go
  - Starts gRPC server
  - Starts metrics HTTP server
  - Opens SQLite
  - Starts outbox publisher loop
- cmd/worker/main.go
  - Connects to JetStream
  - Starts consumer loop + worker pool
  - Starts metrics HTTP server
  - Updates SQLite status

## internal/config/
- config.go
  - Reads env vars (ports, NATS URL, DB path, concurrency)

## internal/queue/
- nats.go
  - NATS/JetStream connection wrapper
  - Publish + consume helpers
  - Stream/consumer setup (if implemented)

## internal/store/
- sqlite.go
  - DB open/init, pragmas (WAL), schema migrations
- jobs.go
  - CRUD for jobs table (status transitions)
- outbox.go
  - Outbox insert + poll + mark-sent
- inbox.go
  - Dedup table for worker (prevent double processing)

## internal/metrics/
- metrics.go
  - Prometheus counters/histograms
  - /metrics handler wiring

## internal/backoff/
- Retry/backoff helpers (used once retries are implemented)

## internal/types/
- Core structs used across API/worker (Job, status enums, etc.)

## proto/
- jetqueue.proto
  - gRPC contract
- jetqueue.pb.go / jetqueue_grpc.pb.go
  - Generated code (DO NOT EDIT)

## deploy/
- docker-compose.yml
  - Local dev stack: nats + prometheus + api + worker
- Dockerfile.api / Dockerfile.worker
  - Build API/worker images
- prometheus.yml
  - Scrape config for api/worker endpoints

## scripts/
- demo scripts (to be added)

## README.md
Recruiter-facing entry point (how to run + 2-min demo + tradeoffs).