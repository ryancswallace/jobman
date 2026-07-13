# Repository test suites

Package-level unit tests live beside their Go source. This directory is reserved
for tests that exercise the assembled command-line program:

- `e2e/` contains end-to-end behavior tests;
- `perf/` contains benchmarks and performance regression tests.

Run all implemented suites with `make test`. The Makefile reports an explicit
skip while either suite has no Go test files, allowing its documented target to
remain stable as the implementation grows.
