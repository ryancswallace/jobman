# ADR-0001: Use one detached supervisor process per active job

Status: accepted
Date: 2026-07-14
Owners: Jobman maintainers
Specification: [Execution architecture](../SPEC.md#6-execution-architecture)

## Context

Jobman must start a command, return control to the user, and continue managing
that command after the submitting terminal or SSH connection closes. It must
also evaluate future dependencies, local concurrency admission, waits, retries,
timeouts, pause requests, live input, logging, and notification policies
without requiring a continuously running global daemon.

A target process alone cannot satisfy those requirements. Some component must
own policy evaluation, output pipes, process reaping, state transitions, and
notification delivery for the lifetime of the job. Making the short-lived
submission client that owner would tie the job to its terminal. Making a shared
service the owner would violate the daemonless product model and introduce
installation, upgrade, authorization, and single-point-of-failure concerns.

Cross-platform process behavior is materially different. Unix sessions and
process groups do not map directly to Windows process groups, consoles, and Job
Objects. Go also guarantees only limited portable signal behavior; notably,
`os.Process.Signal(os.Interrupt)` is not implemented on Windows. The design must
isolate platform mechanics and refuse unsafe approximations.

## Decision

Each accepted job is owned by exactly one detached **supervisor process**. The
supervisor is another invocation of the same Jobman executable in a private
internal mode. It owns only one job and exits after that job and its bounded
post-completion work are finished.

There is no shared resident Jobman daemon, listening global control service, or
privileged helper.

### Ownership protocol

1. The submission client validates the effective immutable job specification.
2. In one database transaction it creates a `submitting` job, generates a
   claim deadline, and stores a cryptographic hash of a 256-bit one-time launch
   credential.
3. It launches the Jobman executable in private supervisor mode. The job ID is
   non-secret and may appear in argv. The credential is sent through an
   inherited stdin pipe and MUST NOT appear in argv, environment variables, or
   persistent plaintext storage.
4. The supervisor creates its own ID, captures its platform process identity,
   and atomically claims the job by matching phase, unexpired deadline, and
   credential hash.
5. Claiming clears the credential hash, writes the supervisor lease, and moves
   the job to `starting` in one transaction.
6. After commit, the supervisor returns a versioned acknowledgement over its
   inherited stdout pipe, closes the handshake streams, and redirects any
   internal standard handles to private diagnostics or the null device.
7. The client validates the acknowledgement, releases its child-process handle
   without terminating the supervisor, prints the job ID, and exits.

The database record, not private-mode command arguments, is authoritative. A
second supervisor cannot claim the same job. Private mode rejects malformed,
expired, previously used, or mismatched claims without starting a target.

### Lost acknowledgement

An acknowledgement timeout is ambiguous, not immediate proof of submission
failure. The client reloads the job:

- a committed valid claim means submission succeeded;
- an unclaimed expired submission is atomically finalized as
  `submission_failed`; and
- conflicting or uncertain identity is reported without signaling a PID based
  on number alone.

This makes submission retry-safe and prevents a lost pipe write from creating a
false inactive record for a live supervisor.

### Process boundaries

The platform launcher must establish a supervisor boundary that survives the
submitting terminal closing:

- Unix-like systems use a new session or equivalent process-group separation;
- Windows uses native detached/process-group facilities selected by the
  platform spike; and
- all inherited terminal handles are closed or replaced after the claim
  handshake.

The supervisor creates a separate target process-tree boundary. Supervisor
identity and target identity are distinct and both include platform creation
identity in addition to PID.

Exact flags and Windows Job Object lifetime behavior are implementation details
that remain gated by the required platform spikes. They must not change the
ownership protocol or weaken identity verification.

### Lifecycle responsibility

The supervisor exclusively decides ordinary forward progress for its job:

- reserve and start runs;
- evaluate dependencies and acquire or release transactional concurrency
  admissions;
- capture output and wait for exit;
- service durable pause/resume intent and private local live input;
- evaluate completion policy;
- update its lease;
- finalize runs and the job; and, in later slices,
- evaluate waits, delays, retries, retention eligibility, and notifications.

Other clients may record idempotent intent such as cancellation. A client may
perform a safe external effect only after durable intent and full process
identity verification. The supervisor remains responsible for observing the
result and finalizing state.

### Leases and recovery

The supervisor renews a bounded lease in the metadata store. A stale lease is
evidence that reconciliation is needed, not proof that a target exited.
Reconciliation checks supervisor identity, target identity, boot/session
identity, and durable transition evidence.

The initial implementation does not adopt a running target after supervisor
loss. If it cannot prove a terminal result, it records `lost`. It never signals
an unverifiable PID.

## Invariants

- At most one supervisor owns a job.
- Claim is compare-and-swap and transactionally clears its credential.
- The client reports success only after a durable claim is observed.
- The supervisor receives no trusted executable or policy through argv.
- Parent cancellation contexts do not remain connected to the accepted
  supervisor.
- Target output never flows to the submitting terminal in detached mode.
- All externally visible intent is durable before its side effect.
- No target starts without a valid admission when a concurrency limit applies.
- PID alone is never sufficient for observation or signaling.
- Supervisor failure cannot produce a fabricated success result.

## Alternatives considered

### Shared background daemon

A daemon would simplify central coordination, control channels, cleanup, and
resource accounting. It was rejected because it contradicts the core product
promise, requires installation and upgrade management, and creates a common
failure domain for unrelated jobs.

### Leave only the target process detached

This is close to `nohup` and uses fewer resources. It was rejected because no
owner remains to drain both output pipes, reap the process, enforce timeouts,
evaluate retries, or commit a trustworthy result.

### Keep the submitting CLI alive

This is simple and useful as an explicit foreground mode. It was rejected as
the default because terminal closure and client interruption would end job
management.

### Delegate to platform service managers

systemd user units, launchd, Windows Task Scheduler, or similar facilities can
provide strong lifecycle integration. They were rejected as the portable core
because they are not uniformly available, change installation and permission
requirements, and can make behavior environment-dependent. Optional adapters
may be considered in a future ADR.

### One wrapper process per run rather than per job

A run wrapper cannot naturally own initial waits or delays between runs and
would require another component to schedule each wrapper. It was rejected in
favor of a job-lifetime supervisor.

### Double-fork on Unix

Hand-written fork/double-fork patterns are inappropriate inside a running Go
program and do not address Windows. Jobman uses supported process-creation
attributes through isolated platform code instead.

## Consequences

### Positive

- The global system remains daemonless while each job has a clear owner.
- A supervisor failure affects one job, not every active job.
- Each supervisor naturally scopes clocks, policy, output handles, and secrets.
- Upgrades do not require coordinating a resident service process.
- The same binary and model support foreground test harnesses and detached use.

### Negative

- Every active or waiting job consumes one additional process and a database
  connection.
- Multi-process SQLite contention and WAL lifecycle must be engineered and
  tested.
- Detachment, handle inheritance, process identity, and tree termination need
  substantial platform-specific code.
- There is no central in-memory scheduler or immediate broadcast mechanism.
- Admission fairness and wakeups require durable coordination and bounded
  polling among independent supervisors.
- Supervisor loss can leave a live target that v1 reports as `lost` rather than
  adopting.

### Operational

- Resource usage must be measured with many waiting and running supervisors.
- Lease intervals must avoid both needless database churn and slow detection.
- Diagnostic logs identify supervisor and job IDs but redact claim and secret
  material.
- Private mode remains undocumented in ordinary help and is not a stable API.

## Security considerations

The launch credential prevents accidental, stale, or competing claims; it is
not a boundary against another process already running as the same OS user,
which can generally access Jobman's files and processes. The credential is
single-use, time-bounded, compared without exposing plaintext, and cleared on
claim.

State directories and handshake-related files use user-only permissions. No
secret is logged. A malformed private invocation cannot choose an executable,
working directory, environment, or log path independently of the durable job
record.

## Validation required before stable release

- Linux, macOS, and Windows launch/acknowledgement spike.
- Terminal and SSH disconnection test without inherited terminal handles.
- Duplicate, expired, bad-token, and replayed claim tests.
- Crash before claim, after claim, before acknowledgement, and after
  acknowledgement.
- Rapid supervisor exit without zombie or leaked OS handle.
- Process-tree start, graceful stop, forced stop, and PID-reuse simulation.
- Race-enabled concurrent claim and cancellation tests.
- Concurrent admission tests proving independent supervisors cannot exceed a
  configured global or pool capacity.
- Platform capability spikes for process-tree pause/resume and private local
  live-input transport before those later milestones are accepted.
- Measurement of idle supervisor memory, handles, and lease write rate.

## Revisit this decision if

- measured per-job process cost makes the expected workload impractical;
- a supported platform cannot safely detach and preserve the ownership
  protocol;
- reliable job adoption requires a longer-lived trusted owner;
- distributed or system-wide multi-user scheduling becomes a product goal; or
- an optional platform service integration can preserve identical semantics
  without becoming mandatory.

## References

- [Go `os/exec` package](https://pkg.go.dev/os/exec)
- [Go `os` process and signal behavior](https://pkg.go.dev/os#Process.Signal)
- [Go `os/signal` package](https://pkg.go.dev/os/signal)
- [Jobman design specification](../SPEC.md)
