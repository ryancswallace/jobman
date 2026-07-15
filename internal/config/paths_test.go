package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultConfigPathsHonorsXDG(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("XDG path applies to Unix platforms other than macOS")
	}

	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	systemPath, userPath, err := DefaultConfigPaths()
	if err != nil {
		t.Fatalf("DefaultConfigPaths() error = %v", err)
	}
	if systemPath != "/etc/jobman/jobman.yml" {
		t.Fatalf("systemPath = %q", systemPath)
	}
	wantUser := filepath.Join(configHome, "jobman", "jobman.yml")
	if userPath != wantUser {
		t.Fatalf("userPath = %q, want %q", userPath, wantUser)
	}
}

func TestFindTrustedProjectConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "src", "package")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	configPath := filepath.Join(root, projectConfigName)
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	path, found, err := FindTrustedProjectConfig(nested, []string{root})
	if err != nil {
		t.Fatalf("FindTrustedProjectConfig() error = %v", err)
	}
	if !found || path != configPath {
		t.Fatalf("FindTrustedProjectConfig() = (%q, %v), want (%q, true)", path, found, configPath)
	}

	_, found, err = FindTrustedProjectConfig(nested, nil)
	if err != nil {
		t.Fatalf("FindTrustedProjectConfig(untrusted) error = %v", err)
	}
	if found {
		t.Fatal("FindTrustedProjectConfig(untrusted) found project configuration")
	}
}

func TestFindTrustedProjectConfigCanonicalizesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation generally requires additional privileges on Windows")
	}

	parent := t.TempDir()
	root := filepath.Join(parent, "real")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, projectConfigName), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	path, found, err := FindTrustedProjectConfig(link, []string{link})
	if err != nil {
		t.Fatalf("FindTrustedProjectConfig() error = %v", err)
	}
	want := filepath.Join(root, projectConfigName)
	if !found || path != want {
		t.Fatalf("FindTrustedProjectConfig() = (%q, %v), want (%q, true)", path, found, want)
	}
}

func TestTrustedProjectSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, projectConfigName), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	source, found, err := TrustedProjectSource(root, []string{root})
	if err != nil {
		t.Fatalf("TrustedProjectSource() error = %v", err)
	}
	if !found || source.Kind != SourceProject || source.Path == "" {
		t.Fatalf("TrustedProjectSource() = (%#v, %v)", source, found)
	}
}

func TestDiscoverSourcesAppliesTrustAndPrecedence(t *testing.T) {
	if runtime.GOOS == goosDarwin || runtime.GOOS == goosWindows {
		t.Skip("test fixture uses XDG configuration discovery")
	}

	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	project := t.TempDir()
	nested := filepath.Join(project, "subdir")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	userDirectory := filepath.Join(configHome, "jobman")
	if err := os.Mkdir(userDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	userYAML := []byte("trusted_project_roots: [" + project + "]\n")
	if err := os.WriteFile(filepath.Join(userDirectory, "jobman.yml"), userYAML, 0o600); err != nil {
		t.Fatalf("WriteFile(user) error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(project, projectConfigName),
		[]byte("concurrency:\n  max_active_slots: 2\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(project) error = %v", err)
	}
	explicit := filepath.Join(t.TempDir(), "explicit.yml")
	if err := os.WriteFile(explicit, []byte("concurrency:\n  max_active_slots: 3\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(explicit) error = %v", err)
	}

	sources, err := DiscoverSources(DiscoveryOptions{
		ExplicitPath:   explicit,
		ProjectStart:   nested,
		Environment:    []string{"JOBMAN_CONCURRENCY_MAX_ACTIVE_SLOTS=4"},
		UseEnvironment: true,
	})
	if err != nil {
		t.Fatalf("DiscoverSources() error = %v", err)
	}
	loaded, err := Load(sources...)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if slots, finite := loaded.Config.Concurrency.MaxActiveSlots.Value(); !finite || slots != 4 {
		t.Fatalf("MaxActiveSlots = (%d, %v), want (4, true)", slots, finite)
	}
	if len(loaded.Sources) < 5 {
		t.Fatalf("len(Sources) = %d, want at least 5 loaded sources", len(loaded.Sources))
	}
	wantKinds := []SourceKind{SourceUser, SourceProject, SourceExplicit, SourceEnvironment}
	for index, want := range wantKinds {
		got := loaded.Sources[len(loaded.Sources)-len(wantKinds)+index].Kind
		if got != want {
			t.Fatalf("source kind at tail index %d = %q, want %q", index, got, want)
		}
	}
}

func TestPathDiscoveryFailureAndComparisonEdges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file fixture: %v", err)
	}
	if _, err := canonicalDirectory(file); err == nil {
		t.Fatal("canonicalDirectory(file) error = nil")
	}
	if _, err := canonicalDirectory(filepath.Join(root, "missing")); err == nil {
		t.Fatal("canonicalDirectory(missing) error = nil")
	}
	if !sameConfigPath(file, filepath.Join(root, ".", "file")) {
		t.Fatal("sameConfigPath(equivalent) = false")
	}
	if sameConfigPath(file, "") || sameConfigPath(file, filepath.Join(root, "other")) {
		t.Fatal("sameConfigPath() accepted empty or distinct path")
	}

	projectDirectory := filepath.Join(root, projectConfigName)
	if err := os.Mkdir(projectDirectory, 0o700); err != nil {
		t.Fatalf("create directory project config: %v", err)
	}
	if _, _, err := FindTrustedProjectConfig(root, []string{root}); err == nil {
		t.Fatal("FindTrustedProjectConfig(directory config) error = nil")
	}
	if _, _, err := FindTrustedProjectConfig(root, []string{filepath.Join(root, "missing")}); err == nil {
		t.Fatal("FindTrustedProjectConfig(missing trusted root) error = nil")
	}
	if source, found, err := TrustedProjectSource(t.TempDir(), nil); err != nil || found || source.Kind != "" {
		t.Fatalf("TrustedProjectSource(not found) = %#v, %t, %v", source, found, err)
	}
}
