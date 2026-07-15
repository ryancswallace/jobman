//go:build linux || darwin

package logstore

import (
	"fmt"
	"os"
	"syscall"
)

func validateSingleLink(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%w: cannot inspect link count for %q", ErrUnsafePath, path)
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("%w: %q has %d hard links", ErrUnsafePath, path, stat.Nlink)
	}

	return nil
}
