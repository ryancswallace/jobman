//go:build windows

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

const storePreparationLockPollMilliseconds = 50

// withStorePreparationLock serializes filesystem security transitions for one
// state root across Jobman processes. SQLite coordinates database writes, but
// it cannot protect the creation-to-DACL-hardening window before a connection
// is allowed to open.
func withStorePreparationLock(
	ctx context.Context,
	stateDir string,
	operation func() error,
) (returnedErr error) {
	absolute, err := filepath.Abs(stateDir)
	if err != nil {
		return fmt.Errorf("resolve store preparation lock path: %w", err)
	}
	canonicalKey := strings.ToLower(filepath.Clean(absolute))
	digest := sha256.Sum256([]byte(canonicalKey))
	name, err := windows.UTF16PtrFromString(`Local\JobmanStore-` + hex.EncodeToString(digest[:]))
	if err != nil {
		return fmt.Errorf("encode store preparation lock name: %w", err)
	}
	handle, createErr := windows.CreateMutex(nil, false, name)
	if createErr != nil && !errors.Is(createErr, windows.ERROR_ALREADY_EXISTS) {
		return fmt.Errorf("create store preparation lock: %w", createErr)
	}
	if handle == 0 {
		return errors.New("create store preparation lock: invalid handle")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	acquired := false
	defer func() {
		if acquired {
			if releaseErr := windows.ReleaseMutex(handle); releaseErr != nil {
				returnedErr = errors.Join(
					returnedErr,
					fmt.Errorf("release store preparation lock: %w", releaseErr),
				)
			}
		}
		if closeErr := windows.CloseHandle(handle); closeErr != nil {
			returnedErr = errors.Join(
				returnedErr,
				fmt.Errorf("close store preparation lock: %w", closeErr),
			)
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for store preparation lock: %w", err)
		}
		result, waitErr := windows.WaitForSingleObject(handle, storePreparationLockPollMilliseconds)
		if waitErr != nil {
			return fmt.Errorf("wait for store preparation lock: %w", waitErr)
		}
		if result == windows.WAIT_OBJECT_0 || result == windows.WAIT_ABANDONED {
			acquired = true
			break
		}
		if result != uint32(windows.WAIT_TIMEOUT) {
			return fmt.Errorf("wait for store preparation lock: unexpected result %d", result)
		}
	}

	return operation()
}
