# v1 dogfood and release-candidate runbook

This runbook gathers the manual evidence required in addition to automated
tests. Run it against the exact release-candidate commit and artifacts on
dedicated Linux, macOS, and Windows test accounts. Do not use production jobs,
credentials, configuration, or a shared home directory.

## Automated baseline

Run the automated acceptance suites before beginning the manual campaign:

```console
make e2etest
make perftest
make soaktest SOAK_TIME=10m
```

On Linux, `TestAssembledBinaryDogfoodContracts` exercises the assembled binary
through preflight health and backup restore, start failures and dependency
predicates, wait-state controls, named-pool and multi-slot admission, log
following, repeated-run live input, configuration failure and redaction,
notification exhaustion, and cleanup dry-run/force behavior. The adjacent
assembled-binary lifecycle and crash-boundary tests cover terminal detachment,
process trees, retries, timeouts, rotation, stale ownership, and durable crash
points. Native macOS and Windows lifecycle tests and the performance/soak
suites provide the remaining automated platform and scale baseline.

These tests deliberately do not replace evidence that requires an independent
SSH client, real release packages, network filesystems, controlled remote
SMTP/HTTPS services, supported-prior-release artifacts, or long-running
resource observation. A release campaign must still perform every applicable
manual step below, and core live-input tests must not be skipped on release
hosts.

## 1. Prepare the test campaign

For each operating system, record:

- OS name, version, architecture, filesystem type, shell/terminal, and whether
  the session is local or SSH;
- release commit, archive/package name, checksum verification result, and
  `jobman --version`;
- test start/end time and operator; and
- a fresh absolute state directory and configuration file used only for this
  campaign.

Verify the release commit first:

```console
make check
make snapshot
```

On the test host, install from the release-candidate artifact rather than
`go run`. Set `STATE_DIR` to a new local-disk directory. Run:

```console
jobman --state-dir "$STATE_DIR" --version
jobman --state-dir "$STATE_DIR" config validate
jobman --state-dir "$STATE_DIR" doctor --json
```

Expected: configuration is valid, the store is healthy, the current schema is
supported, and foreign-key violations are zero. Repeat `doctor` with a path on
a known network filesystem and record the expected rejection; never continue
testing on that state root.

## 2. Basic lifecycle and terminal disconnect

Submit a gated command that writes distinct stdout/stderr markers, waits for a
file, and then exits with zero. Record the job ID. In another terminal confirm
`status`, `show --json`, and growing `logs` work. Close the submitting terminal
entirely, create the gate file from the second terminal, and verify terminal
outcome `success` and exact raw stream bytes.

Repeat from an SSH session:

1. Connect to the same disposable account and submit a job waiting on a gate.
2. Record the ID in a second independent session.
3. Terminate the SSH client process or network connection without logging out
   the whole OS user session.
4. Release the gate from the second session.
5. Verify the target completed and no Jobman process retained the first PTY or
   terminal handles.

Also run commands that exit 0, exit nonzero, receive cancellation, exceed a run
timeout, fail to start, and produce an unterminated final output fragment.
Confirm each documented outcome and exit fact in `show --json`.

## 3. Process-tree control

Use a helper that starts a child and grandchild and records all PIDs. Exercise:

- graceful cancellation where every process handles the graceful request;
- forced escalation where one descendant ignores the graceful request;
- pause while active, proof that progress stops, resume, and proof that
  progress restarts; and
- cancellation while paused.

After every case, independently verify that no recorded PID remains and that a
new unrelated process cannot be affected by a stale Jobman identity. On
Windows, confirm descendants are Job Object members and the forced phase ends
the whole tree. Record whether best-effort console break was observed; failure
of that advisory phase is acceptable only when forced Job Object termination
succeeds after the configured grace period.

## 4. Scheduling and policy matrix

Use short deterministic commands and verify:

- retries for retryable exit, start failure, signal/platform reason, and
  timeout, plus a nonretryable failure;
- constant, linear, and exponential delays, maximum delay, jitter bounds, run
  limit, success target, and failure limit;
- `--after-success`, `--after-finish`, `--after-failed`, each terminal-outcome
  predicate, an impossible dependency, and multiple prerequisites;
- until, delay, file, and executable-probe waits, including abort deadlines;
- global and named-pool concurrency limits, multi-slot jobs, FIFO/bounded
  bypass behavior, `config apply`, and removal of an unused pool; and
- policy-only pause/resume while waiting, queued, and backing off.

Run concurrent `list`, `status`, `show`, `logs`, `cancel`, and `doctor` clients
during these cases. Any partial JSON, impossible transition, database busy loop,
or reader-induced mutation is a release blocker.

## 5. Logs, input, configuration, and redaction

Generate binary data, large lines, interleaved stdout/stderr, and enough output
to rotate every configured segment. Verify raw streams byte-for-byte, combined
ordering status, follow-to-completion, active reads, and the behavior after
`clean` pruning.

For live input, send binary chunks from multiple clients, exercise backpressure,
send EOF, retry EOF, and confirm a later run gets a fresh endpoint. Verify no
TCP listener exists. On Unix inspect socket ownership/mode; on Windows inspect
the named-pipe DACL.

Create configuration layers at every precedence level. Confirm origins,
trusted-project enforcement, unknown-key rejection, and that malformed config
does not prevent `list`, `status`, `show`, `logs`, `cancel`, or `doctor`. Apply
store-wide concurrency with `config apply`, `run`, `rerun`, and policy-based
`clean`; confirm explicit `clean --older-than` does not become an authority
operation.

Put unique canary values in fields named `token`, `password`, and a configured
redaction pattern. Confirm canaries do not appear in human diagnostics, JSON,
notification diagnostics, or error output. It is expected that a target which
prints a canary places it in raw target logs; document this boundary.

## 6. Notifications and recovery

Use disposable local command notifiers and controlled SMTP/HTTPS endpoints.
Never use production credentials. Verify event payload schema, authentication,
timeouts, response truncation, retry timing, maximum attempts, and that delivery
failure does not alter the job outcome.

Terminate a supervisor during a notification claim, wait for claim expiry, and
run `doctor --repair`. Confirm the due delivery is recovered once, attempts are
durable, and idempotency keys prevent an unnoticed duplicate side effect.

## 7. Retention, upgrade, backup, and restore

Create old completed jobs with dependency and notification history. Exercise
policy dry-run and forced cleanup. Confirm finite log age is honored, metadata
is deleted only after logs are pruned, and unresolved dependencies, pending
notifications, or active admissions block deletion.

For every supported prior schema fixture and the most recent released binary:

1. Copy the fixture to a fresh state root.
2. Record a pre-upgrade `doctor --json` where the old binary supports it.
3. Open it with the release candidate.
4. Confirm an automatic file appeared under `backups/` before schema change.
5. Run `doctor`, inspect all representative jobs/logs, and compare counts.
6. Follow [UPGRADING.md](UPGRADING.md) to restore the backup into a different
   state root and verify it with the appropriate binary.
7. Simulate unwritable backup storage and confirm migration aborts without
   changing the original schema.

## 8. Packaging and endurance

Install and uninstall every applicable archive, native package, Homebrew Cask,
and container image. Verify man pages, completions, sample configuration,
unprivileged container identity, signal handling, checksums, signatures, SBOMs,
and that packaged defaults point to writable per-user state.

Run at least one 24-hour soak on each operating system with concurrent short
jobs, periodic retries/timeouts, log rotation, live input, notifications,
cleanup dry-runs, and `doctor`. Record peak database/WAL/log sizes, process and
handle counts, CPU/memory, command latency percentiles, and every nonzero
diagnostic. Growth without a configured bound or leaked processes/handles is a
release blocker.

## 9. Evidence and exit decision

For each scenario retain the commands (with secrets removed), job IDs, relevant
JSON, expected versus actual result, timestamps, artifact version, and issue
link for deviations. Do not attach raw state or logs until reviewed for secrets.

The v1 candidate passes only when:

- automated `make check`, snapshot, and all native CI jobs pass on the release
  commit;
- this runbook passes on Linux, macOS, and Windows without skipped core cases;
- upgrade and restore evidence exists for every supported prior release;
- the 24-hour soaks have no unexplained resource growth or state corruption;
  and
- every deviation is fixed, explicitly accepted as a documented limitation,
  or deferred out of the claimed v1 contract.
