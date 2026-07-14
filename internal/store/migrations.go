package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

const (
	applicationID        = 0x4a4f424d // "JOBM" in big-endian ASCII.
	currentSchemaVersion = 1
)

const migration1SQL = `
CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    applied_at_ns INTEGER NOT NULL CHECK (applied_at_ns >= 0),
    jobman_version TEXT NOT NULL,
    checksum TEXT NOT NULL CHECK (length(checksum) = 64)
) STRICT;

CREATE TABLE jobs (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    name TEXT,
    spec_json TEXT NOT NULL CHECK (json_valid(spec_json)),
    phase TEXT NOT NULL CHECK (phase IN (
        'submitting', 'waiting', 'queued', 'starting', 'running',
        'backoff', 'paused', 'stopping', 'completed'
    )),
    outcome TEXT CHECK (outcome IS NULL OR outcome IN (
        'success', 'failure', 'timed_out', 'cancelled', 'aborted',
        'lost', 'submission_failed'
    )),
    revision INTEGER NOT NULL CHECK (revision > 0),
    submitted_at_ns INTEGER NOT NULL CHECK (submitted_at_ns >= 0),
    claimed_at_ns INTEGER CHECK (claimed_at_ns IS NULL OR claimed_at_ns >= submitted_at_ns),
    started_at_ns INTEGER CHECK (started_at_ns IS NULL OR started_at_ns >= submitted_at_ns),
    completed_at_ns INTEGER CHECK (completed_at_ns IS NULL OR completed_at_ns >= submitted_at_ns),
    active_run_id TEXT REFERENCES runs(id) DEFERRABLE INITIALLY DEFERRED,
    supervisor_id TEXT REFERENCES supervisors(id) DEFERRABLE INITIALLY DEFERRED,
    cancellation_requested_at_ns INTEGER CHECK (
        cancellation_requested_at_ns IS NULL OR cancellation_requested_at_ns >= submitted_at_ns
    ),
    cancellation_reason TEXT,
    last_diagnostic_code TEXT,
    launch_credential_hash BLOB CHECK (
        launch_credential_hash IS NULL OR length(launch_credential_hash) = 32
    ),
    claim_deadline_ns INTEGER CHECK (claim_deadline_ns IS NULL OR claim_deadline_ns >= submitted_at_ns),
    CHECK ((phase = 'completed') = (outcome IS NOT NULL)),
    CHECK ((outcome IS NULL) = (completed_at_ns IS NULL)),
    CHECK ((launch_credential_hash IS NULL) = (claim_deadline_ns IS NULL)),
    CHECK (cancellation_reason IS NULL OR cancellation_requested_at_ns IS NOT NULL)
) STRICT;

CREATE TABLE runs (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
    run_number INTEGER NOT NULL CHECK (run_number > 0),
    phase TEXT NOT NULL CHECK (phase IN ('starting', 'running', 'paused', 'stopping', 'completed')),
    outcome TEXT CHECK (outcome IS NULL OR outcome IN (
        'success', 'failure', 'timed_out', 'cancelled', 'start_failed', 'lost'
    )),
    revision INTEGER NOT NULL CHECK (revision > 0),
    resolved_executable TEXT,
    reserved_at_ns INTEGER NOT NULL CHECK (reserved_at_ns >= 0),
    started_at_ns INTEGER CHECK (started_at_ns IS NULL OR started_at_ns >= reserved_at_ns),
    stop_requested_at_ns INTEGER CHECK (stop_requested_at_ns IS NULL OR stop_requested_at_ns >= reserved_at_ns),
    stop_reason TEXT CHECK (stop_reason IS NULL OR stop_reason IN ('cancellation', 'timeout')),
    completed_at_ns INTEGER CHECK (completed_at_ns IS NULL OR completed_at_ns >= reserved_at_ns),
    process_pid INTEGER CHECK (process_pid IS NULL OR process_pid > 0),
    process_identity_json TEXT CHECK (
        process_identity_json IS NULL OR json_valid(process_identity_json)
    ),
    exit_code INTEGER CHECK (exit_code IS NULL OR exit_code >= 0),
    exit_signal TEXT,
    exit_platform_reason TEXT,
    exit_observed_at_ns INTEGER CHECK (exit_observed_at_ns IS NULL OR exit_observed_at_ns >= reserved_at_ns),
    stdout_path TEXT NOT NULL,
    stderr_path TEXT NOT NULL,
    index_path TEXT NOT NULL,
    stdout_size INTEGER NOT NULL DEFAULT 0 CHECK (stdout_size >= 0),
    stderr_size INTEGER NOT NULL DEFAULT 0 CHECK (stderr_size >= 0),
    log_index_version INTEGER NOT NULL CHECK (log_index_version > 0),
    log_integrity TEXT NOT NULL CHECK (log_integrity IN ('pending', 'valid', 'partial', 'corrupt')),
    recording_health TEXT NOT NULL CHECK (recording_health IN ('healthy', 'degraded')),
    log_diagnostic_code TEXT,
    last_diagnostic_code TEXT,
    UNIQUE (job_id, run_number),
    CHECK ((phase = 'completed') = (outcome IS NOT NULL)),
    CHECK ((outcome IS NULL) = (completed_at_ns IS NULL)),
    CHECK ((process_pid IS NULL) = (process_identity_json IS NULL)),
    CHECK (exit_code IS NULL OR outcome IN ('success', 'failure')),
    CHECK ((stop_reason IS NULL) = (stop_requested_at_ns IS NULL)),
    CHECK ((exit_observed_at_ns IS NULL) = (
        exit_code IS NULL AND exit_signal IS NULL AND exit_platform_reason IS NULL
    ))
) STRICT;

CREATE UNIQUE INDEX runs_one_active_per_job
    ON runs(job_id) WHERE phase != 'completed';

CREATE TABLE supervisors (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id) ON DELETE RESTRICT,
    revision INTEGER NOT NULL CHECK (revision > 0),
    process_pid INTEGER NOT NULL CHECK (process_pid > 0),
    process_identity_json TEXT NOT NULL CHECK (json_valid(process_identity_json)),
    claimed_at_ns INTEGER NOT NULL CHECK (claimed_at_ns >= 0),
    lease_renewed_at_ns INTEGER NOT NULL CHECK (lease_renewed_at_ns >= claimed_at_ns),
    lease_expires_at_ns INTEGER NOT NULL CHECK (lease_expires_at_ns >= lease_renewed_at_ns),
    released_at_ns INTEGER CHECK (released_at_ns IS NULL OR released_at_ns >= claimed_at_ns)
) STRICT;

CREATE TABLE state_events (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
    run_id TEXT REFERENCES runs(id) ON DELETE RESTRICT,
    supervisor_id TEXT REFERENCES supervisors(id) ON DELETE RESTRICT,
    entity_kind TEXT NOT NULL CHECK (entity_kind IN ('job', 'run', 'supervisor')),
    entity_id TEXT NOT NULL CHECK (length(entity_id) = 36),
    event_type TEXT NOT NULL,
    from_phase TEXT,
    to_phase TEXT NOT NULL,
    from_outcome TEXT,
    to_outcome TEXT,
    entity_revision INTEGER NOT NULL CHECK (entity_revision > 0),
    occurred_at_ns INTEGER NOT NULL CHECK (occurred_at_ns >= 0),
    details_json TEXT NOT NULL CHECK (json_valid(details_json)),
    UNIQUE (entity_kind, entity_id, entity_revision)
) STRICT;

CREATE INDEX jobs_submitted_order ON jobs(submitted_at_ns DESC, id DESC);
CREATE INDEX jobs_name ON jobs(name) WHERE name IS NOT NULL;
CREATE INDEX jobs_phase ON jobs(phase, submitted_at_ns DESC);
CREATE INDEX runs_job ON runs(job_id, run_number);
CREATE INDEX events_job ON state_events(job_id, occurred_at_ns, id);
`

type migration struct {
	version int
	sql     string
}

var migrations = []migration{{version: 1, sql: migration1SQL}}

func (s *Store) migrate(ctx context.Context) error {
	return s.writeTransaction(ctx, "store migration", func(tx *sql.Tx) error {
		application, version, err := readSchemaHeaders(ctx, tx)
		if err != nil {
			return err
		}
		if err := validateSchemaHeaders(application, version); err != nil {
			return err
		}
		if err := verifyAppliedMigrations(ctx, tx, version); err != nil {
			return err
		}

		for _, item := range migrations {
			if item.version <= version {
				continue
			}
			if err := s.applyMigration(ctx, tx, item, version); err != nil {
				return err
			}
			version = item.version
		}

		return nil
	})
}

func validateSchemaHeaders(application, version int) error {
	if application != 0 && application != applicationID {
		return &SchemaError{Reason: fmt.Sprintf("application id is %#x, want %#x", application, applicationID)}
	}
	if version < 0 || version > currentSchemaVersion {
		return &SchemaError{Reason: fmt.Sprintf("schema version is %d, newest supported is %d", version, currentSchemaVersion)}
	}
	if version > 0 && application != applicationID {
		return &SchemaError{Reason: fmt.Sprintf("schema version %d has no Jobman application id", version)}
	}

	return nil
}

func (s *Store) applyMigration(ctx context.Context, tx *sql.Tx, item migration, priorVersion int) error {
	if item.version != priorVersion+1 {
		return &SchemaError{Reason: fmt.Sprintf("migration sequence skips from %d to %d", priorVersion, item.version)}
	}
	if _, err := tx.ExecContext(ctx, item.sql); err != nil {
		return fmt.Errorf("apply database migration %d: %w", item.version, classifySQLite("apply database migration", err))
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version, applied_at_ns, jobman_version, checksum)
		 VALUES (?, ?, ?, ?)`,
		item.version,
		s.now().UTC().UnixNano(),
		s.jobmanVersion,
		migrationChecksum(item.sql),
	); err != nil {
		return fmt.Errorf("record database migration %d: %w", item.version, classifySQLite("record database migration", err))
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d", applicationID)); err != nil {
		return fmt.Errorf("set database application id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", item.version)); err != nil {
		return fmt.Errorf("set database schema version: %w", err)
	}

	return nil
}

type schemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readSchemaHeaders(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
},
) (application, version int, err error) {
	if err := queryer.QueryRowContext(ctx, "PRAGMA application_id").Scan(&application); err != nil {
		return 0, 0, fmt.Errorf("read database application id: %w", err)
	}
	if err := queryer.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, 0, fmt.Errorf("read database schema version: %w", err)
	}

	return application, version, nil
}

func verifyAppliedMigrations(ctx context.Context, queryer schemaQueryer, version int) error {
	if version == 0 {
		return nil
	}

	rows, err := queryer.QueryContext(ctx, `
		SELECT version, checksum
		FROM schema_migrations
		ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var migrationVersion int
		var checksum string
		if err := rows.Scan(&migrationVersion, &checksum); err != nil {
			return fmt.Errorf("decode migration history: %w", err)
		}
		seen++
		if migrationVersion != seen || migrationVersion > len(migrations) {
			return &SchemaError{Reason: fmt.Sprintf("migration history contains unexpected version %d", migrationVersion)}
		}
		want := migrationChecksum(migrations[migrationVersion-1].sql)
		if checksum != want {
			return &SchemaError{Reason: fmt.Sprintf("migration %d checksum does not match", migrationVersion)}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate migration history: %w", err)
	}
	if seen != version {
		return &SchemaError{Reason: fmt.Sprintf("migration history ends at %d but header reports %d", seen, version)}
	}

	return nil
}

func migrationChecksum(statement string) string {
	digest := sha256.Sum256([]byte(statement))

	return hex.EncodeToString(digest[:])
}

func (s *Store) verifySchema(ctx context.Context) error {
	application, version, err := readSchemaHeaders(ctx, s.db)
	if err != nil {
		return err
	}
	if application != applicationID {
		return &SchemaError{Reason: fmt.Sprintf("application id is %#x, want %#x", application, applicationID)}
	}
	if version != currentSchemaVersion {
		return &SchemaError{Reason: fmt.Sprintf("schema version is %d, want %d", version, currentSchemaVersion)}
	}

	return verifyAppliedMigrations(ctx, s.db, version)
}
