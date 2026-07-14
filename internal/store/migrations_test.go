package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationCatalogIsContiguousAndChecksummed(t *testing.T) {
	t.Parallel()

	if len(migrations) != currentSchemaVersion {
		t.Fatalf("migration count = %d, current schema version = %d", len(migrations), currentSchemaVersion)
	}
	seenChecksums := make(map[string]int, len(migrations))
	for index, item := range migrations {
		wantVersion := index + 1
		if item.version != wantVersion {
			t.Errorf("migration[%d].version = %d, want %d", index, item.version, wantVersion)
		}
		if strings.TrimSpace(item.sql) == "" {
			t.Errorf("migration %d has no SQL", item.version)
		}

		checksum := migrationChecksum(item.sql)
		digest, err := hex.DecodeString(checksum)
		if err != nil || len(digest) != sha256.Size {
			t.Errorf("migration %d checksum = %q, want SHA-256 hex", item.version, checksum)
		}
		if prior, exists := seenChecksums[checksum]; exists {
			t.Errorf("migrations %d and %d have the same checksum", prior, item.version)
		}
		seenChecksums[checksum] = item.version
	}
}

func TestOpenRejectsIncompleteMigrationHistory(t *testing.T) {
	t.Parallel()

	options := Options{StateDir: filepath.Join(t.TempDir(), "state")}
	store, err := Open(t.Context(), options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if _, deleteErr := store.db.ExecContext(t.Context(), "DELETE FROM schema_migrations WHERE version = 1"); deleteErr != nil {
		t.Fatalf("delete migration history: %v", deleteErr)
	}
	if closeErr := store.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	reopened, err := Open(t.Context(), options)
	if reopened != nil {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Errorf("close unexpectedly opened store: %v", closeErr)
		}
	}
	var schemaErr *SchemaError
	if !errors.As(err, &schemaErr) {
		t.Fatalf("Open() error = %v, want *SchemaError", err)
	}
}

func TestMigratedSchemaObjectsAndIntegrity(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, "schema-invariants", newSequentialEventIDs(0x9000))
	assertStrictSchemaTables(t, store)
	assertSchemaIndexes(t, store)
	assertDatabaseIntegrity(t, store)
}

func assertStrictSchemaTables(t *testing.T, store *Store) {
	t.Helper()

	wantTables := map[string]bool{
		"jobs":              false,
		"runs":              false,
		"schema_migrations": false,
		"state_events":      false,
		"supervisors":       false,
	}
	rows, queryErr := store.db.QueryContext(t.Context(), `
		SELECT name, sql
		FROM sqlite_schema
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if queryErr != nil {
		t.Fatalf("query schema tables: %v", queryErr)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var definition string
		if scanErr := rows.Scan(&name, &definition); scanErr != nil {
			t.Fatalf("scan schema table: %v", scanErr)
		}
		if _, expected := wantTables[name]; !expected {
			t.Fatalf("unexpected schema table %q", name)
		}
		wantTables[name] = true
		if !strings.HasSuffix(strings.TrimSpace(definition), "STRICT") {
			t.Errorf("schema table %q is not STRICT: %s", name, definition)
		}
	}
	if iterationErr := rows.Err(); iterationErr != nil {
		t.Fatalf("iterate schema tables: %v", iterationErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("close schema table rows: %v", closeErr)
	}
	for name, found := range wantTables {
		if !found {
			t.Errorf("schema table %q is missing", name)
		}
	}
}

func assertSchemaIndexes(t *testing.T, store *Store) {
	t.Helper()

	wantIndexes := map[string]bool{
		"events_job":              false,
		"jobs_name":               false,
		"jobs_phase":              false,
		"jobs_submitted_order":    false,
		"runs_job":                false,
		"runs_one_active_per_job": false,
	}
	rows, queryErr := store.db.QueryContext(t.Context(), `
		SELECT name
		FROM sqlite_schema
		WHERE type = 'index' AND name NOT LIKE 'sqlite_%'`)
	if queryErr != nil {
		t.Fatalf("query schema indexes: %v", queryErr)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan schema index: %v", scanErr)
		}
		if _, expected := wantIndexes[name]; !expected {
			t.Fatalf("unexpected schema index %q", name)
		}
		wantIndexes[name] = true
	}
	if iterationErr := rows.Err(); iterationErr != nil {
		t.Fatalf("iterate schema indexes: %v", iterationErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("close schema index rows: %v", closeErr)
	}
	for name, found := range wantIndexes {
		if !found {
			t.Errorf("schema index %q is missing", name)
		}
	}
}

func assertDatabaseIntegrity(t *testing.T, store *Store) {
	t.Helper()

	var integrity string
	if queryErr := store.db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); queryErr != nil {
		t.Fatalf("run SQLite integrity check: %v", queryErr)
	}
	if integrity != "ok" {
		t.Errorf("SQLite integrity check = %q, want ok", integrity)
	}

	rows, queryErr := store.db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if queryErr != nil {
		t.Fatalf("run SQLite foreign-key check: %v", queryErr)
	}
	defer rows.Close()

	if rows.Next() {
		t.Fatal("SQLite foreign-key check reported a violation")
	}
	if iterationErr := rows.Err(); iterationErr != nil {
		t.Fatalf("iterate SQLite foreign-key check: %v", iterationErr)
	}
	if closeErr := rows.Close(); closeErr != nil {
		t.Fatalf("close SQLite foreign-key rows: %v", closeErr)
	}
}
