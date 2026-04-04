# Throughput Benchmarks

This document summarizes throughput measurements for JetQueue under controlled workloads.

JetQueue is a distributed job queue built with Go, NATS JetStream, gRPC, SQLite, and Prometheus. The goal of these benchmarks is to measure how the worker execution pipeline scales under increasing concurrency, and to identify where throughput bottlenecks shift as job execution cost decreases.

---

## Benchmark Goals

These measurements were designed to answer two questions:

1. How does end-to-end job completion throughput scale with worker concurrency?
2. When execution becomes cheap, does the bottleneck remain in job execution or move into orchestration and control-plane overhead?

---

## Test Environment

Benchmark workload characteristics:

- Job type: `sleep`
- Sleep duration: `50 ms`
- Jobs per run: `200`
- Submit parallelism: `20`

The `sleep` workload is useful because it introduces predictable, uniform execution cost. This makes scaling behavior easier to reason about and compare against the theoretical maximum.

A second workload using `echo` jobs was also tested to isolate orchestration overhead by removing almost all meaningful execution cost.

---

## Sleep Workload Results

### Configuration

- Workload: `sleep`
- Duration per job: `50 ms`
- Total jobs: `200`
- Parallel submitters: `20`

### Measured Completion Throughput

| Worker Concurrency | Completion Throughput (jobs/sec) |
|--------------------|----------------------------------|
| 1                  | 18.15                            |
| 5                  | 83.82                            |
| 10                 | 179.10                           |

---

## Scaling Interpretation

For a `50 ms` job, the theoretical upper bound per worker is approximately:

`1 / 0.05 sec = 20 jobs/sec`

That gives the following idealized ceilings:

| Worker Concurrency | Theoretical Ceiling (jobs/sec) | Measured (jobs/sec) |
|--------------------|--------------------------------|---------------------|
| 1                  | 20                             | 18.15               |
| 5                  | 100                            | 83.82               |
| 10                 | 200                            | 179.10              |

### Observations

- Throughput scales close to linearly as worker concurrency increases.
- Measured throughput remains reasonably close to the theoretical ceiling.
- The gap between ideal and measured throughput is expected due to:
  - enqueue RPC cost
  - SQLite transaction overhead
  - outbox polling delay
  - JetStream delivery latency
  - worker scheduling and state persistence
  - benchmark polling / visibility delay

### Conclusion

Under a fixed-cost workload, JetQueue's execution pipeline scales predictably with worker concurrency. The results show that the worker side of the system is not the limiting factor for moderately expensive jobs, and that the queue behaves like a healthy concurrent execution system rather than collapsing under coordination overhead.

---

## Echo Workload Results

The `echo` workload removes most execution cost and is intended to expose orchestration overhead.

### Measured Completion Throughput

| Workload | Completion Throughput (jobs/sec) |
|----------|----------------------------------|
| echo     | ~250–272                         |

---

## Bottleneck Shift Interpretation

Once execution becomes extremely lightweight, throughput no longer reflects worker execution capacity. Instead, the dominant costs shift into the orchestration path:

- gRPC submission overhead
- SQLite writes for job lifecycle state
- transactional outbox persistence
- outbox publisher polling cadence
- JetStream publish + delivery path
- state transition writes during worker execution
- client-side polling visibility

In other words, the system stops being execution-bound and becomes control-plane-bound.

This is a useful result because it confirms that JetQueue is not limited by handler execution for lightweight jobs. Instead, the observed ceiling reflects the cost of durable orchestration and queue coordination.

---

## Takeaways

### 1. Predictable concurrency scaling
For jobs with non-trivial execution time, JetQueue scales nearly linearly with worker concurrency.

### 2. Durable orchestration has a measurable cost
Crash-safe enqueue, idempotent processing, and persistent state transitions introduce overhead, but those costs are stable and understandable.

### 3. Lightweight workloads expose the true system ceiling
The `echo` benchmark shows that the practical orchestration ceiling is roughly `250–272 jobs/sec` in the current implementation and benchmark environment.

### 4. Benchmark behavior matches system design
The results are consistent with JetQueue's architecture: durable control-plane coordination plus concurrent worker execution.

---

## Summary

JetQueue demonstrates:

- predictable throughput scaling under concurrent worker execution
- stable end-to-end behavior under durable queue semantics
- clear separation between execution-bound and orchestration-bound performance regimes

Key measurements:

- `18.15 jobs/sec` at worker concurrency `1`
- `83.82 jobs/sec` at worker concurrency `5`
- `179.10 jobs/sec` at worker concurrency `10`
- orchestration ceiling of approximately `250–272 jobs/sec` under lightweight workloads