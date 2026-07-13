# AGENTS.md

This file defines repository-wide guidance for coding agents working on Jobman.
More specific `AGENTS.md` files, if added later, override these instructions
within their directories.

## Project status and intent

Jobman is a daemonless command-line job manager written in Go. Its target
capabilities include retries, timeouts, durable logs, delayed execution, and
notifications. The implementation is still under active development.

Treat [docs/design/README.md](docs/design/README.md) as a target design and set
of constraints, not proof that a feature already exists. Inspect the current
code and tests before documenting, extending, or relying on behavior. Do not
claim unimplemented functionality is available.

## Repository map

- `main.go` is the thin executable entry point. It should delegate to the CLI
  package, print a returned error to standard error, and select the process exit
  status.
- `jobman/` contains the Cobra command tree and application behavior. Command
  implementations and their unit tests live together here.
- `devel/autocomplete/` and `devel/manpages/` contain deterministic generators
  for release documentation.
- `devel/updates/` contains POSIX `sh` repository-maintenance scripts.
- `tests/e2e/` is reserved for assembled-binary and lifecycle tests.
- `tests/perf/` is reserved for benchmarks and regression measurements.
- `docs/design/` records architectural intent. Generated man pages and shell
  completions live under `docs/manpage/` and `docs/completions/`.
- `site/` is the GitHub Pages source.
- `etc/jobman/` contains safe system-wide configuration assets installed by
  native packages.
- `.github/workflows/`, `.goreleaser.yml`, Docker files, and `.releaserc` form the
  CI and release pipeline. Keep their versions, paths, and assumptions aligned.

## Start every task safely

1. Read the relevant source, tests, documentation, and configuration before
   editing.
2. Check `git status` and preserve unrelated tracked, untracked, ignored, and
   developer-local changes. Never discard or overwrite work merely to obtain a
   clean tree.
3. Prefer the smallest coherent change that solves the requested problem.
4. Do not modify generated output directly or introduce a parallel abstraction
   when an established repository boundary can be extended.
5. Do not commit, push, publish, tag, open a pull request, or change repository
   settings unless explicitly requested.

Never read, print, commit, or copy secrets from `.env` files, the developer's
environment, GitHub tokens, local configuration, logs, or credential stores.
`.devcontainer/.env.local` is developer-local and out of scope unless the user
explicitly asks about it.

## Toolchain and dependency rules

- `go.version` pins the exact Go toolchain patch used by CI and container
  builds. `go.mod` records the compatible Go language baseline, normally only
  major and minor. Do not set the `go` directive to a newer patch than a valid
  local toolchain requires.
- To change Go versions, update `go.version` and run `make update`; do not edit
  every declaration independently. The synchronizer is
  `devel/updates/go-vers.sh`.
- Development-tool versions are pinned in `Makefile` and installed into `bin/`
  by `make setup` or the individual `tool-*` targets. Prefer these tools over
  unpinned global installations.
- Add dependencies only when the standard library or an existing dependency is
  insufficient. Use the narrowest maintained module, check its license and
  security posture, and avoid dependencies for trivial helpers.
- After changing imports or module requirements, run `go mod tidy` and include
  the resulting `go.mod` and `go.sum` changes. Do not hand-edit `go.sum`.
- Do not vendor dependencies unless the repository deliberately adopts a
  vendoring policy.

Bootstrap a fresh checkout with:

```sh
make setup
```

Use `make versions` to inspect the selected project and tool versions.

## Go implementation conventions

Follow standard, idiomatic Go and the stricter rules in `.golangci.yml`.

- Format with `make format`; do not substitute a different formatter.
- Keep packages cohesive and APIs small. Prefer concrete types until an
  interface is needed at a consumer boundary or test seam.
- Accept interfaces and return concrete types where that improves decoupling.
  Define small interfaces close to the code that consumes them.
- Pass `context.Context` as the first parameter when an operation can block,
  perform I/O, spawn work, or be cancelled. Propagate cancellation to child
  processes and external calls.
- Return errors instead of logging and continuing. Wrap errors with useful
  operation context using `%w`; keep error strings lowercase and avoid trailing
  punctuation. Use `errors.Is` and `errors.As` for classification.
- Avoid `panic` for ordinary failures and avoid `os.Exit` outside the executable
  boundary. Deferred cleanup does not run after `os.Exit`.
- Check errors from close, flush, sync, rename, and process-wait operations when
  they affect correctness or durability.
- Make ownership of goroutines, channels, subprocesses, files, and locks clear.
  Every goroutine and child process needs a bounded shutdown path.
- Avoid package-level mutable state. If existing Cobra or Viper globals must be
  touched, restore them in tests and do not allow one command invocation to
  leak state into another.
- Prefer deterministic behavior. Inject clocks, random sources, environment,
  filesystem roots, and process runners when doing so makes policy code
  testable.
- Use `time.Duration` and `time.Time` rather than loosely typed numeric or
  string values after parsing. Document timestamp formats and timezone behavior.
- Use user-private permissions for state, logs, command specifications, and
  files that may contain environment data. Durable updates should use
  write-sync-rename patterns where appropriate.
- Isolate operating-system-specific process and signal behavior in files with
  explicit build constraints. Unsupported platforms should return clear errors
  rather than silently degrade.

## CLI conventions

Jobman uses Cobra and Viper.

- Give every command accurate `Use`, `Short`, argument validation, and help
  text. Examples must be executable and must match implemented behavior.
- Command functions should return errors. The root command owns usage and error
  presentation; subcommands should not print the same error again.
- Read from `cmd.InOrStdin()` and write normal results to `cmd.OutOrStdout()`.
  Write diagnostics to `cmd.ErrOrStderr()`. Do not use process-global standard
  streams in command logic.
- Respect `cmd.Context()` and ensure cancellation reaches blocking work and
  subprocesses.
- Preserve argument boundaries when launching commands. Do not join untrusted
  arguments into a shell string. Shell interpretation must be explicit,
  documented, and tested.
- Separate human-readable diagnostics from stable machine-readable output.
  Do not add decoration, progress text, or logs to data output.
- Exit statuses must be meaningful and stable. Preserve the managed command's
  result when the command contract requires it, and distinguish validation,
  lookup, interruption, and internal failures where practical.
- Do not prompt in automation or non-interactive execution. Destructive
  operations require explicit intent and should support a dry-run where useful.
- Configuration precedence is: command-line flags, environment, explicitly
  selected file, per-user file, then built-in defaults. Avoid hidden global
  configuration mutations.
- New or changed commands must update unit tests, command help, generated man
  pages and completions, user documentation, and examples, when user-visible.

## Daemonless job-management constraints

Process management is security- and correctness-sensitive.

- Validate a complete job specification before creating durable state or
  detaching work.
- Model lifecycle transitions explicitly. Interrupted or repeated operations
  must not produce impossible states or duplicate destructive effects.
- Coordinate concurrent processes and use atomic state updates. Assume multiple
  Jobman invocations may access the same store simultaneously.
- Managed process groups, signal forwarding, escalation, timeouts, and terminal
  hangup behavior require platform-aware tests.
- Never pass secret environment values, command contents, or log data into
  diagnostics by default. Redact sensitive values before structured logging or
  notifications.
- Bound retry loops, polling, callbacks, log growth, cleanup work, and shutdown.
  Make unlimited behavior an explicit policy choice rather than an accident.
- Cleanup must not delete active state or follow attacker-controlled symlinks
  outside the intended state root.

## Testing expectations

Tests should describe behavior and remain deterministic under repetition,
parallel execution, and the race detector.

- Keep unit tests beside the implementation as `*_test.go`.
- Prefer table-driven tests when several inputs share the same contract, but do
  not obscure a simple case behind unnecessary test machinery.
- Use `t.Helper()` in test helpers, `t.Cleanup()` for restoration, `t.TempDir()`
  for filesystem state, and `t.Setenv()` for environment changes.
- Do not use real home directories, developer configuration, shared ports,
  external networks, wall-clock sleeps, or persistent global state in unit
  tests.
- Test observable results, errors, state transitions, permissions, and output
  streams. Avoid assertions tied only to implementation details.
- Cover failure and cancellation paths, including partial writes, child-process
  failures, invalid configuration, repeated cleanup, and ambiguous identifiers.
- Add end-to-end tests under `tests/e2e/` for behavior that depends on the built
  binary, process groups, signals, detachment, or concurrent CLI invocations.
- Add benchmarks under `tests/perf/` only for meaningful workloads. Use
  deterministic fixtures and report allocations.
- Run focused tests while iterating, then the repository gates below before
  handing off.

Useful test targets:

```sh
make unittest
make e2etest
make perftest
make test
make coverage
```

## Generated files and documentation

Man pages and shell completions are derived from the Cobra command tree and are
ignored by Git. Do not edit them directly.

- Change command definitions or the generator under `devel/`.
- Run `make gen-manpage`, `make gen-completions`, or `make gen-all`.
- Run `make docs` to generate and validate man pages, completions, spelling, and
  the production-equivalent GitHub Pages build.
- Keep generators deterministic, non-interactive, and independently testable by
  accepting an output root rather than assuming a developer directory.

Documentation must distinguish current behavior from planned behavior. Keep
`README.md`, `docs/`, `site/`, command help, sample configuration, and release
instructions consistent. Update `CHANGELOG.md` for notable user-visible changes.

## Shell, workflow, container, and release changes

- Scripts in `devel/updates/` must be POSIX `sh`, deterministic, idempotent,
  non-interactive, and safe to rerun. Required-tool failures must return a
  nonzero status. Validate them with `make shellcheck`.
- Validate GitHub Actions with `make workflow-check`. Keep permissions minimal,
  pin actions to full commit SHAs with a release-version comment for Dependabot,
  use concurrency and timeouts, and avoid exposing secrets to forked pull
  requests.
- Runtime containers must remain unprivileged, signal-correct, and minimal.
  Keep the ordinary `Dockerfile` and `Dockerfile.goreleaser` behavior aligned.
- Use `make docker-check` for Dockerfile validation and `make docker-image` for
  an actual local runtime image.
- Release changes must keep `.goreleaser.yml`, `.releaserc`,
  `.github/workflows/release.yml`, `Dockerfile.goreleaser`, `RELEASE.md`, and
  installation documentation consistent.
- Use `make release-check` for configuration-only changes, `make release-build`
  to compile every declared release target, and `make snapshot` when archives,
  packages, SBOMs, generated assets, checksums, or release images may be
  affected. Snapshot builds must never publish.
- If asked to create commits, use Conventional Commit messages because
  semantic-release derives versions from commits on `main`.

## Required validation

Use focused checks first, selected according to the change:

| Change | Minimum focused validation |
| --- | --- |
| Go implementation | `make format`, focused `go test`, `make lint` |
| Dependencies | `make mod-check`, `make vulncheck`, affected tests |
| CLI surface | `make test`, `make docs` |
| Generators or docs | `make docs` |
| Shell scripts | `make shellcheck` |
| GitHub Actions | `make workflow-check` |
| Container runtime | `make docker-check`, `make docker-image` |
| Release packaging | `make release-check`, `make release-build`, `make snapshot` |

`make quick-check` is the normal fast repository loop. Before final handoff,
run the complete gate whenever the environment permits:

```sh
make check
```

`make check` validates modules, formatting, lint, workflows, shell scripts,
reachable vulnerabilities, race-enabled tests, generated documentation,
spelling, the Pages site, the binary, and GoReleaser configuration. It requires
network access for vulnerability data and tool or image downloads, plus a
running Docker daemon for documentation-container checks.

If a required gate cannot run, report the exact command, failure, and unverified
scope. Do not describe partial validation as a fully passing check.

## Handoff expectations

Summarize:

- the behavior changed and why;
- important compatibility or security decisions;
- files or subsystems affected;
- validation commands and results;
- any remaining external configuration, migration, or follow-up work.

Do not hide warnings, skipped suites, flaky behavior, or known limitations.
