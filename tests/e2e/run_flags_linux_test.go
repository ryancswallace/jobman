//go:build linux

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/policy"
)

type runFlagContract struct {
	name  string
	flags []string
	run   func(*testing.T, string)
}

// TestAssembledBinaryRunFlagContracts is the executable contract for the public
// `jobman run` surface. Every public flag must belong to a scenario that crosses
// the assembled-binary boundary and checks either its persisted effective policy
// or its externally observable lifecycle behavior.
func TestAssembledBinaryRunFlagContracts(t *testing.T) {
	binary := buildJobman(t)
	contracts := []runFlagContract{
		{
			name: "direct overlays survive parsing and persistence",
			flags: []string{
				"after-failed", "after-finish", "after-outcome", "after-success",
				"cwd", "env", "failure-limit", "force-after-grace", "group",
				"job-timeout", "max-runs", "name", "pool", "repeat-delay",
				"retry-abort-at", "retry-backoff", "retry-delay", "retry-jitter",
				"retry-max-delay", "retryable-exit-code", "secret-env", "slots",
				"stop-grace", "success-exit-code", "success-target", "tag", "unset-env",
			},
			run: testDirectRunFlagOverlays,
		},
		{
			name:  "job specs and profiles compose in order",
			flags: []string{"job-spec", "profile"},
			run:   testRunJobSpecAndProfiles,
		},
		{
			name:  "detached and attached input modes deliver bytes",
			flags: []string{"foreground", "stdin", "stdin-file", "wait"},
			run:   testRunInputAndAttachmentFlags,
		},
		{
			name: "retry classifications and timeouts drive lifecycle",
			flags: []string{
				"retries", "retry-start-failures", "retry-timeouts", "run-timeout",
			},
			run: testRunRetryAndTimeoutFlags,
		},
		{
			name: "wait conditions honor modes polling and deadlines",
			flags: []string{
				"wait-abort-at", "wait-condition", "wait-delay", "wait-file",
				"wait-mode", "wait-poll", "wait-until",
			},
			run: testRunWaitFlags,
		},
		{
			name: "log capture rotation and retention affect durable logs",
			flags: []string{
				"log-capture", "log-retention", "log-segment-bytes", "log-segments",
			},
			run: testRunLogFlags,
		},
		{
			name:  "notification event overrides reach the selected notifier",
			flags: []string{"notify", "notify-on"},
			run:   testRunNotificationFlags,
		},
		{
			name:  "rerun clones the effective specification",
			flags: []string{"rerun"},
			run:   testRunRerunFlag,
		},
		{
			name: "invalid combinations fail before submission",
			run:  testRunInvalidFlagCombinations,
		},
	}

	assertRunHelpHasContract(t, binary, contracts)
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			contract.run(t, binary)
		})
	}
}

func testDirectRunFlagOverlays(t *testing.T, binary string) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	shell := requireExecutable(t, "sh")
	printf := requireExecutable(t, "printf")
	t.Setenv("JOBMAN_CONTRACT_UNSET", "inherited-value")

	successfulID := submit(t, binary, stateDir, printf, "dependency-success")
	assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, successfulID), "success")
	failedID := submit(t, binary, stateDir, shell, "-c", "exit 9")
	if failed := waitForCompletedJob(t, binary, stateDir, failedID); failed.Summary.Outcome != "failure" {
		t.Fatalf("failed prerequisite outcome = %q, want failure", failed.Summary.Outcome)
	}
	finishedID := submit(t, binary, stateDir, printf, "dependency-finished")
	assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, finishedID), "success")
	outcomeID := submit(t, binary, stateDir, shell, "-c", "exit 11")
	if outcome := waitForCompletedJob(t, binary, stateDir, outcomeID); outcome.Summary.Outcome != "failure" {
		t.Fatalf("outcome prerequisite = %q, want failure", outcome.Summary.Outcome)
	}

	workingDirectory := t.TempDir()
	stdinPath := filepath.Join(t.TempDir(), "stdin.txt")
	if err := os.WriteFile(stdinPath, []byte("unused input"), 0o600); err != nil {
		t.Fatalf("write stdin fixture: %v", err)
	}
	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("test-only-secret"), 0o600); err != nil {
		t.Fatalf("write secret fixture: %v", err)
	}
	namedWaitPath := filepath.Join(t.TempDir(), "named-wait")
	directWaitPath := filepath.Join(t.TempDir(), "direct-wait")
	configuration := writeConfiguration(t, fmt.Sprintf(`---
schema_version: 1
secrets:
  contract_token: %s
concurrency:
  pools:
    research: 3
wait_conditions:
  named_gate:
    type: file-exists
    file_exists:
      path: %s
      type: file
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
      command: [%s, -c, %s]
      output_limit: 4KiB
`, yamlQuote("file:"+secretPath), yamlQuote(namedWaitPath), yamlQuote(shell), yamlQuote("exit 0")))
	executedID := submitConfiguredRun(
		t,
		binary,
		stateDir,
		configuration,
		"--cwd", workingDirectory,
		"--env", "JOBMAN_CONTRACT=value",
		"--unset-env", "JOBMAN_CONTRACT_UNSET",
		"--secret-env", "API_TOKEN=contract_token",
		"--", shell, "-c",
		`[ "$JOBMAN_CONTRACT" = value ] && [ -z "${JOBMAN_CONTRACT_UNSET+x}" ] &&
[ "$API_TOKEN" = test-only-secret ] && pwd`,
	)
	assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, executedID), "success")
	assertLogs(t, binary, stateDir, executedID, "stdout", workingDirectory+"\n")

	now := time.Now().UTC()
	retryAbortAt := now.Add(90 * time.Minute).Format(time.RFC3339Nano)
	waitUntil := now.Add(time.Hour).Format(time.RFC3339Nano)
	waitAbortAt := now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	jobID := submitConfiguredRun(
		t,
		binary,
		stateDir,
		configuration,
		"--name", "complete-contract",
		"--group", "econometrics",
		"--tag", "nightly",
		"--cwd", workingDirectory,
		"--env", "JOBMAN_CONTRACT=value",
		"--unset-env", "JOBMAN_CONTRACT_UNSET",
		"--secret-env", "API_TOKEN=contract_token",
		"--stdin-file", stdinPath,
		"--stop-grace", "275ms",
		"--force-after-grace=false",
		"--max-runs", "4",
		"--success-target", "2",
		"--failure-limit", "3",
		"--success-exit-code", "0",
		"--success-exit-code", "3",
		"--retryable-exit-code", "7",
		"--retry-timeouts",
		"--retry-start-failures",
		"--retry-delay", "125ms",
		"--repeat-delay", "175ms",
		"--retry-backoff", "linear",
		"--retry-jitter", "20ms",
		"--retry-max-delay", "200ms",
		"--retry-abort-at", retryAbortAt,
		"--run-timeout", "10s",
		"--job-timeout", "2h",
		"--after-success", successfulID,
		"--after-finish", finishedID,
		"--after-failed", failedID,
		"--after-outcome", outcomeID+"=failure",
		"--pool", "research",
		"--slots", "2",
		"--wait-condition", "named_gate",
		"--wait-until", waitUntil,
		"--wait-delay", "1h",
		"--wait-file", directWaitPath,
		"--wait-mode", "all",
		"--wait-abort-at", waitAbortAt,
		"--wait-poll", "25ms",
		"--log-segment-bytes", "4KiB",
		"--log-segments", "3",
		"--log-capture", "stderr",
		"--log-retention", "unlimited",
		"--notify", "audit",
		"--notify-on", "job_failed",
		"--", printf, "must-not-run",
	)
	registerCancellationCleanup(t, binary, stateDir, jobID)
	detail := waitForJob(t, binary, stateDir, jobID, func(candidate jobDetail) bool {
		return candidate.Summary.Phase == "waiting"
	})
	if len(detail.Runs) != 0 {
		t.Fatalf("contract job started before its waits: %+v", detail.Runs)
	}

	specification := detail.Specification
	if specification.Executable() != printf || !slices.Equal(specification.Arguments(), []string{"must-not-run"}) ||
		specification.Name() != "complete-contract" || specification.WorkingDirectory() != workingDirectory {
		t.Fatalf("command identity = executable %q arguments %v name %q cwd %q",
			specification.Executable(), specification.Arguments(), specification.Name(), specification.WorkingDirectory())
	}
	if got := specification.Environment(); got["JOBMAN_CONTRACT"] != "value" {
		t.Fatalf("explicit environment = %v", got)
	}
	if !slices.Equal(specification.UnsetEnvironment(), []string{"JOBMAN_CONTRACT_UNSET"}) {
		t.Fatalf("unset environment = %v", specification.UnsetEnvironment())
	}
	if specification.StdinPolicy() != model.StdinFile {
		t.Fatalf("stdin policy = %q, want file", specification.StdinPolicy())
	}
	stop := specification.StopPolicy()
	if stop.GracePeriod != 275*time.Millisecond || stop.ForceAfterGrace {
		t.Fatalf("stop policy = %+v", stop)
	}

	effective := specification.ExecutionPolicy()
	completion := effective.Completion
	if completion.MaxRuns.Value != 4 || completion.SuccessTarget.Value != 2 ||
		completion.FailureLimit.Value != 3 || !completion.HasRetryAbortAt ||
		completion.RetryAbortAt.Format(time.RFC3339Nano) != retryAbortAt {
		t.Fatalf("completion policy = %+v", completion)
	}
	classification := effective.Classification
	if !slices.Equal(classification.SuccessExitCodes, []int{0, 3}) ||
		len(classification.RetryableExitCodes) != 1 || classification.RetryableExitCodes[0].First != 7 ||
		!classification.RetryTimeout || !classification.RetryStartFailure {
		t.Fatalf("classification policy = %+v", classification)
	}
	if effective.FailureDelay.Base != 125*time.Millisecond || effective.SuccessDelay.Base != 175*time.Millisecond ||
		effective.FailureDelay.Backoff != policy.BackoffLinear || effective.SuccessDelay.Backoff != policy.BackoffLinear ||
		effective.FailureDelay.Jitter != 20*time.Millisecond || !effective.FailureDelay.HasMaxDelay ||
		effective.FailureDelay.MaxDelay != 200*time.Millisecond || effective.RunTimeout != 10*time.Second ||
		effective.JobTimeout != 2*time.Hour {
		t.Fatalf("delay/timeout policy = failure %+v success %+v run %s job %s",
			effective.FailureDelay, effective.SuccessDelay, effective.RunTimeout, effective.JobTimeout)
	}
	if effective.Concurrency.Pool != "research" || effective.Concurrency.Slots != 2 ||
		!slices.Equal(effective.Groups, []string{"econometrics"}) || !slices.Equal(effective.Tags, []string{"nightly"}) {
		t.Fatalf("admission/metadata policy = %+v groups %v tags %v",
			effective.Concurrency, effective.Groups, effective.Tags)
	}
	secret := effective.SecretEnv["API_TOKEN"]
	if secret.Provider != "file" || secret.Name != secretPath || effective.StdinPath != stdinPath {
		t.Fatalf("secret/stdin references = secret %+v stdin %q", secret, effective.StdinPath)
	}
	if len(effective.Dependencies) != 4 || len(detail.Dependencies) != 4 || len(effective.WaitConditions) != 4 ||
		effective.WaitMode != policy.WaitModeAll {
		t.Fatalf("prerequisites = spec dependencies %d durable dependencies %d waits %d mode %q",
			len(effective.Dependencies), len(detail.Dependencies), len(effective.WaitConditions), effective.WaitMode)
	}
	for index, condition := range effective.WaitConditions {
		if condition.PollInterval != 25*time.Millisecond ||
			condition.AbortAt.Format(time.RFC3339Nano) != waitAbortAt {
			t.Fatalf("wait condition %d = %+v", index, condition)
		}
	}
	if effective.LogRotateSize != 4096 || effective.LogMaxSegmentsPerStream != 3 ||
		effective.LogCapture != "stderr" || !effective.LogRetentionUnlimited ||
		len(effective.Notifications) != 1 || effective.Notifications[0].Notifier != "audit" ||
		!slices.Equal(effective.Notifications[0].Events, []string{"job_failed"}) {
		t.Fatalf("logging/notification policy = rotate %d segments %d capture %q retention unlimited %t notifications %+v",
			effective.LogRotateSize, effective.LogMaxSegmentsPerStream, effective.LogCapture,
			effective.LogRetentionUnlimited, effective.Notifications)
	}
}

func testRunJobSpecAndProfiles(t *testing.T, binary string) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	printf := requireExecutable(t, "printf")
	configuration := writeConfiguration(t, fmt.Sprintf(`---
schema_version: 1
job_specs:
  base:
    command: [%s, configured-command]
    name: configured-base
profiles:
  first:
    overrides:
      name: first-profile
  second:
    overrides:
      name: second-profile
`, yamlQuote(printf)))
	jobID := submitConfiguredRun(
		t, binary, stateDir, configuration,
		"--job-spec", "base", "--profile", "first", "--profile", "second",
	)
	detail := waitForCompletedJob(t, binary, stateDir, jobID)
	assertJobAndRunOutcome(t, detail, "success")
	if detail.Specification.Name() != "second-profile" || detail.Specification.Executable() != printf ||
		!slices.Equal(detail.Specification.Arguments(), []string{"configured-command"}) {
		t.Fatalf("resolved configured specification = name %q command %q %v",
			detail.Specification.Name(), detail.Specification.Executable(), detail.Specification.Arguments())
	}
	assertLogs(t, binary, stateDir, jobID, "stdout", "configured-command")
}

func testRunInputAndAttachmentFlags(t *testing.T, binary string) {
	t.Helper()
	t.Run("stdin file", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		cat := requireExecutable(t, "cat")
		path := filepath.Join(t.TempDir(), "input.bin")
		payload := []byte{'f', 'i', 'l', 'e', 0, '-', 0xff}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			t.Fatal(err)
		}
		jobID := submitRun(t, binary, stateDir, "--stdin-file", path, "--", cat)
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, jobID), "success")
		logs := invokeWithTimeout(t, binary, stateDir, "logs", "--stream", "stdout", jobID)
		if logs.err != nil || !slices.Equal([]byte(logs.stdout), payload) {
			t.Fatalf("stdin-file logs = %v/%v, want %v", logs.err, []byte(logs.stdout), payload)
		}
	})

	t.Run("live stdin", func(t *testing.T) {
		stateDir := shortStateDir(t)
		requireLiveInputSockets(t, stateDir)
		cat := requireExecutable(t, "cat")
		jobID := submitRun(t, binary, stateDir, "--stdin", "live", "--", cat)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "running" && detail.Runtime.InputEndpoint != ""
		})
		payload := []byte("live-input")
		result := invokeWithInput(t.Context(), binary, stateDir, payload, "input", "--eof", jobID)
		if result.err != nil {
			t.Fatalf("send live input: %v: %s", result.err, result.stderr)
		}
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, jobID), "success")
		assertLogs(t, binary, stateDir, jobID, "stdout", string(payload))
	})

	t.Run("foreground", func(t *testing.T) {
		stateDir := shortStateDir(t)
		requireLiveInputSockets(t, stateDir)
		cat := requireExecutable(t, "cat")
		payload := []byte("foreground-input")
		result := invokeWithInput(t.Context(), binary, stateDir, payload, "run", "--foreground", "--", cat)
		if result.err != nil {
			t.Fatalf("foreground run: %v\nstdout: %q\nstderr: %s", result.err, result.stdout, result.stderr)
		}
		jobID, output, found := strings.Cut(result.stdout, "\n")
		if !found || len(jobID) != 36 || output != string(payload) {
			t.Fatalf("foreground output = %q, want job ID then %q", result.stdout, payload)
		}
		assertJobAndRunOutcome(t, showJob(t, binary, stateDir, jobID), "success")
	})

	t.Run("wait", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		started := time.Now()
		result := invokeWithTimeout(
			t, binary, stateDir, "run", "--wait", "--", shell, "-c", "sleep 0.15; printf waited",
		)
		if result.err != nil || time.Since(started) < 100*time.Millisecond {
			t.Fatalf("run --wait = %v after %s: %s", result.err, time.Since(started), result.stderr)
		}
		jobID := strings.TrimSpace(result.stdout)
		assertJobAndRunOutcome(t, showJob(t, binary, stateDir, jobID), "success")
		assertLogs(t, binary, stateDir, jobID, "stdout", "waited")
	})
}

func testRunRetryAndTimeoutFlags(t *testing.T, binary string) {
	t.Helper()
	t.Run("configured success exit code remaps the outcome", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		jobID := submitRun(t, binary, stateDir, "--success-exit-code", "7", "--", shell, "-c", "exit 7")
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, detail, "success")
		if detail.Runs[0].Exit == nil || detail.Runs[0].Exit.ExitCode == nil || *detail.Runs[0].Exit.ExitCode != 7 {
			t.Fatalf("successful remapped exit = %+v", detail.Runs[0].Exit)
		}
	})

	t.Run("retryable exit succeeds on a later run", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		marker := filepath.Join(t.TempDir(), "attempt")
		shell := requireExecutable(t, "sh")
		jobID := submitRun(
			t, binary, stateDir,
			"--retries", "1", "--retryable-exit-code", "17", "--retry-delay", "75ms",
			"--retry-backoff", "constant", "--retry-jitter", "10ms", "--retry-max-delay", "100ms",
			"--", shell, "-c", `if [ ! -e "$1" ]; then : >"$1"; exit 17; fi`, "jobman-e2e", marker,
		)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "success" || len(detail.Runs) != 2 ||
			detail.Runs[0].Outcome != "failure" || detail.Runs[1].Outcome != "success" {
			t.Fatalf("retry result = %+v", detail)
		}
		if detail.Runs[0].CompletedAt == nil || detail.Runs[1].StartedAt == nil ||
			detail.Runs[1].StartedAt.Sub(*detail.Runs[0].CompletedAt) < 50*time.Millisecond {
			t.Fatalf("retry delay was not observed: runs %+v", detail.Runs)
		}
	})

	t.Run("completion limits count failures and repeated successes", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		counter := filepath.Join(t.TempDir(), "count")
		shell := requireExecutable(t, "sh")
		script := `count=0
if [ -e "$1" ]; then count=$(cat "$1"); fi
count=$((count + 1))
printf '%s' "$count" >"$1"
if [ "$count" -eq 1 ]; then exit 17; fi`
		jobID := submitRun(
			t, binary, stateDir,
			"--max-runs", "5", "--success-target", "2", "--failure-limit", "3",
			"--retryable-exit-code", "17", "--retry-delay", "25ms", "--repeat-delay", "75ms",
			"--", shell, "-c", script, "jobman-e2e", counter,
		)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "success" || len(detail.Runs) != 3 || detail.Runtime.RunCount != 3 ||
			detail.Runtime.SuccessCount != 2 || detail.Runtime.FailureCount != 1 {
			t.Fatalf("completion-count result = %+v", detail)
		}
		if detail.Runs[1].CompletedAt == nil || detail.Runs[2].StartedAt == nil ||
			detail.Runs[2].StartedAt.Sub(*detail.Runs[1].CompletedAt) < 50*time.Millisecond {
			t.Fatalf("successful repeat delay was not observed: %+v", detail.Runs)
		}
	})

	t.Run("failure limit terminates an otherwise unlimited retry policy", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		jobID := submitRun(
			t, binary, stateDir,
			"--max-runs", "unlimited", "--success-target", "1", "--failure-limit", "2",
			"--retryable-exit-code", "17", "--", shell, "-c", "exit 17",
		)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "failure" || len(detail.Runs) != 2 || detail.Runtime.FailureCount != 2 {
			t.Fatalf("failure-limit result = %+v", detail)
		}
	})

	t.Run("retry abort deadline prevents a late attempt", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		shell := requireExecutable(t, "sh")
		abortAt := time.Now().UTC().Add(30 * time.Second).Format(time.RFC3339Nano)
		jobID := submitRun(
			t, binary, stateDir,
			"--retries", "3", "--retry-delay", "1m", "--retry-abort-at", abortAt,
			"--", shell, "-c", "exit 17",
		)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "failure" || len(detail.Runs) != 1 {
			t.Fatalf("retry-abort result = %+v, want one failed run", detail)
		}
	})

	t.Run("run timeout is retryable when selected", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		sleep := requireExecutable(t, "sleep")
		jobID := submitRun(
			t, binary, stateDir,
			"--retries", "1", "--retry-timeouts", "--run-timeout", "100ms", "--stop-grace", "25ms",
			"--", sleep, "60",
		)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "failure" || len(detail.Runs) != 2 ||
			detail.Runs[0].Outcome != "timed_out" || detail.Runs[1].Outcome != "timed_out" {
			t.Fatalf("retryable timeout result = %+v", detail)
		}
	})

	t.Run("start failure is retryable when selected", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		missing := filepath.Join(t.TempDir(), "missing-command")
		jobID := submitRun(t, binary, stateDir, "--retries", "1", "--retry-start-failures", "--", missing)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "failure" || len(detail.Runs) != 2 ||
			detail.Runs[0].Outcome != "start_failed" || detail.Runs[1].Outcome != "start_failed" {
			t.Fatalf("retryable start failure result = %+v", detail)
		}
	})

	t.Run("whole-job timeout bounds an active run", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		sleep := requireExecutable(t, "sleep")
		jobID := submitRun(
			t, binary, stateDir, "--job-timeout", "2s", "--stop-grace", "50ms", "--", sleep, "60",
		)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "timed_out" || len(detail.Runs) != 1 ||
			detail.Runs[0].Outcome != "timed_out" {
			t.Fatalf("whole-job timeout result = %+v", detail)
		}
	})

	t.Run("disabled forced termination lets graceful handling finish", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		ready := filepath.Join(t.TempDir(), "ready")
		graceful := filepath.Join(t.TempDir(), "graceful")
		shell := requireExecutable(t, "sh")
		script := `trap 'sleep 0.2; : >"$2"; exit 0' TERM
: >"$1"
while :; do sleep 1; done`
		jobID := submitRun(
			t, binary, stateDir,
			"--stop-grace", "50ms", "--force-after-grace=false", "--",
			shell, "-c", script, "jobman-e2e", ready, graceful,
		)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		waitForFile(t, ready)
		if canceled := invokeWithTimeout(t, binary, stateDir, "cancel", jobID); canceled.err != nil {
			t.Fatalf("cancel graceful-only job: %v: %s", canceled.err, canceled.stderr)
		}
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, detail, "cancelled") //nolint:misspell // Stable persisted spelling.
		if _, err := os.Stat(graceful); err != nil {
			t.Fatalf("target did not finish its graceful TERM handler: %v", err)
		}
	})
}

func testRunWaitFlags(t *testing.T, binary string) {
	t.Helper()
	t.Run("any mode releases after one condition", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		absent := filepath.Join(t.TempDir(), "absent")
		started := time.Now()
		jobID := submitRun(
			t, binary, stateDir,
			"--wait-delay", "500ms", "--wait-file", absent, "--wait-mode", "any", "--wait-poll", "10ms",
			"--", printf, "any-released",
		)
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, jobID), "success")
		if elapsed := time.Since(started); elapsed < 350*time.Millisecond {
			t.Fatalf("any-mode delay released after %s, want an observable wait", elapsed)
		}
		assertLogs(t, binary, stateDir, jobID, "stdout", "any-released")
	})

	t.Run("all mode waits for time and file", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		gate := filepath.Join(t.TempDir(), "gate")
		until := time.Now().UTC().Add(150 * time.Millisecond).Format(time.RFC3339Nano)
		jobID := submitRun(
			t, binary, stateDir,
			"--wait-until", until, "--wait-file", gate, "--wait-mode", "all", "--wait-poll", "10ms",
			"--", printf, "all-released",
		)
		registerCancellationCleanup(t, binary, stateDir, jobID)
		waitForJob(t, binary, stateDir, jobID, func(detail jobDetail) bool {
			return detail.Summary.Phase == "waiting"
		})
		if err := os.WriteFile(gate, []byte("ready"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, jobID), "success")
	})

	t.Run("named condition and abort deadline", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		gate := filepath.Join(t.TempDir(), "named-gate")
		configuration := writeConfiguration(t, fmt.Sprintf(`---
schema_version: 1
wait_conditions:
  imported:
    type: file-exists
    file_exists:
      path: %s
      type: file
`, yamlQuote(gate)))
		abortAt := time.Now().UTC().Add(300 * time.Millisecond).Format(time.RFC3339Nano)
		jobID := submitConfiguredRun(
			t, binary, stateDir, configuration,
			"--wait-condition", "imported", "--wait-abort-at", abortAt, "--", printf, "must-not-run",
		)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		if detail.Summary.Outcome != "aborted" || len(detail.Runs) != 0 {
			t.Fatalf("named wait abort result = %+v", detail)
		}
	})
}

func testRunLogFlags(t *testing.T, binary string) {
	t.Helper()
	t.Run("capture modes select streams", func(t *testing.T) {
		for _, capture := range []string{"stdout", "stderr", "none"} {
			t.Run(capture, func(t *testing.T) {
				stateDir := filepath.Join(t.TempDir(), "state")
				shell := requireExecutable(t, "sh")
				jobID := submitRun(
					t, binary, stateDir, "--log-capture", capture, "--",
					shell, "-c", "printf stdout-value; printf stderr-value >&2",
				)
				detail := waitForCompletedJob(t, binary, stateDir, jobID)
				assertJobAndRunOutcome(t, detail, "success")
				stdout := invokeWithTimeout(t, binary, stateDir, "logs", "--stream", "stdout", jobID)
				stderr := invokeWithTimeout(t, binary, stateDir, "logs", "--stream", "stderr", jobID)
				wantStdout := ""
				wantStderr := ""
				if capture == "stdout" {
					wantStdout = "stdout-value"
				}
				if capture == "stderr" {
					wantStderr = "stderr-value"
				}
				if stdout.err != nil || stderr.err != nil || stdout.stdout != wantStdout || stderr.stdout != wantStderr {
					t.Fatalf("capture %q logs = stdout %q/%v stderr %q/%v",
						capture, stdout.stdout, stdout.err, stderr.stdout, stderr.err)
				}
			})
		}
	})

	t.Run("rotation remains lossless", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		jobID := submitRun(
			t, binary, stateDir,
			"--log-segment-bytes", "4", "--log-segments", "4", "--", printf, "abcdefghijkl",
		)
		detail := waitForCompletedJob(t, binary, stateDir, jobID)
		assertJobAndRunOutcome(t, detail, "success")
		if detail.Runs[0].Logs.IndexVersion != 2 || detail.Runs[0].Logs.StdoutSize != 12 {
			t.Fatalf("rotated log metadata = %+v", detail.Runs[0].Logs)
		}
		assertLogs(t, binary, stateDir, jobID, "stdout", "abcdefghijkl")
	})

	t.Run("zero retention makes completed logs cleanable", func(t *testing.T) {
		stateDir := filepath.Join(t.TempDir(), "state")
		printf := requireExecutable(t, "printf")
		jobID := submitRun(t, binary, stateDir, "--log-retention", "0s", "--", printf, "ephemeral")
		assertJobAndRunOutcome(t, waitForCompletedJob(t, binary, stateDir, jobID), "success")
		cleaned := invokeWithTimeout(t, binary, stateDir, "clean", "--force")
		if cleaned.err != nil || !strings.HasPrefix(cleaned.stdout, "removed 1 runs") {
			t.Fatalf("retention cleanup = %q/%v: %s", cleaned.stdout, cleaned.err, cleaned.stderr)
		}
		detail := showJob(t, binary, stateDir, jobID)
		if len(detail.Runs) != 1 || detail.Runs[0].Logs.Available || detail.Runs[0].Logs.PrunedAt == nil {
			t.Fatalf("retention cleanup metadata = %+v", detail.Runs)
		}
	})
}

func testRunNotificationFlags(t *testing.T, binary string) {
	t.Helper()
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
`, yamlQuote(shell), yamlQuote(`IFS= read -r line; printf '%s\n' "$line" >"$1"`), yamlQuote(eventPath)))
	jobID := submitConfiguredRun(
		t, binary, stateDir, configuration,
		"--notify", "audit", "--notify-on", "run_started", "--", printf, "notified",
	)
	detail := waitForJob(t, binary, stateDir, jobID, func(candidate jobDetail) bool {
		return candidate.Summary.Phase == "completed" && len(candidate.NotificationDeliveries) == 1 &&
			candidate.NotificationDeliveries[0].Status == "succeeded"
	})
	assertJobAndRunOutcome(t, detail, "success")
	delivery := detail.NotificationDeliveries[0]
	if delivery.Notifier != "audit" || delivery.EventType != "run_started" || delivery.AttemptCount != 1 {
		t.Fatalf("notification delivery = %+v", delivery)
	}
	payload, err := os.ReadFile(eventPath) // #nosec G304 -- Test-controlled path.
	if err != nil {
		t.Fatalf("read notification payload: %v", err)
	}
	var event struct {
		EventType string `json:"type"`
		JobID     string `json:"job_id"`
	}
	if err := json.Unmarshal(payload, &event); err != nil || event.EventType != "run_started" || event.JobID != jobID {
		t.Fatalf("notification payload = %s / %+v / %v", payload, event, err)
	}
}

func testRunRerunFlag(t *testing.T, binary string) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	printf := requireExecutable(t, "printf")
	originalID := submitRun(
		t, binary, stateDir, "--name", "original", "--env", "COPY=value", "--", printf, "copied-output",
	)
	original := waitForCompletedJob(t, binary, stateDir, originalID)
	assertJobAndRunOutcome(t, original, "success")
	copyID := submitRun(t, binary, stateDir, "--rerun", originalID, "--name", "copy", "--wait")
	copied := showJob(t, binary, stateDir, copyID)
	assertJobAndRunOutcome(t, copied, "success")
	if copied.Specification.Name() != "copy" ||
		copied.Specification.Executable() != original.Specification.Executable() ||
		!slices.Equal(copied.Specification.Arguments(), original.Specification.Arguments()) ||
		copied.Specification.Environment()["COPY"] != "value" {
		t.Fatalf("rerun specification = %+v, original = %+v", copied.Specification, original.Specification)
	}
	assertLogs(t, binary, stateDir, copyID, "stdout", "copied-output")
}

func testRunInvalidFlagCombinations(t *testing.T, binary string) {
	t.Helper()
	printf := requireExecutable(t, "printf")
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(stdinPath, []byte("input"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		arguments []string
	}{
		{name: "retries and max runs", arguments: []string{"--retries", "1", "--max-runs", "2", "--", printf, "x"}},
		{name: "stdin and stdin file", arguments: []string{"--stdin", "live", "--stdin-file", stdinPath, "--", printf, "x"}},
		{name: "foreground and stdin file", arguments: []string{"--foreground", "--stdin-file", stdinPath, "--", printf, "x"}},
		{name: "command without boundary", arguments: []string{printf, "x"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			arguments := append([]string{"run"}, test.arguments...)
			result := invokeWithTimeout(t, binary, stateDir, arguments...)
			assertCommandExitCode(t, result, 2)
			listed := invokeWithTimeout(t, binary, stateDir, "list", "--json", "--all")
			var envelope struct {
				Data struct {
					Jobs []json.RawMessage `json:"jobs"`
				} `json:"data"`
			}
			decodeErr := json.Unmarshal([]byte(listed.stdout), &envelope)
			if listed.err != nil || decodeErr != nil || len(envelope.Data.Jobs) != 0 {
				t.Fatalf("invalid run left durable state: %q/%v: %s", listed.stdout, listed.err, listed.stderr)
			}
		})
	}
}

func assertRunHelpHasContract(t *testing.T, binary string, contracts []runFlagContract) {
	t.Helper()
	covered := make(map[string]string)
	for _, contract := range contracts {
		for _, flag := range contract.flags {
			if previous, duplicate := covered[flag]; duplicate {
				t.Fatalf("run flag --%s is assigned to both %q and %q", flag, previous, contract.name)
			}
			covered[flag] = contract.name
		}
	}

	result := invokeWithTimeout(t, binary, filepath.Join(t.TempDir(), "state"), "run", "--help")
	if result.err != nil {
		t.Fatalf("jobman run --help: %v: %s", result.err, result.stderr)
	}
	help := strings.Split(result.stdout, "Global Flags:")[0]
	flagPattern := regexp.MustCompile(`(?m)^\s+(?:-[A-Za-z],\s+)?--([a-z][a-z0-9-]*)\b`)
	public := make(map[string]struct{})
	for _, match := range flagPattern.FindAllStringSubmatch(help, -1) {
		if match[1] != "help" {
			public[match[1]] = struct{}{}
		}
	}
	var missing, stale []string
	for flag := range public {
		if _, found := covered[flag]; !found {
			missing = append(missing, "--"+flag)
		}
	}
	for flag := range covered {
		if _, found := public[flag]; !found {
			stale = append(stale, "--"+flag)
		}
	}
	slices.Sort(missing)
	slices.Sort(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("run flag behavior-contract mismatch: missing %v; stale %v", missing, stale)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect %s: %v", path, err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for file %s: %v", path, ctx.Err())
		case <-ticker.C:
		}
	}
}
