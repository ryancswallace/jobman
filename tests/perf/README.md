# Performance tests

Performance tests measure operations where regressions affect real workloads,
such as listing large state stores, appending or following logs, cleanup, and
concurrent state updates.

Benchmarks should use deterministic fixtures, report allocations, and avoid
network or machine-specific dependencies. Run them with `make bench`.
