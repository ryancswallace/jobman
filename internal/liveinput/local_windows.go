//go:build windows

package liveinput

import (
	"context"
	"net"
)

func listenLocal(string) (net.Listener, error) {
	return nil, ErrUnsupported
}

func dialLocal(context.Context, string) (net.Conn, error) {
	return nil, ErrUnsupported
}

func removeLocal(string) error {
	return nil
}
