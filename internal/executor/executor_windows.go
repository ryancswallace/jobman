//go:build windows

package executor

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

func environmentKey(name string) string {
	return strings.ToUpper(name)
}

func executableCandidates(path string, environment []string) []string {
	if filepath.Ext(path) != "" {
		return []string{path}
	}

	extensions := filepath.SplitList(strings.ReplaceAll(environmentValue(environment, "PATHEXT"), ";", string(filepath.ListSeparator)))
	if len(extensions) == 0 {
		extensions = []string{".com", ".exe", ".bat", ".cmd"}
	}
	candidates := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		if extension != "" {
			candidates = append(candidates, path+strings.ToLower(extension))
		}
	}

	return removeDuplicateStrings(candidates)
}

func isExecutable(_ fs.FileMode) bool {
	return true
}

func removeDuplicateStrings(values []string) []string {
	slices.Sort(values)

	return slices.Compact(values)
}
