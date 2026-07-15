// Package config resolves Jobman's local runtime configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const stateDirEnv = "JOBMAN_STATE_DIR"

// StateDir resolves an explicit state directory or the platform default.
func StateDir(explicit string) (string, error) {
	if explicit == "" {
		explicit = os.Getenv(stateDirEnv)
	}

	if explicit == "" {
		var err error
		explicit, err = defaultStateDir()
		if err != nil {
			return "", err
		}
	}

	absolute, err := filepath.Abs(explicit)
	if err != nil {
		return "", fmt.Errorf("resolve state directory: %w", err)
	}

	return filepath.Clean(absolute), nil
}

func defaultStateDir() (string, error) {
	if runtime.GOOS == goosWindows {
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "Jobman"), nil
		}
	}

	if runtime.GOOS != goosDarwin {
		if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
			return filepath.Join(stateHome, "jobman"), nil
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}

	switch runtime.GOOS {
	case goosDarwin:
		return filepath.Join(home, "Library", "Application Support", "jobman"), nil
	case goosWindows:
		return filepath.Join(home, "AppData", "Local", "Jobman"), nil
	default:
		return filepath.Join(home, ".local", "state", "jobman"), nil
	}
}
