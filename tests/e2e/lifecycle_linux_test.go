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
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/liveinput"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	buildTimeout   = 2 * time.Minute
	commandTimeout = 20 * time.Second
	pollTimeout    = 35 * time.Second
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
	Summary                jobSummary                   `json:"summary"`
	Runs                   []runDetail                  `json:"runs"`
	Runtime                jobRuntime                   `json:"runtime"`
	NotificationDeliveries []notificationDeliveryDetail `json:"notification_deliveries"`
}

type jobRuntime struct {
	InputEndpoint string `json:"input_endpoint"`
}

type jobSummary struct {
	Phase   string `json:"phase"`
	Outcome string `json:"outcome"`
}

type runDetail struct {
	Process *processIdentity `json:"process"`
	Exit    *exitInfo        `json:"exit"`
	Logs    logDetail        `json:"logs"`
	Outcome string           `json:"outcome"`
	Phase   string           `json:"phase"`
}

type logDetail struct {
	Available    bool    `json:"available"`
	IndexVersion int     `json:"index_version"`
	StdoutSize   int64   `json:"stdout_size"`
	PrunedAt     *string `json:"pruned_at"`
}

type notificationDeliveryDetail struct {
	Notifier     string `json:"notifier"`
	EventType    string `json:"event_type"`
	Status       string `json:"status"`
	AttemptCount int    `json:"attempt_count"`
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

	t.Run("retry policy starts another run and preserves both logs", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		counter := filepath.Join(t.TempDir(), "attempted")
		shell := requireExecutable(t, "sh")
		script := `if [ ! -e "$1" ]; then
    : > "$1"
    printf 'first-attempt\n'
    exit 17
fi
printf 'second-attempt\n'`
		jobID := submitRun(
			t,
			binary,
			stateDir,
			"--retries", "1", "--retry-delay", "1ms", "--",
			shell, "-c", script, "jobman-e2e", counter,
		)

		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		if completed.Summary.Outcome != "success" || len(completed.Runs) != 2 ||
			completed.Runs[0].Outcome != "failure" || completed.Runs[1].Outcome != "success" {
			t.Fatalf("retry job = %+v, want failed run followed by successful run", completed)
		}
		result := invokeWithTimeout(t, binary, stateDir, "logs", "--all", "--raw", "--stream", "stdout", jobID)
		if result.err != nil {
			t.Fatalf("jobman logs --all error = %v\nstderr: %s", result.err, result.stderr)
		}
		if result.stdout != "first-attempt\nsecond-attempt\n" {
			t.Fatalf("all-run logs = %q", result.stdout)
		}
	})

	t.Run("dependency waits for successful predecessor", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		gate := filepath.Join(t.TempDir(), "release")
		shell := requireExecutable(t, "sh")
		printf := requireExecutable(t, "printf")
		predecessor := submit(
			t,
			binary,
			stateDir,
			shell,
			"-c", `while [ ! -e "$1" ]; do :; done`, "jobman-e2e", gate,
		)
		registerCancellationCleanup(t, binary, stateDir, predecessor)
		dependent := submitRun(
			t,
			binary,
			stateDir,
			"--after-success", predecessor, "--", printf, "dependency-ran",
		)
		registerCancellationCleanup(t, binary, stateDir, dependent)

		waiting := waitForJob(t, binary, stateDir, dependent, func(detail jobDetail) bool {
			return detail.Summary.Phase == "waiting"
		})
		if len(waiting.Runs) != 0 {
			t.Fatalf("dependent started %d run(s) before predecessor completed", len(waiting.Runs))
		}
		if writeErr := os.WriteFile(gate, []byte("release"), 0o600); writeErr != nil {
			t.Fatalf("release predecessor: %v", writeErr)
		}
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, predecessor), "success")
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, dependent), "success")
		assertLogs(t, binary, stateDir, dependent, "stdout", "dependency-ran")
	})

	t.Run("global admission queues a later job", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		gate := filepath.Join(t.TempDir(), "release")
		configuration := writeConfiguration(t, `---
schema_version: 1
concurrency:
  max_active_slots: 1
`)
		shell := requireExecutable(t, "sh")
		printf := requireExecutable(t, "printf")
		blocker := submitConfiguredRun(
			t, binary, stateDir, configuration,
			"--", shell, "-c", `while [ ! -e "$1" ]; do :; done`, "jobman-e2e", gate,
		)
		registerCancellationCleanup(t, binary, stateDir, blocker)
		waitForJob(t, binary, stateDir, blocker, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running"
		})
		queued := submitConfiguredRun(t, binary, stateDir, configuration, "--", printf, "admitted")
		registerCancellationCleanup(t, binary, stateDir, queued)
		waiting := waitForJob(t, binary, stateDir, queued, func(detail jobDetail) bool {
			return detail.Summary.Phase == "queued"
		})
		if len(waiting.Runs) != 0 {
			t.Fatalf("queued job started %d run(s) before admission", len(waiting.Runs))
		}
		if err := os.WriteFile(gate, []byte("release"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, blocker), "success")
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, queued), "success")
		assertLogs(t, binary, stateDir, queued, "stdout", "admitted")
	})

	t.Run("run timeout records a timed out outcome", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		sleep := requireExecutable(t, "sleep")
		jobID := submitRun(
			t, binary, stateDir,
			"--run-timeout", "150ms", "--stop-grace", "50ms", "--", sleep, "60",
		)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		if completed.Summary.Outcome != "failure" || len(completed.Runs) != 1 ||
			completed.Runs[0].Outcome != "timed_out" {
			t.Fatalf("timed-out run = %+v, want timed_out run and terminal job failure", completed)
		}
	})

	t.Run("rotated logs remain lossless and versioned", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		jobID := submitRun(
			t, binary, stateDir,
			"--log-segment-bytes", "4", "--log-segments", "4", "--", printf, "abcdefghijkl",
		)
		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "success")
		if completed.Runs[0].Logs.IndexVersion != 2 || completed.Runs[0].Logs.StdoutSize != 12 {
			t.Fatalf("rotated log metadata = %+v, want index v2 and 12 stdout bytes", completed.Runs[0].Logs)
		}
		assertLogs(t, binary, stateDir, jobID, "stdout", "abcdefghijkl")
		for _, name := range []string{"stdout.log", "stdout.000002.log", "stdout.000003.log"} {
			if _, err := os.Stat(filepath.Join(stateDir, "logs", jobID, "1", name)); err != nil {
				t.Fatalf("rotated segment %s: %v", name, err)
			}
		}
	})

	t.Run("command notification receives a versioned terminal event", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		eventPath := filepath.Join(t.TempDir(), "event.json")
		shell := requireExecutable(t, "sh")
		printf := requireExecutable(t, "printf")
		configuration := writeConfiguration(t, fmt.Sprintf(`---
schema_version: 1
notifiers:
  audit:
    type: command
    events: [job_succeeded]
    timeout: 5s
    retry:
      max_attempts: 1
      delay: 1ms
      max_delay: 1ms
    command:
      command: [%s, -c, %s, jobman-notifier, %s]
      output_limit: 4KiB
`, yamlQuote(shell), yamlQuote(`IFS= read -r line; printf '%s\n' "$line" > "$1"`), yamlQuote(eventPath)))
		jobID := submitConfiguredRun(
			t, binary, stateDir, configuration, "--notify", "audit", "--", printf, "notified",
		)
		completed := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, completed, "success")
		if len(completed.NotificationDeliveries) != 1 {
			t.Fatalf("notification deliveries = %+v, want one", completed.NotificationDeliveries)
		}
		delivery := completed.NotificationDeliveries[0]
		if delivery.Notifier != "audit" || delivery.EventType != "job_succeeded" ||
			delivery.Status != "succeeded" || delivery.AttemptCount != 1 {
			t.Fatalf("notification delivery = %+v", delivery)
		}
		payload, err := os.ReadFile(eventPath) // #nosec G304 -- Test-controlled path.
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			SchemaVersion int    `json:"schema_version"`
			ID            string `json:"id"`
			Type          string `json:"type"`
			JobID         string `json:"job_id"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode notification event: %v: %s", err, payload)
		}
		if event.SchemaVersion != 1 || event.ID == "" || event.Type != "job_succeeded" || event.JobID != jobID {
			t.Fatalf("notification event = %+v", event)
		}
	})

	t.Run("cancel terminates a grandchild process tree", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		sleep := requireExecutable(t, "sleep")
		script := `printf '%s\n' "$$"
"$1" -c '"$1" 60 & grandchild=$!; printf "%s %s\n" "$$" "$grandchild"; wait "$grandchild"' jobman-child "$2" &
wait "$!"`
		jobID := submit(t, binary, stateDir, shell, "-c", script, "jobman-e2e", shell, sleep)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running"
		})
		pids := waitForLoggedPIDs(t, binary, stateDir, jobID, 3)
		registerProcessGroupCleanup(t, pids[0])
		if result := invokeWithTimeout(t, binary, stateDir, "cancel", jobID); result.err != nil {
			t.Fatalf("cancel grandchild tree: %v: %s", result.err, result.stderr)
		}
		assertJobAndRunOutcome( //nolint:misspell // Stable persisted spelling.
			t, waitForCompletedJob(t, binary, stateDir, jobID), "cancelled",
		)
		waitForProcessesGone(t, pids...)
	})

	t.Run("cancel force terminates a target that ignores graceful stop", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		sleep := requireExecutable(t, "sleep")
		script := `trap '' TERM
printf '%s\n' "$$"
while :; do "$1" 1; done`
		jobID := submitRun(
			t, binary, stateDir,
			"--stop-grace", "100ms", "--force-after-grace=true", "--",
			shell, "-c", script, "jobman-e2e", sleep,
		)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		pid := waitForLoggedPIDs(t, binary, stateDir, jobID, 1)[0]
		registerProcessGroupCleanup(t, pid)
		if result := invokeWithTimeout(t, binary, stateDir, "cancel", jobID); result.err != nil {
			t.Fatalf("force cancel: %v: %s", result.err, result.stderr)
		}
		assertJobAndRunOutcome( //nolint:misspell // Stable persisted spelling.
			t, waitForCompletedJob(t, binary, stateDir, jobID), "cancelled",
		)
		waitForProcessesGone(t, pid)
	})

	t.Run("job survives controlling terminal closure", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		gate := filepath.Join(t.TempDir(), "release")
		scriptCommand := requireExecutable(t, "script")
		shell := requireExecutable(t, "sh")
		targetScript := `while [ ! -e "$1" ]; do :; done; printf terminal-closed`
		commandLine := strings.Join([]string{
			shellQuote(binary), "--state-dir", shellQuote(stateDir), "run", "--",
			shellQuote(shell), "-c", shellQuote(targetScript), "jobman-e2e", shellQuote(gate),
		}, " ")
		ctx, cancel := context.WithTimeout(t.Context(), commandTimeout)
		defer cancel()
		command := exec.CommandContext(ctx, scriptCommand, "-q", "-e", "-c", commandLine, "/dev/null")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("submit through controlling terminal: %v: %s", err, output)
		}
		match := regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`).Find(output)
		if len(match) == 0 {
			t.Fatalf("terminal submission output has no job ID: %q", output)
		}
		jobID := string(match)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running"
		})
		if err := os.WriteFile(gate, []byte("release"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, jobID), "success")
		assertLogs(t, binary, stateDir, jobID, "stdout", "terminal-closed")
	})

	t.Run("pause and resume preserve active process ownership", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		sleep := requireExecutable(t, "sleep")
		jobID := submit(t, binary, stateDir, sleep, "60")
		registerCancellationCleanup(t, binary, stateDir, jobID)
		running := waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running" && len(detail.Runs) == 1 && detail.Runs[0].Process != nil
		})
		registerProcessGroupCleanup(t, running.Runs[0].Process.PID)

		if result := invokeWithTimeout(t, binary, stateDir, "pause", jobID); result.err != nil {
			t.Fatalf("jobman pause error = %v\nstderr: %s", result.err, result.stderr)
		}
		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "paused"
		})
		if result := invokeWithTimeout(t, binary, stateDir, "resume", jobID); result.err != nil {
			t.Fatalf("jobman resume error = %v\nstderr: %s", result.err, result.stderr)
		}
		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running"
		})
		if result := invokeWithTimeout(t, binary, stateDir, "cancel", jobID); result.err != nil {
			t.Fatalf("jobman cancel after resume error = %v\nstderr: %s", result.err, result.stderr)
		}
		assertJobAndRunOutcome( //nolint:misspell // The specification defines this persisted spelling.
			t, waitForCompletedJob(t, binary, stateDir, jobID), "cancelled",
		)
	})

	t.Run("live input delivers binary bytes and durable EOF", func(t *testing.T) {
		stateDir := shortStateDir(t)
		requireLiveInputSockets(t, stateDir)
		cat := requireExecutable(t, "cat")
		jobID := submitRun(t, binary, stateDir, "--stdin", "live", "--", cat)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running" && detail.Runtime.InputEndpoint != ""
		})
		payload := []byte{'b', 'i', 'n', 'a', 'r', 'y', 0, 0xff, '\n'}
		result := invokeWithInput(t.Context(), binary, stateDir, payload, "input", "--eof", jobID)
		if result.err != nil {
			t.Fatalf("jobman input error = %v\nstderr: %s", result.err, result.stderr)
		}
		if strings.TrimSpace(result.stdout) != strconv.Itoa(len(payload)) {
			t.Fatalf("jobman input output = %q, want delivered byte count", result.stdout)
		}
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, jobID), "success")
		logs := invokeWithTimeout(t, binary, stateDir, "logs", "--stream", "stdout", jobID)
		if logs.err != nil || !bytes.Equal([]byte(logs.stdout), payload) {
			t.Fatalf("live-input logs = %v/%q, want %v", logs.err, logs.stdout, payload)
		}
	})

	t.Run("rerun clones the effective specification", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		source := submit(t, binary, stateDir, printf, "rerun-output")
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, source), "success")
		rerun := submitRun(t, binary, stateDir, "--rerun", source)
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, rerun), "success")
		assertLogs(t, binary, stateDir, rerun, "stdout", "rerun-output")
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

func TestAssembledBinaryCrashBoundaries(t *testing.T) {
	binary := buildJobman(t)
	shell := requireExecutable(t, "sh")

	points := []string{
		"job-insert-committed",
		"supervisor-claimed-before-ack",
		"supervisor-acknowledged",
		"target-before-start",
		"target-started-before-identity",
		"target-identity-committed",
		"log-raw-synced-before-index",
		"log-index-synced",
		"run-completion-before-commit",
		"run-completion-committed",
		"job-completion-committed",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			result := invokeFault(
				t.Context(),
				binary,
				stateDir,
				point,
				"run",
				"--",
				shell,
				"-c",
				"printf crash-boundary; sleep 0.2",
			)
			jobID := strings.TrimSpace(result.stdout)
			if len(jobID) != 36 {
				jobID = waitForNewestJobID(t, binary, stateDir)
			}
			completed := waitForCompletedJob(t, binary, stateDir, jobID)
			if completed.Summary.Outcome == "" {
				t.Fatalf("crash point %s produced empty terminal outcome", point)
			}
			if doctor := invokeWithTimeout(t, binary, stateDir, "doctor", "--json"); doctor.err != nil {
				t.Fatalf("doctor after crash point %s: %v: %s", point, doctor.err, doctor.stderr)
			}
		})
	}

	for _, point := range []string{"cancellation-intent-committed", "cancellation-signal-sent"} {
		t.Run(point, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			jobID := submit(t, binary, stateDir, shell, "-c", "sleep 60")
			registerCancellationCleanup(t, binary, stateDir, jobID)
			waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
				return detail.Summary.Phase == "running"
			})
			_ = invokeFault(t.Context(), binary, stateDir, point, "cancel", jobID)
			completed := waitForCompletedJob(t, binary, stateDir, jobID)
			if completed.Summary.Outcome != "cancelled" { //nolint:misspell // Stable persisted spelling.
				t.Fatalf("outcome after %s = %q, want canceled", point, completed.Summary.Outcome)
			}
		})
	}

	t.Run("cleanup-files-removed-before-metadata", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		jobID := submit(t, binary, stateDir, shell, "-c", "printf cleanup-boundary")
		waitForCompletedJob(t, binary, stateDir, jobID)
		point := "cleanup-files-removed-before-metadata"
		crashed := invokeFault(
			t.Context(), binary, stateDir, point,
			"clean", jobID, "--older-than", "0s", "--dry-run=false", "--force",
		)
		if crashed.err == nil {
			t.Fatalf("cleanup fault point %s did not terminate the client", point)
		}
		resumed := invokeWithTimeout(
			t, binary, stateDir,
			"clean", jobID, "--older-than", "0s", "--dry-run=false", "--force",
		)
		if resumed.err != nil {
			t.Fatalf("resume cleanup after crash: %v: %s", resumed.err, resumed.stderr)
		}
		detail := showJob(t, binary, stateDir, jobID)
		if len(detail.Runs) != 1 || detail.Runs[0].Logs.Available || detail.Runs[0].Logs.PrunedAt == nil {
			t.Fatalf("resumed cleanup logs = %+v, want durable pruning metadata", detail.Runs)
		}
		if doctor := invokeWithTimeout(t, binary, stateDir, "doctor", "--json"); doctor.err != nil {
			t.Fatalf("doctor after cleanup crash: %v: %s", doctor.err, doctor.stderr)
		}
	})
}

func buildJobman(t *testing.T) string {
	t.Helper()

	repository := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "jobman")
	ctx, cancel := context.WithTimeout(t.Context(), buildTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "build", "-tags", "jobman_faultinject", "-o", binary, ".")
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

func shortStateDir(t *testing.T) string {
	t.Helper()

	// Native Unix socket addresses have a small fixed path limit. Go's
	// test-name-based TempDir paths can exceed it even though ordinary Jobman
	// state roots do not, so keep this assembled live-input fixture short.
	stateDir, err := os.MkdirTemp("", "jm-e2e-") //nolint:usetesting // t.TempDir paths exceed Unix socket limits here.
	if err != nil {
		t.Fatalf("create short live-input state directory: %v", err)
	}
	t.Cleanup(func() {
		if removeErr := os.RemoveAll(stateDir); removeErr != nil {
			t.Errorf("remove short live-input state directory: %v", removeErr)
		}
	})

	return stateDir
}

func requireLiveInputSockets(t *testing.T, stateDir string) {
	t.Helper()

	probe, err := liveinput.Listen(filepath.Join(stateDir, "socket-probe"))
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("local sockets are blocked by the test environment: %v", err)
		}
		t.Fatalf("probe live-input socket support: %v", err)
	}
	if closeErr := probe.Close(); closeErr != nil {
		t.Fatalf("close live-input socket probe: %v", closeErr)
	}
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

	commandArguments := make([]string, 0, 2+len(arguments))
	commandArguments = append(commandArguments, "--", executable)
	commandArguments = append(commandArguments, arguments...)

	return submitRun(t, binary, stateDir, commandArguments...)
}

func submitRun(t *testing.T, binary, stateDir string, arguments ...string) string {
	t.Helper()

	commandArguments := make([]string, 0, 1+len(arguments))
	commandArguments = append(commandArguments, "run")
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

func submitConfiguredRun(
	t *testing.T,
	binary,
	stateDir,
	configuration string,
	arguments ...string,
) string {
	t.Helper()

	commandArguments := make([]string, 0, 3+len(arguments))
	commandArguments = append(commandArguments, "--config", configuration, "run")
	commandArguments = append(commandArguments, arguments...)
	result := invokeWithTimeout(t, binary, stateDir, commandArguments...)
	if result.err != nil {
		t.Fatalf("configured jobman run error = %v\nstdout: %s\nstderr: %s", result.err, result.stdout, result.stderr)
	}
	jobID := strings.TrimSpace(result.stdout)
	if len(jobID) != 36 || strings.Count(jobID, "-") != 4 {
		t.Fatalf("configured jobman run output = %q, want one canonical job ID", result.stdout)
	}

	return jobID
}

func writeConfiguration(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "jobman.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write Jobman configuration: %v", err)
	}

	return path
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
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
	return invokeWithInput(ctx, binary, stateDir, nil, arguments...)
}

func invokeFault(
	ctx context.Context,
	binary,
	stateDir,
	point string,
	arguments ...string,
) commandResult {
	commandArguments := append([]string{"--state-dir", stateDir}, arguments...)
	command := exec.CommandContext(ctx, binary, commandArguments...)
	command.Env = append(
		removeEnvironment(os.Environ(), "JOBMAN_STATE_DIR"),
		"JOBMAN_ENABLE_FAULT_INJECTION=1",
		"JOBMAN_FAULT_POINT="+point,
	)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()

	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func waitForNewestJobID(t *testing.T, binary, stateDir string) string {
	t.Helper()
	type listEnvelope struct {
		Data struct {
			Jobs []struct {
				ID string `json:"id"`
			} `json:"jobs"`
		} `json:"data"`
	}
	ctx, cancel := context.WithTimeout(t.Context(), pollTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		result := invoke(ctx, binary, stateDir, "list", "--json", "--all")
		var envelope listEnvelope
		if result.err == nil && json.Unmarshal([]byte(result.stdout), &envelope) == nil && len(envelope.Data.Jobs) > 0 {
			return envelope.Data.Jobs[0].ID
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for crash-boundary job: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func invokeWithInput(
	ctx context.Context,
	binary string,
	stateDir string,
	input []byte,
	arguments ...string,
) commandResult {
	commandArguments := make([]string, 0, 2+len(arguments))
	commandArguments = append(commandArguments, "--state-dir", stateDir)
	commandArguments = append(commandArguments, arguments...)
	command := exec.CommandContext(ctx, binary, commandArguments...)
	command.Env = removeEnvironment(os.Environ(), "JOBMAN_STATE_DIR")
	command.Stdin = bytes.NewReader(input)
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
