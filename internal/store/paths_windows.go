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

// hardenTrustedExistingDatabaseFile closes the creation-to-hardening window
// between concurrent Open calls. The inherited ACL must already contain only
// trusted principals before this process is allowed to protect it.
func hardenTrustedExistingDatabaseFile(path string) error {
	if err := winacl.ValidateInherited(path); err != nil {
		return err
	}

	return winacl.Harden(path)
}
