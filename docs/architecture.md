# JetQueue Architecture

JetQueue is a production-style distributed job queue built with Go, NATS JetStream, gRPC, SQLite, and Prometheus.

Its design focuses on reliable background execution with:

- durable enqueue semantics
- at-least-once delivery
- idempotent worker processing
- retry scheduling with exponential backoff
- dead-letter routing on exhaustion
- observable job lifecycle state

---

## High-Level Goals

JetQueue is designed to provide the core guarantees expected from a real background job platform:

1. **Crash-safe enqueue**
   - a submitted job is durably recorded before it is published for execution

2. **At-least-once execution**
   - jobs may be redelivered, but should not be silently lost

3. **Duplicate-safe processing**
   - worker-side idempotency prevents duplicate execution from causing duplicate effects

4. **Retry safety**
   - transient failures are retried with backoff

5. **Failure isolation**
   - permanently failing jobs are routed to a DLQ for inspection or replay

6. **Operational visibility**
   - Prometheus metrics and persisted state expose system behavior

---

## Component Overview

JetQueue consists of the following major components:

### 1. gRPC API
The API accepts job submissions and exposes control-plane reads such as job lookup and listing.

Responsibilities:

- validate job submission requests
- enforce idempotency key behavior
- persist job metadata
- write durable outbox records
- expose control-plane job state

### 2. SQLite Control Plane
SQLite stores durable system state.

Core tables:

- `jobs`
- `idempotency_keys`
- `outbox`
- `processed_messages`

Responsibilities:

- durable job metadata
- submission deduplication
- transactional outbox storage
- processed-message tracking for idempotent worker behavior
- control-plane state visibility

### 3. Outbox Publisher
A publisher loop scans unsent outbox rows and publishes them to JetStream.

Responsibilities:

- decouple durable enqueue from broker publication
- guarantee replay after crash/restart
- prevent job loss between DB commit and broker publish

### 4. JetStream Broker
JetStream provides durable message delivery from the control plane to workers.

Responsibilities:

- durable queue transport
- pull-based worker consumption
- redelivery if a job is not acknowledged

### 5. Worker
Workers pull messages from JetStream and execute job handlers.

Responsibilities:

- bounded concurrent execution
- lifecycle state transitions
- retry classification
- retry scheduling with `NakWithDelay`
- DLQ routing on exhaustion
- idempotent duplicate suppression

### 6. Prometheus Metrics
Prometheus metrics expose system health and behavior.

Responsibilities:

- submission / start / success / failure counters
- retry and DLQ counters
- execution duration histogram
- in-flight execution tracking

---

## System Flow

The core execution path is:

1. client submits job via gRPC
2. API writes:
   - `jobs` row
   - `idempotency_keys` row (if applicable)
   - `outbox` row
3. transaction commits
4. outbox publisher reads unsent records
5. message is published to JetStream
6. worker fetches message
7. worker marks job `running`
8. handler executes
9. worker either:
   - marks job `succeeded`
   - schedules retry with backoff
   - routes job to DLQ
10. message is acknowledged after successful handling of delivery semantics

---

## Architecture Diagram

```text
                   +----------------------+
                   |       Client         |
                   +----------+-----------+
                              |
                              | SubmitJob / GetJob / ListJobs
                              v
                   +----------------------+
                   |      gRPC API        |
                   +----------+-----------+
                              |
                              | SQLite transaction
                              v
          +-----------------------------------------------+
          |                 SQLite Control Plane           |
          |-----------------------------------------------|
          | jobs | idempotency_keys | outbox | processed  |
          +-------------------+---------------------------+
                              |
                              | poll unsent outbox rows
                              v
                   +----------------------+
                   |   Outbox Publisher   |
                   +----------+-----------+
                              |
                              | publish
                              v
                   +----------------------+
                   |   NATS JetStream     |
                   +----------+-----------+
                              |
                              | pull / redelivery
                              v
                   +----------------------+
                   |       Worker         |
                   +----------+-----------+
                              |
             +----------------+----------------+
             |                                 |
             | success                         | failure
             v                                 v
   +----------------------+         +----------------------+
   | mark succeeded       |         | classify error       |
   | ack message          |         | retry or DLQ         |
   +----------------------+         +----------------------+
             |                                 |
             |                                 |
             v                                 v
   +----------------------+         +----------------------+
   | SQLite job state     |         | retry_scheduled /    |
   | persisted            |         | dlq state persisted  |
   +----------------------+         +----------------------+