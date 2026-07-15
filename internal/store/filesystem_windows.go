//go:build windows

package store

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func validateStateFilesystem(path string) error {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return fmt.Errorf("state directory %q has no Windows volume", path)
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return fmt.Errorf("encode state volume: %w", err)
	}
	if driveType := windows.GetDriveType(root); driveType == windows.DRIVE_REMOTE {
		return fmt.Errorf("remote Windows volumes are unsupported for the Jobman state directory")
	}

	return nil
}
