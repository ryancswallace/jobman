# Changelog

All notable user-visible changes to Jobman are documented in this file.

The format follows [Keep a Changelog], and the project uses [Semantic
Versioning]. Release versions are selected from Conventional Commit messages by
semantic-release.

## [Unreleased]

### Added

- Declared the v1 command, exit-status, JSON, configuration, notification,
  immutable job-specification, and persisted-state compatibility contracts.
- Added stable Linux, macOS, and Windows support with native race and assembled
  lifecycle coverage, plus release builds for every documented architecture.
- Added dependency-review, aggregate coverage, documentation, fuzz, CodeQL,
  supply-chain provenance, signing, packaging, container, and release-preview
  gates for the stable release line.
- Added deterministic third-party license notices to archives, native packages,
  and runtime containers.

### Changed

- Promoted Jobman from a prerelease project to its supported v1 release line.
- Staged releases as drafts until archives, packages, SBOMs, checksums,
  signatures, container images, and provenance have all published
  successfully.
- Froze intermediate schema 1 as the oldest directly upgradeable existing
  database and schema 7, already written by tagged releases since v0.6.0, as
  the initial v1 database schema. Earlier prototype state is unsupported;
  eligible databases are backed up before an ordered forward migration, and
  downgrades and cross-OS state moves are unsupported.
- Documented the supported platform baseline, local-filesystem storage
  requirement, 30-day default log retention, container lifecycle, upgrade and
  recovery procedures, and signed-artifact verification workflow.
- Documented the macOS notarization and Windows Authenticode limitations and
  their platform-native first-run consequences without weakening OS security
  controls.
- Made the documented duration grammar consistent across configuration and the
  CLI, including exact `d` and `w` units, and accepted decimal or IEC byte
  sizes for `--log-segment-bytes`.
- Expanded portable archives with the release/support/design documentation and
  branding assets, and installed every generated subcommand man page in native
  Linux packages.
- `rerun JOB` now accepts `--wait`, matching `run --rerun JOB --wait`.

### Removed

- Removed the unsigned, unnotarized Homebrew Cask channel from v1. macOS users
  install the signed-checksum archive until Apple Developer ID signing and
  notarization can be provided without bypassing Gatekeeper.

### Fixed

- Report invalid configuration content and malformed run selectors with usage
  status 2, while preserving status 1 for configuration I/O failures and
  status 3 for valid but absent run selections.
- Derive version, commit, and build-date information from Go module and VCS
  metadata when Jobman is installed with `go install ...@version`, while
  retaining explicit release linker metadata.
- Reconciled the specification with the implemented cleanup, state-path,
  live-input timeout, graceful-stop, retention, and environment-variable
  contracts.

### Security

- Keep each GitHub release in draft and defer the mutable `latest` container
  alias until its checksum signature and both GitHub and SLSA provenance are
  available.
- Added pull-request dependency review and retained digest-pinned, least-
  privilege GitHub Actions release infrastructure.
- Added repository-wide code ownership so protected-branch review policy has a
  concrete owner.
- Prevented nested developer environment files and ignored build state from
  entering Docker build contexts.
- Made release recovery reject published versions, verify GitHub's remote asset
  digests against the signed manifest, and promote only a verified immutable
  container digest.

## [0.9.0] - 2026-07-21

### Added

- Serialized Windows state-path setup so concurrent processes cannot race ACL
  initialization.

### Fixed

- Recognized Darwin's exited process state during native identity checks,
  preventing a completed process from being treated as still active.

## [0.8.4] - 2026-07-20

### Fixed

- Closed log-capture resources when supervisor target-control setup fails.
- Stabilized Windows supervisor deadline tests under race-detector load.

## [0.8.3] - 2026-07-20

### Fixed

- Removed a store renewal race that could surface during concurrent lease
  activity.
- Removed a macOS process-cancellation race around exited targets.

## [0.8.2] - 2026-07-20

### Changed

- Made `CITATION.cff` project-level metadata instead of rewriting a release
  version and date on every publication.
- Replaced the original bitmap branding with SVG wordmark and favicon assets.

### Fixed

- Protected Windows database ACL setup from inherited permissions and
  concurrent initialization failures.

## [0.8.1] - 2026-07-19

### Changed

- Raised aggregate repository coverage to the release threshold and expanded
  failure-boundary tests across every core package.

### Fixed

- Corrected Windows and macOS path handling and made Windows foreground-input
  tests safe under the race detector.

## [0.8.0] - 2026-07-18

### Changed

- Refreshed the v1 release documentation and automated dogfood procedures.

## [0.7.1] - 2026-07-16

### Added

- Added release-record consistency checks and executable snapshot artifact
  validation before publication.

### Changed

- Updated the pure-Go SQLite driver and its runtime dependencies to the latest
  compatible versions reviewed for v1.

### Fixed

- Excluded completion-directory scaffolding from portable release archives.

## [0.7.0] - 2026-07-16

### Added

- Added a task-oriented documentation site with installation, tutorials,
  feature and operations guides, canonical contract publication, validated
  internal links, external-link health checks, and a command reference
  generated from the Cobra tree.

### Fixed

- Made staged and rendered documentation files readable by the GitHub Pages
  artifact uploader.

## [0.6.0] - 2026-07-16

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
[Unreleased]: https://github.com/ryancswallace/jobman/compare/v0.9.0...HEAD
[0.9.0]: https://github.com/ryancswallace/jobman/compare/v0.8.4...v0.9.0
[0.8.4]: https://github.com/ryancswallace/jobman/compare/v0.8.3...v0.8.4
[0.8.3]: https://github.com/ryancswallace/jobman/compare/v0.8.2...v0.8.3
[0.8.2]: https://github.com/ryancswallace/jobman/compare/v0.8.1...v0.8.2
[0.8.1]: https://github.com/ryancswallace/jobman/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/ryancswallace/jobman/compare/v0.7.1...v0.8.0
[0.7.1]: https://github.com/ryancswallace/jobman/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/ryancswallace/jobman/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/ryancswallace/jobman/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/ryancswallace/jobman/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/ryancswallace/jobman/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/ryancswallace/jobman/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/ryancswallace/jobman/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ryancswallace/jobman/releases/tag/v0.1.0
