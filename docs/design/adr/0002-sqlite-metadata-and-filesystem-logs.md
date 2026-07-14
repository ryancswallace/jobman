# ADR-0002: Use SQLite metadata and filesystem-backed logs

Status: proposed  
Date: 2026-07-14  
Owners: Jobman maintainers  
Specification: [Persistence and concurrency](../SPEC.md#7-persistence-and-concurrency)

## Context

Jobman has many independent client and supervisor processes that must read and
update shared per-user state. Submission, supervisor claim, run start,
cancellation intent, exit observation, and finalization require atomic
compare-and-swap transitions. Readers must never observe half a transition, and
an abruptly killed writer must not leave a partially rewritten metadata file.

The project ships a single CGO-free binary across Linux, macOS, and Windows. It
must not require users to install or administer a database server. State is
local to one user and one host. Expected metadata volume is modest, while stdout
and stderr may be large, binary, append-heavy, and read while active.

A directory of JSON or YAML files would keep dependencies small, but would
require Jobman to design cross-platform locking, transactions across files,
indexes, migrations, compare-and-swap updates, and crash recovery. Storing bulk
logs as database blobs would make ordinary transactions large, increase WAL
pressure, and couple streaming and retention to metadata availability.

## Decision

Use one per-user SQLite database for transactional metadata and private
filesystem files for target output and its sequence index.

Access SQLite through Go's `database/sql` using `modernc.org/sqlite`, a CGO-free
port of SQLite. The dependency is pinned to an exact version during
implementation together with the matching `modernc.org/libc` selected by its
module. Jobman verifies the bundled SQLite version at startup and requires a
version containing the 2026 WAL-reset fix; the initial minimum is SQLite
3.51.3.

The driver choice is accepted only after ADR validation passes on every Jobman
release target. If it fails, this ADR is revised rather than hiding a CGO or
platform fallback behind the same build.

### Data boundary

SQLite stores:

- schema and migration metadata;
- immutable job specifications, excluding resolved secrets;
- current job and run snapshots;
- append-only state transition events;
- supervisor claims and leases;
- process identities and boot/session identity;
- cancellation and other durable intent;
- resolved job dependencies and their observed outcomes;
- concurrency pool capacities, admissions, and admission leases;
- pause/resume and live-input EOF intent, but never input payload bytes;
- exit observations and outcomes;
- log paths, sizes, index versions, and integrity status; and
- later, notification attempts and retention metadata.

The filesystem stores:

- raw stdout bytes per run;
- raw stderr bytes per run;
- a versioned chunk-order index;
- bounded supervisor diagnostics; and
- migration backups or recovery artifacts created through supported code.

No database row contains unbounded target output. No log file is treated as the
authority for job phase or outcome.

### Database location

The database and log root live under the private per-user state directory from
the specification. SQLite files, `-wal`, and `-shm` remain in the same local
directory. Jobman does not support a state database on NFS, SMB, cloud-synced
folders, or other filesystems lacking reliable local locking and shared-memory
semantics.

Detection is necessarily best effort. Initialization combines platform
filesystem checks, ownership and permission validation, successful WAL
activation, and a locking/concurrency probe. Failure is explicit and includes
guidance to choose a local `--state-dir`; Jobman does not silently fall back to
unsafe locking.

### SQLite configuration

Every connection is initialized and verified with:

- `foreign_keys=ON`;
- `journal_mode=WAL`, checking that SQLite actually returns `wal`;
- `synchronous=FULL` because correctness and power-loss durability take
  priority over peak submission throughput;
- a bounded busy timeout, initially five seconds;
- cancellation-aware query contexts;
- automatic checkpointing retained initially and observed for starvation; and
- application/schema identity checks before migrations or queries.

Connection-local pragmas are applied to every physical connection, preferably
through driver DSN configuration plus verification rather than one incidental
pooled connection. The initial pool permits one SQLite connection per Jobman
process. Concurrency comes from multiple processes under SQLite WAL, not from
unbounded pools inside each short-lived client or one-job supervisor.

Transactions remain short and contain no process waits, log writes, network
operations, sleeps, or user interaction. Write operations begin with a locking
mode that avoids deferred-transaction upgrade surprises; the driver-specific
mechanism is validated by the SQLite spike.

### WAL lifecycle

WAL permits readers and a writer to proceed concurrently, but SQLite still
allows only one writer at a time. Jobman therefore treats `SQLITE_BUSY` as an
expected bounded contention result, not corruption. Store operations return a
typed busy error with operation context after the timeout; callers may retry
idempotent operations within their own deadline.

Long-lived read transactions are prohibited. Result sets are consumed and
closed promptly so supervisors do not starve checkpoints. Normal SQLite
autocheckpointing is used first; explicit passive checkpoints MAY run during
bounded cleanup if measurement shows a need. Aggressive checkpoints are never
performed while holding unrelated application work.

The database, WAL, and shared-memory files form one state unit. Backups use the
SQLite backup API or another SQLite-supported consistent mechanism. Jobman MUST
NOT copy or move only the main database file while live connections may exist.

### Schema and migrations

The database uses a fixed SQLite application ID, `PRAGMA user_version`, and a
`schema_migrations` table containing migration number, applied time, Jobman
version, and migration checksum. The redundant human-queryable history helps
diagnose partial upgrades, while startup verifies that all three indicators
agree.

Migrations are immutable after release, ordered, and tested from every
supported prior schema. Each migration is transactional where SQLite permits.
A migration that requires nontransactional work uses an explicit resumable
state machine and backup. Unknown newer schemas are opened neither for reads
nor writes by an older binary.

Migration 1 creates normalized current-state tables plus an append-only event
table. Common filters, uniqueness, foreign keys, enum checks, nonnegative
values, and one-active-run constraints are enforced in SQL as well as Go.
Versioned canonical JSON is reserved for immutable nested specification data or
structured details that do not need relational constraints.

### Optimistic concurrency

Mutable current-state rows have a monotonically increasing revision. Updates
include expected revision and expected phase predicates. The transition event
and current snapshot update occur in the same transaction.

Zero affected rows means conflict. The store never reports it as success and
never retries a noncommutative transition without reloading and reevaluating
the domain event. Naturally idempotent requests, such as repeating a matching
cancellation, return the already committed result.

### Log organization and integrity

Each run has private raw stream files and a chunk index under a path derived
only from validated canonical IDs and run numbers. User-controlled names never
form path components.

The raw stream files are authoritative for their individual bytes. The chunk
index records Jobman's observed interleaving with sequence number, stream,
offset, length, and timestamp. Index records are versioned, bounded, and
checksummed so a torn tail can be discarded safely.

Capture writes stream bytes before the corresponding index record. Therefore a
crash can leave an unindexed raw tail but cannot make the index refer to bytes
that were never written. Recovery preserves unindexed bytes and reports their
combined ordering as unknown.

Before recording a normally completed run, the supervisor closes the child
pipes, finishes capture, flushes according to the durability policy, and writes
final log sizes/integrity status. A log failure does not falsify the target's
exit result: the run records the factual execution outcome plus a degraded
recording health status. Capture continues draining to a bounded discard path
after a storage error so the target cannot deadlock on full pipes.

### Filesystem safety

- State roots are absolute, canonical, user-owned, and private.
- Unix directories use `0700` and files use `0600`; Windows uses user-restricted
  ACLs.
- Creation uses no-follow/exclusive primitives where supported.
- Cleanup and readers operate relative to trusted directory handles or recheck
  containment and identity before mutation.
- Symlinks, reparse points, hard-link surprises, and ownership changes produce
  explicit safety errors.
- Database and log files are never created from a job display name.

## Invariants

- Every visible state transition is an atomic committed snapshot plus event.
- At most one writer commits at a time; contention is bounded and reported.
- Foreign keys and schema checks are enabled on every connection.
- No operation waits on a target or filesystem stream inside a transaction.
- Bulk logs never inflate metadata transactions.
- Raw stream bytes are never reconstructed from a lossy text representation.
- An index never claims bytes beyond the corresponding raw stream file.
- Resolved secret values are stored in neither SQLite nor log metadata.
- Unknown schemas and unsafe database locations fail closed.
- A target outcome and recording-integrity outcome remain distinguishable.

## Alternatives considered

### Directory of JSON/YAML records

This avoids SQLite and keeps state inspectable with basic tools. It was rejected
because multi-record transitions, locking, indexing, compare-and-swap updates,
and crash-safe migrations would become a custom database implementation with
weaker tooling and substantially more platform risk.

### BoltDB/bbolt or another embedded key-value store

A key-value store is single-binary and transactional. It was rejected because
Jobman needs multi-process readers/writers, relational constraints, flexible
inspection queries, migrations, and operational tooling that SQLite provides
more directly. A single-writer file lock would also make a long-lived shared
handle or broker more attractive, conflicting with the process model.

### SQLite through CGO

`mattn/go-sqlite3` is mature and widely used. It was rejected for the core build
because CGO complicates the existing cross-platform release matrix and violates
the intended static single-binary installation experience.

### SQLite compiled to WebAssembly

A WASM-hosted pure-Go driver such as `ncruces/go-sqlite3` avoids CGO and may be a
credible fallback. It was not selected initially because the modernc port more
directly matches the required release matrix and `database/sql` use without a
WASM runtime layer. The validation spike will compare operational correctness,
binary size, build cost, and race behavior before ADR acceptance.

### Store logs as SQLite blobs or rows

This would make log metadata and bytes transactionally colocated. It was
rejected because high-volume append traffic, streaming readers, rotation,
retention, and WAL growth have very different characteristics from small state
transitions.

### One SQLite database per job

Per-job files reduce write contention and failure scope. They were rejected
because `list`, name resolution, retention, migrations, and global constraints
would require an additional catalog and cross-database coordination.

### External database service

PostgreSQL or another service would provide strong concurrency and remote
operation. It was rejected because per-user local use must require no daemon or
administration.

## Consequences

### Positive

- SQLite supplies proven transactions, locking, constraints, queries, and crash
  recovery instead of custom implementations.
- WAL supports concurrent short-lived clients and supervisors on one host.
- The pure-Go driver preserves CGO-free cross-platform artifacts.
- Filesystem logs provide efficient append, tail, rotation, and direct recovery.
- Schema and transition history support diagnosis and controlled upgrades.

### Negative

- The modernc dependency is large, increases compile time and binary size, and
  has a tightly coupled `modernc.org/libc` dependency.
- WAL adds `-wal`/`-shm` files, checkpoints, and network-filesystem restrictions.
- SQLite remains a single-writer system, so poor transaction design can affect
  every job.
- Metadata and log bytes cannot be committed atomically together; recovery and
  integrity status are required.
- Filesystem security needs separate cross-platform hardening beyond SQL.

### Operational

- Dependency updates must preserve the driver's expected libc version and pass
  all crash/concurrency tests.
- Startup records SQLite library version and journal configuration in debug
  diagnostics without exposing paths unless requested.
- `doctor` will eventually run integrity checks, report WAL size/checkpoint
  health, validate log containment, and offer explicit repair actions.
- Support requests must collect the database schema/version and integrity
  output, never the database or logs by default because they may be sensitive.

## Validation required before acceptance

- Verify the exact bundled SQLite version and WAL-reset fix.
- Compile database code for every release GOOS/GOARCH and run CRUD/migration
  tests on representative native or emulated runners for every first-class OS
  and architecture family.
- Run `go test -race` against store contention and abrupt-exit tests.
- Demonstrate multiple processes reading while supervisors perform short writes.
- Demonstrate bounded `SQLITE_BUSY` behavior and context cancellation.
- Contend for concurrency admissions across processes and prove capacity,
  fairness, lease recovery, and exactly-once release invariants.
- Exercise dependency observation and cleanup tombstones transactionally with
  job completion.
- Kill writers before, during, and after commits; verify integrity and valid
  old-or-new state.
- Exercise a long reader and verify checkpoint starvation is observable and
  recoverable.
- Simulate disk full, read-only directory, removed directory, corrupt database,
  unsafe permissions, and unsupported filesystem behavior.
- Verify migration checksum, rollback, unsupported-newer-schema, and backup.
- Fuzz log index parsing and test torn/truncated/corrupt tails.
- Measure binary size, clean-build time, idle memory, query latency, and write
  contention against expected workloads.
- Compare the selected driver with the WASM alternative if any release target
  or race/crash gate fails.

## Revisit this decision if

- the pure-Go driver cannot meet a supported platform or race/correctness gate;
- typical workloads experience unacceptable single-writer contention;
- state must be placed on network filesystems or shared across hosts;
- binary size or build cost materially harms distribution;
- log/metadata consistency requirements cannot be met by the repair model; or
- system-wide multi-user or remote operation becomes a product goal.

## References

- [`modernc.org/sqlite` package documentation](https://pkg.go.dev/modernc.org/sqlite)
- [SQLite write-ahead logging](https://www.sqlite.org/wal.html)
- [SQLite file locking and concurrency](https://www.sqlite.org/lockingv3.html)
- [SQLite foreign-key pragma](https://www.sqlite.org/pragma.html#pragma_foreign_keys)
- [Go `database/sql` package](https://pkg.go.dev/database/sql)
- [Jobman design specification](../SPEC.md)
