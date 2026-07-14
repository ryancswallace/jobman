//go:build linux

package platform

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

const linuxZombieHelperEnvironment = "JOBMAN_LINUX_ZOMBIE_HELPER"

func TestParseLinuxProcessStat(t *testing.T) {
	t.Parallel()

	stat, err := parseLinuxProcessStat(
		[]byte("123 (command with ) character) Z 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 987"),
	)
	if err != nil {
		t.Fatalf("parseLinuxProcessStat() error = %v", err)
	}
	if stat.state != "Z" {
		t.Fatalf("parseLinuxProcessStat().state = %q, want Z", stat.state)
	}
	if stat.creation != "987" {
		t.Fatalf("parseLinuxProcessStat().creation = %q, want 987", stat.creation)
	}
}

func TestAliveReportsZombieAsNotAlive(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	// #nosec G204 -- executable is the current test binary, not external input.
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestLinuxZombieHelper$")
	command.Env = append(os.Environ(), linuxZombieHelperEnvironment+"=1")
	if startErr := command.Start(); startErr != nil {
		t.Fatalf("start zombie helper: %v", startErr)
	}
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		killErr := command.Process.Kill()
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			t.Errorf("kill zombie helper during cleanup: %v", killErr)
		}
		waitErr := command.Wait()
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			t.Errorf("wait for zombie helper during cleanup: %v", waitErr)
		}
	})

	identity, err := Inspect(command.Process.Pid)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		alive, aliveErr := Alive(identity)
		if aliveErr != nil {
			t.Fatalf("Alive() error = %v", aliveErr)
		}
		if !alive {
			break
		}

		select {
		case <-timer.C:
			t.Fatal("Alive() continued to report an exited, unreaped process as live")
		default:
			runtime.Gosched()
		}
	}

	if waitErr := command.Wait(); waitErr != nil {
		t.Fatalf("wait for zombie helper: %v", waitErr)
	}
	waited = true
}

func TestLinuxZombieHelper(t *testing.T) {
	if os.Getenv(linuxZombieHelperEnvironment) != "1" {
		t.Skip("helper process only")
	}
}
