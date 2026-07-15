package store

import (
	"context"
	"errors"
	"fmt"
)

// HealthReport is the stable, serializable result of an offline store health
// inspection.
type HealthReport struct {
	Healthy              bool   `json:"healthy"`
	StateDirectory       string `json:"state_directory"`
	DatabasePath         string `json:"database_path"`
	SQLiteVersion        string `json:"sqlite_version"`
	SchemaVersion        int    `json:"schema_version"`
	SupportedSchema      int    `json:"supported_schema_version"`
	IntegrityResult      string `json:"integrity_result"`
	ForeignKeyViolations int    `json:"foreign_key_violations"`
	WALCheckpointed      bool   `json:"wal_checkpointed"`
	MigrationBackup      string `json:"migration_backup,omitempty"`
}

// CheckHealth verifies database integrity, foreign keys, schema headers, file
// safety, and local-filesystem support. Repair additionally checkpoints WAL;
// it never edits corrupt rows or invents lifecycle transitions.
func (s *Store) CheckHealth(ctx context.Context, repair bool) (HealthReport, error) {
	report := HealthReport{
		StateDirectory:  s.stateDir,
		DatabasePath:    s.databasePath,
		SQLiteVersion:   s.sqliteVersion,
		SupportedSchema: currentSchemaVersion,
		MigrationBackup: s.lastBackup,
	}
	if err := validateStateFilesystem(s.stateDir); err != nil {
		return report, fmt.Errorf("check state filesystem: %w", err)
	}
	if _, err := prepareStateDir(s.stateDir); err != nil {
		return report, fmt.Errorf("check state directory: %w", err)
	}
	if err := validateDatabaseFile(s.databasePath); err != nil {
		return report, fmt.Errorf("check database file: %w", err)
	}
	if err := validateDatabaseSidecars(s.databasePath); err != nil {
		return report, fmt.Errorf("check database sidecars: %w", err)
	}
	application, version, headerErr := readSchemaHeaders(ctx, s.db)
	if headerErr != nil {
		return report, headerErr
	}
	report.SchemaVersion = version
	if schemaErr := validateSchemaHeaders(application, version); schemaErr != nil {
		return report, schemaErr
	}

	if integrityErr := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&report.IntegrityResult); integrityErr != nil {
		return report, fmt.Errorf("check database integrity: %w", integrityErr)
	}
	report.ForeignKeyViolations, headerErr = s.countForeignKeyViolations(ctx)
	if headerErr != nil {
		return report, headerErr
	}
	if repair {
		if checkpointErr := s.checkpointWAL(ctx); checkpointErr != nil {
			return report, checkpointErr
		}
		report.WALCheckpointed = true
	}
	report.Healthy = report.IntegrityResult == "ok" && report.ForeignKeyViolations == 0 &&
		report.SchemaVersion == report.SupportedSchema
	if !report.Healthy {
		return report, fmt.Errorf(
			"store health check failed: integrity=%q foreign_key_violations=%d schema=%d/%d",
			report.IntegrityResult,
			report.ForeignKeyViolations,
			report.SchemaVersion,
			report.SupportedSchema,
		)
	}

	return report, nil
}

func (s *Store) countForeignKeyViolations(ctx context.Context) (count int, returnedErr error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, fmt.Errorf("check database foreign keys: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("finish database foreign-key check: %w", closeErr))
		}
	}()
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read database foreign-key check: %w", err)
	}

	return count, nil
}

func (s *Store) checkpointWAL(ctx context.Context) error {
	var busy, logFrames, checkpointed int
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).
		Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint database WAL: %w", err)
	}
	if busy != 0 {
		return errors.New("checkpoint database WAL: database is busy")
	}

	return nil
}
