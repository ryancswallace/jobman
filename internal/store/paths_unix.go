//go:build unix

package store

import (
	"fmt"
	"os"
	"syscall"
)

func validateOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errorsForUnexpectedFileInfo(info)
	}
	currentUID := os.Geteuid()
	if currentUID < 0 {
		return fmt.Errorf("current user has invalid uid %d", currentUID)
	}
	if uint64(stat.Uid) != uint64(currentUID) {
		return fmt.Errorf("owned by uid %d instead of current uid %d", stat.Uid, currentUID)
	}

	return nil
}

func validatePermissions(info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%q has unsafe permissions %04o", info.Name(), info.Mode().Perm())
	}

	return nil
}

func validateSingleLink(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errorsForUnexpectedFileInfo(info)
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("has %d hard links instead of one", stat.Nlink)
	}

	return nil
}

func errorsForUnexpectedFileInfo(info os.FileInfo) error {
	return fmt.Errorf("unsupported file metadata for %q", info.Name())
}
