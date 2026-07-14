//go:build !windows

package logstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPathsArePrivate(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 3)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}
	paths := run.Paths()
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	for _, path := range []string{
		stateDir,
		filepath.Join(stateDir, "logs"),
		filepath.Join(stateDir, "logs", testJobID),
		paths.Directory,
	} {
		assertMode(t, path, directoryMode)
	}
	for _, path := range []string{paths.Stdout, paths.Stderr, paths.Index} {
		assertMode(t, path, fileMode)
	}

	if chmodErr := os.Chmod(paths.Stdout, 0o644); chmodErr != nil { //nolint:gosec // Test deliberately creates unsafe permissions.
		t.Fatalf("chmod stdout log: %v", chmodErr)
	}
	if _, openErr := OpenRun(stateDir, testJobID, 3); !errors.Is(openErr, ErrUnsafePath) {
		t.Fatalf("OpenRun() error = %v, want ErrUnsafePath", openErr)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %q = %04o, want %04o", path, got, want)
	}
}
