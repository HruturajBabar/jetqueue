# JetQueue Architecture

## What this repo is
JetQueue is a small, production-ish distributed job queue you can run locally with Docker Compose:
- API (gRPC): accepts jobs, stores metadata, enqueues work
- Worker: consumes jobs, processes them concurrently, updates status
- NATS JetStream: durable message broker (queue)
- SQLite: state store (jobs + idempotency + outbox/inbox patterns)
- Prometheus: metrics scraping

The goal is **reliability and observability** in a small footprint.

---

## Big picture data flow

Client -> (gRPC) -> API
  1) Validate request
  2) Transactionally write:
     - jobs row
     - idempotency row (if provided)
     - outbox row (message to publish)
  3) Background outbox publisher publishes to JetStream
  4) Worker consumes message
  5) Worker updates job status and acks message

Why we do it this way:
- Publishing to a broker and writing to a DB are two separate systems.
- If you do them separately, you can lose jobs in crash windows.
- Outbox makes it crash-safe.

---

## Components

### API service (cmd/api)
Responsibilities:
- gRPC interface for SubmitJob / GetJob / ListJobs
- writes job metadata to SQLite
- emits metrics (/metrics)
- publishes jobs to NATS JetStream (via outbox drain)

Key concept: **Outbox pattern**
- We insert the "message to publish" into SQLite in the same transaction as the job row.
- A background goroutine reads unsent outbox rows and publishes to JetStream.
- If the API crashes after DB commit, the outbox row is still there and will be published on restart.

This is one of the most valuable “real system” patterns in interviews.

### Worker service (cmd/worker)
Responsibilities:
- connects to JetStream consumer
- processes jobs concurrently (worker pool)
- updates status in SQLite
- emits metrics (/metrics)
- retries + DLQ (later phases)

Key concept: **At-least-once delivery**
JetStream consumers can provide at-least-once semantics (messages may redeliver),
so the worker must be robust to duplicates. We'll handle this with an Inbox/processed table.

---

## NATS vs JetStream vs Core NATS (what we are using)

Core NATS is lightweight pub-sub (messages are not stored long-term).
JetStream is NATS' built-in persistence engine: messages can be stored and replayed later. 
This is what makes it viable as a durable job queue.

JetStream basics:
- Stream = durable message log/storage
- Consumer = stateful cursor/view tracking delivery + acknowledgements
- Ack policy = how messages are marked processed

JetStream supports storing/replaying messages. :contentReference[oaicite:3]{index=3}  
Consumers can provide at-least-once delivery guarantees. :contentReference[oaicite:4]{index=4}  
Streams are “message stores” that capture messages published to matching subjects. :contentReference[oaicite:5]{index=5}

---

## SQLite in this repo

SQLite is used as the job state store because:
- zero infra (single file)
- easy to run locally
- good enough for MVP

We’ll run SQLite in WAL mode for better concurrency (readers and writers don’t block each other as much). WAL is the official SQLite write-ahead log mode. :contentReference[oaicite:6]{index=6}

Tables you’ll see:
- jobs: job metadata + status
- idempotency_keys: request dedupe key -> job_id
- outbox: messages that still need to be published
- inbox (or processed): message/job ids already processed by worker (dedupe)

---

## Prometheus in this repo

Prometheus collects metrics by **scraping** HTTP endpoints (pull model). :contentReference[oaicite:7]{index=7}  
Both API and Worker expose `/metrics`, and Prometheus polls them on an interval.
This gives you:
- counts of submitted/processed/failed
- latency histograms for processing time
- visibility into queue throughput

---

## Reliability guarantees (what we claim)

### API side
- "SubmitJob" is durable once the SQLite transaction commits.
- Outbox ensures the publish eventually happens after restart (no job gets stuck because of API crash at the wrong time).

### Queue side
- JetStream provides durable storage and replay.
- Consumer ack gives at-least-once processing semantics.

### Worker side
- Worker must be idempotent due to potential redelivery (we will enforce this with an Inbox/processed table).

---

## What’s intentionally NOT guaranteed (yet)
- Exactly-once processing: we do at-least-once + idempotency.
- Scheduling/delays: retries/DLQ come later.
- Multi-node HA: local compose first; design keeps options open.