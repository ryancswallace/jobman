//go:build windows

package liveinput

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenLocal(endpoint string) (net.Listener, error) {
	securityDescriptor, err := currentUserPipeSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(windowsPipeName(endpoint), &winio.PipeConfig{
		SecurityDescriptor: securityDescriptor,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
	if err != nil {
		return nil, fmt.Errorf("listen for live input: %w", err)
	}

	return listener, nil
}

func dialLocal(ctx context.Context, endpoint string) (net.Conn, error) {
	connection, err := winio.DialPipeContext(ctx, windowsPipeName(endpoint))
	if err != nil {
		return nil, fmt.Errorf("connect to live-input endpoint: %w", err)
	}

	return connection, nil
}

func removeLocal(string) error {
	return nil
}

func windowsPipeName(endpoint string) string {
	digest := sha256.Sum256([]byte(endpoint))

	return fmt.Sprintf(`\\.\pipe\jobman-%x`, digest[:16])
}

func currentUserPipeSecurityDescriptor() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("query current Windows user for live input: %w", err)
	}

	return fmt.Sprintf(
		"O:%sD:P(A;;GA;;;%s)(A;;GA;;;SY)(A;;GA;;;BA)",
		user.User.Sid,
		user.User.Sid,
	), nil
}
