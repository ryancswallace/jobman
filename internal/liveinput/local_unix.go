//go:build !windows

package liveinput

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

func listenLocal(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create live-input directory: %w", err)
	}
	// Owner execute permission is required to traverse this private directory.
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil { //nolint:gosec // This is a directory, not a credential file.
		return nil, fmt.Errorf("restrict live-input directory: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale live-input endpoint: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen for live input: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(fmt.Errorf("restrict live-input endpoint: %w", err), listener.Close())
	}

	return listener, nil
}

func dialLocal(ctx context.Context, path string) (net.Conn, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("connect to live-input endpoint: %w", err)
	}

	return connection, nil
}

func removeLocal(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}
