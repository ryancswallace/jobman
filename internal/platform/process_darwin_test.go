//go:build darwin

package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestDarwinProcessIsExiting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state int8
		flags int32
		want  bool
	}{
		{name: "running", state: 2},
		{name: "working on exit", state: 2, flags: darwinProcessFlagExiting, want: true},
		{name: "zombie", state: darwinProcessStateZombie, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := darwinProcessIsExiting(test.state, test.flags); got != test.want {
				t.Fatalf("darwinProcessIsExiting(%d, %#x) = %t, want %t", test.state, test.flags, got, test.want)
			}
		})
	}
}

func TestDarwinOriginalProcessExitingValidation(t *testing.T) {
	t.Parallel()

	identity, err := Inspect(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	exiting, err := originalProcessExiting(identity)
	if err != nil || exiting {
		t.Fatalf("originalProcessExiting(current) = %t, %v", exiting, err)
	}

	reused := identity
	reused.Creation = "different process"
	if _, err := originalProcessExiting(reused); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("originalProcessExiting(reused PID) error = %v", err)
	}

	gone := ProcessIdentity{PID: 1 << 30, Creation: "missing", Boot: "missing"}
	exiting, err = originalProcessExiting(gone)
	if err != nil || exiting {
		t.Fatalf("originalProcessExiting(missing) = %t, %v", exiting, err)
	}
}

func TestDarwinOriginalProcessExiting(t *testing.T) {
	t.Parallel()

	command := exec.Command("/bin/sh", "-c", "read ignored || true")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = stdin.Close()
		if !waited {
			_ = command.Wait()
		}
	})
	identity, err := Inspect(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		exiting, inspectErr := originalProcessExiting(identity)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if exiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("helper process did not enter an exiting state")
		case <-ticker.C:
		}
	}
	if err := Terminate(identity, true); err != nil {
		t.Fatalf("Terminate(exiting) error = %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	waited = true
}
