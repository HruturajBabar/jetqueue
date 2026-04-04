# Latency Benchmarks

This document summarizes end-to-end latency measurements for JetQueue under a controlled workload.

The goal of this benchmark is to measure the time from successful job submission to completed visibility in the control plane, and to characterize the latency envelope introduced by durable queue orchestration.

---

## Benchmark Goals

This benchmark answers the following question:

How long does a job take to move through the full JetQueue pipeline under a realistic workload?

That includes:

- gRPC submission
- SQLite persistence
- transactional outbox write
- outbox publisher delay
- JetStream publish and delivery
- worker scheduling
- handler execution
- final state persistence
- control-plane visibility through polling

---

## Test Configuration

Workload characteristics:

- Job type: `sleep`
- Sleep duration: `50 ms`
- Total jobs: `200`
- Submit parallelism: `20`

This workload was chosen because it introduces stable handler execution cost while still allowing the queue orchestration path to dominate a meaningful share of total latency.

---

## Measured Latency Summary

| Metric | Value |
|--------|-------|
| Count  | 200   |
| Min    | 224 ms |
| P50    | 605 ms |
| P95    | 1058 ms |
| P99    | 1442 ms |
| Max    | 1460 ms |
| Avg    | 641.61 ms |

---

## Interpretation

These numbers represent end-to-end queue latency rather than raw handler runtime.

Although each job only executes `50 ms` of application logic, total latency is higher because the system performs several durable coordination steps before and after execution.

### Latency Contributors

The measured latency envelope includes:

1. **Submission RPC**
   - gRPC request handling
   - request validation
   - enqueue path processing

2. **Durable control-plane persistence**
   - job row insertion
   - idempotency key handling
   - transactional outbox write
   - SQLite commit cost

3. **Outbox publication delay**
   - polling interval before unsent outbox rows are published
   - JetStream publish path

4. **Broker scheduling**
   - stream delivery
   - worker fetch timing
   - consumer dispatch

5. **Worker execution**
   - job state transition to `running`
   - handler execution (`50 ms sleep`)
   - final state persistence

6. **Completion visibility**
   - polling interval used by the benchmark harness
   - timing of final observation from the control plane

---

## Percentile Analysis

### P50: 605 ms

The median job completes in a little over half a second. This is reasonable for a durable background job system where enqueue safety and observable state transitions are part of the design.

### P95: 1058 ms

Most jobs complete within about one second. This suggests the system remains stable under the tested load and does not exhibit large long-tail blowups for the benchmark conditions.

### P99: 1442 ms

The tail is present but controlled. Jobs at the high end are still completing within roughly 1.4 seconds, which is consistent with queueing delay, polling cadence, and orchestration overhead rather than pathological stalls.

### Min: 224 ms

The minimum observed latency reflects the lower bound of the pipeline when publication, delivery, execution, and visibility align favorably.

---

## Why Latency Is Much Higher Than Handler Runtime

The handler itself only takes `50 ms`, but JetQueue is intentionally not a "direct execution" system. It is a durable, observable queue architecture.

That means each job pays for:

- durable enqueue semantics
- asynchronous outbox publication
- broker-mediated delivery
- worker-side state transitions
- visibility through persisted control-plane state

This is expected behavior for a production-style queueing system. The latency numbers should therefore be interpreted as full orchestration latency, not just business-logic execution time.

---

## What These Results Show

### 1. Latency is consistent with the architecture
The measured values match the expected behavior of a system that prioritizes durability and delivery guarantees over minimal single-job latency.

### 2. The tail is bounded
The difference between P95 and P99 is noticeable but not extreme, suggesting reasonably stable performance under the tested workload.

### 3. Observability and safety introduce real but understandable cost
Transactional persistence, outbox publishing, and idempotent lifecycle tracking add overhead, but they also provide reliability properties that lightweight in-memory queues do not.

---

## Summary

JetQueue's measured end-to-end latency under the benchmark workload was:

- `P50: 605 ms`
- `P95: 1058 ms`
- `P99: 1442 ms`

With a `50 ms` handler workload, these results show the cost of durable orchestration across the full job lifecycle.

The benchmark confirms that JetQueue behaves like a production-style distributed background job system: latency is dominated not only by execution, but by reliable enqueue, broker delivery, state persistence, and completion visibility.