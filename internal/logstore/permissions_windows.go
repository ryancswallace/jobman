//go:build windows

package logstore

import (
	"fmt"
	"io/fs"

	"github.com/ryancswallace/jobman/internal/winacl"
)

func validatePrivateMode(path string, _ fs.FileInfo, _ fs.FileMode) error {
	if err := winacl.Validate(path); err != nil {
		return fmt.Errorf("%w: unsafe log path %q: %v", ErrUnsafePath, path, err)
	}

	return nil
}

func hardenPrivatePath(path string) error {
	return winacl.Harden(path)
}
