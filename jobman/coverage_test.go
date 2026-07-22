package jobman

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/policy"
	"github.com/ryancswallace/jobman/internal/store"
)

func TestHumanListShowAndConfigCommands(t *testing.T) {
	backend := newFakeBackend(t)
	started := time.Now().UTC()
	completed := started.Add(time.Second)
	exitCode := 7
	backend.details.Runs = []model.RunState{{
		Number: 1, Phase: model.RunPhaseCompleted, Outcome: model.RunOutcomeFailure,
		StartedAt: &started, CompletedAt: &completed,
	}}
	for _, test := range []struct {
		arguments []string
		contains  string
	}{
		{arguments: []string{"list"}, contains: "SUBMITTED"},
		{arguments: []string{"show", testJobID}, contains: "Completed runs:"},
		{arguments: []string{"config", "show"}, contains: "schema_version"},
		{arguments: []string{"config", "show", "--origins"}, contains: "sources"},
		{arguments: []string{"config", "paths"}, contains: "system"},
		{arguments: []string{"config", "validate"}, contains: "valid"},
	} {
		stdout, err := executeCommand(t, dependenciesFor(backend), test.arguments)
		if err != nil || !strings.Contains(stdout, test.contains) {
			t.Errorf("%v = (%q, %v), want %q", test.arguments, stdout, err, test.contains)
		}
		backend.closed = false
	}
	process := &model.ProcessIdentity{PID: 123, Platform: "test", CreationID: "create", BootID: "boot", TreeID: "tree"}
	exit := &model.ExitInfo{ExitCode: &exitCode, ObservedAt: completed}
	if presentProcess(process).PID != 123 || presentProcess(nil) != nil || presentExit(exit).ExitCode == nil || presentExit(nil) != nil {
		t.Fatal("process/exit presentation did not preserve optional values")
	}
	if formatOptionalTime(nil) != "" || formatOptionalTime(&completed) == "" {
		t.Fatal("formatOptionalTime() returned an unexpected value")
	}
}

func TestExecuteVersionPath(t *testing.T) {
	prior := os.Args
	os.Args = []string{"jobman", "--version"}
	t.Cleanup(func() { os.Args = prior })
	if err := Execute(); err != nil {
		t.Fatalf("Execute(--version) error = %v", err)
	}
}

func TestLogAndRunSelectionHelpers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		content string
		lines   uint64
		want    string
	}{
		{content: "", lines: 1, want: ""},
		{content: "one\ntwo\nthree\n", lines: 0, want: ""},
		{content: "one\ntwo\nthree\n", lines: 2, want: "two\nthree\n"},
		{content: "one", lines: 4, want: "one"},
	} {
		if got := string(lastLines([]byte(test.content), test.lines)); got != test.want {
			t.Errorf("lastLines(%q, %d) = %q, want %q", test.content, test.lines, got, test.want)
		}
	}
	details := app.JobDetails{Runs: []model.RunState{{Number: 2}, {Number: 5}}}
	for selection, want := range map[string]uint64{"2": 2, "-1": 5, "-2": 2} {
		if got, err := resolveRunSelection(selection, details); err != nil || got != want {
			t.Errorf("resolveRunSelection(%q) = (%d, %v), want %d", selection, got, err, want)
		}
	}
	for _, selection := range []string{"0", "bad"} {
		if _, err := resolveRunSelection(selection, details); !errors.Is(err, errUsage) {
			t.Errorf("resolveRunSelection(%q) error = %v, want usage", selection, err)
		}
	}
	for _, selection := range []string{"1", "-3"} {
		if _, err := resolveRunSelection(selection, details); !errors.Is(err, app.ErrNotFound) {
			t.Errorf("resolveRunSelection(%q) error = %v, want not found", selection, err)
		}
	}
}

func TestRunPolicyParsingHelpers(t *testing.T) {
	t.Parallel()

	if got, err := dependencyPredicate([]string{"failure"}); err != nil || got != "failed" {
		t.Fatalf("dependencyPredicate(failure) = (%q, %v)", got, err)
	}
	if got, err := dependencyPredicate([]string{"success", "failure", "success"}); err != nil || got != "outcomes:failure,success" {
		t.Fatalf("dependencyPredicate(set) = (%q, %v)", got, err)
	}
	for _, outcomes := range [][]string{nil, {"unknown"}} {
		if _, err := dependencyPredicate(outcomes); err == nil {
			t.Fatalf("dependencyPredicate(%v) error = nil", outcomes)
		}
	}
	if parsed, err := parseOptionalTimestamp("2026-07-15T12:00:00Z"); err != nil || parsed.Location() != time.UTC {
		t.Fatalf("parseOptionalTimestamp() = (%v, %v)", parsed, err)
	}
	if parsed, err := parseOptionalTimestamp(""); err != nil || !parsed.IsZero() {
		t.Fatalf("parseOptionalTimestamp(empty) = (%v, %v)", parsed, err)
	}
	if _, err := parseOptionalTimestamp("bad"); err == nil {
		t.Fatal("parseOptionalTimestamp(bad) error = nil")
	}
	if limit, err := parseLimitFlag("runs", config.Unlimited); err != nil || !limit.Unlimited {
		t.Fatalf("parseLimitFlag(unlimited) = (%+v, %v)", limit, err)
	}
	if limit, err := parseLimitFlag("runs", "3"); err != nil || limit.Value != 3 {
		t.Fatalf("parseLimitFlag(3) = (%+v, %v)", limit, err)
	}
	for _, value := range []string{"0", "bad"} {
		if _, err := parseLimitFlag("runs", value); err == nil {
			t.Errorf("parseLimitFlag(%q) error = nil", value)
		}
	}
	if limit, err := limitFromConfig("runs", config.UnlimitedIntegerLimit()); err != nil || !limit.Unlimited {
		t.Fatalf("limitFromConfig(unlimited) = (%+v, %v)", limit, err)
	}
	if limit, err := limitFromConfig("runs", config.NewIntegerLimit(2)); err != nil || limit.Value != 2 {
		t.Fatalf("limitFromConfig(2) = (%+v, %v)", limit, err)
	}
	classification := classificationFromConfig(config.CompletionPolicy{
		SuccessExitCodes: []int{0, 2}, RetryableExitCodes: []int{7}, RetryTimeouts: true,
	})
	if len(classification.RetryableExitCodes) != 1 || !classification.RetryTimeout {
		t.Fatalf("classificationFromConfig() = %+v", classification)
	}
	maximumDelay, err := config.NewDurationLimit(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	delay, err := delayFromConfig(config.DelayPolicy{MaxDelay: maximumDelay})
	if err != nil || !delay.HasMaxDelay || delay.MaxDelay != 5*time.Second {
		t.Fatalf("delayFromConfig(maximum) = (%+v, %v)", delay, err)
	}
	for name, completion := range map[string]config.CompletionPolicy{
		"max runs":       {MaxRuns: config.NewIntegerLimit(0)},
		"success target": {SuccessTarget: config.NewIntegerLimit(0)},
		"failure limit":  {MaxFailures: config.NewIntegerLimit(0)},
	} {
		if _, completionErr := completionFromConfig(completion); completionErr == nil {
			t.Errorf("completionFromConfig(%s) error = nil", name)
		}
	}
}

func TestConfiguredWaitAndNotifierConversion(t *testing.T) {
	t.Parallel()

	oneSecond, err := config.NewDuration(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	shortPoll, err := config.NewDuration(10 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := config.ParseSecretRef("env:JOBMAN_TEST_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	configuration := config.Config{
		Secrets: map[string]config.SecretRef{"token": secret},
		WaitConditions: map[string]config.WaitCondition{
			"until": {Type: string(model.WaitUntil), Until: "2026-07-16T00:00:00Z"},
			"delay": {Type: string(model.WaitDelay), Delay: shortPoll},
			"file":  {Type: "file-exists", FileExists: &config.FileCondition{Path: "/tmp/ready", Type: "file"}},
			"probe": {
				Type: string(model.WaitProbe),
				Probe: &config.ProbeCondition{
					Command: []string{"/bin/true"}, Timeout: oneSecond, PollInterval: shortPoll,
					OutputLimit: config.NewByteLimit(128),
					Environment: config.Environment{Secrets: map[string]string{"TOKEN": "token"}},
				},
			},
		},
	}
	waits, err := waitsFromConfig(configuration, config.WaitPolicy{
		Conditions: []string{"until", "delay", "file", "probe"}, AbortAt: "2026-07-17T00:00:00Z",
	})
	if err != nil || len(waits) != 4 || waits[2].FileKind != policy.FileKindRegular || waits[3].Probe.OutputLimit != 128 {
		t.Fatalf("waitsFromConfig() = (%+v, %v)", waits, err)
	}
	if _, err := waitsFromConfig(configuration, config.WaitPolicy{Conditions: []string{"missing"}}); err == nil {
		t.Fatal("waitsFromConfig(missing) error = nil")
	}

	configuration.Notifiers = configuredNotifierFixtures(t, oneSecond)
	subscriptions := notificationSubscriptions(configuration, config.NotificationPolicy{
		Notifiers: []string{"command", "http", "smtp"}, Events: []string{"job_failed"},
	})
	definitions, err := notifierDefinitions(configuration, subscriptions)
	if err != nil || len(definitions) != 3 || definitions[0].Command == nil ||
		definitions[1].Webhook == nil || definitions[2].SMTP == nil {
		t.Fatalf("notifierDefinitions() = (%+v, %v)", definitions, err)
	}
	if _, err := notifierDefinitions(configuration, []model.NotificationSubscription{{Notifier: "missing"}}); err == nil {
		t.Fatal("notifierDefinitions(missing) error = nil")
	}
	bindings, err := secretEnvironmentFromConfig(configuration, map[string]string{"TOKEN": "token"})
	if err != nil || bindings["TOKEN"].Provider != "env" {
		t.Fatalf("secretEnvironmentFromConfig() = (%+v, %v)", bindings, err)
	}
	if _, err := secretEnvironmentFromConfig(configuration, map[string]string{"TOKEN": "missing"}); err == nil {
		t.Fatal("secretEnvironmentFromConfig(missing) error = nil")
	}
	dependencies, err := dependenciesFromConfig([]config.Dependency{{Job: testJobID, Outcomes: []string{"success"}}})
	if err != nil || len(dependencies) != 1 || dependencies[0].Predicate != "success" {
		t.Fatalf("dependenciesFromConfig() = (%+v, %v)", dependencies, err)
	}
	runTimeout, err := config.NewDurationLimit(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := config.NewDurationLimit(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	configured := config.JobSpec{
		Command: []string{"/bin/echo", "hello"}, Name: "configured", Tags: []string{"nightly"}, Groups: []string{"workers"},
		WorkingDirectory: ".", Environment: config.Environment{
			Set: map[string]string{"A": "B"}, Unset: []string{"OLD"}, Secrets: map[string]string{"TOKEN": "token"},
		},
		Stdin: string(model.StdinNull), Stop: config.StopPolicy{GracePeriod: oneSecond, ForceAfterGrace: true},
		Dependencies: []config.Dependency{{Job: testJobID, Outcomes: []string{"success"}}},
		Wait:         config.WaitPolicy{Mode: string(policy.WaitModeAll), Conditions: []string{"delay"}},
		Admission:    config.Admission{Pool: "build", Slots: 2},
		Completion: config.CompletionPolicy{
			MaxRuns: config.NewIntegerLimit(3), SuccessTarget: config.NewIntegerLimit(1), MaxFailures: config.NewIntegerLimit(2),
			SuccessExitCodes: []int{0}, RetryableExitCodes: []int{7}, RetryTimeouts: true,
		},
		Delay:    config.DelayPolicy{Strategy: "constant", Initial: shortPoll, Jitter: shortPoll},
		Timeouts: config.TimeoutPolicy{Run: runTimeout},
		Logging: config.LoggingPolicy{
			Capture: "both", SegmentBytes: config.NewByteLimit(1024), SegmentsPerRun: config.NewIntegerLimit(2),
			CompletedLogMaxAge: retention,
		},
		Notification: config.NotificationPolicy{Notifiers: []string{"command"}, Events: []string{"job_failed"}},
	}
	request, err := submitRequestFromConfig(configuration, configured)
	if err != nil || request.Executable != "/bin/echo" || request.ExecutionPolicy.Concurrency.Slots != 2 ||
		len(request.ExecutionPolicy.NotifierDefinitions) != 1 || request.ExecutionPolicy.LogRotateSize != 1024 {
		t.Fatalf("submitRequestFromConfig() = (%+v, %v)", request, err)
	}
}

func configuredNotifierFixtures(t *testing.T, timeout config.Duration) map[string]config.Notifier {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	delay, err := config.NewDuration(0)
	if err != nil {
		t.Fatal(err)
	}
	retry := config.NotifierRetry{MaxAttempts: 1, Delay: delay, MaxDelay: delay}
	return map[string]config.Notifier{
		"command": {
			Type: "command", Timeout: timeout, Retry: retry, Events: []string{"job_failed"},
			Command: &config.CommandNotifier{Command: []string{executable}, OutputLimit: config.NewByteLimit(128)},
		},
		"http": {
			Type: "http", Timeout: timeout, Retry: retry, Events: []string{"job_failed"},
			HTTP: &config.HTTPNotifier{URL: "https://example.test/events"},
		},
		"smtp": {
			Type: "smtp", Timeout: timeout, Retry: retry, Events: []string{"job_failed"},
			SMTP: &config.SMTPNotifier{
				Address: "smtp.example.test:587", TLS: "starttls", From: "jobman@example.test", To: []string{"ops@example.test"},
			},
		},
	}
}

func TestHiddenSupervisorCommandAndDefaultDependencies(t *testing.T) {
	called := false
	runtimeDependencies := dependencies{Supervise: func(
		context.Context, string, string, io.Reader, io.Writer,
	) error {
		called = true
		return nil
	}}
	if _, err := executeCommand(t, runtimeDependencies, []string{"--state-dir", t.TempDir(), "__supervise", testJobID}); err != nil || !called {
		t.Fatalf("__supervise = called %t, error %v", called, err)
	}
	if _, err := executeCommand(t, dependencies{}, []string{"__supervise", testJobID}); err == nil {
		t.Fatal("__supervise without runtime error = nil")
	}
	defaults := defaultDependencies()
	if defaults.OpenBackend == nil || defaults.Supervise == nil {
		t.Fatal("defaultDependencies() returned nil runtime functions")
	}
	if !errors.Is(usageError(errors.New("bad")), errUsage) {
		t.Fatal("usageError() did not wrap errUsage")
	}
}

func TestExecuteCommandWithUnavailableBackend(t *testing.T) {
	t.Parallel()
	if _, err := executeCommand(t, dependencies{}, []string{"list"}); err == nil {
		t.Fatal("list without backend error = nil")
	}
}

func TestExtendedLogCleanupAndForegroundCommands(t *testing.T) {
	backend := newFakeBackend(t)
	backend.logs = []byte("one\ntwo\nthree\n")
	backend.details.Runs = []model.RunState{{Number: 1}, {Number: 3}}
	for _, test := range []struct {
		arguments []string
		contains  string
	}{
		{arguments: []string{"logs", testJobID, "--lines", "1"}, contains: "three"},
		{arguments: []string{"logs", testJobID, "--run", "-1"}, contains: "one"},
		{arguments: []string{"logs", testJobID, "--all"}, contains: "==> run 1 <=="},
		{arguments: []string{"logs", testJobID, "--follow"}, contains: "one"},
		{arguments: []string{"clean", testJobID, "--dry-run"}, contains: "would remove 1 runs"},
		{arguments: []string{"clean", testJobID, "--dry-run=false", "--force", "--older-than", "1h"}, contains: "removed 1 runs"},
	} {
		stdout, err := executeCommand(t, dependenciesFor(backend), test.arguments)
		if err != nil || !strings.Contains(stdout, test.contains) {
			t.Errorf("%v = (%q, %v), want %q", test.arguments, stdout, err, test.contains)
		}
		backend.closed = false
	}
	if backend.cleanRequest == nil || backend.cleanRequest.OlderThan != time.Hour || backend.cleanRequest.UsePolicy {
		t.Fatalf("Clean request = %+v", backend.cleanRequest)
	}
	if backend.appliedConfig != 1 || backend.configured != 1 {
		t.Fatalf("cleanup configuration calls = applied %d configured %d, want policy cleanup only",
			backend.appliedConfig, backend.configured)
	}
	for _, arguments := range [][]string{
		{"logs", testJobID, "--stream", "invalid"},
		{"logs", testJobID, "--follow", "--all"},
		{"logs", testJobID, "--lines", "-2"},
		{"clean", testJobID, "--dry-run=false"},
	} {
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); err == nil {
			t.Errorf("%v error = nil", arguments)
		}
	}

	backend.jobs[0].Outcome = model.JobOutcomeSuccess
	backend.waitForInputEOF = true
	stdout, err := executeCommand(t, dependenciesFor(backend), []string{"run", "--foreground", "--", "true"})
	if err != nil || backend.followed.Load() < 2 || !backend.inputEOF.Load() || !strings.Contains(stdout, testJobID) {
		t.Fatalf("foreground run = (%q, %v), followed %d, EOF %t", stdout, err, backend.followed.Load(), backend.inputEOF.Load())
	}
}

func TestForegroundAndWaitHelpers(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.jobs[0].Outcome = model.JobOutcomeSuccess
	if err := waitForSubmittedJob(t.Context(), backend, testJobID); err != nil {
		t.Fatalf("waitForSubmittedJob(success) error = %v", err)
	}
	backend.jobs[0].Outcome = model.JobOutcomeFailure
	if err := waitForSubmittedJob(t.Context(), backend, testJobID); err == nil {
		t.Fatal("waitForSubmittedJob(failure) error = nil")
	}
	backend.input = nil
	backend.inputEOF.Store(false)
	if err := pumpForegroundInput(t.Context(), backend, testJobID, strings.NewReader("payload")); err != nil {
		t.Fatalf("pumpForegroundInput() error = %v", err)
	}
	if string(backend.input) != "payload" || !backend.inputEOF.Load() {
		t.Fatalf("foreground input = %q, EOF %t", backend.input, backend.inputEOF.Load())
	}
}

func TestRunOptionValidationMatrix(t *testing.T) {
	backend := newFakeBackend(t)
	stdinFile := t.TempDir() + "/input"
	for _, arguments := range [][]string{
		{"run", "--stdin", "null", "--stdin-file", stdinFile, "--", "true"},
		{"run", "--foreground", "--stdin", "null", "--", "true"},
		{"run", "--foreground", "--stdin-file", stdinFile, "--", "true"},
		{"run", "--retries", "2", "--max-runs", "3", "--", "true"},
		{"run", "--retries", "18446744073709551615", "--", "true"},
		{"run", "--max-runs", "0", "--", "true"},
		{"run", "--slots", "0", "--", "true"},
		{"run", "--wait-condition", "missing", "--", "true"},
		{"run", "--wait-abort-at", "bad", "--", "true"},
		{"run", "--wait-until", "bad", "--", "true"},
		{"run", "--wait-delay", "bad", "--", "true"},
		{"run", "--wait-delay", "-1s", "--", "true"},
		{"run", "--log-segments", "0", "--", "true"},
		{"run", "--log-segments", "65536", "--", "true"},
		{"run", "--log-segment-bytes", "18446744073709551615", "--", "true"},
		{"run", "--log-retention", "bad", "--", "true"},
		{"run", "--notify", "missing", "--", "true"},
		{"run", "--after-outcome", "bad", "--", "true"},
		{"run", "--after-outcome", "job=unknown", "--", "true"},
		{"run", "--env", "bad", "--", "true"},
		{"run", "--secret-env", "bad", "--", "true"},
		{"run", "--secret-env", "TOKEN=missing", "--", "true"},
		{"run", "--retry-abort-at", "bad", "--", "true"},
		{"run", "--retry-backoff", "unknown", "--", "true"},
		{"run", "--log-capture", "invalid", "--", "true"},
	} {
		backend.submitRequest = nil
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); err == nil {
			t.Errorf("%v error = nil", arguments)
		}
		if backend.submitRequest != nil {
			t.Errorf("%v submitted an invalid request", arguments)
		}
	}

	options := &runOptions{
		afterSuccess: []string{"success"}, afterFinish: []string{"finish"}, afterFailed: []string{"failed"},
		afterOutcome: []string{"selected=success,failure"},
	}
	dependencies, err := appendDependencyFlags(options)
	if err != nil || len(dependencies) != 4 || dependencies[3].Predicate != "outcomes:failure,success" {
		t.Fatalf("appendDependencyFlags() = (%+v, %v)", dependencies, err)
	}
	stdout, err := executeCommand(t, dependenciesFor(backend), []string{"rerun", testJobID, "--name", "again"})
	if err != nil || backend.rerunRequest == nil || backend.rerunRequest.Name != "again" || !strings.Contains(stdout, testJobID) {
		t.Fatalf("rerun command = (%q, %v), request %+v", stdout, err, backend.rerunRequest)
	}
}

func TestLifecycleAndStatusCommandSuccess(t *testing.T) {
	backend := newFakeBackend(t)
	for _, arguments := range [][]string{
		{"cancel", testJobID},
		{"pause", testJobID},
		{"resume", testJobID},
		{"wait", testJobID},
		{"status", testJobID},
		{"input", testJobID, "--eof"},
	} {
		stdout, err := executeCommand(t, dependenciesFor(backend), arguments)
		if err != nil || stdout == "" {
			t.Errorf("%v = (%q, %v)", arguments, stdout, err)
		}
		backend.closed = false
	}
	if !backend.canceled || !backend.paused || !backend.resumed || !backend.inputEOF.Load() {
		t.Fatalf("lifecycle effects = cancel %t pause %t resume %t eof %t",
			backend.canceled, backend.paused, backend.resumed, backend.inputEOF.Load())
	}
}

func TestCommandOutputFailures(t *testing.T) {
	for _, arguments := range [][]string{
		{"list"},
		{"list", "--format", "json"},
		{"show", testJobID},
		{"show", testJobID, "--format", "json"},
		{"cancel", testJobID},
		{"pause", testJobID},
		{"resume", testJobID},
		{"wait", testJobID},
		{"status", testJobID},
		{"input", testJobID},
		{"rerun", testJobID},
		{"logs", testJobID},
		{"clean", testJobID, "--dry-run"},
		{"run", "--", "true"},
		{"run", "--rerun", testJobID},
		{"config", "show"},
		{"config", "paths"},
		{"config", "validate"},
		{"config", "apply"},
	} {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			backend := newFakeBackend(t)
			backend.logs = []byte("log")
			command := newRootCommand(dependenciesFor(backend))
			command.SetArgs(arguments)
			command.SetIn(strings.NewReader("input"))
			command.SetOut(&commandFailWriter{})
			command.SetErr(io.Discard)
			if err := command.ExecuteContext(t.Context()); err == nil {
				t.Fatalf("%v output error = nil", arguments)
			}
		})
	}
}

func TestExplicitConfigPathOutputAndRedactionFailures(t *testing.T) {
	t.Parallel()

	configurationPath := filepath.Join(t.TempDir(), "jobman.yml")
	if err := os.WriteFile(configurationPath, []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := newFakeBackend(t)
	command := newRootCommand(dependenciesFor(backend))
	command.SetArgs([]string{"--config", configurationPath, "config", "paths"})
	command.SetOut(&failAfterWrites{allowed: 1})
	command.SetErr(io.Discard)
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("config paths explicit output error = nil")
	}

	direct := &cobra.Command{}
	direct.SetContext(t.Context())
	if err := configureRedactor(direct, config.Config{Redaction: config.RedactionConfig{Patterns: []string{"["}}}); err == nil {
		t.Fatal("configureRedactor(invalid pattern) error = nil")
	}
	if err := writeJSON(direct, make(chan int)); err == nil {
		t.Fatal("writeJSON(unsupported value) error = nil")
	}
}

type failAfterWrites struct {
	allowed int
	writes  int
}

func (writer *failAfterWrites) Write(payload []byte) (int, error) {
	writer.writes++
	if writer.writes > writer.allowed {
		return 0, errors.New("output failed")
	}

	return len(payload), nil
}

type commandFailWriter struct{}

func (*commandFailWriter) Write([]byte) (int, error) { return 0, errors.New("output failed") }

func TestFullJobDetailPresentation(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.FixedZone("test", 3600))
	exitCode := 7
	statusCode := 503
	commandCode := 1
	backend.details.Job.Cancellation = &model.CancellationIntent{RequestedAt: now, Reason: model.StopReasonCancellation}
	backend.details.Runs = []model.RunState{{
		ID: model.RunID(testJobID), Number: 1, Phase: model.RunPhaseCompleted,
		ReservedAt: now, StartedAt: &now, CompletedAt: &now,
		Process: &model.ProcessIdentity{PID: 42, Platform: "test", CreationID: "creation", BootID: "boot"},
		Exit:    &model.ExitInfo{ExitCode: &exitCode, ObservedAt: now},
		Logs: model.LogMetadata{
			StdoutPath: "/stdout", StderrPath: "/stderr", IndexPath: "/index",
			Integrity: model.LogIntegrityValid, RecordingHealth: model.RecordingHealthy,
		},
	}}
	backend.details.Runtime = store.JobRuntime{
		Revision: 2, RunCount: 1, SuccessCount: 1, NextRunAt: &now,
		PausedAt: &now, PrerequisitesSatisfiedAt: &now, TotalPaused: time.Second,
	}
	backend.details.Dependencies = []store.Dependency{{
		JobID: model.JobID(testJobID), DependsOn: model.JobID(testJobID),
		Predicate: store.DependencySuccess, ObservedRevision: 2, ObservedOutcome: model.JobOutcomeSuccess,
		SatisfiedAt: &now,
	}}
	backend.details.WaitEvaluations = []store.WaitEvaluation{{
		ConditionIndex: 1, ConditionKind: model.WaitDelay, EvaluatedAt: &now,
		SatisfiedAt: &now, AttemptCount: 2,
	}}
	backend.details.Admission = &store.Admission{
		JobID: model.JobID(testJobID), RunID: model.RunID(testJobID), Pool: "build", Slots: 2,
		AcquiredAt: now, LeaseExpires: now.Add(time.Minute), ReleasedAt: &now,
	}
	backend.details.NotificationDeliveries = []store.NotificationDelivery{{
		JobID: model.JobID(testJobID), EventID: model.EventID(testJobID), RunID: model.RunID(testJobID),
		NotifierName: "ops", EventType: "job_failed", Status: store.NotificationDeliveryPending,
		OccurredAt: now, CreatedAt: now, NextAttemptAt: &now, ClaimedAt: &now,
		ClaimExpiresAt: &now, CompletedAt: &now, MaxAttempts: 3, AttemptCount: 1,
	}}
	backend.details.NotificationAttempts = []store.NotificationAttempt{{
		ID: model.EventID(testJobID), JobID: model.JobID(testJobID), EventID: model.EventID(testJobID),
		NotifierName: "ops", EventType: "job_failed", AttemptNumber: 1,
		Status: store.NotificationAttemptFailed, CreatedAt: now, StartedAt: &now, CompletedAt: &now,
		NextAttemptAt: &now, Retryable: true, ResponseStatusCode: &statusCode,
		CommandExitCode: &commandCode,
	}}

	presented := detail(backend.details)
	if len(presented.Runs) != 1 || len(presented.Dependencies) != 1 ||
		len(presented.WaitEvaluations) != 1 || presented.Admission == nil ||
		len(presented.NotificationDeliveries) != 1 || len(presented.NotificationAttempts) != 1 ||
		presented.CancellationRequested == nil {
		t.Fatalf("detail() omitted values: %+v", presented)
	}
	if utcTime(&now).Location() != time.UTC || utcTime(nil) != nil {
		t.Fatal("utcTime() did not normalize optional timestamps")
	}
}

func TestRunAppliesCompleteFlagOverlay(t *testing.T) {
	backend := newFakeBackend(t)
	stdinPath := filepath.Join(t.TempDir(), "stdin")
	arguments := []string{
		"run",
		"--name", "complete",
		"--group", "workers",
		"--tag", "nightly",
		"--cwd", t.TempDir(),
		"--env", "A=B",
		"--unset-env", "OLD",
		"--stdin-file", stdinPath,
		"--stop-grace", "2s",
		"--force-after-grace=false",
		"--max-runs", "5",
		"--success-target", "2",
		"--failure-limit", "3",
		"--success-exit-code", "0",
		"--retryable-exit-code", "7",
		"--retry-timeouts",
		"--retry-start-failures",
		"--retry-delay", "1s",
		"--repeat-delay", "2s",
		"--retry-backoff", "exponential",
		"--retry-jitter", "100ms",
		"--retry-max-delay", "5s",
		"--retry-abort-at", "2026-07-16T00:00:00Z",
		"--run-timeout", "3s",
		"--job-timeout", "1m",
		"--after-success", testJobID,
		"--after-finish", testJobID,
		"--after-failed", testJobID,
		"--pool", "workers",
		"--slots", "2",
		"--wait-until", "2026-07-16T00:00:00Z",
		"--wait-delay", "250ms",
		"--wait-file", "/tmp/ready",
		"--wait-mode", "any",
		"--wait-abort-at", "2026-07-17T00:00:00Z",
		"--wait-poll", "10ms",
		"--log-segment-bytes", "1024",
		"--log-segments", "3",
		"--log-capture", "stderr",
		"--log-retention", config.Unlimited,
		"--", "printf", "done",
	}
	stdout, err := executeCommand(t, dependenciesFor(backend), arguments)
	if err != nil || stdout != testJobID+"\n" {
		t.Fatalf("complete run flags = (%q, %v)", stdout, err)
	}
	request := backend.submitRequest
	if request == nil || request.StdinPolicy != model.StdinFile ||
		request.ExecutionPolicy.StdinPath != stdinPath || !request.ExecutionPolicy.LogRetentionUnlimited ||
		len(request.ExecutionPolicy.WaitConditions) != 3 || len(request.Dependencies) != 3 ||
		request.ExecutionPolicy.Completion.MaxRuns.Value != 5 {
		t.Fatalf("complete run request = %+v", request)
	}

	backend = newFakeBackend(t)
	backend.jobs[0].Outcome = model.JobOutcomeSuccess
	stdout, err = executeCommand(t, dependenciesFor(backend), []string{
		"run", "--rerun", testJobID, "--name", "again", "--wait",
	})
	if err != nil || stdout != testJobID+"\n" || backend.rerunRequest == nil {
		t.Fatalf("run --rerun --wait = (%q, %v, %+v)", stdout, err, backend.rerunRequest)
	}
}

func TestConfiguredWaitValidationFailures(t *testing.T) {
	t.Parallel()

	duration, err := config.NewDuration(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	configuration := config.Config{WaitConditions: map[string]config.WaitCondition{
		"until-empty": {Type: string(model.WaitUntil)},
		"file-empty":  {Type: "file-exists"},
		"probe-empty": {Type: string(model.WaitProbe)},
		"unsupported": {Type: "unsupported"},
		"large-probe": {
			Type: string(model.WaitProbe),
			Probe: &config.ProbeCondition{
				Command: []string{"/bin/true"}, Timeout: duration, PollInterval: duration,
				OutputLimit: config.NewByteLimit(math.MaxUint64),
			},
		},
	}}
	for _, wait := range []config.WaitPolicy{
		{AbortAt: "invalid"},
		{Conditions: []string{"until-empty"}},
		{Conditions: []string{"file-empty"}},
		{Conditions: []string{"probe-empty"}},
		{Conditions: []string{"unsupported"}},
		{Conditions: []string{"large-probe"}},
	} {
		if _, err := waitsFromConfig(configuration, wait); err == nil {
			t.Errorf("waitsFromConfig(%+v) error = nil", wait)
		}
	}
}

func TestNotifierDefinitionValidationFailures(t *testing.T) {
	t.Parallel()

	duration, err := config.NewDuration(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := config.NewDuration(0)
	if err != nil {
		t.Fatal(err)
	}
	retry := config.NotifierRetry{MaxAttempts: 1, Delay: zero, MaxDelay: zero}
	secret, err := config.ParseSecretRef("env:JOBMAN_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		notifier config.Notifier
		secrets  map[string]config.SecretRef
	}{
		{name: "command missing", notifier: config.Notifier{Type: "command", Timeout: duration, Retry: retry}},
		{name: "command relative", notifier: config.Notifier{
			Type: "command", Timeout: duration, Retry: retry,
			Command: &config.CommandNotifier{Command: []string{"relative"}, OutputLimit: config.NewByteLimit(1)},
		}},
		{name: "command output overflow", notifier: config.Notifier{
			Type: "command", Timeout: duration, Retry: retry,
			Command: &config.CommandNotifier{Command: []string{"/bin/true"}, OutputLimit: config.NewByteLimit(math.MaxUint64)},
		}},
		{name: "command secret missing", notifier: config.Notifier{
			Type: "command", Timeout: duration, Retry: retry,
			Command: &config.CommandNotifier{
				Command: []string{"/bin/true"}, OutputLimit: config.NewByteLimit(1),
				Environment: config.Environment{Secrets: map[string]string{"TOKEN": "missing"}},
			},
		}},
		{name: "http missing", notifier: config.Notifier{Type: "http", Timeout: duration, Retry: retry}},
		{name: "http secret missing", notifier: config.Notifier{
			Type: "http", Timeout: duration, Retry: retry,
			HTTP: &config.HTTPNotifier{URL: "https://example.test", SecretHeaders: map[string]string{"Authorization": "missing"}},
		}},
		{name: "smtp missing", notifier: config.Notifier{Type: "smtp", Timeout: duration, Retry: retry}},
		{name: "smtp address", notifier: config.Notifier{
			Type: "smtp", Timeout: duration, Retry: retry,
			SMTP: &config.SMTPNotifier{Address: "missing-port", From: "a@example.test", To: []string{"b@example.test"}},
		}},
		{name: "unsupported", notifier: config.Notifier{Type: "carrier-pigeon", Timeout: duration, Retry: retry}},
		{name: "invalid definition", notifier: config.Notifier{
			Type: "http", Timeout: duration, Retry: retry, HTTP: &config.HTTPNotifier{},
		}, secrets: map[string]config.SecretRef{"unused": secret}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := config.Config{
				Notifiers: map[string]config.Notifier{"test": test.notifier}, Secrets: test.secrets,
			}
			if _, err := notifierDefinitions(configuration, []model.NotificationSubscription{{Notifier: "test"}}); err == nil {
				t.Fatal("notifierDefinitions() error = nil")
			}
		})
	}

	valid := configuredNotifierFixtures(t, duration)
	configuration := config.Config{Notifiers: valid}
	definitions, err := notifierDefinitions(configuration, []model.NotificationSubscription{
		{Notifier: "command"}, {Notifier: "command"},
	})
	if err != nil || len(definitions) != 1 {
		t.Fatalf("notifierDefinitions(duplicate) = (%+v, %v)", definitions, err)
	}

	valid["http"].HTTP.SigningSecret = "shared"
	valid["smtp"].SMTP.Username = "jobman"
	valid["smtp"].SMTP.PasswordSecret = "shared"
	configuration = config.Config{
		Notifiers: valid,
		Secrets:   map[string]config.SecretRef{"shared": secret},
	}
	definitions, err = notifierDefinitions(configuration, []model.NotificationSubscription{
		{Notifier: "http"}, {Notifier: "smtp"},
	})
	if err != nil || len(definitions) != 2 || definitions[0].Webhook.SigningSecret == nil ||
		definitions[1].SMTP.PasswordSecret == nil {
		t.Fatalf("notifierDefinitions(secrets) = (%+v, %v)", definitions, err)
	}
}

func TestSubmitRequestConfigurationValidationFailures(t *testing.T) {
	t.Parallel()

	base := config.JobSpec{Command: []string{"/bin/true"}}
	tests := []struct {
		name          string
		configuration config.Config
		specification config.JobSpec
	}{
		{name: "completion", specification: func() config.JobSpec {
			value := base
			value.Completion.MaxRuns = config.NewIntegerLimit(0)
			return value
		}()},
		{name: "delay", specification: func() config.JobSpec {
			value := base
			value.Delay.Strategy = "invalid"
			return value
		}()},
		{name: "secret", specification: func() config.JobSpec {
			value := base
			value.Environment.Secrets = map[string]string{"TOKEN": "missing"}
			return value
		}()},
		{name: "dependency", specification: func() config.JobSpec {
			value := base
			value.Dependencies = []config.Dependency{{Job: testJobID}}
			return value
		}()},
		{name: "wait", specification: func() config.JobSpec {
			value := base
			value.Wait.Conditions = []string{"missing"}
			return value
		}()},
		{name: "segment bytes", specification: func() config.JobSpec {
			value := base
			value.Logging.SegmentBytes = config.NewByteLimit(math.MaxUint64)
			return value
		}()},
		{name: "segments", specification: func() config.JobSpec {
			value := base
			value.Logging.SegmentsPerRun = config.NewIntegerLimit(1 << 16)
			return value
		}()},
		{name: "notifier", specification: func() config.JobSpec {
			value := base
			value.Notification.Notifiers = []string{"missing"}
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := submitRequestFromConfig(test.configuration, test.specification); err == nil {
				t.Fatal("submitRequestFromConfig() error = nil")
			}
		})
	}

	request, err := submitRequestFromConfig(config.Config{}, base)
	if err != nil || request.StdinPolicy != model.StdinNull || request.ExecutionPolicy.Concurrency.Slots != 1 ||
		request.ExecutionPolicy.WaitMode != policy.WaitModeAll {
		t.Fatalf("submitRequestFromConfig(defaults) = (%+v, %v)", request, err)
	}
}

func TestCommandsPropagateBackendFailures(t *testing.T) {
	wantErr := errors.New("backend failed")
	for _, arguments := range [][]string{
		{"list"},
		{"show", testJobID},
		{"status", testJobID},
		{"cancel", testJobID},
		{"pause", testJobID},
		{"resume", testJobID},
		{"wait", testJobID},
		{"input", testJobID},
		{"rerun", testJobID},
		{"logs", testJobID},
		{"logs", testJobID, "--follow"},
		{"clean", testJobID, "--dry-run"},
		{"run", "--", "true"},
		{"run", "--rerun", testJobID},
	} {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			backend := newFakeBackend(t)
			backend.operationErr = wantErr
			_, err := executeCommand(t, dependenciesFor(backend), arguments)
			if !errors.Is(err, wantErr) {
				t.Fatalf("%v error = %v, want %v", arguments, err, wantErr)
			}
		})
	}
}

func TestCommandsRejectBackendsWithoutOptionalCapabilities(t *testing.T) {
	backend := &basicBackend{base: newFakeBackend(t)}
	runtimeDependencies := dependencies{OpenBackend: func(context.Context, string) (app.Backend, error) {
		return backend, nil
	}}
	for _, arguments := range [][]string{
		{"pause", testJobID},
		{"input", testJobID},
		{"rerun", testJobID},
		{"clean", testJobID, "--dry-run"},
		{"logs", testJobID, "--follow"},
		{"run", "--foreground", "--", "true"},
		{"run", "--rerun", testJobID},
	} {
		if _, err := executeCommand(t, runtimeDependencies, arguments); err == nil {
			t.Errorf("%v error = nil", arguments)
		}
		backend.base.closed = false
	}

	if _, err := executeCommand(t, dependencies{}, []string{
		"--config", filepath.Join(t.TempDir(), "missing.yml"), "config", "paths",
	}); err == nil {
		t.Fatal("config paths with missing explicit file error = nil")
	}
	if _, err := executeCommand(t, dependencies{}, []string{
		"--config", "one.yml", "config", "validate", "two.yml",
	}); !errors.Is(err, errUsage) {
		t.Fatalf("config validate conflicting paths error = %v", err)
	}
}

func TestForegroundAttachmentFailureBoundaries(t *testing.T) {
	newCommand := func() *cobra.Command {
		command := &cobra.Command{}
		command.SetContext(t.Context())
		command.SetIn(strings.NewReader("input"))
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		return command
	}

	t.Run("backend failure", func(t *testing.T) {
		backend := newFakeBackend(t)
		backend.operationErr = errors.New("foreground backend failed")
		if err := attachForeground(newCommand(), backend, backend.jobs[0]); err == nil {
			t.Fatal("attachForeground() error = nil")
		}
	})

	t.Run("failed job", func(t *testing.T) {
		backend := newFakeBackend(t)
		backend.jobs[0].Phase = model.JobPhaseCompleted
		backend.jobs[0].Outcome = model.JobOutcomeFailure
		if err := attachForeground(newCommand(), backend, backend.jobs[0]); err == nil {
			t.Fatal("attachForeground(failed job) error = nil")
		}
	})
}

func TestForegroundInputFailureBoundaries(t *testing.T) {
	backend := newFakeBackend(t)
	backend.operationErr = errors.New("send failed")
	if err := pumpForegroundInput(t.Context(), backend, testJobID, strings.NewReader("payload")); err == nil {
		t.Fatal("pumpForegroundInput(send failure) error = nil")
	}

	backend = newFakeBackend(t)
	if err := pumpForegroundInput(t.Context(), backend, testJobID, foregroundErrorReader{}); err == nil {
		t.Fatal("pumpForegroundInput(read failure) error = nil")
	}
}

type foregroundErrorReader struct{}

func (foregroundErrorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestWaitForSubmittedJobFailureBoundaries(t *testing.T) {
	if err := waitForSubmittedJob(t.Context(), &basicBackend{base: newFakeBackend(t)}, testJobID); err == nil {
		t.Fatal("waitForSubmittedJob(unsupported) error = nil")
	}
	backend := newFakeBackend(t)
	backend.operationErr = errors.New("wait failed")
	if err := waitForSubmittedJob(t.Context(), backend, testJobID); err == nil {
		t.Fatal("waitForSubmittedJob(wait failure) error = nil")
	}
	backend = newFakeBackend(t)
	backend.jobs[0].Outcome = model.JobOutcomeFailure
	if err := waitForSubmittedJob(t.Context(), backend, testJobID); err == nil {
		t.Fatal("waitForSubmittedJob(failure outcome) error = nil")
	}
}

func TestApplyRunOptionsInitializesSecretAndNotifierDefaults(t *testing.T) {
	command := newRunCommand(dependencies{}, &rootOptions{})
	for name, value := range map[string]string{
		"secret-env": "TOKEN=shared",
		"notify":     "operations",
	} {
		if err := command.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	options := &runOptions{
		secretEnvironment: []string{"TOKEN=shared"},
		notifiers:         []string{"operations"},
		slots:             1,
	}
	notifierExecutable := filepath.Join(t.TempDir(), "notifier")
	loaded, err := config.Load(config.BytesSource(config.SourceExplicit, "test", []byte(fmt.Sprintf(`
secrets:
  shared: env:JOBMAN_TOKEN
notifiers:
  operations:
    type: command
    events: [job_succeeded]
    command:
      command: [%q]
`, notifierExecutable))))
	if err != nil {
		t.Fatal(err)
	}
	configuration := loaded.Config
	configured, err := configuration.ResolveJobSpecWithCommand("", []string{notifierExecutable})
	if err != nil {
		t.Fatal(err)
	}
	request, err := submitRequestFromConfig(configuration, configured)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRunOptions(command, options, nil, configuration, &request); err != nil {
		t.Fatalf("applyRunOptions() error = %v", err)
	}
	if request.ExecutionPolicy.SecretEnv["TOKEN"].Name != "JOBMAN_TOKEN" ||
		len(request.ExecutionPolicy.Notifications) != 1 ||
		len(request.ExecutionPolicy.Notifications[0].Events) != 1 {
		t.Fatalf("request = %+v", request)
	}
}

func TestLogRunSelectionAndValidationBranches(t *testing.T) {
	backend := newFakeBackend(t)
	backend.details.Runs = []model.RunState{{
		ID: "01980f4c-7b2a-7a6f-8c10-0123456789ac", Number: 1,
	}}
	backend.logs = []byte("first\nsecond\n")
	valid := [][]string{
		{"logs", testJobID, "--run", "all"},
		{"logs", testJobID, "--run", "1"},
		{"logs", testJobID, "--lines", "0"},
		{"show", "run", testJobID, "1"},
	}
	for _, arguments := range valid {
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); err != nil {
			t.Errorf("%v error = %v", arguments, err)
		}
		backend.closed = false
	}
	invalid := [][]string{
		{"logs", testJobID, "--follow", "--all"},
		{"logs", testJobID, "--run", "1", "--all"},
		{"logs", testJobID, "--lines", "-2"},
		{"logs", testJobID, "--run", "99"},
		{"show", "run", testJobID, "99"},
	}
	for _, arguments := range invalid {
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); err == nil {
			t.Errorf("%v error = nil", arguments)
		}
		backend.closed = false
	}
}

func TestOptionalCommandBackendAndOperationFailures(t *testing.T) {
	backend := newFakeBackend(t)
	backend.details.Runs = []model.RunState{{
		ID: "01980f4c-7b2a-7a6f-8c10-0123456789ac", Number: 1,
	}}
	backend.operationErr = errors.New("operation failed")
	for _, arguments := range [][]string{
		{"cancel", "run", testJobID, "1"},
		{"show", "run", testJobID, "1"},
		{"logs", testJobID, "--run", "1"},
		{"doctor"},
	} {
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); err == nil {
			t.Errorf("%v error = nil", arguments)
		}
		backend.closed = false
	}

	basic := &basicBackend{base: newFakeBackend(t)}
	backendDependencies := dependenciesFor(basic)
	if output, err := executeCommand(t, backendDependencies, []string{"list"}); err != nil || output == "" {
		t.Fatalf("fallback list = (%q, %v)", output, err)
	}
	basic.base.closed = false
	for _, arguments := range [][]string{
		{"doctor"},
		{"logs", testJobID, "--all"},
		{"logs", testJobID, "--run", "1"},
	} {
		if _, err := executeCommand(t, backendDependencies, arguments); err == nil {
			t.Errorf("%v error = nil", arguments)
		}
		basic.base.closed = false
	}
}

func TestCancelRunValidationBranches(t *testing.T) {
	backend := newFakeBackend(t)
	backend.details.Runs = []model.RunState{{
		ID: "01980f4c-7b2a-7a6f-8c10-0123456789ac", Number: 1,
	}}
	backend.details.Job.ActiveRunID = ""
	if _, err := executeCommand(t, dependenciesFor(backend), []string{"cancel", "run", testJobID, "1"}); err == nil {
		t.Fatal("cancel run accepted inactive run")
	}
	backend = newFakeBackend(t)
	backend.details.Runs = []model.RunState{{
		ID: "01980f4c-7b2a-7a6f-8c10-0123456789ac", Number: 1,
	}}
	if _, err := executeCommand(t, dependenciesFor(backend), []string{"cancel", "run", testJobID, "99"}); err == nil {
		t.Fatal("cancel run accepted unknown run")
	}
}

func TestCommandAliasesFallbackErrorsAndNamedSpecification(t *testing.T) {
	backend := newFakeBackend(t)
	runID := model.RunID("01980f4c-7b2a-7a6f-8c10-0123456789ac")
	backend.details.Runs = []model.RunState{{ID: runID, Number: 1}}
	backend.details.Job.ActiveRunID = runID
	for _, arguments := range [][]string{
		{"cancel", "job", testJobID},
		{"cancel", "run", testJobID, "1"},
		{"config", "show", "--origins"},
	} {
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); err != nil {
			t.Errorf("%v error = %v", arguments, err)
		}
		backend.closed = false
	}

	basic := &basicBackend{base: newFakeBackend(t)}
	basic.base.operationErr = errors.New("list failed")
	if _, err := executeCommand(t, dependenciesFor(basic), []string{"list"}); err == nil {
		t.Fatal("fallback list failure error = nil")
	}

	configuration := filepath.Join(t.TempDir(), "jobman.yml")
	if err := os.WriteFile(configuration, []byte(`
job_specs:
  named:
    command: [/bin/true]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	backend = newFakeBackend(t)
	if _, err := executeCommand(t, dependenciesFor(backend), []string{
		"--config", configuration, "run", "--job-spec", "named",
	}); err != nil {
		t.Fatalf("run named specification error = %v", err)
	}
}

type basicBackend struct{ base *fakeBackend }

func (backend *basicBackend) Close() error { return backend.base.Close() }

func (backend *basicBackend) ApplyConfig(ctx context.Context, configuration config.Config) error {
	return backend.base.ApplyConfig(ctx, configuration)
}

func (backend *basicBackend) Submit(ctx context.Context, request app.SubmitRequest) (model.JobState, error) {
	return backend.base.Submit(ctx, request)
}

func (backend *basicBackend) List(ctx context.Context) ([]model.JobState, error) {
	return backend.base.List(ctx)
}

func (backend *basicBackend) Inspect(ctx context.Context, selector string) (app.JobDetails, error) {
	return backend.base.Inspect(ctx, selector)
}

func (backend *basicBackend) ReadLogs(
	ctx context.Context,
	selector string,
	stream app.LogStream,
) ([]byte, error) {
	return backend.base.ReadLogs(ctx, selector, stream)
}

func (backend *basicBackend) Cancel(ctx context.Context, selector string) (model.JobState, error) {
	return backend.base.Cancel(ctx, selector)
}
