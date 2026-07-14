//go:build !windows

package logstore

import (
	"fmt"
	"io/fs"
)

func validatePrivateMode(path string, info fs.FileInfo, maximum fs.FileMode) error {
	if info.Mode().Perm()&^maximum.Perm() != 0 {
		return fmt.Errorf(
			"%w: %q permissions %04o allow group or other access",
			ErrUnsafePath,
			path,
			info.Mode().Perm(),
		)
	}

	return nil
}
