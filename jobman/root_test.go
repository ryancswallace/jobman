package jobman

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/model"
)

const testJobID = "01980f4c-7b2a-7a6f-8c10-0123456789ab"

type fakeBackend struct {
	submitRequest *app.SubmitRequest
	jobs          []model.JobState
	details       app.JobDetails
	logs          []byte
	canceled      bool
	closed        bool
}

func (backend *fakeBackend) Close() error {
	backend.closed = true

	return nil
}

func (backend *fakeBackend) Submit(_ context.Context, request app.SubmitRequest) (model.JobState, error) {
	submittedRequest := request
	submittedRequest.Arguments = append([]string(nil), request.Arguments...)
	submittedRequest.Environment = cloneMap(request.Environment)
	backend.submitRequest = &submittedRequest

	return backend.jobs[0], nil
}

func (backend *fakeBackend) List(context.Context) ([]model.JobState, error) {
	return append([]model.JobState(nil), backend.jobs...), nil
}

func (backend *fakeBackend) Inspect(context.Context, string) (app.JobDetails, error) {
	return backend.details, nil
}

func (backend *fakeBackend) ReadLogs(context.Context, string, app.LogStream) ([]byte, error) {
	return append([]byte(nil), backend.logs...), nil
}

func (backend *fakeBackend) Cancel(context.Context, string) (model.JobState, error) {
	backend.canceled = true

	return backend.jobs[0], nil
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
		jobs:    []model.JobState{job},
		details: app.JobDetails{Job: job, Runs: []model.RunState{}},
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
