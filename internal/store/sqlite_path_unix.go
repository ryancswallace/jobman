//go:build !windows

package store

import "path/filepath"

func sqliteURIPath(databasePath string) string {
	return filepath.ToSlash(databasePath)
}
