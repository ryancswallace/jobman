//go:build linux

package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	buildTimeout   = 2 * time.Minute
	commandTimeout = 20 * time.Second
	pollTimeout    = 15 * time.Second
	pollInterval   = 25 * time.Millisecond
)

type commandResult struct {
	stdout string
	stderr string
	err    error
}

type showEnvelope struct {
	Data jobDetail `json:"data"`
}

type jobDetail struct {
	Summary jobSummary  `json:"summary"`
	Runs    []runDetail `json:"runs"`
}

type jobSummary struct {
	Phase   string `json:"phase"`
	Outcome string `json:"outcome"`
}

type runDetail struct {
	Process *processIdentity `json:"process"`
	Exit    *exitInfo        `json:"exit"`
	Outcome string           `json:"outcome"`
	Phase   string           `json:"phase"`
}

type processIdentity struct {
	PID int `json:"pid"`
}

type exitInfo struct {
	ExitCode *int `json:"exit_code"`
}

func TestAssembledBinaryLifecycle(t *testing.T) {
	binary := buildJobman(t)

	t.Run("detached success captures exact streams", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		gate := filepath.Join(t.TempDir(), "release")
		shell := requireExecutable(t, "sh")
		script := `while [ ! -e "$1" ]; do :; done
printf 'stdout line 1\nstdout-no-newline'
printf 'stderr line 1\nstderr-no-newline' >&2`

		jobID := submit(t, binary, stateDir, shell, "-c", script, "jobman-e2e", gate)
		running := waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running"
		})
		if running.Summary.Outcome != "" {
			t.Fatalf("running job outcome = %q, want empty", running.Summary.Outcome)
		}
		if writeErr := os.WriteFile(gate, []byte("release"), 0o600); writeErr != nil {
			t.Fatalf("create target release file: %v", writeErr)
		}

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "success")
		assertLogs(t, binary, stateDir, jobID, "stdout", "stdout line 1\nstdout-no-newline")
		assertLogs(t, binary, stateDir, jobID, "stderr", "stderr line 1\nstderr-no-newline")
	})

	t.Run("growing logs are readable while active", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		gate := filepath.Join(t.TempDir(), "release")
		shell := requireExecutable(t, "sh")
		script := `printf 'first chunk'
while [ ! -e "$1" ]; do :; done
printf ' and final chunk'`
		jobID := submit(t, binary, stateDir, shell, "-c", script, "jobman-e2e", gate)
		registerCancellationCleanup(t, binary, stateDir, jobID)

		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running"
		})
		waitForLogs(t, binary, stateDir, jobID, "stdout", "first chunk")
		active := showJob(t, binary, stateDir, jobID)
		if active.Summary.Phase != "running" {
			t.Fatalf("job phase after reading growing log = %q, want running", active.Summary.Phase)
		}
		if writeErr := os.WriteFile(gate, []byte("release"), 0o600); writeErr != nil {
			t.Fatalf("create target release file: %v", writeErr)
		}

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "success")
		assertLogs(t, binary, stateDir, jobID, "stdout", "first chunk and final chunk")
	})

	t.Run("failed exit records outcome and code", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		jobID := submit(t, binary, stateDir, shell, "-c", "exit 23")

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "failure")
		if len(completed.Runs) != 1 || completed.Runs[0].Exit == nil || completed.Runs[0].Exit.ExitCode == nil {
			t.Fatalf("failed run exit = %#v, want code 23", completed.Runs)
		}
		if exitCode := *completed.Runs[0].Exit.ExitCode; exitCode != 23 {
			t.Fatalf("failed run exit code = %d, want 23", exitCode)
		}
	})

	t.Run("argument boundaries are preserved", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		sentinel := filepath.Join(t.TempDir(), "must-not-exist")
		arguments := []string{
			"a b",
			"$HOME",
			"$(printf injected)",
			"; touch " + sentinel,
			"*",
			"",
			"line one\nline two",
			`"quoted"`,
		}
		targetArguments := append([]string{"<%s>\n"}, arguments...)
		jobID := submit(t, binary, stateDir, printf, targetArguments...)

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "success")
		var expected strings.Builder
		for _, argument := range arguments {
			_, _ = fmt.Fprintf(&expected, "<%s>\n", argument)
		}
		assertLogs(t, binary, stateDir, jobID, "stdout", expected.String())
		if _, statErr := os.Stat(sentinel); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("shell metacharacter argument created sentinel; stat error = %v", statErr)
		}
	})

	t.Run("cancel terminates a blocking job", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		sleep := requireExecutable(t, "sleep")
		jobID := submit(t, binary, stateDir, sleep, "60")
		registerCancellationCleanup(t, binary, stateDir, jobID)

		running := waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running" && len(detail.Runs) == 1 && detail.Runs[0].Process != nil
		})
		pid := running.Runs[0].Process.PID
		result := invokeWithTimeout(t, binary, stateDir, "cancel", jobID)
		if result.err != nil {
			t.Fatalf("jobman cancel error = %v\nstderr: %s", result.err, result.stderr)
		}

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "cancelled") //nolint:misspell // The specification defines this persisted spelling.
		waitForProcessesGone(t, pid)
	})

	t.Run("cancel terminates shell and child process group", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		sleep := requireExecutable(t, "sleep")
		script := `"$1" 60 &
child=$!
printf '%s %s\n' "$$" "$child"
wait "$child"`
		jobID := submit(t, binary, stateDir, shell, "-c", script, "jobman-e2e", sleep)
		registerCancellationCleanup(t, binary, stateDir, jobID)

		running := waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running" && len(detail.Runs) == 1 && detail.Runs[0].Process != nil
		})
		pids := waitForLoggedPIDs(t, binary, stateDir, jobID, 2)
		if pids[0] != running.Runs[0].Process.PID {
			t.Fatalf("logged shell PID = %d, recorded target PID = %d", pids[0], running.Runs[0].Process.PID)
		}
		registerProcessGroupCleanup(t, pids[0])
		result := invokeWithTimeout(t, binary, stateDir, "cancel", jobID)
		if result.err != nil {
			t.Fatalf("jobman cancel error = %v\nstderr: %s", result.err, result.stderr)
		}

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "cancelled") //nolint:misspell // The specification defines this persisted spelling.
		waitForProcessesGone(t, pids...)
	})

	t.Run("readers remain consistent during cancellation", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		sleep := requireExecutable(t, "sleep")
		jobID := submit(t, binary, stateDir, sleep, "60")
		registerCancellationCleanup(t, binary, stateDir, jobID)
		running := waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running" && len(detail.Runs) == 1 && detail.Runs[0].Process != nil
		})
		registerProcessGroupCleanup(t, running.Runs[0].Process.PID)

		ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
		defer cancel()
		start := make(chan struct{})
		errorsChannel := make(chan error, 16)
		operations := [][]string{
			{"status", jobID},
			{"show", "--json", jobID},
			{"logs", "--stream", "stdout", jobID},
		}
		var wait sync.WaitGroup
		for _, arguments := range operations {
			for range 3 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					<-start
					for range 3 {
						result := invoke(ctx, binary, stateDir, arguments...)
						if result.err != nil {
							errorsChannel <- fmt.Errorf("jobman %s: %w: %s", arguments[0], result.err, result.stderr)

							return
						}
					}
				}()
			}
		}
		cancelResult := make(chan commandResult, 1)
		go func() {
			<-start
			cancelResult <- invoke(ctx, binary, stateDir, "cancel", jobID)
		}()
		close(start)
		wait.Wait()
		close(errorsChannel)
		for operationErr := range errorsChannel {
			t.Errorf("concurrent reader error: %v", operationErr)
		}
		result := <-cancelResult
		if result.err != nil {
			t.Fatalf("concurrent jobman cancel error = %v\nstderr: %s", result.err, result.stderr)
		}

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "cancelled") //nolint:misspell // The specification defines this persisted spelling.
	})

	t.Run("stale killed supervisor reconciles to lost", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		sleep := requireExecutable(t, "sleep")
		jobID := submit(t, binary, stateDir, sleep, "60")
		running := waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running" && len(detail.Runs) == 1 && detail.Runs[0].Process != nil
		})
		targetPID := running.Runs[0].Process.PID
		registerProcessGroupCleanup(t, targetPID)

		owner := loadSupervisor(t, stateDir, jobID)
		if killErr := platform.Terminate(platform.ProcessIdentity{
			PID:      owner.Process.PID,
			Creation: owner.Process.CreationID,
			Boot:     owner.Process.BootID,
		}, true); killErr != nil {
			t.Fatalf("kill supervisor PID %d: %v", owner.Process.PID, killErr)
		}
		waitForProcessesGone(t, owner.Process.PID)
		expireSupervisorLease(t, stateDir, owner)

		reconciled := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, reconciled, "lost")
	})
}

func buildJobman(t *testing.T) string {
	t.Helper()

	repository := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "jobman")
	ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	command.Dir = repository
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build assembled Jobman binary: %v\n%s", err, output)
	}

	return binary
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate e2e test source")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func requireExecutable(t *testing.T, name string) string {
	t.Helper()

	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("locate required test executable %q: %v", name, err)
	}

	return path
}

func submit(t *testing.T, binary, stateDir, executable string, arguments ...string) string {
	t.Helper()

	commandArguments := make([]string, 0, 3+len(arguments))
	commandArguments = append(commandArguments, "run", "--", executable)
	commandArguments = append(commandArguments, arguments...)
	result := invokeWithTimeout(t, binary, stateDir, commandArguments...)
	if result.err != nil {
		t.Fatalf("jobman run error = %v\nstdout: %s\nstderr: %s", result.err, result.stdout, result.stderr)
	}
	jobID := strings.TrimSpace(result.stdout)
	if len(jobID) != 36 || strings.Count(jobID, "-") != 4 {
		t.Fatalf("jobman run output = %q, want one canonical job ID", result.stdout)
	}

	return jobID
}

func assertLogs(t *testing.T, binary, stateDir, jobID, stream, want string) {
	t.Helper()

	result := invokeWithTimeout(t, binary, stateDir, "logs", "--stream", stream, jobID)
	if result.err != nil {
		t.Fatalf("jobman logs --stream %s error = %v\nstderr: %s", stream, result.err, result.stderr)
	}
	if result.stdout != want {
		t.Errorf("%s log = %q, want %q", stream, result.stdout, want)
	}
}

func waitForLogs(t *testing.T, binary, stateDir, jobID, stream, want string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), pollTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var last commandResult
	for {
		last = invoke(ctx, binary, stateDir, "logs", "--stream", stream, jobID)
		if last.err == nil && last.stdout == want {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for %s log of job %s: %v\nlast error: %v\nstdout: %q\nstderr: %s",
				stream,
				jobID,
				ctx.Err(),
				last.err,
				last.stdout,
				last.stderr,
			)
		case <-ticker.C:
		}
	}
}

func waitForLoggedPIDs(t *testing.T, binary, stateDir, jobID string, count int) []int {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), pollTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var last commandResult
	for {
		last = invoke(ctx, binary, stateDir, "logs", "--stream", "stdout", jobID)
		fields := strings.Fields(last.stdout)
		if last.err == nil && len(fields) == count {
			pids := make([]int, 0, count)
			valid := true
			for _, field := range fields {
				pid, conversionErr := strconv.Atoi(field)
				if conversionErr != nil || pid <= 0 {
					valid = false

					break
				}
				pids = append(pids, pid)
			}
			if valid {
				return pids
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for %d logged PIDs from job %s: %v\nlast error: %v\nstdout: %q\nstderr: %s",
				count,
				jobID,
				ctx.Err(),
				last.err,
				last.stdout,
				last.stderr,
			)
		case <-ticker.C:
		}
	}
}

func showJob(t *testing.T, binary, stateDir, jobID string) jobDetail {
	t.Helper()

	result := invokeWithTimeout(t, binary, stateDir, "show", "--json", jobID)
	if result.err != nil {
		t.Fatalf("jobman show error = %v\nstderr: %s", result.err, result.stderr)
	}
	var envelope showEnvelope
	if decodeErr := json.Unmarshal([]byte(result.stdout), &envelope); decodeErr != nil {
		t.Fatalf("decode jobman show JSON: %v\nstdout: %s", decodeErr, result.stdout)
	}

	return envelope.Data
}

func registerCancellationCleanup(t *testing.T, binary, stateDir, jobID string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), commandTimeout)
		defer cancel()
		_ = invoke(ctx, binary, stateDir, "cancel", jobID).err
	})
}

func registerProcessGroupCleanup(t *testing.T, leaderPID int) {
	t.Helper()

	t.Cleanup(func() {
		killErr := syscall.Kill(-leaderPID, syscall.SIGKILL)
		if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
			t.Errorf("clean up process group %d: %v", leaderPID, killErr)
		}
	})
}

func waitForProcessesGone(t *testing.T, pids ...int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), pollTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	remaining := slices.Clone(pids)
	for {
		remaining = slices.DeleteFunc(remaining, func(pid int) bool {
			return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
		})
		if len(remaining) == 0 {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("wait for PIDs %v to disappear: %v", remaining, ctx.Err())
		case <-ticker.C:
		}
	}
}

func loadSupervisor(t *testing.T, stateDir, jobIDText string) model.SupervisorState {
	t.Helper()

	database := openStore(t, stateDir)
	defer closeStore(t, database)
	jobID, parseErr := model.ParseJobID(jobIDText)
	if parseErr != nil {
		t.Fatalf("parse submitted job ID: %v", parseErr)
	}
	owner, ownerErr := database.GetSupervisorForJob(t.Context(), jobID)
	if ownerErr != nil {
		t.Fatalf("load job supervisor: %v", ownerErr)
	}

	return owner
}

func expireSupervisorLease(t *testing.T, stateDir string, owner model.SupervisorState) {
	t.Helper()

	database := openStore(t, stateDir)
	defer closeStore(t, database)
	current, getErr := database.GetSupervisor(t.Context(), owner.ID)
	if getErr != nil {
		t.Fatalf("reload job supervisor: %v", getErr)
	}
	renewedAt := current.LeaseRenewedAt.Add(time.Nanosecond)
	expiresAt := renewedAt.Add(time.Nanosecond)
	if !time.Now().UTC().After(expiresAt) {
		t.Fatalf("test lease expiry %s is not in the past", expiresAt)
	}
	if _, renewErr := database.RenewLease(t.Context(), current.ID, renewedAt, expiresAt); renewErr != nil {
		t.Fatalf("expire job supervisor lease through store API: %v", renewErr)
	}
}

func openStore(t *testing.T, stateDir string) *store.Store {
	t.Helper()

	identifiers, idErr := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if idErr != nil {
		t.Fatalf("construct e2e event ID source: %v", idErr)
	}
	database, openErr := store.Open(t.Context(), store.Options{
		StateDir:      stateDir,
		JobmanVersion: "e2e",
		Now:           time.Now,
		EventIDs:      identifiers,
	})
	if openErr != nil {
		t.Fatalf("open e2e state store: %v", openErr)
	}

	return database
}

func closeStore(t *testing.T, database *store.Store) {
	t.Helper()

	if closeErr := database.Close(); closeErr != nil {
		t.Errorf("close e2e state store: %v", closeErr)
	}
}

func waitForCompletedJob(t *testing.T, binary, stateDir, jobID string) jobDetail {
	t.Helper()

	return waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
		return detail.Summary.Phase == "completed"
	})
}

func waitForJob(
	t *testing.T,
	binary string,
	stateDir string,
	jobID string,
	ready func(jobDetail) bool,
) jobDetail {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), pollTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	var last commandResult
	var lastDetail jobDetail
	for {
		last = invoke(ctx, binary, stateDir, "show", "--json", jobID)
		if last.err == nil {
			var envelope showEnvelope
			if decodeErr := json.Unmarshal([]byte(last.stdout), &envelope); decodeErr == nil {
				lastDetail = envelope.Data
				if ready(lastDetail) {
					return lastDetail
				}
			} else {
				last.err = decodeErr
			}
		}

		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for job %s: %v\nlast detail: %+v\nlast error: %v\nstdout: %s\nstderr: %s",
				jobID,
				ctx.Err(),
				lastDetail,
				last.err,
				last.stdout,
				last.stderr,
			)
		case <-ticker.C:
		}
	}
}

func assertJobAndRunOutcome(t *testing.T, detail jobDetail, outcome string) {
	t.Helper()

	if detail.Summary.Phase != "completed" || detail.Summary.Outcome != outcome {
		t.Fatalf("job phase/outcome = %q/%q, want completed/%s", detail.Summary.Phase, detail.Summary.Outcome, outcome)
	}
	if len(detail.Runs) != 1 {
		t.Fatalf("run count = %d, want 1", len(detail.Runs))
	}
	if detail.Runs[0].Phase != "completed" || detail.Runs[0].Outcome != outcome {
		t.Fatalf("run phase/outcome = %q/%q, want completed/%s", detail.Runs[0].Phase, detail.Runs[0].Outcome, outcome)
	}
}

func invokeWithTimeout(t *testing.T, binary, stateDir string, arguments ...string) commandResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
	defer cancel()

	return invoke(ctx, binary, stateDir, arguments...)
}

func invoke(ctx context.Context, binary, stateDir string, arguments ...string) commandResult {
	commandArguments := make([]string, 0, 2+len(arguments))
	commandArguments = append(commandArguments, "--state-dir", stateDir)
	commandArguments = append(commandArguments, arguments...)
	command := exec.CommandContext(ctx, binary, commandArguments...)
	command.Env = removeEnvironment(os.Environ(), "JOBMAN_STATE_DIR")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()

	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func removeEnvironment(environment []string, name string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}

	return result
}
