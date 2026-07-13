package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenAutocomplete(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := genAutocomplete(root); err != nil {
		t.Fatalf("genAutocomplete() error = %v", err)
	}

	paths := []string{
		filepath.Join(root, "docs", "completions", "bash", "jobman"),
		filepath.Join(root, "docs", "completions", "powershell", "jobman.ps1"),
		filepath.Join(root, "docs", "completions", "zsh", "_jobman"),
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat generated completion %q: %v", path, err)

			continue
		}
		if info.Size() == 0 {
			t.Errorf("generated completion %q is empty", path)
		}
	}
}
