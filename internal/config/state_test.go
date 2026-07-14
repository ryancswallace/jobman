package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateDirExplicit(t *testing.T) {
	t.Parallel()

	dir, err := StateDir(filepath.Join(t.TempDir(), "state", "..", "jobman"))
	if err != nil {
		t.Fatalf("StateDir() error = %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("StateDir() = %q, want absolute path", dir)
	}
	if filepath.Base(dir) != "jobman" {
		t.Fatalf("StateDir() = %q, want cleaned jobman path", dir)
	}
}

func TestStateDirEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("environment path comparison differs on Windows")
	}

	want := filepath.Join(t.TempDir(), "state")
	t.Setenv(stateDirEnv, want)

	got, err := StateDir("")
	if err != nil {
		t.Fatalf("StateDir() error = %v", err)
	}
	if got != want {
		t.Fatalf("StateDir() = %q, want %q", got, want)
	}
}
