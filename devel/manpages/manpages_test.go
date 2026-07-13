package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenManpages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := genManpages(root); err != nil {
		t.Fatalf("genManpages() error = %v", err)
	}

	path := filepath.Join(root, "docs", "manpage", "jobman.1")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat generated man page %q: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("generated man page %q is empty", path)
	}
}
