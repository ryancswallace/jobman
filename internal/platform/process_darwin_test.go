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

func TestDarwinOriginalProcessZombieValidation(t *testing.T) {
	t.Parallel()

	identity, err := Inspect(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	zombie, err := originalProcessZombie(identity)
	if err != nil || zombie {
		t.Fatalf("originalProcessZombie(current) = %t, %v", zombie, err)
	}

	reused := identity
	reused.Creation = "different process"
	if _, err := originalProcessZombie(reused); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("originalProcessZombie(reused PID) error = %v", err)
	}

	gone := ProcessIdentity{PID: 1 << 30, Creation: "missing", Boot: "missing"}
	zombie, err = originalProcessZombie(gone)
	if err != nil || zombie {
		t.Fatalf("originalProcessZombie(missing) = %t, %v", zombie, err)
	}
}

func TestDarwinOriginalProcessZombie(t *testing.T) {
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
		zombie, inspectErr := originalProcessZombie(identity)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if zombie {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("helper process did not enter zombie state")
		case <-ticker.C:
		}
	}
	if err := Terminate(identity, true); err != nil {
		t.Fatalf("Terminate(zombie) error = %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	waited = true
}
