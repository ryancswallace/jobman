//go:build !windows

package store

func validateExistingDatabaseSidecarSecurity(path string) error {
	return validatePathSecurity(path)
}
