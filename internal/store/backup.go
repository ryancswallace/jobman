package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const backupDirectory = "backups"

func (s *Store) backupBeforeUpgrade(ctx context.Context) error {
	application, version, err := readSchemaHeaders(ctx, s.db)
	if err != nil {
		return fmt.Errorf("inspect database before backup: %w", err)
	}
	if err := validateSchemaHeaders(application, version); err != nil {
		return err
	}
	if version == 0 || version == currentSchemaVersion {
		return nil
	}

	directory := filepath.Join(s.stateDir, backupDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create migration backup directory: %w", err)
	}
	if err := hardenPath(directory); err != nil {
		return fmt.Errorf("restrict migration backup directory access: %w", err)
	}
	name := fmt.Sprintf(
		"jobman-schema-%d-%d.db",
		version,
		s.now().UTC().UnixNano(),
	)
	destination := filepath.Join(directory, name)
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, destination); err != nil {
		return fmt.Errorf("create migration backup: %w", classifySQLite("create migration backup", err))
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("restrict migration backup: %w", err)
	}
	if err := hardenPath(destination); err != nil {
		return fmt.Errorf("restrict migration backup access: %w", err)
	}
	if err := validateDatabaseFile(destination); err != nil {
		return fmt.Errorf("validate migration backup: %w", err)
	}
	s.lastBackup = destination

	return nil
}

// Backup writes a transactionally consistent SQLite snapshot to a new path.
// The destination must not already exist.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if destination == "" {
		return errors.New("backup store: destination is empty")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("backup store: resolve destination: %w", err)
	}
	if _, err := os.Lstat(absolute); err == nil {
		return errors.New("backup store: destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("backup store: inspect destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return fmt.Errorf("backup store: create destination directory: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, absolute); err != nil {
		return fmt.Errorf("backup store: %w", classifySQLite("backup store", err))
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		return fmt.Errorf("backup store: restrict destination: %w", err)
	}
	if err := hardenPath(absolute); err != nil {
		return fmt.Errorf("backup store: restrict destination access: %w", err)
	}

	return nil
}
