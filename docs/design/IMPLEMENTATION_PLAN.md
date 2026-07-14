# Initial vertical-slice implementation plan

Status: initial Linux slice implemented; portability and hardening in progress
Scope: first end-to-end Jobman implementation slice
Specification: [Jobman design specification](SPEC.md)
Decisions: [ADR-0001](adr/0001-per-job-supervisor.md),
[ADR-0002](adr/0002-sqlite-metadata-and-filesystem-logs.md)

## 1. Purpose

This plan delivers the smallest production-shaped path through Jobman's core:
submit one direct command, transfer responsibility to a detached per-job
supervisor, persist state, capture output, inspect the job, and cancel it. The
slice intentionally exercises the difficult architectural boundaries before
adding the broad policy surface described by the specification.

The slice is not a throwaway prototype. Its schemas, state invariants, package
boundaries, failure handling, and tests are expected to form the foundation of
later milestones. Its feature set is deliberately narrow.

### 1.1 Review focus

The accepted plan establishes these slice-level choices:

- implement all six scoped commands before adding retry or wait flags;
- use UUIDv7 as the canonical opaque identifier format;
- keep a current-state snapshot and append-only transition event in the same
  transaction;
- preserve separate raw streams plus a checksummed ordering index;
- distinguish target execution outcome from degraded log-recording health;
- provide no migration path from the unconstrained prototype state; and
- permit Linux-first end-to-end implementation while macOS and Windows remain
  compiling, explicitly gated platform work during pre-1.0 development.

These choices are now implemented as pre-1.0 compatibility surfaces. Any
challenge should be resolved before the first release; after a format ships,
changes require a new migration or format version rather than rewriting
history.

### 1.2 Implementation checkpoint

This checkpoint records evidence as of 2026-07-14. The
[persisted-schema reference](PERSISTED_SCHEMA.md) and
[platform capability record](PLATFORM_CAPABILITIES.md) contain the detailed
handoff. "Implemented" below means present for the narrow scope in Section 2;
it does not include any deferred specification feature.

| Workstream | Current evidence | Remaining gate |
| --- | --- | --- |
| CLI construction | The six scoped commands use an isolated, dependency-injected Cobra tree. Unit tests cover help, argument boundaries, environment parsing, JSON envelopes, binary log output, cancellation dispatch, and exit-code mapping. | Regenerate and review published command documentation as the pre-1.0 CLI contract evolves. |
| Model and SQLite store | UUIDv7 IDs, canonical immutable specifications, transition rules, migration 1, snapshot/event transactions, compare-and-swap updates, selectors, bounded busy handling, and Unix privacy checks are implemented and unit tested. | Add process-level abrupt-writer and broader fault/property tests; retain migration upgrade tests as later schemas are added. |
| Raw logs and executor | Separate raw streams and the checksummed version 1 index are implemented. Tests cover binary bytes, observed ordering, concurrent appends, a torn tail, corruption, an unindexed raw tail, and malformed-index fuzz input. Direct execution preserves arguments. | Add supervisor-crash injection at log-write boundaries and sustained high-volume backpressure and recovery tests. |
| Per-job supervisor | Credential claim, bounded acknowledgement, lease renewal, one-run orchestration, signal-driven target shutdown, start-failure handling, and terminal finalization are implemented. A killed-supervisor end-to-end case is reconciled to `lost`. | Add lost-ack and additional crash-boundary process tests plus a real terminal or SSH-disconnection test. |
| Linux lifecycle | The assembled binary passes detached success, failed exit, exact argument, active-log, separate-stream, shell-and-child process-group cancellation, concurrent reader/canceller, and stale killed-supervisor scenarios. Process identity uses start time and boot ID. | Add grandchild-tree, forced-escalation, actual PID-reuse, and full session-hangup scenarios. |
| macOS portability | Platform adapters compile and select native session, process-group, identity, and signal APIs. | Run the full suite natively and close every gap in the platform capability record before claiming support. |
| Windows portability | Platform adapters compile and select detached-process and creation-time APIs. | Implement Job Object tree ownership, graceful escalation, restart-scoped identity, and user-only ACL enforcement, then run native tests. |
| Repository handoff | The aggregate `make check` passes, including race-enabled unit/end-to-end tests, vulnerability analysis, generated docs, spelling, the production-equivalent site build, and all release build targets. Model/log-index fuzzing, a complete non-publishing release snapshot, and the runtime container build also pass. | The complete fault matrix and native macOS/Windows validation remain stable-release gates. |

## 2. User-visible scope

The slice implements:

```text
jobman run [--name NAME] [--cwd PATH] [--env NAME=VALUE] -- COMMAND [ARG...]
jobman list [--json]
jobman status [--json] JOB
jobman show [--json] JOB
jobman logs [--stream stdout|stderr|both] JOB
jobman cancel JOB
```

The root command displays help. Target execution always requires `run` and is
direct: arguments are preserved exactly and never joined into a shell command.

### 2.1 Required behavior

- `run` validates the request before creating durable state.
- It returns only after the supervisor has atomically claimed the job.
- On success, human output contains only the canonical job ID plus a newline.
- The job survives closure of the submitting terminal or SSH connection.
- One target run starts immediately with null stdin.
- stdout and stderr are captured without altering their bytes.
- `list`, `status`, and `show` observe transactionally consistent state.
- `logs` can read either stream or a combined view in observed order.
- `cancel` durably records intent before stopping the target process tree.
- Completion records exit code or platform termination reason and timestamps.
- Selectors accept exact ID, unique ID prefix, or unambiguous exact name.
- Human diagnostics use stderr; JSON and other command data use stdout.

### 2.2 Explicitly deferred behavior

The slice does not implement:

- retries, repeated successful runs, or backoff;
- wait conditions or probes;
- run-level or job-level timeouts;
- job dependencies and dependency predicates;
- store-wide or named-pool concurrency admission;
- pause and resume;
- live input to a detached run;
- log rotation, following, retention cleanup, or `clean`;
- notifications;
- project configuration or named job specifications;
- rerun;
- secret references;
- foreground execution or inherited stdin;
- supervisor adoption after failure;
- a shared recovery daemon or remote-control listener; or
- state migration from any pre-specification prototype.

Deferred fields MAY appear in versioned model types when needed to avoid a
known incompatible schema, but they MUST NOT expose nonfunctional CLI flags.

## 3. Success criteria

The vertical slice is complete when all of the following are true. The current
evidence and open gaps are tracked in Section 1.2; this list remains a gate and
must not be read as a declaration that every item already passes.

1. A command submitted from a terminal continues after that terminal closes.
2. A separate invocation can inspect the active job and read growing logs.
3. Normal exit produces a durable, correct terminal outcome.
4. Cancellation stops the verified process tree without signaling a reused or
   unrelated PID.
5. Concurrent readers and cancellation cannot corrupt state or observe a
   partially committed transition.
6. Abrupt client or supervisor termination resolves to a valid documented
   state, never a falsely successful state.
7. State and log files are private to the current user by default.
8. Linux end-to-end acceptance tests pass. macOS and Windows compile from the
   start, and their platform spikes either pass or produce explicit tracked
   gaps before the slice is declared portable.
9. `make check` passes, including the race detector, vulnerability scan,
   documentation generation, and release builds.
10. Help, examples, JSON fixtures, and the specification agree.

## 4. Engineering principles

- **Correctness before convenience:** uncertain state becomes `lost`; it is not
  guessed from a missing PID or stale heartbeat.
- **Durable intent before side effects:** commit launch, cancellation, and
  completion intent before performing the corresponding external action.
- **Idempotency:** claim, cancel, reconcile, and finalize operations tolerate
  retries.
- **Narrow transactions:** never wait for a process, filesystem stream, or user
  interaction while holding a database transaction.
- **Explicit dependencies:** clocks, ID sources, launchers, executors, and
  stores are constructed and passed; packages do not rely on mutable globals.
- **Domain isolation:** Cobra, Viper, SQL, and OS process types do not enter the
  model or transition packages.
- **No arbitrary sleeps in tests:** use controlled clocks or bounded eventually
  assertions around real OS behavior.
- **No speculative frameworks:** add interfaces only at tested boundaries with
  more than one behavior or a required fake.

## 5. Proposed package layout

```text
main.go
jobman/
  command.go              public command construction and Execute facade
  errors.go               stable CLI error-to-exit-code mapping
  output.go               human and versioned JSON presenters
internal/
  app/                    use cases: submit, list, inspect, logs, cancel
  model/                  specs, IDs, phases, outcomes, transition rules
  store/                  store API, SQLite implementation, migrations
  supervisor/             claim handshake and one-run orchestration
  executor/               target launch, wait, and captured result
  platform/               detach, identity, process-tree, signal adapters
  logstore/               stream files, chunk index, combined reader
  config/                 typed defaults and state-path resolution
  testproc/               helper-process modes used only by tests
```

The existing package-global commands and `init` registration are replaced.
`jobman.NewCommand(dependencies)` constructs an isolated command tree, enabling
parallel CLI tests without resetting global Cobra or Viper state.

The `jobman` package is not yet promised as a stable general-purpose Go API.
Only documented CLI behavior and persisted schema commitments are reviewed for
compatibility during pre-1.0 development.

## 6. Core model

### 6.1 Identifiers

Use canonical UUIDv7 job, run, supervisor, and event IDs encoded as lowercase
text. UUIDv7 is time ordered for operational sorting but remains opaque to
users. Run display numbers remain monotonically increasing positive integers
within a job.

ID creation uses an injected cryptographically secure source. Tests use a
deterministic source. Ordering MUST use persisted timestamps plus ID as a
tie-breaker, never assume wall clocks are perfectly monotonic.

### 6.2 Initial immutable specification

The initial `JobSpec` contains:

- schema version;
- executable and ordered arguments;
- canonical working directory;
- non-secret environment additions and removals;
- environment inheritance policy identifier;
- optional display name; and
- stop policy with graceful period and force behavior.

The effective specification is serialized canonically for inspection and
future reruns. Runtime observations do not mutate it.

### 6.3 Initial state

`JobState` contains phase, optional outcome, revision, submission/claim/start/
completion times, active run ID, supervisor lease summary, cancellation intent,
and last diagnostic code. `RunState` contains display number, phase, outcome,
revision, process identity, timing, exit information, and log metadata.

State transitions are implemented as pure functions that return either a new
state plus required effects or a typed conflict. Every transition has a table
test covering valid sources, invalid sources, idempotent repetition, and
terminal-state behavior.

### 6.4 Initial transitions

| Entity | Event | From | To | Required durable data |
| --- | --- | --- | --- | --- |
| Job | submit | none | submitting | spec, ID, launch credential hash/deadline |
| Job | supervisor claim | submitting | starting | supervisor ID, identity, lease |
| Run | reserve | none | starting | run ID/number, log paths |
| Run | process started | starting | running | verified process identity, start time |
| Job | process started | starting | running | active run ID, start time |
| Job | cancel requested | active | stopping | cancellation event and request time |
| Run | stop requested | running | stopping | stop reason and request time |
| Run | process exited | active | completed | outcome, exit information, end time |
| Job | run finalized | active | completed | outcome, end time, cleared lease |
| Job | claim failed | submitting | completed | `submission_failed`, diagnostic code |
| Job | ownership lost | active | completed | `lost`, evidence summary |

The detailed table created during implementation additionally states
preconditions, SQL compare-and-swap predicate, external effect, retry behavior,
and crash result for each row.

## 7. Persistence design for the slice

ADR-0002 controls the storage decision. The implementation starts with these
logical tables; column details are reviewed in the first migration:

```text
schema_migrations
jobs
runs
supervisors
state_events
```

- `jobs` and `runs` hold current query-optimized snapshots with a revision.
- `state_events` is an append-only diagnostic transition history written in the
  same transaction as its snapshot update.
- `supervisors` holds claim, lease, boot identity, and process identity data.
- Specs and structured error details use versioned canonical JSON only where a
  normalized column is not needed for constraints or common queries.
- Enumerations are validated by both Go and database constraints.
- Foreign keys, uniqueness constraints, and nonnegative counter checks enforce
  invariants independently of application code.

Every update uses an expected revision or expected phase predicate. A zero-row
update becomes a typed conflict and causes the caller to reload; it is never
treated as success.

### 7.1 Log files

```text
<state-dir>/logs/<job-id>/<run-number>/
  stdout.log
  stderr.log
  chunks.idx
```

The stdout and stderr files retain raw bytes. `chunks.idx` contains a versioned,
checksummed sequence of fixed or length-delimited records with sequence number,
stream, stream offset, length, wall timestamp, and integrity information.
Capture serializes index assignment but never combines target bytes into line
records.

Writing a chunk follows this order:

1. append and flush bytes to the appropriate stream file according to the
   durability policy;
2. append the index record; and
3. periodically flush the index.

After a crash, raw bytes not covered by a valid index record remain available
in their individual stream. Recovery may mark their combined order unknown but
must not invent an order or discard bytes silently.

## 8. Supervisor protocol

ADR-0001 controls supervisor ownership. The slice uses this handshake:

1. The client generates a one-time random credential and stores only its hash
   and a short claim deadline in the submission transaction.
2. It starts the same executable in a private supervisor mode with the job ID
   in argv. The credential is delivered over the child's inherited stdin pipe,
   not argv, an environment variable, or persistent plaintext storage.
3. The supervisor reads the credential, derives its own ID and platform process
   identity, and atomically claims the matching `submitting` job.
4. The claim clears the credential hash, establishes a lease, and advances the
   job to `starting`.
5. Only after commit does the supervisor send a small versioned acknowledgement
   over stdout and detach from the handshake streams.
6. The client verifies the acknowledgement, releases its process handle, prints
   the job ID, and exits.

The parent does not use `exec.CommandContext` with a context that would kill
the supervisor after acknowledgement. No secret or executable specification is
accepted from the private supervisor argv; the claimed database record is the
authority.

If acknowledgement times out, the client reloads state before acting. A valid
claim means submission succeeded even if the acknowledgement was lost. An
unclaimed job is atomically marked `submission_failed`; the exact spawned
identity is stopped if it can still be verified.

## 9. Process execution and cancellation

The supervisor reserves a run and log paths before target creation. It opens
private log files, constructs a direct `exec.Cmd`, sets null stdin, and asks the
platform adapter to establish a new process tree boundary.

After `Start`, Jobman obtains platform creation identity before committing the
run as `running`. If identity cannot be established, it stops the process it
just created and records `start_failed`; it does not publish an unverifiable
active PID.

Cancellation proceeds as follows:

1. resolve an unambiguous selector;
2. transactionally record cancellation intent using expected revision;
3. load and verify supervisor/target identity;
4. request graceful process-tree termination;
5. wait the configured grace period without holding a transaction;
6. reverify identity and force termination if still active; and
7. let the owning supervisor reap and finalize, or reconcile to `lost` if proof
   is unavailable.

The platform adapter returns structured capabilities and errors. Unix signal
names are not exposed on Windows unless an equivalent is intentionally defined.

## 10. CLI and output contract

Command handlers parse into typed request objects and call `internal/app` use
cases. They do not query SQL or manage processes directly.

JSON uses an envelope:

```json
{
  "schema_version": 1,
  "data": {}
}
```

Errors use stable typed categories mapped centrally to the exit codes in the
specification. JSON error output, if requested, goes to stderr so stdout remains
empty on failure. Golden tests cover human and JSON output with times and IDs
normalized through injected dependencies.

## 11. Work sequence

### Phase 0: validate architectural assumptions

The original plan required these disposable spikes before production packages
depended on their results. Linux assumptions now have production and native
test evidence, but native macOS and Windows spikes and the full crash matrix
remain open. That known deviation prevents a portable-support claim.

1. **Supervisor detach spike:** demonstrate launch, pipe acknowledgement,
   process-handle release, terminal closure survival, and no inherited terminal
   streams on Linux, macOS, and Windows.
2. **Process-tree spike:** launch a helper that launches a child; verify graceful
   and forced tree termination plus creation identity on each platform.
3. **SQLite spike:** exercise the selected pure-Go driver with WAL, concurrent
   processes, busy timeout, abrupt writer exit, migrations, and race-enabled Go
   tests on every released OS/architecture available in CI.
4. **Log-capture spike:** concurrently emit binary and unterminated stdout and
   stderr data; prove lossless per-stream storage and valid index recovery after
   an injected crash.

Spike code lives under `devel/spikes/` or a temporary branch and is not imported
by production code. Findings are recorded in the relevant ADR. A failed
assumption reopens the ADR before implementation continues.

### Phase 1: replace the CLI skeleton

- Introduce dependency-injected command construction and typed errors.
- Implement help, strict argument boundaries, selectors, and output presenters.
- Remove package-global commands and implicit shell execution.
- Add golden CLI tests and regenerate man pages/completions.

Gate: CLI tests, lint, docs generation, and cross-platform compilation pass.

### Phase 2: model and store

- Implement immutable specs, phases, outcomes, IDs, and transition functions.
- Add SQLite connection initialization, migration 1, constraints, and store
  methods.
- Add state path and permission handling.
- Add table, property, migration, contention, and abrupt-exit tests.

Gate: no process code is needed to prove all initial state invariants and store
compare-and-swap behavior.

### Phase 3: logs and executor

- Implement private run directories, raw streams, and chunk index.
- Implement direct execution and wait/result classification.
- Add helper-process integration tests for arguments, environment, binary
  output, signals, children, and abrupt exits.

Gate: foreground test harness runs are lossless and finalize correctly without
the detached supervisor.

### Phase 4: detached supervisor and submit

- Implement platform supervisor launchers and private mode.
- Implement one-time credential claim and acknowledgement.
- Orchestrate one run and update leases/transitions.
- Implement submission-time reconciliation and terminal-disconnect tests.

Gate: `run`, `status`, and `show` pass end-to-end with a detached target.

### Phase 5: inspection, logs, and cancellation

- Implement `list`, selectors, and consistent query snapshots.
- Implement per-stream and combined `logs` reading.
- Implement durable cancellation, tree termination, and idempotent repeats.
- Add concurrency and PID-reuse simulations.

Gate: all user-visible vertical-slice acceptance scenarios pass.

### Phase 6: hardening and handoff

- Add bounded reconciliation for stale submissions and leases.
- Audit permissions, redaction, symlink handling, and database path validation.
- Run race, fuzz, vulnerability, cross-platform, release-build, and fault tests.
- Update the specification only where measured platform behavior requires it.
- Record deferred work as scoped follow-on issues, not hidden TODOs in core
  transitions.

Gate: every success criterion in Section 3 has current evidence.

## 12. Test strategy

### 12.1 Unit tests

- Transition table and idempotency.
- Selector resolution and ambiguity.
- Exit classification and typed error mapping.
- Canonical spec serialization.
- Log index encode/decode, checksum, torn tail, and bounds.
- SQL row conversion and invariant validation.

### 12.2 Property and fuzz tests

- Random event sequences never violate state invariants.
- IDs round-trip and preserve uniqueness under a deterministic stress source.
- Arbitrary log indexes cannot panic or escape configured paths.
- Arbitrary database JSON fields fail safely and strictly.
- Argument vectors round-trip without shell interpretation.

### 12.3 Integration tests

Test helper modes include successful exit, selected exit code, stdout/stderr
patterns, binary bytes, no final newline, blocked process, ignored graceful
termination, child/grandchild tree, and rapid exit.

Integration tests use temporary private state directories and independent
Jobman processes. They assert eventual state with bounded polling and preserve
diagnostic artifacts on failure in CI.

### 12.4 Fault tests

Inject termination immediately before and after:

- job insert commit;
- supervisor claim commit and acknowledgement;
- target `Start` and identity commit;
- cancellation intent commit and signal;
- raw log append and index append; and
- run completion and job completion commits.

Each fault point has a documented set of valid recovered states. Success is
never valid unless the target result was durably observed.

## 13. Review checkpoints

| Checkpoint | Status |
| --- | --- |
| Accept ADR-0001 and ADR-0002 before production code. | Complete: both ADRs are accepted. |
| Review migration 1 and the persisted log-index format. | Implemented and documented for pre-1.0 use. Compatibility review remains required before declaring either format stable. |
| Review private supervisor mode and platform launch code. | Linux implementation exists; native macOS and Windows review is open. |
| Declare selector, JSON, or exit-code behavior stable. | Open. The implemented contracts remain pre-1.0 and are not declared stable by this plan. |
| Approve expansion into dependencies, concurrency admission, retries, waits, timeouts, pause/resume, live input, or notifications. | Open by design. None of these deferred features is part of the implemented slice. |

Schema and supervisor reviews include a failure-sequence walkthrough, not only
an API or happy-path review.

## 14. Known risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Platform detach semantics differ | Mandatory spikes and build-tagged adapters; explicit unsupported errors. |
| PID reuse signals an unrelated process | Persist and reverify creation identity; refuse uncertain signals. |
| Client loses acknowledgement | Reload durable claim state before marking failure. |
| SQLite contention blocks CLI | Short transactions, WAL on local storage, bounded busy timeout, cancellation-aware operations. |
| WAL is unsafe on a network filesystem | Detect/reject unsupported locations where practical and document local-only storage. |
| Driver or bundled SQLite regression | Pin exact versions, verify bundled SQLite version, Dependabot, race/crash/concurrency tests. |
| Torn or lagging log index | Raw streams are authoritative; checksummed index tail is repairable. |
| Existing prototype shapes new internals | Replace it by package boundary; preserve only accepted specification behavior. |
| Scope expands into policy features | Do not add a flag until its end-to-end behavior belongs to an accepted slice. |
| Later concurrency limits imply a daemon | Reserve schema seams for transactional admissions; correctness must not depend on a coordinator process. |
| Later live input leaks data or creates remote control | Use private local IPC, bounded delivery, and no persisted input or network listener. |

## 15. Deliverables

The completed slice is expected to produce the following. Section 1.2 records
which deliverables have evidence and which remain release gates:

- accepted ADR-0001 and ADR-0002 with spike findings;
- production package boundaries described above;
- SQLite migration 1 and schema documentation;
- version 1 job/run JSON and log-index schemas;
- the six scoped commands and generated documentation;
- cross-platform platform-capability notes;
- state-machine, integration, fault, race, and fuzz tests; and
- a follow-on plan for dependencies, concurrency admission, retries, waits,
  timeouts, rotation, pause/resume, notifications, and late-v1 live input.
