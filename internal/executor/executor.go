// Package executor builds direct target processes without shell interpretation.
package executor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Request describes one direct process invocation.
type Request struct {
	Executable string
	Arguments  []string
	Directory  string
	BaseEnv    []string
	AddEnv     map[string]string
	RemoveEnv  []string
}

// Command returns an exec.Cmd with exact argument boundaries and its resolved
// executable path.
func Command(request Request) (*exec.Cmd, string, error) {
	if request.Executable == "" {
		return nil, "", errors.New("resolve executable: executable is required")
	}
	if !filepath.IsAbs(request.Directory) {
		return nil, "", errors.New("resolve executable: working directory must be absolute")
	}

	environment := MergeEnvironment(request.BaseEnv, request.AddEnv, request.RemoveEnv)
	resolved, err := Resolve(request.Executable, request.Directory, environment)
	if err != nil {
		return nil, "", err
	}

	command := &exec.Cmd{
		Path: resolved,
		Args: append([]string{request.Executable}, request.Arguments...),
		Dir:  request.Directory,
		Env:  environment,
	}

	return command, resolved, nil
}

// MergeEnvironment applies additions and removals to a base environment and
// returns stable key ordering.
func MergeEnvironment(base []string, additions map[string]string, removals []string) []string {
	values := make(map[string]string, len(base)+len(additions))
	for _, item := range base {
		name, value, ok := strings.Cut(item, "=")
		if ok && name != "" {
			values[environmentKey(name)] = name + "=" + value
		}
	}
	for _, name := range removals {
		delete(values, environmentKey(name))
	}
	for name, value := range additions {
		values[environmentKey(name)] = name + "=" + value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}

	return result
}

// Resolve finds executable using PATH from environment and directory for
// relative paths containing a separator.
func Resolve(executable, directory string, environment []string) (string, error) {
	if filepath.IsAbs(executable) || strings.ContainsRune(executable, filepath.Separator) {
		candidate := executable
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(directory, candidate)
		}

		return validateExecutable(candidate)
	}

	pathValue := environmentValue(environment, "PATH")
	for _, directory := range filepath.SplitList(pathValue) {
		if directory == "" {
			directory = "."
		}
		for _, candidate := range executableCandidates(filepath.Join(directory, executable), environment) {
			resolved, err := validateExecutable(candidate)
			if err == nil {
				return resolved, nil
			}
		}
	}

	return "", fmt.Errorf("resolve executable %q: not found in PATH", executable)
}

func validateExecutable(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect executable %q: %w", absolute, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("inspect executable %q: not a regular file", absolute)
	}
	if !isExecutable(info.Mode()) {
		return "", fmt.Errorf("inspect executable %q: permission denied", absolute)
	}

	return filepath.Clean(absolute), nil
}

func environmentValue(environment []string, wanted string) string {
	wanted = environmentKey(wanted)
	for _, item := range environment {
		name, value, ok := strings.Cut(item, "=")
		if ok && environmentKey(name) == wanted {
			return value
		}
	}

	return ""
}
