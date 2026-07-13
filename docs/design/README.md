# Jobman design

This document records the target behavior and architectural constraints for
Jobman. It is a design contract, not a claim that every feature is implemented.
User-facing behavior should graduate into command help, tests, and the published
documentation as it becomes stable.

## Product model

Jobman manages a command as a **job**. A job has an immutable identity and
specification plus one or more **runs**. Each run records its lifecycle, exit
status, timestamps, and output. A job may create another run when its retry
policy permits.

The CLI is daemonless: submitting, inspecting, following logs, stopping, and
cleaning jobs must not depend on a continuously running privileged service.
Background execution may use a detached worker process, but all durable state
must remain inspectable after the submitting terminal exits.

## Target commands

| Command | Purpose |
| --- | --- |
| `jobman run COMMAND [ARG...]` | Submit and execute a managed command. |
| `jobman list` | List jobs and their current state. |
| `jobman show JOB` | Show a job and its run history. |
| `jobman logs JOB` | Read or follow recorded output. |
| `jobman kill JOB` | Request termination with a configurable signal. |
| `jobman clean` | Remove eligible completed jobs and logs. |
| `jobman config` | Inspect the effective configuration. |

Stable commands must support machine-readable output and meaningful exit codes.
Identifiers accepted by destructive commands must be unambiguous; names that
match multiple jobs require an explicit selection policy or an error.

## Execution policy

A job specification may combine:

- wait conditions based on time, files, or executable probes;
- an abort deadline for waiting or retrying;
- accepted success and failure exit codes;
- maximum run, success, or failure counts;
- constant or exponential retry delay with bounded jitter;
- run-level and job-level timeouts;
- success, retry, and failure notification callbacks;
- log retention limits by age, size, job count, or run count.

Policy validation happens before background execution. Invalid combinations
must fail without creating partial state. Durations and timestamps use Go's
documented duration syntax and RFC 3339 unless a command explicitly documents
another representation.

## State and concurrency

The default store is local and per-user. Updates must be atomic, tolerate an
interrupted writer, and coordinate concurrent Jobman processes. State schema
versions are recorded so migrations can be explicit and testable.

A practical layout is:

```text
state/
  version
  jobs/
    <job-id>/
      spec
      state
      jobman.log
      runs/
        <run-number>/
          state
          jobman.log
          output/
```

Files containing commands, environment values, logs, or callback data may be
sensitive. New files must use user-only permissions by default, and diagnostic
output must not expose secret values.

## Process and signal behavior

- Managed commands run in their own process group where supported.
- Terminal hangup does not terminate a deliberately detached job.
- Stop requests target the process group and escalate only according to an
  explicit policy.
- Jobman forwards container and operating-system termination signals.
- State transitions remain valid if either Jobman or the managed command exits
  unexpectedly.
- Shell execution is opt-in and visible; argument-preserving execution is the
  safe default for untrusted input.

Platform-specific behavior must be isolated and covered by platform builds or
tests. Unsupported behavior should fail clearly rather than silently degrading.

## Logs and notifications

Standard output and standard error retain their stream identity and ordering as
far as the operating system permits. Following logs must terminate cleanly when
the run ends. Rotation and cleanup never delete state for an active run.

Notification callbacks receive a documented, versioned payload and execute with
bounded time and output. Callback failures are recorded but do not rewrite the
underlying job result.

## Configuration

Precedence is explicit and testable:

1. command-line flags;
2. environment variables;
3. an explicitly selected configuration file;
4. the per-user configuration file;
5. built-in defaults.

Unknown keys and invalid values should produce actionable errors. Configuration
paths follow platform conventions, with `XDG_CONFIG_HOME` honored on Unix-like
systems. A schema change that cannot be interpreted compatibly requires release
notes and a migration path.

## Production acceptance criteria

Before declaring the implementation stable:

- lifecycle transitions, retries, timeouts, and interruption recovery have
  deterministic unit and end-to-end coverage;
- concurrent commands cannot corrupt or observe partial state;
- permissions and secret-redaction behavior have security tests;
- Linux, macOS, and Windows builds pass, with documented feature differences;
- command help, man pages, completions, examples, and sample configuration
  agree;
- release archives, native packages, checksums, signatures, SBOMs, and container
  images can be installed and verified from a clean environment;
- upgrades from each supported state and configuration schema are tested.
