//go:build !windows

package liveinput

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalTransportFilesystemFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	blockingParent := filepath.Join(root, "file")
	if err := os.WriteFile(blockingParent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenLocal(filepath.Join(blockingParent, "input.sock")); err == nil {
		t.Fatal("listenLocal(blocked parent) error = nil")
	}

	staleDirectory := filepath.Join(root, "stale.sock")
	if err := os.Mkdir(staleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staleDirectory, "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenLocal(staleDirectory); err == nil {
		t.Fatal("listenLocal(nonempty stale directory) error = nil")
	}

	tooLong := filepath.Join(root, strings.Repeat("x", 200)+".sock")
	if _, err := listenLocal(tooLong); err == nil {
		t.Fatal("listenLocal(overlong path) error = nil")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := dialLocal(ctx, filepath.Join(root, "missing.sock")); !errors.Is(err, context.Canceled) {
		t.Fatalf("dialLocal(canceled) error = %v", err)
	}
	if err := removeLocal(filepath.Join(root, "missing.sock")); err != nil {
		t.Fatalf("removeLocal(missing) error = %v", err)
	}
	if err := removeLocal(staleDirectory); err == nil {
		t.Fatal("removeLocal(nonempty directory) error = nil")
	}
}
