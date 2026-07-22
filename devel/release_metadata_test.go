package devel_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseMetadataSelectsSupportedStableTags(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	copyFixtureFile(t, "updates/release-metadata.sh", filepath.Join(root, "devel", "updates", "release-metadata.sh"))
	copyFixtureFile(t, "check-release.sh", filepath.Join(root, "devel", "check-release.sh"))
	writeFixtureFile(t, filepath.Join(root, "CHANGELOG.md"), `# Changelog

## [Unreleased]

### Added

- Stable v1 contract.

## [0.1.0] - 2026-07-21

### Added

- Prerelease behavior.

[Unreleased]: https://github.com/ryancswallace/jobman/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ryancswallace/jobman/releases/tag/v0.1.0
`)

	runFixtureCommand(t, root, "git", "init", "--quiet")
	runFixtureCommand(t, root, "git", "add", "CHANGELOG.md")
	runFixtureCommand(t, root, "git", "-c", "user.name=Jobman Tests", "-c", "user.email=tests@example.com", "commit", "--quiet", "-m", "test: add changelog")
	runFixtureCommand(t, root, "git", "tag", "v0.0.19")
	runFixtureCommand(t, root, "git", "tag", "v0.1.0")
	for _, tag := range []string{"v1.0.0", "v1.2.3", "v10.2.3"} {
		writeFixtureFile(t, filepath.Join(root, "candidate"), tag+"\n")
		runFixtureCommand(t, root, "git", "add", "candidate")
		runFixtureCommand(t, root, "git", "-c", "user.name=Jobman Tests", "-c", "user.email=tests@example.com", "commit", "--quiet", "-m", "feat!: freeze "+tag)
		runFixtureCommand(t, root, "git", "tag", tag)
	}
	runFixtureCommand(t, root, "git", "tag", "v11.0.0-rc.1")

	runFixtureCommand(t, root, "sh", "./devel/updates/release-metadata.sh")
	runFixtureCommand(t, root, "sh", "./devel/check-release.sh", "metadata")

	contents, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## [1.0.0] - ",
		"## [1.2.3] - ",
		"## [10.2.3] - ",
		"[Unreleased]: https://github.com/ryancswallace/jobman/compare/v10.2.3...HEAD",
		"[1.0.0]: https://github.com/ryancswallace/jobman/compare/v0.1.0...v1.0.0",
		"[1.2.3]: https://github.com/ryancswallace/jobman/compare/v1.0.0...v1.2.3",
		"[10.2.3]: https://github.com/ryancswallace/jobman/compare/v1.2.3...v10.2.3",
	} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("updated changelog does not contain %q:\n%s", want, contents)
		}
	}
	for _, unwanted := range []string{"[0.0.19]", "[11.0.0-rc.1]"} {
		if strings.Contains(string(contents), unwanted) {
			t.Errorf("unsupported tag %s was added to changelog:\n%s", unwanted, contents)
		}
	}
}

func copyFixtureFile(t *testing.T, source, destination string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, destination, string(contents))
}

func writeFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- The fixture path is assembled entirely from test-controlled temporary directories.
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runFixtureCommand(t *testing.T, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), name, arguments...) // #nosec G204 -- Test arguments are repository-controlled constants.
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
}
