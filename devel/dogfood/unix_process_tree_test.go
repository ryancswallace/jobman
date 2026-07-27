//go:build linux || darwin

package dogfood

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnixProcessTreeRecordsEveryRole(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script, err := filepath.Abs("unix-process-tree.sh")
	if err != nil {
		t.Fatalf("resolve helper path: %v", err)
	}

	command := exec.CommandContext( // #nosec G204 -- The command and arguments are repository-controlled test fixtures.
		t.Context(),
		script,
		"graceful",
		filepath.Join(root, "pids"),
		filepath.Join(root, "progress"),
	)
	if startErr := command.Start(); startErr != nil {
		t.Fatalf("start helper: %v", startErr)
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- command.Wait()
	}()
	t.Cleanup(func() {
		if command.ProcessState != nil {
			return
		}
		if signalErr := command.Process.Signal(syscall.SIGTERM); signalErr != nil &&
			!errors.Is(signalErr, os.ErrProcessDone) {
			t.Errorf("stop helper during cleanup: %v", signalErr)
		}
		select {
		case <-waitResult:
		case <-time.After(5 * time.Second):
			if killErr := command.Process.Kill(); killErr != nil &&
				!errors.Is(killErr, os.ErrProcessDone) {
				t.Errorf("kill helper during cleanup: %v", killErr)
			}
			<-waitResult
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	var roles map[string]int
	for time.Now().Before(deadline) {
		roles, err = readRecordedRoles(filepath.Join(root, "pids"))
		if err == nil && len(roles) == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read recorded roles: %v", err)
	}
	for _, role := range []string{"parent", "child", "grandchild"} {
		if roles[role] <= 0 {
			t.Errorf("missing valid %s PID in %v", role, roles)
		}
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop helper: %v", err)
	}
	select {
	case err := <-waitResult:
		if err != nil {
			t.Fatalf("wait for helper: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper did not stop after SIGTERM")
	}
}

func readRecordedRoles(path string) (map[string]int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	roles := make(map[string]int)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, errors.New("invalid PID record")
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, err
		}
		roles[fields[0]] = pid
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return roles, nil
}
