//go:build windows

package store

import "os"

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
