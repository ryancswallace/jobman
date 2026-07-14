//go:build !windows

package executor

import (
	"io/fs"
)

func environmentKey(name string) string {
	return name
}

func executableCandidates(path string, _ []string) []string {
	return []string{path}
}

func isExecutable(mode fs.FileMode) bool {
	return mode.Perm()&0o111 != 0
}
