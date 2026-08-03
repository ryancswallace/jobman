//go:build darwin || windows

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/platform"
)

const nativeHelperEnvironment = "JOBMAN_NATIVE_E2E_HELPER"

type nativeEnvelope struct {
	Data nativeJob `json:"data"`
}

type nativeJob struct {
	Summary struct {
		Phase   string `json:"phase"`
		Outcome string `json:"outcome"`
	} `json:"summary"`
	Runs []struct {
		Process *struct {
			PID int `json:"pid"`
		} `json:"process"`
	} `json:"runs"`
}

func TestNativeAssembledBinaryLifecycle(t *testing.T) {
	binary := buildNativeJobman(t)

	t.Run("detached target outlives submitting process", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		gate := filepath.Join(t.TempDir(), "gate")
		jobID := nativeSubmit(t, binary, stateDir, "gate", gate)
		// The assembled submitting process has exited here. The detached
		// supervisor must remain able to observe and complete the target.
		waitNativeJob(t, binary, stateDir, jobID, func(job nativeJob) bool {
			return job.Summary.Phase == "running"
		})
		if err := os.WriteFile(gate, []byte("release"), 0o600); err != nil {
			t.Fatal(err)
		}
		completed := waitNativeCompleted(t, binary, stateDir, jobID)
		if completed.Summary.Outcome != "success" {
			t.Fatalf("outcome = %q, want success", completed.Summary.Outcome)
		}
		if got := nativeInvoke(t, binary, stateDir, "logs", "--stream", "stdout", jobID); got != "detached-success" {
			t.Fatalf("logs = %q", got)
		}
	})

	t.Run("cancellation terminates an assembled process tree", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		jobID := nativeSubmit(t, binary, stateDir, "tree")
		waitNativeJob(t, binary, stateDir, jobID, func(job nativeJob) bool {
			return job.Summary.Phase == "running"
		})
		var pids []int
		deadline := time.Now().Add(10 * time.Second)
		for len(pids) != 2 && time.Now().Before(deadline) {
			output := nativeInvoke(t, binary, stateDir, "logs", "--stream", "stdout", jobID)
			fields := strings.Fields(output)
			pids = pids[:0]
			for _, field := range fields {
				pid, err := strconv.Atoi(field)
				if err == nil {
					pids = append(pids, pid)
				}
			}
			if len(pids) != 2 {
				time.Sleep(25 * time.Millisecond)
			}
		}
		if len(pids) != 2 {
			t.Fatalf("target did not report parent and child pids")
		}
		nativeInvoke(t, binary, stateDir, "cancel", jobID)
		completed := waitNativeCompleted(t, binary, stateDir, jobID)
		if completed.Summary.Outcome != "cancelled" { //nolint:misspell // Stable persisted spelling.
			t.Fatalf("outcome = %q, want persisted cancellation outcome", completed.Summary.Outcome)
		}
		for _, pid := range pids {
			waitNativeProcessGone(t, pid)
		}
	})

	t.Run("live input crosses the native local transport", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		jobID := nativeSubmitWithOptions(t, binary, stateDir, []string{"--stdin", "live"}, "input")
		waitNativeJob(t, binary, stateDir, jobID, func(job nativeJob) bool {
			return job.Summary.Phase == "running"
		})
		command := exec.CommandContext( // #nosec G702 -- Test-controlled assembled binary path and arguments.
			t.Context(),
			binary,
			"--state-dir",
			stateDir,
			"input",
			"--eof",
			jobID,
		)
		command.Stdin = strings.NewReader("native-input")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("input error = %v: %s", err, output)
		}
		completed := waitNativeCompleted(t, binary, stateDir, jobID)
		if completed.Summary.Outcome != "success" {
			t.Fatalf("outcome = %q, want success", completed.Summary.Outcome)
		}
		if got := nativeInvoke(t, binary, stateDir, "logs", "--stream", "stdout", jobID); got != "native-input" {
			t.Fatalf("logs = %q", got)
		}
	})

	t.Run("pause and resume suspend policy progress", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		progress := filepath.Join(t.TempDir(), "progress")
		jobID := nativeSubmit(t, binary, stateDir, "progress", progress)
		waitNativeJob(t, binary, stateDir, jobID, func(job nativeJob) bool {
			return job.Summary.Phase == "running"
		})
		waitNativeFileGrowth(t, progress, 0)
		nativeInvoke(t, binary, stateDir, "pause", jobID)
		before := nativeFileSize(t, progress)
		time.Sleep(300 * time.Millisecond)
		if after := nativeFileSize(t, progress); after != before {
			t.Fatalf("progress grew while paused: %d -> %d", before, after)
		}
		nativeInvoke(t, binary, stateDir, "resume", jobID)
		waitNativeFileGrowth(t, progress, before)
		nativeInvoke(t, binary, stateDir, "cancel", jobID)
		waitNativeCompleted(t, binary, stateDir, jobID)
	})

	t.Run("selected cleanup removes completed job", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		jobID := nativeSubmit(t, binary, stateDir, "input")
		waitNativeCompleted(t, binary, stateDir, jobID)
		cleaned := nativeInvoke(t, binary, stateDir, "clean", jobID, "--force")
		if !strings.Contains(cleaned, "removed 1 runs") || !strings.Contains(cleaned, "1 jobs") {
			t.Fatalf("selected cleanup output = %q, want one run and one job", cleaned)
		}
		if listed := nativeInvoke(t, binary, stateDir, "list", "--all"); strings.Contains(listed, jobID) {
			t.Fatalf("list after selected cleanup = %q, still contains %s", listed, jobID)
		}
	})
}

func TestMain(m *testing.M) {
	exitCode := 0
	if os.Getenv(nativeHelperEnvironment) != "1" {
		exitCode = m.Run()
	} else if err := runNativeHelper(context.Background()); err != nil {
		exitCode = 1
	}
	os.Exit(exitCode)
}

func runNativeHelper(ctx context.Context) error {
	arguments := os.Args
	separator := -1
	for index, argument := range arguments {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(arguments) {
		return errors.New("native helper mode is missing")
	}
	values := arguments[separator+1:]
	switch values[0] {
	case "gate":
		if len(values) != 2 {
			return errors.New("native gate helper requires a path")
		}
		for {
			if _, err := os.Stat(values[1]); err == nil { // #nosec G703 -- Test-controlled gate path.
				if _, writeErr := os.Stdout.WriteString("detached-success"); writeErr != nil {
					return fmt.Errorf("write gate result: %w", writeErr)
				}

				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect gate path: %w", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	case "tree":
		child := exec.CommandContext( // #nosec G204,G702 -- Fixed self-executable and arguments.
			ctx,
			os.Args[0],
			"-test.run=TestNativeHelperProcess",
			"--",
			"sleep",
		)
		child.Env = append(os.Environ(), nativeHelperEnvironment+"=1")
		if err := child.Start(); err != nil {
			return fmt.Errorf("start child helper: %w", err)
		}
		if _, err := fmt.Fprintf(os.Stdout, "%d %d\n", os.Getpid(), child.Process.Pid); err != nil {
			return fmt.Errorf("write process identifiers: %w", err)
		}
		if err := child.Wait(); err != nil {
			return fmt.Errorf("wait for child helper: %w", err)
		}

		return nil
	case "sleep":
		time.Sleep(2 * time.Minute)

		return nil
	case "input":
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			return fmt.Errorf("copy native input: %w", err)
		}

		return nil
	case "progress":
		if len(values) != 2 {
			return errors.New("native progress helper requires a path")
		}
		for {
			file, err := os.OpenFile( // #nosec G703 -- Test-controlled progress path.
				values[1],
				os.O_CREATE|os.O_APPEND|os.O_WRONLY,
				0o600,
			)
			if err != nil {
				return fmt.Errorf("open progress file: %w", err)
			}
			if _, err := file.WriteString("x"); err != nil {
				if closeErr := file.Close(); closeErr != nil {
					return errors.Join(
						fmt.Errorf("write progress file: %w", err),
						fmt.Errorf("close progress file: %w", closeErr),
					)
				}

				return fmt.Errorf("write progress file: %w", err)
			}
			if err := file.Close(); err != nil {
				return fmt.Errorf("close progress file: %w", err)
			}
			time.Sleep(25 * time.Millisecond)
		}
	default:
		return fmt.Errorf("unknown native helper mode %q", values[0])
	}
}

func buildNativeJobman(t *testing.T) string {
	t.Helper()
	name := "jobman"
	if filepath.Ext(os.Args[0]) == ".exe" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	arguments := []string{"build", "-trimpath"}
	if os.Getenv("JOBMAN_E2E_COVERDIR") != "" {
		arguments = append(arguments, "-cover", "-covermode=atomic", "-coverpkg=./...")
	}
	arguments = append(arguments, "-o", binary, "../..")
	command := exec.CommandContext(ctx, "go", arguments...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build assembled binary: %v: %s", err, output)
	}

	return binary
}

func nativeSubmit(t *testing.T, binary, stateDir, mode string, arguments ...string) string {
	t.Helper()
	return nativeSubmitWithOptions(t, binary, stateDir, nil, mode, arguments...)
}

func nativeSubmitWithOptions(
	t *testing.T,
	binary,
	stateDir string,
	options []string,
	mode string,
	arguments ...string,
) string {
	t.Helper()
	values := make([]string, 0, 10+len(options)+len(arguments))
	values = append(values, "--state-dir", stateDir, "run", "--env", nativeHelperEnvironment+"=1")
	values = append(values, options...)
	values = append(values, "--", os.Args[0], "-test.run=TestNativeHelperProcess", "--", mode)
	values = append(values, arguments...)
	result := nativeCommand(t, binary, values...)
	return strings.TrimSpace(result)
}

func nativeInvoke(t *testing.T, binary, stateDir string, arguments ...string) string {
	t.Helper()
	values := append([]string{"--state-dir", stateDir}, arguments...)
	return nativeCommand(t, binary, values...)
}

func nativeCommand(t *testing.T, binary string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, arguments...) // #nosec G702 -- Test-controlled assembled binary path and arguments.
	command.Env = nativeAssembledBinaryEnvironment()
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%s %v: %v\nstderr: %s", binary, arguments, err, stderr.String())
	}

	return stdout.String()
}

func nativeAssembledBinaryEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "GOCOVERDIR=") {
			environment = append(environment, item)
		}
	}
	if directory := os.Getenv("JOBMAN_E2E_COVERDIR"); directory != "" {
		environment = append(environment, "GOCOVERDIR="+directory)
	}

	return environment
}

func waitNativeJob(
	t *testing.T,
	binary,
	stateDir,
	jobID string,
	predicate func(nativeJob) bool,
) nativeJob {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		output := nativeInvoke(t, binary, stateDir, "show", "--json", jobID)
		var envelope nativeEnvelope
		if err := json.Unmarshal([]byte(output), &envelope); err != nil {
			t.Fatalf("decode show output: %v: %s", err, output)
		}
		if predicate(envelope.Data) {
			return envelope.Data
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for job %s", jobID)

	return nativeJob{}
}

func waitNativeCompleted(t *testing.T, binary, stateDir, jobID string) nativeJob {
	t.Helper()
	return waitNativeJob(t, binary, stateDir, jobID, func(job nativeJob) bool {
		return job.Summary.Phase == "completed"
	})
}

func waitNativeProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		identity, err := platform.Inspect(pid)
		if err != nil {
			return
		}
		alive, err := platform.Alive(identity)
		if err == nil && !alive || errors.Is(err, platform.ErrIdentityMismatch) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("process %d remained alive", pid)
}

func waitNativeFileGrowth(t *testing.T, path string, prior int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if nativeFileSize(t, path) > prior {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("file %s did not grow", path)
}

func nativeFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}

	return info.Size()
}
