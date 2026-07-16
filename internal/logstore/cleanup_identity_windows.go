//go:build windows

package logstore

import (
	"fmt"
	"os"
)

func primeCleanupIdentities(directory os.FileInfo, entries []cleanupEntry) error {
	// Windows resolves os.FileInfo file IDs lazily from the original path.
	// Prime them before the atomic directory rename makes those paths stale.
	if !os.SameFile(directory, directory) {
		return fmt.Errorf("%w: cannot identify run log directory before cleanup", ErrUnsafePath)
	}
	for _, entry := range entries {
		if !os.SameFile(entry.info, entry.info) {
			return fmt.Errorf("%w: cannot identify cleanup file %q", ErrUnsafePath, entry.name)
		}
	}

	return nil
}
