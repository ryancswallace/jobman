# Initial persisted schema

Status: implemented, pre-1.0 compatibility surface
Schema version: 1
Specification: [Persistence and concurrency](SPEC.md#7-persistence-and-concurrency)
Decision: [ADR-0002](adr/0002-sqlite-metadata-and-filesystem-logs.md)

This document records the format written by the initial vertical slice. It is
an implementation reference, not permission to edit Jobman's state by hand.
The migration source and format encoders remain authoritative. Before a stable
release, an incompatible change may still be made through a new migration or
format version; released migrations are never rewritten in place.

## State layout

The state root is selected by `--state-dir`, then `JOBMAN_STATE_DIR`, then the
platform default documented in the specification. Jobman resolves it to an
absolute path. The initial layout is:

```text
<state-dir>/
  jobman.db
  jobman.db-wal
  jobman.db-shm
  logs/
    <job-uuidv7>/
      <run-number>/
        stdout.log
        stderr.log
        chunks.idx
```

The WAL and shared-memory files exist only while SQLite requires them. The
database and those sidecar files form one storage unit and must not be copied
independently while Jobman processes are using the store.

On Unix-like systems, Jobman creates directories with mode `0700` and database
and run log files with mode `0600`, and rejects unsafe ownership, permissions,
symlinks, or hard links where checked. Windows ACL enforcement is not complete;
see the [platform capability record](PLATFORM_CAPABILITIES.md).

## SQLite identity and configuration

Migration 1 sets:

- `PRAGMA application_id = 0x4a4f424d` (`JOBM`);
- `PRAGMA user_version = 1`; and
- a matching row in `schema_migrations`, including the SHA-256 checksum of the
  immutable migration text.

Startup rejects a foreign application ID, a newer schema, a missing migration,
or a checksum mismatch. Each process uses one pooled physical connection. The
connection enables foreign keys, WAL mode, `synchronous=FULL`, immediate write
transactions, and a bounded busy timeout of five seconds by default. The
bundled SQLite library must report version 3.51.3 or newer.

The store is supported only on a local filesystem with reliable SQLite locking
and shared-memory behavior. Automatic rejection of every unsafe network or
cloud-synchronized filesystem remains a pre-1.0 hardening gap.

## Migration 1 tables

All five tables are SQLite `STRICT` tables. Canonical IDs are lowercase,
hyphenated UUIDv7 strings. Times ending in `_ns` are nonnegative Unix nanosecond
timestamps; the Go model normalizes them to UTC. Revisions are positive and
increase on every mutable snapshot transition.

| Table | Purpose and principal constraints |
| --- | --- |
| `schema_migrations` | Records each positive migration version, application time, Jobman version, and 64-character checksum. |
| `jobs` | Stores the immutable specification plus the current job phase, outcome, revision, lifecycle times, active run, supervisor, cancellation intent, diagnostics, and temporary launch-credential hash/deadline. A completed phase requires an outcome and completion time. The plaintext launch credential is never stored. |
| `runs` | Stores one numbered target invocation, its current phase/outcome/revision, resolved executable, verified process identity, stop and exit observations, and log paths, sizes, version, integrity, and recording health. `(job_id, run_number)` is unique and a partial unique index permits at most one nonterminal run per job. |
| `supervisors` | Stores one supervisor identity and lease per job. The job reference is unique; release time is retained for history. |
| `state_events` | Stores append-only job, run, and supervisor transition events. `(entity_kind, entity_id, entity_revision)` is unique. An event and its updated snapshot commit in the same transaction. |

Foreign keys use restrictive deletion. SQL checks duplicate the principal Go
model invariants, including allowed phases and outcomes, paired nullable fields,
nonnegative sizes and times, and consistency between terminal state and
outcome. Mutation statements compare the prior revision and phase; a zero-row
update is a conflict, not success.

The exact columns and constraints are defined by
[`internal/store/migrations.go`](../../internal/store/migrations.go). Migration
tests verify initialization, concurrent initialization, application and version
headers, checksums, transaction rollback, compare-and-swap conflicts, and busy
error classification.

## Canonical job specification JSON

`jobs.spec_json` contains schema version 1 with unknown and duplicate keys
rejected when read. A representative value is:

```json
{
  "schema_version": 1,
  "executable": "/usr/bin/example",
  "arguments": ["one", "two"],
  "working_directory": "/home/user/work",
  "environment": {
    "inheritance": "submission",
    "set": {"MODE": "batch"},
    "unset": []
  },
  "name": "example",
  "stop_policy": {
    "grace_period": "10s",
    "force_after_grace": true
  },
  "stdin_policy": "null"
}
```

The serialized object is compact in the database. Object keys and environment
names are emitted deterministically by Go's JSON encoder, unset names are
normalized, and argument order is preserved. The initial slice supports only
submission-environment inheritance and null standard input. It stores explicit
environment additions and removals, but not a second copy of the complete
inherited environment.

Process identity is also stored as strict JSON alongside a separately checked
PID. The migration 1 representation currently has this shape:

```json
{
  "PID": 1234,
  "Platform": "linux",
  "CreationID": "1234567",
  "BootID": "01234567-89ab-cdef-0123-456789abcdef",
  "TreeID": "1234"
}
```

Creation and boot identity reject a reused PID; `TreeID` records the target
boundary when one is available. The process-identity decoder rejects unknown
members. Event details must be valid JSON and are currently `{}` or an object
such as `{"diagnostic_code":"target_start_failed"}`. These key spellings are
part of migration 1 and must change through an explicit migration if revised
after release.

## Log files and chunk index

`stdout.log` and `stderr.log` are authoritative, byte-preserving stream files.
`chunks.idx` records the order in which Jobman observed stream appends. Version
1 uses fixed 52-byte records:

| Byte range | Encoding | Meaning |
| --- | --- | --- |
| 0-3 | bytes | Magic `JMLI`. |
| 4 | unsigned byte | Index version, currently `1`. |
| 5 | unsigned byte | Stream: `1` for stdout, `2` for stderr. |
| 6-7 | zero bytes | Reserved. |
| 8-15 | little-endian `uint64` | Positive, contiguous global sequence number. |
| 16-23 | little-endian `uint64` | Contiguous offset within the selected raw stream. |
| 24-31 | little-endian `uint64` | Chunk length, from 1 byte through 16 MiB. |
| 32-39 | little-endian `int64` | Observation time, Unix seconds. |
| 40-47 | little-endian `int64` | Observation time, nanoseconds within the second. |
| 48-51 | little-endian `uint32` | CRC-32C of bytes 0 through 47. |

For each chunk, Jobman writes and syncs raw bytes before it writes and syncs
the index record. A crash can therefore leave an unindexed raw tail but must
not leave a valid record that names nonexistent bytes. Readers discard a
partial final record, reject corruption in a complete record, preserve raw
tails in the individual streams, and omit those tails from the combined view
because their cross-stream ordering is unknowable.

## Compatibility rules

- Do not change migration 1 or chunk-index version 1 after a release has
  shipped them; add a migration or a new format version.
- Do not infer lifecycle state from files when the database says otherwise.
- Do not remove or rewrite state events independently of their snapshots.
- Do not derive paths from display names or other user-controlled path text.
- Do not persist resolved secret values or plaintext launch credentials.
- Back up a live database only with a SQLite-supported consistent mechanism.
- Treat the CLI JSON envelope separately from this internal persistence
  contract; neither format silently substitutes for the other.
