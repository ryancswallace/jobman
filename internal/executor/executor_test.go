package executor

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestMergeEnvironment(t *testing.T) {
	t.Parallel()

	got := MergeEnvironment(
		[]string{"B=old", "A=one", "REMOVE=yes"},
		map[string]string{"B": "two", "C": "three"},
		[]string{"REMOVE"},
	)
	want := []string{"A=one", "B=two", "C=three"}
	if !slices.Equal(got, want) {
		t.Fatalf("MergeEnvironment() = %#v, want %#v", got, want)
	}
}

func TestCommandPreservesArguments(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	directory := t.TempDir()
	arguments := []string{"a b", "$HOME", "x;y", `quote"value`}
	command, resolved, err := Command(Request{
		Executable: executable,
		Arguments:  arguments,
		Directory:  directory,
		BaseEnv:    os.Environ(),
	})
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	if resolved != filepath.Clean(executable) {
		t.Fatalf("Command() resolved = %q, want %q", resolved, executable)
	}
	if !slices.Equal(command.Args[1:], arguments) {
		t.Fatalf("Command().Args = %#v, want %#v", command.Args[1:], arguments)
	}
	if command.Dir != directory {
		t.Fatalf("Command().Dir = %q, want %q", command.Dir, directory)
	}
}

func TestResolveUsesProvidedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture mode is Unix-specific")
	}

	directory := t.TempDir()
	executable := filepath.Join(directory, "helper")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	// #nosec G302 -- this test fixture must be executable by its owner.
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatalf("make fixture executable: %v", err)
	}

	resolved, err := Resolve("helper", directory, []string{"PATH=" + directory})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved != executable {
		t.Fatalf("Resolve() = %q, want %q", resolved, executable)
	}
}
