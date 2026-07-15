# Changelog

All notable user-visible changes to Jobman are documented in this file.

The format follows [Keep a Changelog], and the project uses [Semantic
Versioning]. Release versions are selected from Conventional Commit messages by
semantic-release.

## [Unreleased]

### Added

- Added the initial daemonless job lifecycle: direct detached execution,
  SQLite-backed state, per-run raw logs, inspection commands, and durable
  process-tree cancellation.
- Added UUIDv7 identifiers, strict versioned JSON output, optimistic lifecycle
  transitions, supervisor ownership leases, and stale-ownership reconciliation.
- Added race-enabled model, store, log, supervisor, CLI, and assembled-binary
  lifecycle tests, including retry, dependency, pause/resume, binary input,
  rerun, process-tree, and crash-recovery scenarios.
- Added repeated-run and retry policies with exit classification, success and
  failure targets, constant/linear/exponential delay, jitter, and run/job
  timeouts.
- Added time, delay, file, and executable-probe prerequisites; immutable
  outcome dependencies; and transactional global/named-pool slot admission
  with durable bounded-bypass fairness.
- Added strict layered YAML configuration, trusted project files, named job
  specifications and profiles, environment/file secret references, and
  effective-configuration inspection and validation commands.
- Added log following, stream-selective capture, rotation, retention cleanup,
  historical run selection, pause/resume, wait, rerun, foreground attachment,
  and private live input.
- Added bounded command, HTTPS webhook, and SMTP notifications with a
  recoverable delivery queue and persisted attempt diagnostics that do not
  change the managed job's outcome.
- Added inspection of wait evaluations, policy counters, dependency results,
  admission history, and pending or completed notification work.
- Added large-store, concurrent-submission, rotated-log throughput, cleanup,
  and admission-fairness performance benchmarks plus a race-enabled scheduled
  soak suite.
- Added native race jobs, explicit release-architecture smoke builds, a full
  fuzz-target matrix, and a derived-container smoke test with persistent state.
- Added a v1 container contract, maintenance/support policy, compatibility and
  upgrade runbooks, release health checks, migration backups, conservative
  recovery, and automatic release-specific citation metadata.

### Changed

- Modernized repository automation, dependency management, documentation, and
  production-readiness checks.
- Hardened workflow dependencies and permissions, expanded supported-platform
  validation, and added supply-chain analysis and container signing.
- Added continuous configuration fuzzing, digest-pinned build images, and
  GitHub and SLSA provenance attestations for release artifacts and container
  images.
- Publish Homebrew Cask updates through reviewed pull requests so releases work
  with protected-branch requirements.
- Expanded the persisted SQLite schema and immutable job specification through
  ordered, checksum-verified migrations while retaining version 1 read
  compatibility for stored job specifications.
- Stabilized policy inspection JSON with explicit snake-case fields and kept
  internal notification claim tokens out of user-facing output.
- Hardened policy crash boundaries with stale-owner reconciliation during
  waits and admission, run-bound live input, durable EOF intent, timeout-bounded
  notification retries, and atomic admission release on ownership loss.
- Added schema migration 7 to repair historical runtime counters and provide
  deterministic admission tie-breaking without altering prior migrations.
- Separated unit coverage from assembled-binary and performance packages so
  each validation tier runs once with an explicit contract.

### Fixed

- Apply durable concurrency configuration before `run`, `rerun`, and
  policy-based `clean`, while keeping explicit age cleanup and emergency
  commands usable with malformed configuration.
- Apply list filters in SQLite before the result limit and use keyset
  pagination for full-store cleanup and doctor reconciliation.
- Transition capacity-blocked submissions from `starting` to `queued` so
  lifecycle output accurately reports admission waits.
- Preserve a checksummed cleanup handoff until pruning metadata commits, making
  log cleanup resumable across the filesystem/database crash boundary.
- Correct protected-branch check names and require every release architecture
  build in the settings-as-code policy.
- Fail setup and aggregate checks early when the active Go patch release does
  not match the security-patched version pinned in `go.version`.

## [0.5.0] - 2026-07-13

### Changed

- Published Homebrew Cask updates through reviewed pull requests so protected
  branch requirements no longer block artifact releases.

## [0.4.0] - 2026-07-13

### Changed

- Improved repository security controls and OpenSSF Scorecard signals.

## [0.3.0] - 2026-07-13

### Changed

- Validated the pull-request-triggered automated release path with a
  prerelease test change.

## [0.2.0] - 2026-07-13

### Changed

- Validated automated minor-version selection and publication with prerelease
  test changes.

## [0.1.0] - 2026-07-12

### Changed

- Established the initial public development release and automated release
  pipeline.

[Keep a Changelog]: https://keepachangelog.com/en/1.1.0/
[Semantic Versioning]: https://semver.org/spec/v2.0.0.html
[Unreleased]: https://github.com/ryancswallace/jobman/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/ryancswallace/jobman/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/ryancswallace/jobman/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/ryancswallace/jobman/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ryancswallace/jobman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ryancswallace/jobman/releases/tag/v0.1.0
