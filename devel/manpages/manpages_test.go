package main

import (
	"errors"
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

func TestManpageGenerationFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if err := genManpages(blocked); err == nil {
		t.Fatal("genManpages(blocked root) error = nil")
	}
	if err := removeGeneratedManpages(filepath.Join(root, "missing")); err == nil {
		t.Fatal("removeGeneratedManpages(missing) error = nil")
	}

	manPath := filepath.Join(root, "man")
	generatedDirectory := filepath.Join(manPath, "jobman-nonempty.1")
	if err := os.MkdirAll(generatedDirectory, 0o700); err != nil {
		t.Fatalf("create generated directory fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(generatedDirectory, "child"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write child fixture: %v", err)
	}
	if err := removeGeneratedManpages(manPath); err == nil {
		t.Fatal("removeGeneratedManpages(nonempty generated directory) error = nil")
	}
	generatedRoot := filepath.Join(root, "generated")
	generatedManPath := filepath.Join(generatedRoot, "docs", "manpage", "jobman-nonempty.1")
	if err := os.MkdirAll(generatedManPath, 0o700); err != nil {
		t.Fatalf("create blocked generated tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(generatedManPath, "child"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write blocked generated child: %v", err)
	}
	if err := genManpages(generatedRoot); err == nil {
		t.Fatal("genManpages(remove failure) error = nil")
	}
}

func TestRunGeneratorReportsFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("failed")
	var got error
	runGenerator(func(string) error { return want }, func(values ...any) {
		var ok bool
		got, ok = values[0].(error)
		if !ok {
			t.Errorf("reported value type = %T, want error", values[0])
		}
	})
	if !errors.Is(got, want) {
		t.Fatalf("reported error = %v, want %v", got, want)
	}
}

func TestMainGeneratesManpagesInWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	main()
	if _, err := os.Stat(filepath.Join(root, "docs", "manpage", "jobman.1")); err != nil {
		t.Fatalf("main() did not generate root man page: %v", err)
	}
}
