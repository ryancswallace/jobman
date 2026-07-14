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
  lifecycle tests, including process-tree and crash-recovery scenarios.

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

## [0.1.0] - 2026-07-12

### Changed

- Established the initial public development release and automated release
  pipeline.

[Keep a Changelog]: https://keepachangelog.com/en/1.1.0/
[Semantic Versioning]: https://semver.org/spec/v2.0.0.html
[Unreleased]: https://github.com/ryancswallace/jobman/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ryancswallace/jobman/releases/tag/v0.1.0
