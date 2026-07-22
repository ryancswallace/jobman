---
layout: default
title: Backup and recovery
parent: Operations
nav_order: 2
permalink: /operations/backup-recovery/
---

# Backup and recovery

Jobman provides consistent SQLite snapshots and conservative lifecycle repair.
A complete operational backup should also preserve retained log files.

## Create a database snapshot

```console
$ jobman doctor --backup "$HOME/jobman-backup.db"
```

The destination must be a new path. The command uses SQLite backup semantics
instead of copying a live database file. Record the Jobman version and retain a
matching `doctor --json` report.

Jobman also creates a private automatic snapshot below `STATE_DIR/backups/`
before a supported schema migration. Migration stops if the snapshot cannot be
created and validated.

## Preserve logs

Database backup does not embed raw stdout/stderr files. For complete recovery:

1. finish or cancel important active jobs;
2. prevent new Jobman invocations;
3. create the database snapshot;
4. archive the state root's retained log tree without following symlinks; and
5. preserve file ownership, permissions, and ACLs.

Never rely on an inconsistent copy of `jobman.db`, `jobman.db-wal`, and
`jobman.db-shm` made while Jobman is active.

## Repair supported stale state

```console
$ jobman doctor --json
$ jobman doctor --repair
$ jobman doctor --json
```

Repair checkpoints the WAL, reconciles expired lifecycle ownership that can be
proven stale, and recovers due notification work. It does not rewrite corrupt
rows, infer target success, or resurrect host-specific process identities.

## Restore offline

Restore only when no client, supervisor, or target uses the state root. Keep
the failed directory in quarantine for diagnosis. Create a new private state
root, install the snapshot as `jobman.db`, and do not bring old WAL sidecars
into the new directory. Then run the same or newer binary:

```console
$ jobman --state-dir NEW_STATE doctor
$ jobman --state-dir NEW_STATE list --all
```

Inspect representative job histories and logs before returning the store to
normal use. The full [upgrade and restore runbook]({{ site.baseurl }}/operations/upgrading/)
documents platform moves and migration rehearsal.

{: .warning }
Moving state between Linux, macOS, and Windows is not supported because path,
process identity, ACL, and IPC semantics differ. Start a fresh destination
store and retain the source as an offline archive.
