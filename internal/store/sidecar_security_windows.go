//go:build windows

package store

import "github.com/ryancswallace/jobman/internal/winacl"

func validateExistingDatabaseSidecarSecurity(path string) error {
	return winacl.ValidateInherited(path)
}
