package main

import (
	"errors"
	"io"
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

func TestWriteCompletionFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if err := writeCompletion(completion{
		path: filepath.Join(blocked, "completion"),
	}); err == nil {
		t.Fatal("writeCompletion(blocked parent) error = nil")
	}
	if err := genAutocomplete(blocked); err == nil {
		t.Fatal("genAutocomplete(blocked root) error = nil")
	}

	generateFailure := errors.New("generate failed")
	path := filepath.Join(root, "completion")
	if err := writeCompletion(completion{
		path: path,
		generate: func(io.Writer) error {
			return generateFailure
		},
	}); !errors.Is(err, generateFailure) {
		t.Fatalf("writeCompletion(generate failure) error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove failed completion: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("replace completion with directory: %v", err)
	}
	if err := writeCompletion(completion{path: path}); err == nil {
		t.Fatal("writeCompletion(directory target) error = nil")
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

func TestMainGeneratesInWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	main()
	if _, err := os.Stat(filepath.Join(root, "docs", "completions", "bash", "jobman")); err != nil {
		t.Fatalf("main() did not generate bash completion: %v", err)
	}
}
