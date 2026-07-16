//go:build unix

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDatabaseSidecarSafetyFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		create func(*testing.T, string)
	}{
		{name: "directory", create: func(t *testing.T, path string) {
			t.Helper()
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "public", create: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, nil, 0o644); err != nil { //nolint:gosec // Intentionally unsafe mode under test.
				t.Fatal(err)
			}
		}},
		{name: "hard-link", create: func(t *testing.T, path string) {
			t.Helper()
			source := path + ".source"
			if err := os.WriteFile(source, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(source, path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			directory := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			databasePath := filepath.Join(directory, DatabaseFilename)
			if err := os.WriteFile(databasePath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			item.create(t, databasePath+"-wal")
			if err := validateDatabaseSidecars(databasePath); err == nil {
				t.Fatal("validateDatabaseSidecars() error = nil")
			}
		})
	}
}

func TestStoreHealthRejectsUnsafePathsAndCanceledContext(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "health-errors", newSequentialEventIDs(0xa600))
	if err := os.Chmod(database.StateDir(), 0o755); err != nil { //nolint:gosec // Intentionally unsafe mode under test.
		t.Fatal(err)
	}
	if _, err := database.CheckHealth(t.Context(), false); err == nil {
		t.Fatal("CheckHealth(public state) error = nil")
	}
	if err := os.Chmod(database.StateDir(), 0o700); err != nil { //nolint:gosec // Directory requires owner-only search access.
		t.Fatal(err)
	}
	if err := os.Chmod(database.DatabasePath(), 0o644); err != nil { //nolint:gosec // Intentionally unsafe mode under test.
		t.Fatal(err)
	}
	if _, err := database.CheckHealth(t.Context(), false); err == nil {
		t.Fatal("CheckHealth(public database) error = nil")
	}
	if err := os.Chmod(database.DatabasePath(), 0o600); err != nil {
		t.Fatal(err)
	}
	walPath := database.DatabasePath() + "-wal"
	if _, err := os.Stat(walPath); err == nil {
		if err := os.Chmod(walPath, 0o644); err != nil { //nolint:gosec // Intentionally unsafe mode under test.
			t.Fatal(err)
		}
		if _, err := database.CheckHealth(t.Context(), false); err == nil {
			t.Fatal("CheckHealth(public WAL) error = nil")
		}
		if err := os.Chmod(walPath, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := database.CheckHealth(canceled, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckHealth(canceled) error = %v", err)
	}
	if err := database.Backup(canceled, filepath.Join(t.TempDir(), "canceled.db")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backup(canceled) error = %v", err)
	}
	parentFile := filepath.Join(t.TempDir(), "parent")
	if err := os.WriteFile(parentFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.Backup(t.Context(), filepath.Join(parentFile, "backup.db")); err == nil {
		t.Fatal("Backup(file parent) error = nil")
	}
}

func TestNewStoreHelpersPropagateDatabaseFailures(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "helper-errors", newSequentialEventIDs(0xa680))
	tx, err := database.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	jobID := mustJobID(t, 0xa6, 8)
	if _, err := completedJobEligibleForPruning(t.Context(), tx, jobID, storeTestTime()); err == nil {
		t.Fatal("completedJobEligibleForPruning(rolled back) error = nil")
	}
	if _, err := metadataPruningBlocked(t.Context(), tx, jobID, false); err == nil {
		t.Fatal("metadataPruningBlocked(rolled back) error = nil")
	}
	if err := deleteCompletedJobMetadata(t.Context(), tx, jobID); err == nil {
		t.Fatal("deleteCompletedJobMetadata(rolled back) error = nil")
	}
	if _, err := listConcurrencyPools(t.Context(), tx); err == nil {
		t.Fatal("listConcurrencyPools(rolled back) error = nil")
	}
	if err := removeOmittedConcurrencyPools(t.Context(), tx, nil); err == nil {
		t.Fatal("removeOmittedConcurrencyPools(rolled back) error = nil")
	}
	if err := synchronizeConcurrencyLimits(t.Context(), tx, nil, nil, storeTestTime()); err == nil {
		t.Fatal("synchronizeConcurrencyLimits(rolled back) error = nil")
	}
	tooLarge := ^uint64(0)
	if err := upsertConcurrencyLimit(
		t.Context(), tx, "global", "", &tooLarge, storeTestTime(),
	); err == nil {
		t.Fatal("upsertConcurrencyLimit(too large) error = nil")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := database.backupBeforeUpgrade(t.Context()); err == nil {
		t.Fatal("backupBeforeUpgrade(closed) error = nil")
	}
	if _, err := database.countForeignKeyViolations(t.Context()); err == nil {
		t.Fatal("countForeignKeyViolations(closed) error = nil")
	}
	if err := database.checkpointWAL(t.Context()); err == nil {
		t.Fatal("checkpointWAL(closed) error = nil")
	}
	if _, err := database.PruneCompletedJobMetadata(
		t.Context(), jobID, storeTestTime(), false,
	); err == nil {
		t.Fatal("PruneCompletedJobMetadata(closed) error = nil")
	}
	if err := database.SynchronizeConcurrencyLimits(t.Context(), nil, nil, storeTestTime()); err == nil {
		t.Fatal("SynchronizeConcurrencyLimits(closed) error = nil")
	}
}

func TestStoreHealthReportsSchemaAndForeignKeyFailures(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "health-corruption", newSequentialEventIDs(0xa650))
	if _, err := database.db.ExecContext(t.Context(), "PRAGMA application_id = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CheckHealth(t.Context(), false); err == nil {
		t.Fatal("CheckHealth(application ID) error = nil")
	}
	if _, err := database.db.ExecContext(
		t.Context(), "PRAGMA application_id = "+strconv.Itoa(applicationID),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `
		INSERT INTO job_dependencies(job_id, dependency_job_id, predicate)
		VALUES (?, ?, 'finish')`,
		"018f0000-0000-7000-8000-000000000001",
		"018f0000-0000-7000-8000-000000000002",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	report, err := database.CheckHealth(t.Context(), false)
	if err == nil || report.Healthy || report.ForeignKeyViolations == 0 {
		t.Fatalf("CheckHealth(foreign keys) = %+v, %v", report, err)
	}
}

func TestStorePathValidationFailures(t *testing.T) {
	t.Parallel()
	if _, err := prepareStateDir(""); err == nil {
		t.Fatal("prepareStateDir(empty) error = nil")
	}
	directory := t.TempDir()
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareStateDir(file); err == nil {
		t.Fatal("prepareStateDir(file) error = nil")
	}
	missing := filepath.Join(directory, "missing")
	if err := validateDatabaseFile(missing); err == nil {
		t.Fatal("validateDatabaseFile(missing) error = nil")
	}
	notFile := filepath.Join(directory, "directory")
	if err := os.Mkdir(notFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateDatabaseFile(notFile); err == nil {
		t.Fatal("validateDatabaseFile(directory) error = nil")
	}
	public := filepath.Join(directory, "public.db")
	if err := os.WriteFile(public, nil, 0o644); err != nil { //nolint:gosec // Intentionally unsafe mode under test.
		t.Fatal(err)
	}
	if err := validateDatabaseFile(public); err == nil {
		t.Fatal("validateDatabaseFile(public) error = nil")
	}
	original := filepath.Join(directory, "original.db")
	linked := filepath.Join(directory, "linked.db")
	if err := os.WriteFile(original, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, linked); err != nil {
		t.Fatal(err)
	}
	if err := validateDatabaseFile(linked); err == nil {
		t.Fatal("validateDatabaseFile(hard link) error = nil")
	}
	if err := hardenPath(missing); err == nil {
		t.Fatal("hardenPath(missing) error = nil")
	}
	if err := prepareDatabaseFile(notFile); err == nil {
		t.Fatal("prepareDatabaseFile(directory) error = nil")
	}
	if err := prepareDatabaseFile(filepath.Join(directory, "absent", "jobman.db")); err == nil {
		t.Fatal("prepareDatabaseFile(missing parent) error = nil")
	}
	if err := validateStateFilesystem(filepath.Join(directory, "absent")); err == nil {
		t.Fatal("validateStateFilesystem(missing) error = nil")
	}
}

func TestHardenDatabaseSidecars(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), DatabaseFilename)
	for _, suffix := range []string{"-wal", "-shm"} {
		path := databasePath + suffix
		if err := os.WriteFile(path, nil, 0o644); err != nil { //nolint:gosec // Intentionally broadened before hardening.
			t.Fatal(err)
		}
	}
	if err := validateExistingDatabaseSidecars(databasePath); err == nil {
		t.Fatal("validateExistingDatabaseSidecars(public sidecars) error = nil")
	}
	if err := hardenDatabaseSidecars(databasePath); err != nil {
		t.Fatalf("hardenDatabaseSidecars() error = %v", err)
	}
	if err := validateExistingDatabaseSidecars(databasePath); err != nil {
		t.Fatalf("validateExistingDatabaseSidecars() error = %v", err)
	}
	if err := validateDatabaseSidecarsWith(databasePath, func(string) error {
		return errors.New("injected sidecar security failure")
	}); err == nil {
		t.Fatal("validateDatabaseSidecarsWith(security failure) error = nil")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, err := os.Stat(databasePath + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("sidecar %s mode = %04o, want 0600", suffix, got)
		}
	}

	invalidDatabase := filepath.Join(t.TempDir(), DatabaseFilename)
	if err := os.Mkdir(invalidDatabase+"-wal", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hardenDatabaseSidecars(invalidDatabase); err == nil {
		t.Fatal("hardenDatabaseSidecars(directory) error = nil")
	}

	restricted := t.TempDir()
	if err := os.Chmod(restricted, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(restricted, 0o700); err != nil { // #nosec G302 -- Restores private directory traversal for cleanup.
			t.Errorf("restore restricted directory permissions: %v", err)
		}
	})
	restrictedDatabase := filepath.Join(restricted, DatabaseFilename)
	if err := validateDatabaseSidecars(restrictedDatabase); err == nil {
		t.Fatal("validateDatabaseSidecars(inaccessible parent) error = nil")
	}
	if err := hardenDatabaseSidecars(restrictedDatabase); err == nil {
		t.Fatal("hardenDatabaseSidecars(inaccessible parent) error = nil")
	}
}
