package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func prepareStateDir(path string) (string, error) {
	if path == "" {
		return "", errors.New("state directory is required")
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve state directory: %w", err)
	}
	absolute = filepath.Clean(absolute)

	err = os.MkdirAll(absolute, 0o700)
	if err != nil {
		return "", fmt.Errorf("create state directory: %w", err)
	}

	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize state directory: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", fmt.Errorf("resolve canonical state directory: %w", err)
	}

	info, err := os.Lstat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("state path is not a real directory")
	}
	if err := validatePermissions(info); err != nil {
		return "", fmt.Errorf("validate state directory permissions: %w", err)
	}
	if err := validateOwner(info); err != nil {
		return "", fmt.Errorf("validate state directory owner: %w", err)
	}

	return canonical, nil
}

func prepareDatabaseFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- path is derived from the validated state root.
	if err == nil {
		if closeErr := file.Close(); closeErr != nil {
			return fmt.Errorf("close new database file: %w", closeErr)
		}

		return validateDatabaseFile(path)
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create database file: %w", err)
	}

	return validateDatabaseFile(path)
}

func validateDatabaseFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect database file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("database path is not a regular file")
	}
	if err := validatePermissions(info); err != nil {
		return fmt.Errorf("validate database file permissions: %w", err)
	}
	if err := validateOwner(info); err != nil {
		return fmt.Errorf("validate database file owner: %w", err)
	}
	if err := validateSingleLink(info); err != nil {
		return fmt.Errorf("validate database file links: %w", err)
	}

	return nil
}
