# Tech Stack: what each tool does and why it's here

## gRPC
Why:
- Strong contracts via .proto
- Efficient binary protocol over HTTP/2
- Great for demonstrating "real backend API" beyond REST

Tradeoff:
- More tooling (protoc) than REST
- Debugging requires grpcurl or a client

---

## NATS JetStream (queue)
What it gives us:
- Pub/sub messaging
- JetStream adds persistence: messages stored + replayed later. :contentReference[oaicite:8]{index=8}
- Consumers can provide at-least-once delivery. :contentReference[oaicite:9]{index=9}

Why it’s good for this repo:
- Lightweight to run locally
- Operationally simpler than Kafka for a small project
- Still “serious enough” to talk about streams/consumers/acks

Comparisons:
- Kafka: amazing for high-throughput event streaming, but heavier ops + local setup for a 7-day repo.
- RabbitMQ: great broker, strong routing, but different model; JetStream gives a durable log + consumer state in one place.
- Redis queues: fast, but durability/ack/replay semantics require careful engineering.
- SQS: great managed queue, but not free/offline and harder to demo locally.

---

## SQLite (state store)
What it gives:
- A relational DB in a single file
- Perfect for local demo

Why it’s here:
- We need a source of truth for job status (queued/running/succeeded/failed)
- We need idempotency keys
- We need the outbox/inbox patterns

Comparisons:
- Postgres: better concurrency and production choice, but more setup (we can swap later).
- Redis: not a great source of truth for long-lived job history.
- MySQL: fine, but Postgres is usually the go-to for this use case; SQLite keeps MVP tiny.

SQLite + WAL:
- WAL mode improves concurrency characteristics and is the standard SQLite journaling approach for this style of workload. :contentReference[oaicite:10]{index=10}

---

## Prometheus (observability)
What it gives:
- Pull-based scraping of /metrics endpoints. :contentReference[oaicite:11]{index=11}
- Time-series storage + query UI (localhost:9090)

Why it’s here:
- Interview signal: you can measure throughput, failures, latency
- Demo signal: "targets UP" page + counters moving is instant credibility

Comparisons:
- Datadog/New Relic: great but paid/hosted.
- CloudWatch: AWS-only.
- OpenTelemetry: awesome, we can add later; Prometheus is the quickest win.

---

## Docker Compose
Why:
- One command brings up NATS + Prometheus + API + Worker
- Reproducible demo on any laptop