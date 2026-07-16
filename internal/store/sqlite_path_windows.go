//go:build windows

package store

import (
	"path/filepath"
	"strings"
)

func sqliteURIPath(databasePath string) string {
	path := filepath.ToSlash(databasePath)
	if filepath.VolumeName(databasePath) != "" && !strings.HasPrefix(path, "/") {
		// RFC 8089 Windows file URIs require an absolute-path slash before the
		// drive letter. Without it SQLite interprets file:C:/... as a relative
		// URI and modernc reports a misleading SQLITE_NOMEM during open.
		path = "/" + path
	}

	return path
}
