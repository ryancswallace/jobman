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

func TestLinuxProcessParsingAndGonePaths(t *testing.T) {
	t.Parallel()

	for _, data := range [][]byte{
		[]byte("missing terminator"),
		[]byte("1 (cmd) R 1 2"),
		[]byte("1 (cmd) RUNNING 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19"),
		[]byte("1 (cmd) R 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 invalid"),
	} {
		if _, err := parseLinuxProcessStat(data); err == nil {
			t.Fatalf("parseLinuxProcessStat(%q) error = nil", data)
		}
	}
	path, err := procStatPath(os.Getpid())
	if err != nil {
		t.Fatalf("procStatPath(self) error = %v", err)
	}
	if path == "" {
		t.Fatal("procStatPath(self) returned empty path")
	}
	stat, err := readLinuxProcessStat(os.Getpid())
	if err != nil || stat.creation == "" {
		t.Fatalf("readLinuxProcessStat(self) = %#v, %v", stat, err)
	}
	identity, err := Inspect(os.Getpid())
	if err != nil {
		t.Fatalf("Inspect(self) error = %v", err)
	}
	identity.Creation += "-wrong"
	if _, err := processAlive(identity); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("processAlive(mismatch) error = %v", err)
	}
	missing := ProcessIdentity{PID: 1 << 30}
	if alive, err := processAlive(missing); err != nil || alive {
		t.Fatalf("processAlive(missing) = %t, %v", alive, err)
	}
	if alive, err := processAlive(ProcessIdentity{PID: -1}); err != nil || alive {
		t.Fatalf("processAlive(namespace-wide probe with missing stat) = %t, %v", alive, err)
	}
	if err := terminateProcess(missing, false); err != nil {
		t.Fatalf("terminateProcess(missing) error = %v", err)
	}
	if err := pauseProcess(missing); err != nil {
		t.Fatalf("pauseProcess(missing) error = %v", err)
	}
	if err := resumeProcess(missing); err != nil {
		t.Fatalf("resumeProcess(missing) error = %v", err)
	}
	if !isProcessGone(os.ErrNotExist) || isProcessGone(errors.New("other")) {
		t.Fatal("isProcessGone() classification mismatch")
	}
	if linuxProcEntryMatches("/definitely/missing", "1", nil) {
		t.Fatal("linuxProcEntryMatches(missing) = true")
	}
}

func TestAliveReportsZombieAsNotAlive(t *testing.T) {
	command := exec.CommandContext(t.Context(), "/bin/true")
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
