# Platform capability record

Status: v1 adapters implemented; Linux evidence runs locally and native
macOS/Windows evidence runs in GitHub Actions
Recorded: 2026-07-15
Specification: [Platform requirements](SPEC.md#17-platform-requirements)
Decision: [ADR-0001](adr/0001-per-job-supervisor.md)

This record distinguishes implementation from evidence. A release is supported
on an operating system only after its native `Test` workflow job passes for the
release commit; cross-compilation alone is never treated as acceptance.

## v1 matrix

| Capability | Linux | macOS | Windows |
| --- | --- | --- | --- |
| Supervisor detachment | New session via `setsid`; assembled tests prove the target outlives the submitting process. | New session via `setsid`; the hosted native suite runs the same assembled detachment scenario. | Detached hidden process in a new process group; the hosted native suite proves the target outlives the submitter. |
| Target tree | Dedicated process group. | Dedicated process group. | The target starts suspended, is assigned to a named Job Object, then its initial thread is resumed. Descendants inherit Job Object membership. |
| Process identity | PID, `/proc` start ticks, and kernel boot ID. | PID, `KERN_PROC` start time, and kernel boot time. | PID, process-creation `FILETIME`, and system boot time from `NtQuerySystemInformation`. |
| Stop | Revalidated `SIGTERM`, then optional `SIGKILL`, to the process group. | Revalidated `SIGTERM`, then optional `SIGKILL`, to the process group. | Best-effort `CTRL_BREAK_EVENT`, followed after the configured grace period by guaranteed Job Object termination. |
| Pause/resume | Revalidated `SIGSTOP`/`SIGCONT` for the process group. | Revalidated `SIGSTOP`/`SIGCONT` for the process group. | Every current Job Object member is revalidated and suspended/resumed with the native process suspension API. |
| Live input | Private owner-only Unix-domain socket. | Private owner-only Unix-domain socket. | Private named pipe with a protected current-user, SYSTEM, and administrators DACL. |
| State privacy | UID ownership, mode, symlink, and hard-link checks. | UID ownership, mode, symlink, and hard-link checks. | New paths receive protected ACLs; existing state/database paths must be owned by the current user and must not grant broad-principal access. |
| Local storage | Known remote/distributed filesystems are rejected before SQLite is opened. | NFS, SMB, WebDAV, AFP, and FUSE state roots are rejected. | Remote drive roots are rejected. |
| Assembled-binary evidence | Full Linux lifecycle and crash-boundary suite. | Native detachment, output, tree cancellation, live input, pause/resume, and package tests. | Native detachment, output, Job Object cancellation, named-pipe input, pause/resume, ACL, and package tests. |
| Race evidence | Linux unit and assembled suites run with the race detector. | Platform, live-input, log, store, supervisor, and assembled-binary packages run natively with the race detector. | Platform, live-input, log, store, supervisor, and assembled-binary packages run natively with the race detector. |
| Architecture evidence | `amd64`, `arm64`, and `386` release-style builds. | `amd64` and `arm64` release-style builds. | `amd64`, `arm64`, and `386` release-style builds. |

Platform-neutral retry, dependency, wait, timeout, admission, cleanup, and
notification policy is exercised by the package test suite on every native CI
runner. Release-style builds remain `CGO_ENABLED=0`. Cross-architecture build
jobs prove compilation only; they do not replace native lifecycle or race
evidence.

## Crash-boundary evidence

The Linux assembled binary is built with the opt-in `jobman_faultinject` tag.
Production builds compile inert hooks. Tests abruptly terminate the responsible
Jobman process at these boundaries:

- after the job insert transaction;
- after supervisor claim, before acknowledgement;
- after acknowledgement;
- immediately before target start;
- after target start, before process-identity publication;
- after process-identity publication;
- after a raw log fsync, before its index record;
- after an index fsync;
- before and after run-completion commit;
- after terminal job-completion commit; and
- after cancellation-intent commit and after the cancellation side effect.

Every case must converge to a valid terminal state and pass `jobman doctor`.
Raw bytes without an index record are treated as an unindexed tail, never as a
valid-looking ordered suffix. An uncertain lifecycle becomes `lost`; Jobman
does not infer success.

## Deliberate differences

- Windows console control delivery is inherently dependent on console
  attachment. Jobman treats it as best effort and relies on Job Object
  termination for the guaranteed forced phase.
- Unix process groups and Windows Job Objects are different primitives; their
  user-visible contract is tree-wide lifecycle control, not identical signals.
- Ending an entire operating-system user session may terminate jobs. The v1
  guarantee covers closing the submitting terminal or SSH connection.

## Release gate

Before publishing a supported release, confirm all three native workflow jobs
and every declared architecture build passed on the exact release commit,
review the scheduled soak result, and complete the manual scenarios in the
[dogfood runbook](../DOGFOOD.md). A skipped native suite is a failed release
gate, not equivalent evidence. The support window is defined in
[SECURITY.md](../../SECURITY.md) and [SUPPORT.md](../../SUPPORT.md).
