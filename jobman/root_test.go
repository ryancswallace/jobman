package jobman

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/liveinput"
	"github.com/ryancswallace/jobman/internal/model"
)

const testJobID = "01980f4c-7b2a-7a6f-8c10-0123456789ab"

type fakeBackend struct {
	submitRequest   *app.SubmitRequest
	jobs            []model.JobState
	details         app.JobDetails
	logs            []byte
	canceled        bool
	paused          bool
	resumed         bool
	rerunRequest    *app.RerunRequest
	input           []byte
	inputEOF        atomic.Bool
	inputEOFOnce    sync.Once
	inputEOFSignal  chan struct{}
	waitForInputEOF bool
	followed        atomic.Int64
	cleanRequest    *app.CleanRequest
	closed          bool
	operationErr    error
}

func (backend *fakeBackend) Close() error {
	backend.closed = true

	return nil
}

func (backend *fakeBackend) Submit(_ context.Context, request app.SubmitRequest) (model.JobState, error) {
	if backend.operationErr != nil {
		return model.JobState{}, backend.operationErr
	}
	submittedRequest := request
	submittedRequest.Arguments = append([]string(nil), request.Arguments...)
	submittedRequest.Environment = cloneMap(request.Environment)
	backend.submitRequest = &submittedRequest

	return backend.jobs[0], nil
}

func (backend *fakeBackend) List(context.Context) ([]model.JobState, error) {
	if backend.operationErr != nil {
		return nil, backend.operationErr
	}
	return append([]model.JobState(nil), backend.jobs...), nil
}

func (backend *fakeBackend) Inspect(context.Context, string) (app.JobDetails, error) {
	if backend.operationErr != nil {
		return app.JobDetails{}, backend.operationErr
	}
	return backend.details, nil
}

func (backend *fakeBackend) ReadLogs(context.Context, string, app.LogStream) ([]byte, error) {
	if backend.operationErr != nil {
		return nil, backend.operationErr
	}
	return append([]byte(nil), backend.logs...), nil
}

func (backend *fakeBackend) Cancel(context.Context, string) (model.JobState, error) {
	if backend.operationErr != nil {
		return model.JobState{}, backend.operationErr
	}
	backend.canceled = true

	return backend.jobs[0], nil
}

func (backend *fakeBackend) Pause(context.Context, string) (model.JobState, error) {
	if backend.operationErr != nil {
		return model.JobState{}, backend.operationErr
	}
	backend.paused = true

	return backend.jobs[0], nil
}

func (backend *fakeBackend) Resume(context.Context, string) (model.JobState, error) {
	if backend.operationErr != nil {
		return model.JobState{}, backend.operationErr
	}
	backend.resumed = true

	return backend.jobs[0], nil
}

func (backend *fakeBackend) Wait(ctx context.Context, _ string) (model.JobState, error) {
	if backend.operationErr != nil {
		return model.JobState{}, backend.operationErr
	}
	if backend.waitForInputEOF {
		select {
		case <-ctx.Done():
			return model.JobState{}, ctx.Err()
		case <-backend.inputEOFSignal:
		}
	}
	return backend.jobs[0], nil
}

func (backend *fakeBackend) Rerun(
	_ context.Context,
	_ string,
	request app.RerunRequest,
) (model.JobState, error) {
	if backend.operationErr != nil {
		return model.JobState{}, backend.operationErr
	}
	backend.rerunRequest = &request

	return backend.jobs[0], nil
}

func (backend *fakeBackend) SendInput(
	_ context.Context,
	_ string,
	source io.Reader,
	sendEOF bool,
) (liveinput.Result, error) {
	if backend.operationErr != nil {
		return liveinput.Result{}, backend.operationErr
	}
	content, err := io.ReadAll(source)
	if err != nil {
		return liveinput.Result{}, err
	}
	backend.input = append(backend.input, content...)
	backend.inputEOF.Store(sendEOF)
	if sendEOF {
		backend.inputEOFOnce.Do(func() { close(backend.inputEOFSignal) })
	}

	return liveinput.Result{Delivered: uint64(len(content)), EOF: sendEOF}, nil
}

func (backend *fakeBackend) ReadRunLogs(context.Context, string, app.LogStream, uint64) ([]byte, error) {
	if backend.operationErr != nil {
		return nil, backend.operationErr
	}
	return append([]byte(nil), backend.logs...), nil
}

func (backend *fakeBackend) FollowLogs(
	_ context.Context,
	_ string,
	_ app.LogStream,
	_ uint64,
	destination io.Writer,
) error {
	if backend.operationErr != nil {
		return backend.operationErr
	}
	backend.followed.Add(1)
	_, err := destination.Write(backend.logs)

	return err
}

func (backend *fakeBackend) Clean(_ context.Context, request app.CleanRequest) (app.CleanResult, error) {
	if backend.operationErr != nil {
		return app.CleanResult{}, backend.operationErr
	}
	backend.cleanRequest = &request

	return app.CleanResult{Runs: 1, Files: 3, Bytes: 12}, nil
}

func TestRootShowsHelpWithoutOpeningStore(t *testing.T) {
	t.Parallel()

	stdout, err := executeCommand(t, Dependencies{}, nil)
	if err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if !strings.Contains(stdout, "Available Commands:") || !strings.Contains(stdout, "run") {
		t.Fatalf("root output = %q, want command help", stdout)
	}
	if strings.Contains(stdout, "__supervise") || strings.Contains(stdout, "kill") {
		t.Fatalf("root output exposes private or removed command: %q", stdout)
	}
}

func TestRootReportsBuildVersion(t *testing.T) {
	t.Parallel()

	stdout, err := executeCommand(t, Dependencies{}, []string{"--version"})
	if err != nil {
		t.Fatalf("--version error = %v", err)
	}
	if !strings.HasPrefix(stdout, "jobman ") || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("--version output = %q", stdout)
	}
}

func TestRunPreservesArgumentsAndEnvironment(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	stateDir := filepath.Join(t.TempDir(), "state")
	stdout, err := executeCommand(t, dependenciesFor(backend), []string{
		"--state-dir", stateDir,
		"run", "--name", "example", "--cwd", t.TempDir(), "--env", "KEY=a b", "--",
		"printf", "%s", "a b", "$HOME", "x;y",
	})
	if err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if stdout != testJobID+"\n" {
		t.Fatalf("run output = %q, want only canonical ID", stdout)
	}
	if backend.submitRequest == nil {
		t.Fatal("Submit() was not called")
	}
	if backend.submitRequest.Executable != "printf" {
		t.Fatalf("executable = %q, want printf", backend.submitRequest.Executable)
	}
	wantArguments := []string{"%s", "a b", "$HOME", "x;y"}
	if !slices.Equal(backend.submitRequest.Arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", backend.submitRequest.Arguments, wantArguments)
	}
	if backend.submitRequest.Environment["KEY"] != "a b" {
		t.Fatalf("environment = %#v, want KEY preserved", backend.submitRequest.Environment)
	}
	if !backend.closed {
		t.Fatal("backend was not closed")
	}
}

func TestRunOverlaysStopPolicyFlagsIndependently(t *testing.T) {
	tests := []struct {
		name      string
		flags     []string
		wantGrace time.Duration
		wantForce bool
	}{
		{name: "force only", flags: []string{"--force-after-grace=false"}, wantGrace: 10 * time.Second},
		{name: "grace only", flags: []string{"--stop-grace=2s"}, wantGrace: 2 * time.Second, wantForce: true},
		{
			name: "explicit zero no force", flags: []string{"--stop-grace=0s", "--force-after-grace=false"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend(t)
			arguments := []string{"run"}
			arguments = append(arguments, test.flags...)
			arguments = append(arguments, "--", "true")
			if _, err := executeCommand(t, dependenciesFor(backend), arguments); err != nil {
				t.Fatalf("ExecuteContext() error = %v", err)
			}
			if backend.submitRequest == nil {
				t.Fatal("Submit() was not called")
			}
			got := backend.submitRequest.StopPolicy
			if !backend.submitRequest.StopPolicySet || got.GracePeriod != test.wantGrace ||
				got.ForceAfterGrace != test.wantForce {
				t.Fatalf("stop policy = %#v (set=%t), want grace=%s force=%t",
					got, backend.submitRequest.StopPolicySet, test.wantGrace, test.wantForce)
			}
		})
	}
}

func TestRunRequiresArgumentBoundary(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	_, err := executeCommand(t, dependenciesFor(backend), []string{"run", "printf", "hello"})
	if !errors.Is(err, ErrUsage) || ExitCode(err) != 2 {
		t.Fatalf("run without -- error/code = %v/%d, want usage/2", err, ExitCode(err))
	}
	if backend.submitRequest != nil {
		t.Fatal("invalid run opened the backend")
	}
}

func TestRunAppliesDeferredPolicyFlags(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	_, err := executeCommand(t, dependenciesFor(backend), []string{
		"run", "--name", "policy", "--group", "workers", "--tag", "nightly",
		"--stdin", "live", "--retries", "2", "--retry-timeouts",
		"--retry-delay", "1s", "--repeat-delay", "2s", "--retry-backoff", "exponential",
		"--retry-jitter", "100ms", "--retry-max-delay", "5s",
		"--run-timeout", "3s", "--job-timeout", "1m", "--after-success", testJobID,
		"--pool", "workers", "--slots", "2", "--wait-delay", "250ms",
		"--log-segment-bytes", "1024", "--log-segments", "3", "--log-capture", "stdout",
		"--log-retention", "2h", "--", "printf", "done",
	})
	if err != nil {
		t.Fatalf("run policy flags error = %v", err)
	}
	request := backend.submitRequest
	if request == nil {
		t.Fatal("Submit() was not called")
	}
	policy := request.ExecutionPolicy
	if policy.Completion.MaxRuns.Unlimited || policy.Completion.MaxRuns.Value != 3 ||
		!policy.Classification.RetryTimeout {
		t.Fatalf("completion policy = %+v/%+v, want three runs and retryable timeouts", policy.Completion, policy.Classification)
	}
	if policy.FailureDelay.Base != time.Second || policy.SuccessDelay.Base != 2*time.Second ||
		policy.FailureDelay.Backoff != "exponential" || policy.FailureDelay.Jitter != 100*time.Millisecond {
		t.Fatalf("delay policies = %+v/%+v", policy.FailureDelay, policy.SuccessDelay)
	}
	if policy.RunTimeout != 3*time.Second || policy.JobTimeout != time.Minute ||
		policy.Concurrency.Pool != "workers" || policy.Concurrency.Slots != 2 {
		t.Fatalf("timeout/admission policy = %+v", policy)
	}
	if !slices.Equal(policy.Groups, []string{"workers"}) || !slices.Equal(policy.Tags, []string{"nightly"}) ||
		len(request.Dependencies) != 1 || len(policy.WaitConditions) != 1 {
		t.Fatalf("labels/prerequisites = groups %v tags %v dependencies %v waits %v",
			policy.Groups, policy.Tags, request.Dependencies, policy.WaitConditions)
	}
	if request.StdinPolicy != model.StdinLive || policy.LogRotateSize != 1024 ||
		policy.LogMaxSegmentsPerStream != 3 || policy.LogCapture != "stdout" ||
		policy.LogRetentionMaxAge != 2*time.Hour {
		t.Fatalf("input/log policy = stdin %q policy %+v", request.StdinPolicy, policy)
	}
}

func TestRunRerunSourceIsCanonicalAndRejectsPolicyOverrides(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	stdout, err := executeCommand(t, dependenciesFor(backend), []string{
		"run", "--rerun", testJobID, "--name", "copied",
	})
	if err != nil {
		t.Fatalf("run --rerun error = %v", err)
	}
	if stdout != testJobID+"\n" || backend.rerunRequest == nil || backend.rerunRequest.Name != "copied" {
		t.Fatalf("run --rerun output/request = %q/%+v", stdout, backend.rerunRequest)
	}

	backend.rerunRequest = nil
	_, err = executeCommand(t, dependenciesFor(backend), []string{
		"run", "--rerun", testJobID, "--slots", "2",
	})
	if !errors.Is(err, ErrUsage) || backend.rerunRequest != nil {
		t.Fatalf("run --rerun with override error/request = %v/%+v, want usage/no rerun", err, backend.rerunRequest)
	}
}

func TestLifecycleAndInputCommandsUseTypedBackends(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	if _, err := executeCommand(t, dependenciesFor(backend), []string{"pause", testJobID}); err != nil {
		t.Fatalf("pause error = %v", err)
	}
	if _, err := executeCommand(t, dependenciesFor(backend), []string{"resume", testJobID}); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if !backend.paused || !backend.resumed {
		t.Fatalf("pause/resume calls = %t/%t", backend.paused, backend.resumed)
	}

	command := NewCommand(dependenciesFor(backend))
	command.SetArgs([]string{"input", "--eof", testJobID})
	command.SetIn(strings.NewReader("secret bytes\x00"))
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("input error = %v", err)
	}
	if string(backend.input) != "secret bytes\x00" || !backend.inputEOF.Load() {
		t.Fatalf("input/eof = %q/%t", backend.input, backend.inputEOF.Load())
	}
	_, err := executeCommand(t, dependenciesFor(backend), []string{"input", testJobID, "secret-on-argv"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("positional input error = %v, want usage", err)
	}
}

func TestListJSONUsesVersionedEnvelope(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	stdout, err := executeCommand(t, dependenciesFor(backend), []string{"list", "--json"})
	if err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if !strings.Contains(stdout, `"schema_version":1`) || !strings.Contains(stdout, testJobID) {
		t.Fatalf("list JSON = %q, want version and job ID", stdout)
	}
}

func TestStatusAndShow(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	stdout, err := executeCommand(t, dependenciesFor(backend), []string{"status", testJobID})
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !strings.Contains(stdout, testJobID) || !strings.Contains(stdout, "submitting") {
		t.Fatalf("status output = %q", stdout)
	}

	stdout, err = executeCommand(t, dependenciesFor(backend), []string{"show", "--json", testJobID})
	if err != nil {
		t.Fatalf("show error = %v", err)
	}
	if !strings.Contains(stdout, `"specification"`) || !strings.Contains(stdout, `"runs"`) {
		t.Fatalf("show output = %q", stdout)
	}
}

func TestLogsPreserveBinaryBytes(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	backend.logs = []byte{0, 'a', '\n', 0xff}
	stdout, err := executeCommand(t, dependenciesFor(backend), []string{"logs", testJobID})
	if err != nil {
		t.Fatalf("logs error = %v", err)
	}
	if !bytes.Equal([]byte(stdout), backend.logs) {
		t.Fatalf("logs output = %v, want %v", []byte(stdout), backend.logs)
	}
}

func TestCancelUsesCanonicalCommand(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	_, err := executeCommand(t, dependenciesFor(backend), []string{"cancel", testJobID})
	if err != nil {
		t.Fatalf("cancel error = %v", err)
	}
	if !backend.canceled {
		t.Fatal("Cancel() was not called")
	}

	_, err = executeCommand(t, dependenciesFor(backend), []string{"kill", testJobID})
	if err == nil {
		t.Fatal("removed kill command unexpectedly succeeded")
	}
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		want int
	}{
		{err: nil, want: 0},
		{err: ErrUsage, want: 2},
		{err: app.ErrNotFound, want: 3},
		{err: app.ErrAmbiguous, want: 4},
		{err: app.ErrConflict, want: 5},
		{err: fmt.Errorf("partial live input: %w", io.ErrShortWrite), want: 6},
		{err: errors.New("boom"), want: 1},
	}
	for _, test := range tests {
		if got := ExitCode(test.err); got != test.want {
			t.Errorf("ExitCode(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}

func executeCommand(
	t *testing.T,
	dependencies Dependencies,
	arguments []string,
) (stdoutText string, executeErr error) {
	t.Helper()

	command := NewCommand(dependencies)
	stdin := bytes.NewReader(nil)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command.SetArgs(arguments)
	command.SetIn(stdin)
	command.SetOut(stdout)
	command.SetErr(stderr)
	executeErr = command.ExecuteContext(t.Context())

	return stdout.String(), executeErr
}

func dependenciesFor(backend app.Backend) Dependencies {
	return Dependencies{
		OpenBackend: func(context.Context, string) (app.Backend, error) {
			return backend, nil
		},
	}
}

func newFakeBackend(t *testing.T) *fakeBackend {
	t.Helper()

	id, err := model.ParseJobID(testJobID)
	if err != nil {
		t.Fatalf("ParseJobID() error = %v", err)
	}
	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable:             "printf",
		Arguments:              []string{"hello"},
		WorkingDirectory:       t.TempDir(),
		EnvironmentInheritance: model.EnvironmentInheritSubmission,
		StopPolicy: model.StopPolicy{
			GracePeriod:     5 * time.Second,
			ForceAfterGrace: true,
		},
		StdinPolicy: model.StdinNull,
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}
	job := model.JobState{
		ID:          id,
		Spec:        specification,
		Phase:       model.JobPhaseSubmitting,
		Revision:    1,
		SubmittedAt: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
	}

	return &fakeBackend{
		jobs:           []model.JobState{job},
		details:        app.JobDetails{Job: job, Runs: []model.RunState{}},
		inputEOFSignal: make(chan struct{}),
	}
}

func cloneMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

var _ io.Closer = (*fakeBackend)(nil)
