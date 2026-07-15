//go:build darwin

package store

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func validateStateFilesystem(path string) error {
	var status unix.Statfs_t
	if err := unix.Statfs(path, &status); err != nil {
		return fmt.Errorf("inspect filesystem for %q: %w", path, err)
	}
	bytes := make([]byte, 0, len(status.Fstypename))
	for _, value := range status.Fstypename {
		if value == 0 {
			break
		}
		bytes = append(bytes, byte(value))
	}
	name := strings.ToLower(string(bytes))
	switch name {
	case "nfs", "smbfs", "webdav", "afpfs", "osxfuse", "macfuse":
		return fmt.Errorf("%s is unsupported for the Jobman state directory", name)
	default:
		return nil
	}
}
