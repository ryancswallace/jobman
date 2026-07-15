//go:build windows

package logstore

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func validateSingleLink(path string, _ os.FileInfo) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("inspect link count for %q: %w", path, err)
	}
	var information windows.ByHandleFileInformation
	informationErr := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &information)
	closeErr := file.Close()
	if informationErr != nil {
		return fmt.Errorf("inspect link count for %q: %w", path, informationErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close link-count handle for %q: %w", path, closeErr)
	}
	if information.NumberOfLinks != 1 {
		return fmt.Errorf("%w: %q has %d hard links", ErrUnsafePath, path, information.NumberOfLinks)
	}

	return nil
}
