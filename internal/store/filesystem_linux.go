//go:build linux

package store

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func validateStateFilesystem(path string) error {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return fmt.Errorf("inspect filesystem for %q: %w", path, err)
	}
	// SQLite WAL locking is not supported by Jobman on remote/distributed
	// filesystems. Values are Linux filesystem magic constants.
	unsupported := map[uint32]string{
		0x6969:     "NFS",
		0xff534d42: "CIFS/SMB",
		0xfe534d42: "SMB2",
		0x9fa0:     "9P",
		0x73757245: "CODA",
		0x5346414f: "AFS",
		0x65735546: "FUSE",
	}
	// Linux filesystem magic values occupy 32 bits even though Statfs_t.Type's
	// signed Go representation varies between 32- and 64-bit architectures.
	filesystemType := uint32(status.Type) //nolint:gosec // Intentional normalization of an OS-defined 32-bit bit pattern.
	if name, found := unsupported[filesystemType]; found {
		return fmt.Errorf("%s is unsupported for the Jobman state directory", name)
	}

	return nil
}
