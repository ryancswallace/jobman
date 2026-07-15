package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderReleaseMetadata(test *testing.T) {
	test.Parallel()
	source := []byte("cff-version: 1.2.0\nversion: 0.5.0\ndate-released: 2026-07-13\n")
	got, err := render(source, "1.2.3-rc.1", time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		test.Fatal(err)
	}
	want := "cff-version: 1.2.0\nversion: 1.2.3-rc.1\ndate-released: 2026-07-15\n"
	if string(got) != want {
		test.Fatalf("render() = %q, want %q", got, want)
	}
}

func TestRunWritesGeneratedCitationWithoutChangingInput(test *testing.T) {
	test.Parallel()
	directory := test.TempDir()
	input := filepath.Join(directory, "CITATION.cff")
	output := filepath.Join(directory, "release", "CITATION.cff")
	source := "version: 0.5.0\ndate-released: 2026-07-13\n"
	if err := os.WriteFile(input, []byte(source), 0o600); err != nil {
		test.Fatal(err)
	}
	if err := run(options{
		input: input, output: output, version: "1.0.0", date: "2026-07-15T12:34:56Z",
	}); err != nil {
		test.Fatal(err)
	}
	rendered, err := os.ReadFile(output)
	if err != nil {
		test.Fatal(err)
	}
	if !strings.Contains(string(rendered), "version: 1.0.0\n") ||
		!strings.Contains(string(rendered), "date-released: 2026-07-15\n") {
		test.Fatalf("generated citation = %q", rendered)
	}
	unchanged, err := os.ReadFile(input)
	if err != nil {
		test.Fatal(err)
	}
	if string(unchanged) != source {
		test.Fatalf("input changed to %q", unchanged)
	}
}

func TestRenderRejectsIncompleteTemplate(test *testing.T) {
	test.Parallel()
	if _, err := render([]byte("version: 0.5.0\n"), "1.0.0", time.Now()); err == nil {
		test.Fatal("render() error = nil")
	}
}

func TestParseReleaseDateRejectsInvalidValue(test *testing.T) {
	test.Parallel()
	if _, err := parseReleaseDate("not-a-date"); err == nil {
		test.Fatal("parseReleaseDate() error = nil")
	}
}

func TestExecuteSuccessAndFailures(test *testing.T) {
	test.Parallel()
	directory := test.TempDir()
	input := filepath.Join(directory, "CITATION.cff")
	if err := os.WriteFile(input, []byte("version: 0.5.0\ndate-released: 2026-07-13\n"), 0o600); err != nil {
		test.Fatal(err)
	}
	tests := []struct {
		name      string
		arguments []string
		wantCode  int
	}{
		{name: "success", arguments: []string{
			"-input=" + input, "-output=" + filepath.Join(directory, "out.cff"),
			"-version=1.0.0", "-date=2026-07-15",
		}},
		{name: "flag", arguments: []string{"-unknown"}, wantCode: 2},
		{name: "run", arguments: []string{"-input=" + input}, wantCode: 1},
	}
	for _, item := range tests {
		test.Run(item.name, func(test *testing.T) {
			test.Parallel()
			stderr := &bytes.Buffer{}
			if got := execute(item.arguments, stderr); got != item.wantCode {
				test.Fatalf("execute() = %d, want %d; stderr=%q", got, item.wantCode, stderr)
			}
		})
	}
}

func TestRunRejectsInvalidInputsAndOutput(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	template := filepath.Join(directory, "CITATION.cff")
	if err := os.WriteFile(template, []byte("version: 0.5.0\ndate-released: 2026-07-13\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []options{
		{input: template, output: filepath.Join(directory, "out"), version: "v1.0.0", date: "2026-07-15"},
		{input: template, output: filepath.Join(directory, "out"), version: "1.0.0", date: "bad"},
		{input: filepath.Join(directory, "missing"), output: filepath.Join(directory, "out"), version: "1.0.0", date: "2026-07-15"},
		{input: template, output: directory, version: "1.0.0", date: "2026-07-15"},
	}
	for _, configuration := range tests {
		if err := run(configuration); err == nil {
			t.Errorf("run(%+v) error = nil", configuration)
		}
	}
}
