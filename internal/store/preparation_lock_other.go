//go:build !windows

package store

import "context"

func withStorePreparationLock(_ context.Context, _ string, operation func() error) error {
	return operation()
}
