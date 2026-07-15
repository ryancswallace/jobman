# Performance tests

Performance tests measure operations where regressions affect real workloads:

- bounded queries over a store preloaded with 5,000 durable jobs;
- submissions from concurrent callers through the real SQLite transaction
  boundary;
- fsync-backed log throughput with per-stream rotation;
- retention planning over 100,000 run candidates;
- physical cleanup of closed rotated logs; and
- admission decisions with 256 durably queued jobs and FIFO/tie-break fairness.

Benchmarks should use deterministic fixtures, report allocations, and avoid
network or machine-specific dependencies. Run them with `make perftest` or
`make bench`. Override `PERF_TIME` or `JOBMAN_PERF_STORE_JOBS` for longer local
runs. Benchmark output is evidence for comparison; the suite avoids brittle
wall-clock pass/fail thresholds on shared CI runners.

`make soaktest SOAK_TIME=10m` opts into a race-enabled workload that performs
concurrent submissions, large-store reads, rotated log writes, physical
cleanup, and repeated fairness cycles for the requested duration. It reports
operation and byte totals and fails if any worker stops making progress. The
weekly `Soak` workflow runs the same test for five minutes. This suite is not
part of `make check` because its purpose is sustained evidence rather than a
bounded presubmit signal.
