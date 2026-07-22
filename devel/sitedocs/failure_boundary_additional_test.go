package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSiteFilesystemFailureBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("remove previous output", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		blocker := filepath.Join(root, "output-parent")
		writeTestFile(t, blocker, "not a directory")
		if err := generateSite(root, filepath.Join(blocker, "output")); err == nil {
			t.Fatal("generateSite(output below regular file) error = nil")
		}
	})

	t.Run("open missing site", func(t *testing.T) {
		t.Parallel()

		if err := validateSite(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("validateSite(missing root) error = nil")
		}
	})

	t.Run("walk missing site", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		siteRoot, err := os.OpenRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = siteRoot.Close() })
		if _, _, err := collectSitePages(siteRoot, filepath.Join(root, "missing")); err == nil {
			t.Fatal("collectSitePages(missing root) error = nil")
		}
	})

	for _, test := range []struct {
		name string
		path string
	}{
		{name: "theme configuration is a directory", path: "_config.yml"},
		{name: "head include is a directory", path: filepath.Join("_includes", "head_custom.html")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := newValidSiteFixture(t)
			path := filepath.Join(root, test.path)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := validateSite(root); err == nil {
				t.Fatal("validateSite(required file replaced by directory) error = nil")
			}
		})
	}
}

func TestGenerateSiteReportsEveryStagingBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "canonical page",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(root, defaultSiteSource), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:  "sample configuration",
			setup: populateCanonicalSiteInputs,
		},
		{
			name: "site image",
			setup: func(t *testing.T, root string) {
				t.Helper()
				populateCanonicalSiteInputs(t, root)
				writeTestFile(t, filepath.Join(root, "etc", "jobman", "jobman.yml"), "schema_version: 1\n")
			},
		},
		{
			name: "command reference",
			setup: func(t *testing.T, root string) {
				t.Helper()
				populateCompleteStaticInputs(t, root)
				writeTestFile(t, filepath.Join(root, defaultSiteSource, "reference", "commands"), "blocked")
			},
		},
		{
			name:  "site validation",
			setup: populateCompleteStaticInputs,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			test.setup(t, root)
			if err := generateSite(root, filepath.Join(root, "output")); err == nil {
				t.Fatal("generateSite(incomplete staging inputs) error = nil")
			}
		})
	}
}

func populateCanonicalSiteInputs(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, defaultSiteSource), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, page := range publishedPages {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(page.source)), "# Canonical page\n")
	}
}

func populateCompleteStaticInputs(t *testing.T, root string) {
	t.Helper()
	populateCanonicalSiteInputs(t, root)
	writeTestFile(t, filepath.Join(root, "etc", "jobman", "jobman.yml"), "schema_version: 1\n")
	for _, name := range []string{"logo.svg", "logo-dark.svg", "logo-dark-transparent.svg", "favicon.svg", "favicon-dark.svg"} {
		writeTestFile(t, filepath.Join(root, "assets", name), "<svg></svg>")
	}
}
