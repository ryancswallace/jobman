package platform

import (
	"errors"
	"os"
	"testing"
)

func TestInspectCurrentProcess(t *testing.T) {
	t.Parallel()

	identity, err := Inspect(os.Getpid())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if identity.PID != os.Getpid() {
		t.Fatalf("Inspect().PID = %d, want %d", identity.PID, os.Getpid())
	}
	if identity.Creation == "" || identity.Boot == "" {
		t.Fatalf("Inspect() = %#v, want creation and boot identity", identity)
	}

	alive, err := Alive(identity)
	if err != nil {
		t.Fatalf("Alive() error = %v", err)
	}
	if !alive {
		t.Fatal("Alive() = false for current process")
	}
}

func TestAliveRejectsIdentityMismatch(t *testing.T) {
	t.Parallel()

	identity, err := Inspect(os.Getpid())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	identity.Creation += "-different"

	alive, err := Alive(identity)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("Alive() error = %v, want %v", err, ErrIdentityMismatch)
	}
	if alive {
		t.Fatal("Alive() = true for mismatched identity")
	}
}
