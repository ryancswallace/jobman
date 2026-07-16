//go:build windows

package store

import (
	"os"

	"github.com/ryancswallace/jobman/internal/winacl"
)

// Windows ACL validation is performed by the platform hardening layer. These
// checks deliberately do not infer ACL safety from emulated POSIX mode bits.
func validateOwner(_ os.FileInfo) error {
	return nil
}

func validatePermissions(_ os.FileInfo) error {
	return nil
}

func validateSingleLink(_ os.FileInfo) error {
	return nil
}

func hardenPath(path string) error {
	return winacl.Harden(path)
}

func validatePathSecurity(path string) error {
	return winacl.Validate(path)
}
