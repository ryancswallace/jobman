package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverDependenciesUnionsTargetsAndReplacements(t *testing.T) {
	t.Parallel()

	temporary := t.TempDir()
	outputs := map[string]string{
		"linux": `{"ImportPath":"standard/library"}
{"ImportPath":"example.com/root","Module":{"Path":"example.com/root","Main":true,"Dir":"/root"}}
{"ImportPath":"example.com/one/pkg","Module":{"Path":"example.com/one","Version":"v1.2.3","Dir":"` + temporary + `"}}
`,
		"windows": `{"ImportPath":"example.com/one/pkg","Module":{"Path":"example.com/one","Version":"v1.2.3","Dir":"` + temporary + `"}}
{"ImportPath":"example.com/two/pkg","Module":{"Path":"example.com/two","Version":"v2.0.0","Dir":"/unused","Replace":{"Path":"example.net/fork","Version":"v2.0.1","Dir":"` + temporary + `"}}}
`,
	}
	runner := func(_ context.Context, target target) ([]byte, error) {
		return []byte(outputs[target.goos]), nil
	}

	got, err := discoverDependencies(t.Context(), []target{
		{goos: "linux", goarch: "amd64"},
		{goos: "windows", goarch: "amd64"},
	}, runner)
	if err != nil {
		t.Fatalf("discoverDependencies() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("discoverDependencies() returned %d modules, want 2: %#v", len(got), got)
	}
	if got[0].path != "example.com/one" || got[0].version != "v1.2.3" {
		t.Fatalf("first dependency = %#v", got[0])
	}
	if got[1].path != "example.com/two" || got[1].dir != temporary ||
		got[1].replacement != "example.net/fork v2.0.1" {
		t.Fatalf("replacement dependency = %#v", got[1])
	}
}

func TestReleaseTargetsMatchGoReleaserBuildMatrix(t *testing.T) {
	t.Parallel()

	want := map[target]bool{
		{goos: releaseOSDarwin, goarch: releaseArchitecture}:       true,
		{goos: releaseOSDarwin, goarch: releaseArchitectureARM64}:  true,
		{goos: releaseOSLinux, goarch: releaseArchitecture386}:     true,
		{goos: releaseOSLinux, goarch: releaseArchitecture}:        true,
		{goos: releaseOSLinux, goarch: releaseArchitectureARM64}:   true,
		{goos: releaseOSWindows, goarch: releaseArchitecture386}:   true,
		{goos: releaseOSWindows, goarch: releaseArchitecture}:      true,
		{goos: releaseOSWindows, goarch: releaseArchitectureARM64}: true,
	}
	if len(releaseTargets) != len(want) {
		t.Fatalf("releaseTargets contains %d entries, want %d", len(releaseTargets), len(want))
	}
	for _, target := range releaseTargets {
		if !want[target] {
			t.Errorf("unexpected release target: %#v", target)
		}
		delete(want, target)
	}
	if len(want) != 0 {
		t.Errorf("missing release targets: %#v", want)
	}
}

func TestExecute(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	moduleDirectory := filepath.Join(directory, "module")
	if err := os.Mkdir(moduleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, moduleDirectory, "LICENSE", "terms\n")
	runner := func(context.Context, target) ([]byte, error) {
		return []byte(`{"Module":{"Path":"example.com/module","Version":"v1.0.0","Dir":"` + moduleDirectory + `"}}`), nil
	}
	output := filepath.Join(directory, "notices.md")
	var stderr bytes.Buffer
	if code := execute(t.Context(), []string{"-output", output}, &stderr, runner); code != 0 {
		t.Fatalf("execute(generate) = %d, stderr = %q", code, stderr.String())
	}
	if code := execute(t.Context(), []string{"-check", "-output", output}, &stderr, runner); code != 0 {
		t.Fatalf("execute(check) = %d, stderr = %q", code, stderr.String())
	}
	if code := execute(t.Context(), []string{"-help"}, &stderr, runner); code != 0 {
		t.Errorf("execute(help) = %d", code)
	}
	if code := execute(t.Context(), []string{"-unknown"}, &stderr, runner); code != 2 {
		t.Errorf("execute(bad flag) = %d, want 2", code)
	}
	if code := execute(t.Context(), []string{"extra"}, &stderr, runner); code != 2 {
		t.Errorf("execute(positional argument) = %d, want 2", code)
	}
	failure := func(context.Context, target) ([]byte, error) {
		return nil, errors.New("injected list error")
	}
	if code := execute(t.Context(), []string{"-output", output}, &stderr, failure); code != 1 {
		t.Errorf("execute(failure) = %d, want 1", code)
	}
}

func TestRunGoListAndStandardLibraryNotice(t *testing.T) {
	t.Parallel()

	data, err := runGoList(t.Context(), target{goos: "linux", goarch: releaseArchitecture})
	if err != nil {
		t.Fatalf("runGoList() error = %v", err)
	}
	if !bytes.Contains(data, []byte(`"Module"`)) {
		t.Fatalf("runGoList() did not return module metadata")
	}
	if _, invalidErr := runGoList(t.Context(), target{goos: "not-an-operating-system", goarch: releaseArchitecture}); invalidErr == nil || !strings.Contains(invalidErr.Error(), "list not-an-operating-system") {
		t.Fatalf("runGoList(invalid target) error = %v", invalidErr)
	}
	notice, err := loadStandardLibraryNotice(t.Context())
	if err != nil {
		t.Fatalf("loadStandardLibraryNotice() error = %v", err)
	}
	if notice.version == "" || len(notice.files) != 2 || notice.files[0].name != "LICENSE" ||
		notice.files[1].name != "PATENTS" || !strings.Contains(notice.files[0].text, "Redistribution") {
		t.Fatalf("standard library notice = %#v", notice)
	}
}

func TestDependencyFromModuleValidation(t *testing.T) {
	t.Parallel()

	for _, module := range []*moduleJSON{nil, {Main: true}} {
		if _, include, err := dependencyFromModule(module); err != nil || include {
			t.Errorf("dependencyFromModule(%#v) = include %v, error %v", module, include, err)
		}
	}
	if _, _, err := dependencyFromModule(&moduleJSON{Path: "example.com/missing"}); err == nil ||
		!strings.Contains(err.Error(), "go mod download") {
		t.Fatalf("dependencyFromModule(missing directory) error = %v", err)
	}
	dep, include, err := dependencyFromModule(&moduleJSON{
		Path: "example.com/original",
		Replace: &moduleJSON{
			Path: "../local", Dir: t.TempDir(),
		},
	})
	if err != nil || !include || dep.replacement != "../local" {
		t.Fatalf("dependencyFromModule(local replacement) = (%#v, %v, %v)", dep, include, err)
	}
}

func TestDiscoverDependenciesReportsRunnerAndJSONErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("list failed")
	_, err := discoverDependencies(t.Context(), []target{{goos: "linux"}},
		func(context.Context, target) ([]byte, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Fatalf("runner error = %v, want %v", err, want)
	}

	_, err = discoverDependencies(t.Context(), []target{{goos: "linux"}},
		func(context.Context, target) ([]byte, error) { return []byte("{"), nil })
	if err == nil || !strings.Contains(err.Error(), "decode go list output") {
		t.Fatalf("JSON error = %v", err)
	}
}

func TestFindNoticeFiles(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	writeTestFile(t, directory, "NOTICE", "notice\r\n")
	writeTestFile(t, directory, "LICENSE.txt", "license\rbody\n")
	writeTestFile(t, directory, "README.md", "not included")
	if err := os.Mkdir(filepath.Join(directory, "LICENSES"), 0o700); err != nil {
		t.Fatal(err)
	}

	files, err := findNoticeFiles(directory)
	if err != nil {
		t.Fatalf("findNoticeFiles() error = %v", err)
	}
	if len(files) != 2 || files[0].name != "LICENSE.txt" || files[1].name != "NOTICE" {
		t.Fatalf("findNoticeFiles() = %#v", files)
	}
	if files[0].text != "license\nbody\n" {
		t.Fatalf("normalized license = %q", files[0].text)
	}
}

func TestFindNoticeFilesErrors(t *testing.T) {
	t.Parallel()

	if _, err := findNoticeFiles(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("findNoticeFiles(missing) error = nil")
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "LICENSE"), []byte{0xff}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findNoticeFiles(directory); err == nil || !strings.Contains(err.Error(), "not UTF-8") {
		t.Fatalf("findNoticeFiles(invalid UTF-8) error = %v", err)
	}
	brokenDirectory := t.TempDir()
	if err := os.Symlink(filepath.Join(brokenDirectory, "missing"), filepath.Join(brokenDirectory, "LICENSE")); err != nil {
		t.Skipf("cannot create a broken test symlink: %v", err)
	}
	if _, err := findNoticeFiles(brokenDirectory); err == nil || !strings.Contains(err.Error(), "read LICENSE") {
		t.Fatalf("findNoticeFiles(broken symlink) error = %v", err)
	}
}

func TestRenderNotices(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	writeTestFile(t, directory, "LICENSE", "line one\n\nline two\n")

	got, err := renderNotices(standardLibraryNotice{version: "go1.26.5", files: []noticeFile{
		{name: "LICENSE", text: "Go terms\n"},
		{name: "PATENTS", text: "Patent terms\n"},
	}}, []dependency{{
		path: "example.com/module", version: "v1.0.0", dir: directory,
		replacement: "example.net/fork v1.0.1",
	}})
	if err != nil {
		t.Fatalf("renderNotices() error = %v", err)
	}
	for _, want := range []string{
		"Code generated by go run ./devel/thirdpartynotices",
		"## Go standard library go1.26.5",
		"Source: <https://go.dev/LICENSE>",
		"Source: <https://go.dev/PATENTS>",
		"## example.com/module v1.0.0",
		"Module: <https://pkg.go.dev/example.com/module@v1.0.0>",
		"Replaced by: `example.net/fork v1.0.1`",
		"### LICENSE\n\n    line one\n\n    line two\n",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("rendered notices do not contain %q:\n%s", want, got)
		}
	}
}

func TestRenderNoticesRejectsMissingLicense(t *testing.T) {
	t.Parallel()

	_, err := renderNotices(standardLibraryNotice{version: "go1.26.5", files: []noticeFile{
		{name: "LICENSE", text: "Go terms\n"},
		{name: "PATENTS", text: "Patent terms\n"},
	}}, []dependency{{
		path: "example.com/module", version: "v1.0.0", dir: t.TempDir(),
	}})
	if err == nil || !strings.Contains(err.Error(), "has no root LICENSE") {
		t.Fatalf("renderNotices() error = %v", err)
	}
}

func TestRunWritesAndChecksOutput(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	moduleDirectory := filepath.Join(directory, "module")
	if err := os.Mkdir(moduleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, moduleDirectory, "LICENSE", "terms\n")
	output := filepath.Join(directory, "notices.md")
	runner := func(context.Context, target) ([]byte, error) {
		return []byte(`{"Module":{"Path":"example.com/module","Version":"v1.0.0","Dir":"` + moduleDirectory + `"}}`), nil
	}

	if err := run(t.Context(), output, false, runner); err != nil {
		t.Fatalf("run(generate) error = %v", err)
	}
	if err := run(t.Context(), output, true, runner); err != nil {
		t.Fatalf("run(check) error = %v", err)
	}
	if err := os.WriteFile(output, []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), output, true, runner); err == nil ||
		!strings.Contains(err.Error(), "is stale") {
		t.Fatalf("run(stale check) error = %v", err)
	}
	if err := run(t.Context(), output, false, runner); err != nil {
		t.Fatalf("run(regenerate) error = %v", err)
	}
}

func TestRunReportsMissingCheckFileAndOutputDirectory(t *testing.T) {
	t.Parallel()

	runner := func(context.Context, target) ([]byte, error) { return nil, nil }
	missing := filepath.Join(t.TempDir(), "missing", "notices.md")
	if err := run(t.Context(), missing, true, runner); err == nil ||
		!strings.Contains(err.Error(), "read ") {
		t.Fatalf("run(missing check file) error = %v", err)
	}
	if err := run(t.Context(), missing, false, runner); err == nil ||
		!strings.Contains(err.Error(), "write ") {
		t.Fatalf("run(missing output directory) error = %v", err)
	}
}

func TestRunRejectsModuleWithoutNoticeFile(t *testing.T) {
	t.Parallel()

	moduleDirectory := t.TempDir()
	runner := func(context.Context, target) ([]byte, error) {
		return []byte(`{"Module":{"Path":"example.com/module","Version":"v1.0.0","Dir":"` + moduleDirectory + `"}}`), nil
	}
	err := run(t.Context(), filepath.Join(t.TempDir(), "notices.md"), false, runner)
	if err == nil || !strings.Contains(err.Error(), "has no root LICENSE") {
		t.Fatalf("run(module without notice) error = %v", err)
	}
}

func TestDecodeDependenciesRejectsMissingModuleDirectory(t *testing.T) {
	t.Parallel()

	_, err := decodeDependencies(
		[]byte(`{"Module":{"Path":"example.com/missing","Version":"v1.0.0"}}`),
		target{goos: "linux", goarch: releaseArchitecture},
	)
	if err == nil || !strings.Contains(err.Error(), "go mod download") {
		t.Fatalf("decodeDependencies(missing directory) error = %v", err)
	}
}

func TestNoticeFilename(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"LICENSE", "license.md", britishLicenseName + "-MIT", "COPYING_3RD", "NOTICE.txt", "PATENTS",
	} {
		if !isNoticeFilename(name) {
			t.Errorf("isNoticeFilename(%q) = false", name)
		}
	}
	for _, name := range []string{"README.md", "UNLICENSE", "LICENSES", "NOTICEBOARD"} {
		if isNoticeFilename(name) {
			t.Errorf("isNoticeFilename(%q) = true", name)
		}
	}
}

func writeTestFile(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
