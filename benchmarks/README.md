# Benchmarks

This directory contains reproducible benchmark tooling for JetQueue.

## Benchmark modes

`enqueue_jobs.go` supports two useful modes:

1. **Submit-only mode**
   - Measures enqueue throughput through the gRPC API.
   - Covers API -> SQLite jobs row -> SQLite outbox row.

2. **Wait mode**
   - Submits jobs, then polls job state until terminal completion.
   - Useful for end-to-end throughput validation.

## Prerequisites

Start NATS with JetStream enabled.

Start the API service.

Start the worker service.

## Worker concurrency

Worker concurrency is controlled through:

```bash
export JETQUEUE_WORKER_CONCURRENCY=4