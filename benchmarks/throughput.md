# Throughput Benchmark

## Workload
- Job type: `sleep`
- Sleep duration: `50ms`
- Number of jobs per run: `200`
- Enqueue parallelism: `20`
- Poll interval: `100ms`

## Environment
- Local machine (MacBook, Docker-based NATS + SQLite)
- Single-node deployment (no distributed workers)

## Results

| Worker Concurrency | Submit Rate (jobs/sec) | Completion Time | Completion Rate (jobs/sec) | Notes                                    |
|--------------------|------------------------|-----------------|----------------------------|------------------------------------------|
| 1                  | 1327.87                | 11.02s          | 18.15                      | Baseline single-worker run               |
| 5                  | 1426.25                | 2.39s           | 83.82                      | Strong scaling with moderate concurrency |
| 10                 | 1158.10                | 1.12s           | 179.10                     | Near theoretical ceiling for 50ms jobs   |

## Results (Echo Workload)

| Worker Concurrency | Submit Rate (jobs/sec) | Completion Time | Completion Rate (jobs/sec) | Notes |
|--------------------|------------------------|-----------------|----------------------------|-------|
| 1                  | 2401.77                | 1.93s           | 258.82                     | Baseline |
| 5                  | 2054.65                | 1.99s           | 251.87                     | Similar throughput despite higher concurrency |
| 10                 | 1075.59                | 1.84s           | 271.85                     | Clear plateau; limited by system overhead |
## Expected Ceiling
For `sleep(50ms)`, each worker can ideally complete about `20 jobs/sec`.

Approximate ideal throughput:
- 1 worker → 20 jobs/sec
- 5 workers → 100 jobs/sec
- 10 workers → 200 jobs/sec

## Observations
- Completion throughput increased significantly as worker concurrency increased.
- Scaling was close to the theoretical execution ceiling for a 50ms synthetic workload.
- Submit throughput remained consistently high (>1000 jobs/sec), indicating the API + SQLite + outbox path was not the primary bottleneck in these tests.
- Measured completion throughput was slightly below the ideal ceiling due to end-to-end system overhead, including outbox publication delay, JetStream delivery, DB state transitions, and benchmark polling.
- These results indicate that JetQueue’s worker concurrency model scales predictably under controlled load.


## Observations (Echo)
- Echo jobs remove almost all execution cost, exposing system-level overheads.
- Completion throughput plateaued around 250–272 jobs/sec, even as worker concurrency increased from 1 to 10.
- This indicates the bottleneck shifts away from job execution and toward queueing and control-plane overheads such as outbox publication, JetStream delivery, SQLite state transitions, and benchmark polling.
- In contrast to the `sleep(50ms)` workload, higher worker concurrency did not produce meaningful throughput gains for trivial jobs.
- This benchmark suggests JetQueue’s execution model scales well when work is substantial, while lightweight-job throughput is bounded by orchestration overhead rather than worker slots.
- The outbox publisher loop (polling every 200ms) likely introduces batching latency, which becomes a dominant factor for lightweight jobs and contributes to the observed throughput ceiling.


## Comparative Interpretation

The two workloads highlight different bottlenecks in JetQueue:

- For `sleep(50ms)` jobs, throughput scaled strongly with worker concurrency, reaching 179.10 jobs/sec at concurrency 10. This shows that the worker execution model and concurrency controls behave predictably under meaningful job workloads.
- For `echo` jobs, throughput plateaued around 250–272 jobs/sec across concurrency levels 1, 5, and 10. This indicates that when execution cost is negligible, the bottleneck shifts away from worker capacity and toward system overhead in the control plane and broker pipeline.

Together, these results show that JetQueue scales effectively when execution dominates, while trivial-job throughput is constrained primarily by orchestration overhead.