package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const projectConfigName = ".jobman.yml"

// DiscoveryOptions controls secure configuration source discovery.
type DiscoveryOptions struct {
	ExplicitPath   string
	ProjectStart   string
	Environment    []string
	UseEnvironment bool
}

// DefaultConfigPaths returns the system and per-user YAML paths for this platform.
func DefaultConfigPaths() (systemPath, userPath string, err error) {
	userDirectory, err := defaultUserConfigDir()
	if err != nil {
		return "", "", err
	}

	return defaultSystemConfigPath(), filepath.Join(userDirectory, "jobman.yml"), nil
}

// DefaultFileSources returns optional system and user sources in precedence order.
func DefaultFileSources() ([]Source, error) {
	systemPath, userPath, err := DefaultConfigPaths()
	if err != nil {
		return nil, err
	}

	return []Source{
		OptionalFileSource(SourceSystem, systemPath),
		OptionalFileSource(SourceUser, userPath),
	}, nil
}

// DiscoverSources returns all selected sources in normative precedence order.
// Project discovery is opt-in through ProjectStart and uses only trust roots
// supplied by user or explicitly selected configuration, never system/project
// configuration.
func DiscoverSources(options DiscoveryOptions) ([]Source, error) {
	base, err := DefaultFileSources()
	if err != nil {
		return nil, err
	}
	preliminary := append([]Source(nil), base...)
	var explicit Source
	if options.ExplicitPath != "" {
		explicit = FileSource(SourceExplicit, options.ExplicitPath)
		preliminary = append(preliminary, explicit)
	}
	loaded, err := Load(preliminary...)
	if err != nil {
		return nil, fmt.Errorf("load configuration for project trust discovery: %w", err)
	}

	sources := append([]Source(nil), base...)
	if options.ProjectStart != "" {
		project, found, projectErr := TrustedProjectSource(options.ProjectStart, loaded.Config.TrustedProjectRoots)
		if projectErr != nil {
			return nil, projectErr
		}
		if found && !sameConfigPath(project.Path, options.ExplicitPath) {
			sources = append(sources, project)
		}
	}
	if options.ExplicitPath != "" {
		sources = append(sources, explicit)
	}
	if options.UseEnvironment {
		environ := options.Environment
		if environ == nil {
			environ = os.Environ()
		}
		environment, found, environmentErr := EnvironmentSource(environ)
		if environmentErr != nil {
			return nil, environmentErr
		}
		if found {
			sources = append(sources, environment)
		}
	}

	return sources, nil
}

// FindTrustedProjectConfig searches from start toward the filesystem root. A
// candidate is returned only when its canonical directory exactly matches a
// canonical, explicitly trusted project root.
func FindTrustedProjectConfig(start string, trustedRoots []string) (path string, found bool, err error) {
	current, err := canonicalDirectory(start)
	if err != nil {
		return "", false, fmt.Errorf("resolve project search directory: %w", err)
	}
	trusted := make(map[string]struct{}, len(trustedRoots))
	for _, root := range trustedRoots {
		canonical, err := canonicalDirectory(root)
		if err != nil {
			return "", false, fmt.Errorf("resolve trusted project root %q: %w", root, err)
		}
		trusted[canonical] = struct{}{}
	}

	for {
		candidate := filepath.Join(current, projectConfigName)
		info, statErr := os.Lstat(candidate)
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return "", false, fmt.Errorf("project configuration %q is not a regular file", candidate)
			}
			if _, allowed := trusted[current]; allowed {
				return candidate, true, nil
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", false, fmt.Errorf("inspect project configuration %q: %w", candidate, statErr)
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return "", false, nil
}

// TrustedProjectSource creates a project source when a trusted config is found.
func TrustedProjectSource(start string, trustedRoots []string) (Source, bool, error) {
	path, found, err := FindTrustedProjectConfig(start, trustedRoots)
	if err != nil || !found {
		return Source{}, found, err
	}

	return FileSource(SourceProject, path), true, nil
}

func defaultUserConfigDir() (string, error) {
	if runtime.GOOS == goosWindows {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Jobman"), nil
		}
	}
	if runtime.GOOS != goosDarwin {
		if configHome := os.Getenv("XDG_CONFIG_HOME"); configHome != "" {
			return filepath.Join(configHome, "jobman"), nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory for configuration: %w", err)
	}

	switch runtime.GOOS {
	case goosDarwin:
		return filepath.Join(home, "Library", "Application Support", "jobman"), nil
	case goosWindows:
		return filepath.Join(home, "AppData", "Roaming", "Jobman"), nil
	default:
		return filepath.Join(home, ".config", "jobman"), nil
	}
}

func defaultSystemConfigPath() string {
	switch runtime.GOOS {
	case goosDarwin:
		return filepath.Join(string(filepath.Separator), "Library", "Application Support", "jobman", "jobman.yml")
	case goosWindows:
		if programData := os.Getenv("ProgramData"); programData != "" {
			return filepath.Join(programData, "Jobman", "jobman.yml")
		}

		return filepath.Join("C:"+string(filepath.Separator), "ProgramData", "Jobman", "jobman.yml")
	default:
		return filepath.Join(string(filepath.Separator), "etc", "jobman", "jobman.yml")
	}
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}

	return filepath.Clean(resolved), nil
}

func sameConfigPath(first, second string) bool {
	if first == "" || second == "" {
		return false
	}
	firstAbsolute, firstErr := filepath.Abs(first)
	secondAbsolute, secondErr := filepath.Abs(second)
	if firstErr != nil || secondErr != nil {
		return false
	}

	return filepath.Clean(firstAbsolute) == filepath.Clean(secondAbsolute)
}
