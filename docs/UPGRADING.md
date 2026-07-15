# Upgrading and restoring Jobman

Jobman upgrades the per-user SQLite schema automatically on first open. Before
an upgrade transaction begins, it writes a consistent private snapshot under
`STATE_DIR/backups/`. If that snapshot cannot be written or validated, Jobman
does not migrate the database.

v1 supports forward upgrades within the v1 release line. Downgrades are not
supported after a newer binary has migrated the database. Patch and minor
releases preserve the CLI, JSON, configuration, and persisted-state contracts
described in `docs/COMPATIBILITY.md`; release notes identify any newly added
fields or migrations.

## Before upgrading

1. Finish or cancel important active jobs.
2. Record `jobman --version` and `jobman --state-dir PATH doctor --json`.
3. Create an operator-selected snapshot:

   ```console
   jobman --state-dir "$STATE_DIR" doctor --backup "$HOME/jobman-before-upgrade.db"
   ```

4. Stop invoking Jobman from other terminals while replacing the binary.
5. Install the new binary and run `jobman --state-dir "$STATE_DIR" doctor`.
6. Inspect representative jobs and logs, then run one disposable managed job
   before returning the state root to normal use.

The automatic migration backup path is reported by `doctor --json` during the
opening process that performed the upgrade and remains visible on disk.

## Offline restore

Restore only while no Jobman client, supervisor, or target associated with the
state root is running. Keep the failed database and WAL sidecars for diagnosis.

1. Move the entire failed state directory to a timestamped quarantine path.
2. Create a new owner-private state directory.
3. Copy the selected backup to `jobman.db` in that directory without copying
   the old `jobman.db-wal` or `jobman.db-shm` files.
4. Ensure the directory is accessible only to the current user and the database
   is not a symlink or hard link.
5. Run the same or newer Jobman binary with `--state-dir NEW_STATE doctor`.
6. Inspect `list --all`, representative `show` output, and retained logs before
   resuming normal use.

Do not edit SQLite rows by hand. If `doctor` reports integrity or foreign-key
failures, preserve the quarantined state and use SQLite recovery tooling on a
copy; Jobman's `--repair` deliberately does not invent lifecycle state or
rewrite corrupt rows.

## Platform or architecture changes

Portable state may be copied only while no Jobman process owns it. Moving a
state root between supported architectures of the same operating system is
expected to preserve SQLite metadata and log bytes, but active process
identities are host-specific and must not be resumed. Run `doctor`, inspect all
nonterminal jobs, and submit a disposable test job on the destination host.

Moving state between Linux, macOS, and Windows is not a supported v1 migration
because path syntax, process identity, ACLs, and local IPC differ. Preserve the
source state as an offline archive and start with a new state root instead.

## Release-candidate upgrade rehearsal

Before a v1 release, rehearse the oldest supported v1 schema-to-current upgrade
on each supported operating system. Retain the pre-upgrade database digest,
automatic migration backup, `doctor --json` before and after output, job/log
spot checks, and an offline restore result with the release evidence. A
successful cross-compile is not upgrade evidence.
