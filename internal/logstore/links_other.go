//go:build !linux && !darwin && !windows

package logstore

import "os"

func validateSingleLink(_ string, _ os.FileInfo) error {
	return nil
}
