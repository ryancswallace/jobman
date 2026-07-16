//go:build !windows

package logstore

import "os"

func primeCleanupIdentities(os.FileInfo, []cleanupEntry) error {
	return nil
}
