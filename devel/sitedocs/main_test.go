package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ryancswallace/jobman/jobman"
)

func TestGenerateSitePublishesCompleteDeterministicTree(t *testing.T) {
	t.Parallel()

	repositoryRoot := testRepositoryRoot(t)
	outputRoot := filepath.Join(t.TempDir(), "site")
	if err := generateSite(repositoryRoot, outputRoot); err != nil {
		t.Fatalf("generateSite() error = %v", err)
	}
	for _, relative := range []string{
		"index.md",
		"_config.yml",
		"_includes/head_custom.html",
		"assets/examples/jobman.yml",
		"assets/images/logo.svg",
		"assets/images/logo-dark.svg",
		"assets/images/logo-dark-transparent.svg",
		"assets/images/favicon.svg",
		"assets/images/favicon-dark.svg",
		"guides/containers.md",
		"reference/configuration.md",
		"reference/commands/run/index.md",
		"reference/commands/completion/bash/index.md",
		"operations/security.md",
		"project/changelog.md",
	} {
		path := filepath.Join(outputRoot, filepath.FromSlash(relative))
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read generated %s: %v", relative, err)
		}
		if len(contents) == 0 {
			t.Fatalf("generated %s is empty", relative)
		}
	}
	if _, err := os.Stat(filepath.Join(outputRoot, "README.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged README stat error = %v, want not exist", err)
	}
	commandPage, err := os.ReadFile(filepath.Join(outputRoot, "reference", "commands", "run", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(commandPage), "# jobman run") || strings.Contains(string(commandPage), "Auto generated") {
		t.Fatalf("generated command page is incomplete or nondeterministic:\n%s", commandPage)
	}
	if runtime.GOOS != "windows" {
		for _, relative := range []string{
			"index.md",
			"assets/examples/jobman.yml",
			"assets/images/logo.svg",
			"assets/images/logo-dark.svg",
			"assets/images/favicon.svg",
			"assets/images/favicon-dark.svg",
			"guides/containers.md",
			"reference/commands/run/index.md",
		} {
			info, statErr := os.Stat(filepath.Join(outputRoot, filepath.FromSlash(relative)))
			if statErr != nil {
				t.Fatalf("stat generated %s: %v", relative, statErr)
			}
			if got := info.Mode().Perm(); got != 0o644 {
				t.Errorf("generated %s mode = %04o, want 0644", relative, got)
			}
		}
	}

	secondRoot := filepath.Join(t.TempDir(), "site")
	if secondErr := generateSite(repositoryRoot, secondRoot); secondErr != nil {
		t.Fatalf("second generateSite() error = %v", secondErr)
	}
	secondPage, err := os.ReadFile(filepath.Join(secondRoot, "reference", "commands", "run", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(secondPage, commandPage) {
		t.Fatal("command reference changed between identical generations")
	}
}

func TestCommandReferenceHelpers(t *testing.T) {
	t.Parallel()

	if segments := commandSegments("jobman config apply"); strings.Join(segments, "/") != "config/apply" {
		t.Fatalf("commandSegments() = %#v", segments)
	}
	if commandSegments("jobman") != nil {
		t.Fatal("commandSegments(root) != nil")
	}
	if got := commandPermalink("jobman"); got != "/reference/commands/" {
		t.Fatalf("commandPermalink(root) = %q", got)
	}
	if got := commandPermalink("jobman config show"); got != "/reference/commands/config/show/" {
		t.Fatalf("commandPermalink(nested) = %q", got)
	}
	if got := commandLink("jobman_config_show.md"); got != "{{ site.baseurl }}/reference/commands/config/show/" {
		t.Fatalf("commandLink() = %q", got)
	}

	polished := polishCommandMarkdown("## jobman\n### Synopsis\n### Examples\n### Options\n### Options inherited from parent commands\n### SEE ALSO\n")
	for _, heading := range []string{"# jobman", "## Synopsis", "## Examples", "## Options", "## Global options", "## Related commands"} {
		if !strings.Contains(polished, heading) {
			t.Errorf("polished Markdown lacks %q: %s", heading, polished)
		}
	}

	frontMatter := pageFrontMatter("run", "Command-line reference", "Reference", "/reference/commands/run/", 2, true)
	for _, field := range []string{`title: "run"`, `parent: "Command-line reference"`, `grand_parent: "Reference"`, "nav_order: 2", "has_children: true", "permalink: /reference/commands/run/"} {
		if !strings.Contains(frontMatter, field) {
			t.Errorf("front matter lacks %q: %s", field, frontMatter)
		}
	}
	minimal := pageFrontMatter("Home", "", "", "/", 0, false)
	if strings.Contains(minimal, "parent:") || strings.Contains(minimal, "nav_order:") || strings.Contains(minimal, "has_children:") {
		t.Fatalf("minimal front matter has optional fields: %s", minimal)
	}

	root := jobman.NewCommand()
	root.InitDefaultCompletionCmd()
	disableAutoGenerationDates(root)
	commands := availableCommands(root)
	if len(commands) < 20 || commands[0] != root {
		t.Fatalf("availableCommands() returned %d commands", len(commands))
	}
	for _, command := range commands {
		if !command.DisableAutoGenTag {
			t.Fatalf("%s has date generation enabled", command.CommandPath())
		}
	}
}

func TestCopyAndPublishFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := generateSite(root, filepath.Join(root, defaultSiteSource)); err == nil {
		t.Fatal("generateSite(same output) error = nil")
	}
	if err := generateSite(root, filepath.Join(root, "output")); err == nil {
		t.Fatal("generateSite(missing source) error = nil")
	}
	if err := copyAuthoredSite(filepath.Join(root, "missing"), filepath.Join(root, "copy")); err == nil {
		t.Fatal("copyAuthoredSite(missing) error = nil")
	}
	if err := copyFile(filepath.Join(root, "missing"), filepath.Join(root, "out")); err == nil {
		t.Fatal("copyFile(missing) error = nil")
	}
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockedDestination := filepath.Join(root, "blocked")
	if err := os.Mkdir(blockedDestination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(source, blockedDestination); err == nil {
		t.Fatal("copyFile(directory destination) error = nil")
	}
	blockedParent := filepath.Join(root, "blocked-parent")
	if err := os.WriteFile(blockedParent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(source, filepath.Join(blockedParent, "out")); err == nil {
		t.Fatal("copyFile(blocked parent) error = nil")
	}
	if err := publishCanonicalPage(root, filepath.Join(root, "published"), publishedPage{source: "missing.md"}); err == nil {
		t.Fatal("publishCanonicalPage(missing) error = nil")
	}
	canonical := filepath.Join(root, "canonical.md")
	if err := os.WriteFile(canonical, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := publishCanonicalPage(root, blockedParent, publishedPage{
		source: "canonical.md", destination: "nested/page.md",
	}); err == nil {
		t.Fatal("publishCanonicalPage(blocked parent) error = nil")
	}
	publishedRoot := filepath.Join(root, "published-write")
	if err := os.MkdirAll(filepath.Join(publishedRoot, "nested", "page.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publishCanonicalPage(root, publishedRoot, publishedPage{
		source: "canonical.md", destination: "nested/page.md",
	}); err == nil {
		t.Fatal("publishCanonicalPage(directory destination) error = nil")
	}
	blockedOutput := filepath.Join(root, "command-output")
	if err := os.WriteFile(blockedOutput, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generateCommandReference(blockedOutput); err == nil {
		t.Fatal("generateCommandReference(blocked output) error = nil")
	}
	commandOutput := filepath.Join(root, "command-write")
	if err := os.MkdirAll(filepath.Join(commandOutput, "index.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeCommandPage(commandOutput, jobman.NewCommand(), 1); err == nil {
		t.Fatal("writeCommandPage(directory destination) error = nil")
	}
}

func TestCopyAuthoredSiteSkipsGeneratedAndReadme(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "index.md"), []byte("index"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("readme"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(source, "_site"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "_site", "old.html"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "output")
	if err := copyAuthoredSite(source, output); err != nil {
		t.Fatalf("copyAuthoredSite() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "index.md")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"README.md", filepath.Join("_site", "old.html")} {
		if _, err := os.Stat(filepath.Join(output, relative)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat skipped %s error = %v", relative, err)
		}
	}
}

func TestValidateSiteRejectsInvalidContent(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*testing.T, string){
		"missing front matter": func(t *testing.T, root string) {
			t.Helper()
			writeTestFile(t, filepath.Join(root, "index.md"), "# Home\n")
		},
		"missing permalink": func(t *testing.T, root string) {
			t.Helper()
			writeTestFile(t, filepath.Join(root, "index.md"), "---\ntitle: Home\n---\n")
		},
		"duplicate permalink": func(t *testing.T, root string) {
			t.Helper()
			writeSitePage(t, root, "duplicate.md", "Duplicate", "/")
		},
		"missing required page": func(t *testing.T, root string) {
			t.Helper()
			if err := os.Remove(filepath.Join(root, "guides.md")); err != nil {
				t.Fatal(err)
			}
		},
		"missing required asset": func(t *testing.T, root string) {
			t.Helper()
			if err := os.Remove(filepath.Join(root, "assets", "images", "favicon-dark.svg")); err != nil {
				t.Fatal(err)
			}
		},
		"missing required include": func(t *testing.T, root string) {
			t.Helper()
			if err := os.Remove(filepath.Join(root, "_includes", "head_custom.html")); err != nil {
				t.Fatal(err)
			}
		},
		"unpinned remote theme": func(t *testing.T, root string) {
			t.Helper()
			writeTestFile(t, filepath.Join(root, "_config.yml"), "remote_theme: just-the-docs/just-the-docs\n")
		},
		"incomplete favicon include": func(t *testing.T, root string) {
			t.Helper()
			writeTestFile(t, filepath.Join(root, "_includes", "head_custom.html"), "<link href=\"/assets/images/favicon.svg\">\n")
		},
		"legacy content": func(t *testing.T, root string) {
			t.Helper()
			writeSitePage(t, root, "legacy.md", "Legacy", "/legacy/")
			appendTestFile(t, filepath.Join(root, "legacy.md"), "pip install -U jobman\n")
		},
		"missing asset": func(t *testing.T, root string) {
			t.Helper()
			appendTestFile(t, filepath.Join(root, "index.md"), "[asset]({{ site.baseurl }}/assets/missing.yml)\n")
		},
		"missing page": func(t *testing.T, root string) {
			t.Helper()
			appendTestFile(t, filepath.Join(root, "index.md"), "[missing]({{ site.baseurl }}/missing/)\n")
		},
		"relative link": func(t *testing.T, root string) {
			t.Helper()
			appendTestFile(t, filepath.Join(root, "index.md"), "[relative](other.md)\n")
		},
		"relative reference link": func(t *testing.T, root string) {
			t.Helper()
			appendTestFile(t, filepath.Join(root, "index.md"), "[relative]: other.md\n")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := newValidSiteFixture(t)
			mutate(t, root)
			if err := validateSite(root); err == nil {
				t.Fatal("validateSite() error = nil")
			}
		})
	}

	root := newValidSiteFixture(t)
	appendTestFile(t, filepath.Join(root, "index.md"), "[home]({{ site.baseurl }}/#top)\n[external](https://example.com)\n[email](mailto:help@example.com)\n[anchor](#top)\n[asset]({{ site.baseurl }}/assets/example.yml)\n")
	writeTestFile(t, filepath.Join(root, "assets", "example.yml"), "value: true\n")
	if err := validateSite(root); err != nil {
		t.Fatalf("validateSite(valid links) error = %v", err)
	}
}

func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	return root
}

func newValidSiteFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for path, permalink := range map[string]string{
		"index.md":                           "/",
		"getting-started.md":                 "/getting-started/",
		"guides.md":                          "/guides/",
		"reference.md":                       "/reference/",
		filepath.Join("reference", "run.md"): "/reference/commands/run/",
		"operations.md":                      "/operations/",
		"project.md":                         "/project/",
		"404.md":                             "/404.html",
	} {
		writeSitePage(t, root, path, path, permalink)
	}
	writeTestFile(t, filepath.Join(root, "assets", "examples", "jobman.yml"), "schema_version: 1\n")
	writeTestFile(t, filepath.Join(root, "_config.yml"), "remote_theme: just-the-docs/just-the-docs@v0.12.0\n")
	writeTestFile(t, filepath.Join(root, "_includes", "head_custom.html"), "<link href=\"/assets/images/favicon.svg\">\n<link href=\"/assets/images/favicon-dark.svg\">\n")
	for _, name := range []string{"logo.svg", "logo-dark.svg", "logo-dark-transparent.svg", "favicon.svg", "favicon-dark.svg"} {
		writeTestFile(t, filepath.Join(root, "assets", "images", name), "<svg></svg>")
	}

	return root
}

func writeSitePage(t *testing.T, root, relative, title, permalink string) {
	t.Helper()
	writeTestFile(t, filepath.Join(root, relative), "---\ntitle: "+title+"\npermalink: "+permalink+"\n---\n\n# "+title+"\n")
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendTestFile(t *testing.T, path, contents string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
