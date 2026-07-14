//go:build windows

package logstore

import "io/fs"

func validatePrivateMode(_ string, _ fs.FileInfo, _ fs.FileMode) error {
	// Windows privacy is enforced by the state directory's user-only ACL. Go's
	// portable FileMode does not expose ACL entries for additional validation.
	return nil
}
