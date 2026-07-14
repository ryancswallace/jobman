package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenManpages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manPath := filepath.Join(root, "docs", "manpage")
	if err := os.MkdirAll(manPath, 0o750); err != nil {
		t.Fatalf("create man page directory: %v", err)
	}
	stalePath := filepath.Join(manPath, "jobman-obsolete.1")
	if err := os.WriteFile(stalePath, []byte("obsolete"), 0o600); err != nil {
		t.Fatalf("write stale man page: %v", err)
	}
	preservedPath := filepath.Join(manPath, "README.md")
	if err := os.WriteFile(preservedPath, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("write preserved file: %v", err)
	}

	if err := genManpages(root); err != nil {
		t.Fatalf("genManpages() error = %v", err)
	}

	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("stat stale man page error = %v, want not exist", err)
	}
	if _, err := os.Stat(preservedPath); err != nil {
		t.Fatalf("stat preserved file: %v", err)
	}

	path := filepath.Join(manPath, "jobman.1")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated man page %q: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("generated man page %q is empty", path)
	}
}
