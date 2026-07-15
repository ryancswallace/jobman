# Platform capability record

Status: Linux core lifecycle and Unix policy adapters implemented; full policy
acceptance and native macOS/Windows validation incomplete
Recorded: 2026-07-14
Specification: [Platform requirements](SPEC.md#17-platform-requirements)
Decision: [ADR-0001](adr/0001-per-job-supervisor.md)

This record separates behavior demonstrated by native tests from adapters that
only compile. It covers the initial vertical slice and the implemented v1
policy expansion. A compiling adapter is not a portability claim.

## Current matrix

| Capability | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Build | Native build and tests pass in the development environment. | Cross-compiles; no native execution evidence yet. | Cross-compiles; no native execution evidence yet. |
| Supervisor boundary | Uses a new session with `setsid`. The assembled-binary test proves that a target continues after the submitting client exits. | Adapter requests `setsid`; native launch, handle, and hangup behavior is unverified. | Adapter requests a detached, hidden process in a new process group; native handle inheritance and acknowledgement behavior is unverified. |
| Target boundary | Uses a new process group with the target as group leader. | Adapter requests the same process-group arrangement; native behavior is unverified. | Adapter creates a new process group, but no Windows Job Object owns descendants. |
| Process identity | Uses PID, `/proc` start-time ticks, and the kernel boot ID. Native tests cover a live process, identity mismatch, and zombie detection. | Uses PID, `KERN_PROC` start time, and boot time from `sysctl`; native identity and PID-reuse tests are missing. | Uses PID and process creation `FILETIME`. Boot identity is currently a constant placeholder, so restart identity is not yet strong enough for the final contract. |
| Graceful and forced stop | Revalidates identity and signals the process group with `SIGTERM` or `SIGKILL`. A native end-to-end test covers graceful cancellation of a shell and its child in one process group; forced escalation remains unverified. | Adapter signals the process group with `SIGTERM` or `SIGKILL`; child-tree, escalation, and PID-reuse tests are missing. | Both paths call `TerminateProcess` for only the recorded process. Graceful escalation and descendant-tree termination are not implemented. |
| Pause/resume | Revalidates identity and sends `SIGSTOP`/`SIGCONT` to the process group. Policy-only pauses in waiting, queued, or backoff phases do not signal a process. A pause during the narrow process-creation `starting` window returns a retryable state conflict instead of racing launch publication. Focused native process-tree acceptance remains incomplete. | Uses the same process-group signals; compiles but lacks native acceptance evidence. | Policy-only pauses work before an active run. Suspending an active target returns an explicit unsupported error. |
| Private live input | Uses a user-private Unix-domain socket owned by the per-job supervisor. Focused and assembled-binary tests cover binary delivery, partial writes, durable and repeated-run EOF, payload limits, endpoint permissions, rejection of a stale run identity, and concurrent-client admission order. Sustained backpressure coverage remains incomplete. | Uses the same Unix-domain-socket adapter; native execution is unverified. | The adapter returns an explicit unsupported error; no named-pipe implementation exists yet. |
| Repeated-run policies and admission | Scheduler, dependency, wait, retry/repetition, timeout, and SQLite admission logic run natively in focused Linux tests. Paused time is excluded from elapsed run/job timeout accounting. | Platform-neutral logic compiles; native supervisor integration is unverified. | Platform-neutral logic compiles; native supervisor integration is unverified. |
| Notification transports | Command, HTTPS webhook, and SMTP implementations are platform-neutral and bounded; durable delivery leases and attempt metadata are stored locally. | Compiles; native command-notifier and recovery behavior is unverified. | Compiles; native command-notifier and recovery behavior is unverified. |
| State privacy | Native Unix ownership, mode, symlink, and database hard-link checks; log permission tests verify `0700` directories and `0600` files. | Shares the Unix checks, but they have not run natively in this slice. | Portable mode checks are intentionally no-ops and a user-only ACL hardening layer has not been implemented. |
| Raw output and direct arguments | Native assembled-binary tests cover exact stdout/stderr bytes, an unterminated final fragment, failure exit code, and shell metacharacters passed without shell interpretation. | Platform-neutral unit tests and cross-compilation only. | Platform-neutral unit tests and cross-compilation only. |

The Linux assembled-binary suite currently demonstrates detached success,
failed exit classification, exact argument boundaries, growing active logs,
separate raw streams, retry followed by success, dependency waiting,
pause/resume, binary live input with EOF, specification rerun,
shell-and-child process-group cancellation, concurrent readers during
cancellation, and reconciliation of a killed supervisor to a `lost` outcome.
Store, log-index, executor, supervisor protocol, and model tests run natively
on Linux, including the race detector.

That evidence does **not** yet simulate closing a real terminal, dropping an
SSH transport, or terminating an entire user session. It also does not yet
exercise a grandchild process tree, graceful-timeout escalation, supervisor
death at every protocol boundary, or actual PID reuse.

## Shared behavior

- Commands execute directly from an argument vector; Jobman does not insert a
  shell.
- Detached target standard input is the platform null device unless a file or
  supported private live-input policy is explicitly selected.
- The launch credential travels through an inherited pipe, is bounded and
  single-use, and is stored only as a SHA-256 digest before claim.
- A supervisor acknowledgement is bounded, versioned, and strictly decoded.
- Jobman persists process identity before treating a target as safely
  addressable, and rechecks that identity before termination.
- Linux and macOS request a supervisor session distinct from the submitting
  terminal and a target process group distinct from the supervisor.

The policy scheduler, dependencies, concurrency admission, waits, retries,
timeouts, log retention, and notification payload construction are intended to
be platform-neutral. Process suspension and private local IPC are not silently
emulated where the platform adapter cannot provide the required safety.

## Pre-1.0 portability gaps

The following work is required before Jobman can claim the vertical slice is
portable across its release matrix:

1. Run native macOS and Windows launch, claim, execution, inspection, logging,
   cancellation, and abrupt-exit tests in CI.
2. Add a PTY or equivalent session-hangup test on Unix-like systems and a native
   console-disconnection test on Windows. An SSH-specific test may use a local
   disposable server, but must not depend on external infrastructure.
3. Exercise child and grandchild process trees, graceful stop, forced stop, and
   creation-identity mismatch on every platform.
4. Implement Windows descendant ownership and termination, most likely with a
   Job Object whose lifetime is compatible with the per-job supervisor.
5. Implement and test Windows user-only ACL creation and validation for the
   state root, database, WAL sidecars, and logs.
6. Replace the Windows boot placeholder with restart-scoped identity evidence,
   or document and enforce an equally safe alternative.
7. Add fault tests around client exit, supervisor claim, lost acknowledgement,
   target publication, lease expiry, log append, and terminal-state commit.
8. Validate the SQLite driver, WAL contention, and abrupt-writer recovery
   natively on every released operating system and architecture.
9. Implement Windows private live input with user-private local IPC and verify
   binary delivery, backpressure, EOF, cleanup, and no network listener.
10. Either implement safe Windows active-tree suspension/resumption or retain a
    documented unsupported result while guaranteeing policy-only pause/resume.
11. Run the complete retry, dependency, wait, timeout, admission, rotation,
    cleanup, notification, and live-input matrix through assembled binaries on
    every claimed platform.

Until these gates pass, Linux is the only platform with native initial-slice
evidence. macOS and Windows are build targets with explicit pre-1.0 gaps, not
feature-equivalent supported runtimes.
