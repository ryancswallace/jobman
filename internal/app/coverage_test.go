package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/liveinput"
	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/store"
	"github.com/ryancswallace/jobman/internal/supervisor"
)

func TestOpenApplyConfigAndClose(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	backend, err := Open(t.Context(), stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	service, ok := backend.(*Service)
	if !ok {
		t.Fatalf("Open() backend type = %T", backend)
	}
	global, err := config.NewSlotLimit(3)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := config.NewSlotLimit(2)
	if err != nil {
		t.Fatal(err)
	}
	configuration := config.Config{
		Concurrency: config.Concurrency{MaxActiveSlots: global, Pools: map[string]config.SlotLimit{"build": pool}},
		Retention: config.Retention{
			MaxJobs: config.NewIntegerLimit(4), MaxRunsPerJob: config.UnlimitedIntegerLimit(),
			MaxLogBytesPerJob: config.NewByteLimit(1024), MaxTotalLogBytes: config.UnlimitedByteLimit(),
		},
	}
	if err := service.ApplyConfig(t.Context(), configuration); err != nil {
		t.Fatalf("ApplyConfig() error = %v", err)
	}
	if _, found := service.knownPools["build"]; !found || service.retention.MaxJobs != configuration.Retention.MaxJobs {
		t.Fatalf("applied config = pools %#v retention %#v", service.knownPools, service.retention)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	stateFile := filepath.Join(t.TempDir(), "state-file")
	if err := os.WriteFile(stateFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), stateFile); err == nil {
		t.Fatal("Open(file state root) error = nil")
	}
}

func TestServicePauseResumeAndRerunWithoutActiveTarget(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{
		Name: "source", Executable: "true", WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(time.Second)
	if _, err := service.store.MoveJob(t.Context(), job.ID, model.JobPhaseQueued, clock.now, "ready"); err != nil {
		t.Fatalf("MoveJob(queued) error = %v", err)
	}
	paused, err := service.Pause(t.Context(), job.ID.String())
	if err != nil || paused.Phase != model.JobPhasePaused {
		t.Fatalf("Pause() = (%+v, %v)", paused, err)
	}
	clock.now = clock.now.Add(time.Second)
	resumed, err := service.Resume(t.Context(), job.ID.String())
	if err != nil || resumed.Phase != model.JobPhaseQueued {
		t.Fatalf("Resume() = (%+v, %v)", resumed, err)
	}
	clone, err := service.Rerun(t.Context(), job.ID.String(), RerunRequest{Name: "clone"})
	if err != nil || clone.ID == job.ID || clone.Spec.Name() != "clone" || clone.Spec.Executable() != job.Spec.Executable() {
		t.Fatalf("Rerun() = (%+v, %v)", clone, err)
	}
}

func TestServiceFollowCompletedRunAndSelectionErrors(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, run, _ := completeCapturedRun(t, service, clock)
	var output bytes.Buffer
	if err := service.FollowLogs(t.Context(), job.ID.String(), LogStdout, run.Number, &output); err != nil {
		t.Fatalf("FollowLogs(stdout) error = %v", err)
	}
	if output.String() != "captured\n" {
		t.Fatalf("FollowLogs(stdout) = %q", output.String())
	}
	output.Reset()
	if err := service.FollowLogs(t.Context(), job.ID.String(), LogStderr, 0, &output); err != nil {
		t.Fatalf("FollowLogs(stderr) error = %v", err)
	}
	output.Reset()
	if err := service.FollowLogs(t.Context(), job.ID.String(), LogBoth, 0, &output); err != nil || output.String() != "captured\n" {
		t.Fatalf("FollowLogs(both) = (%q, %v)", output.String(), err)
	}
	for _, stream := range []LogStream{LogStdout, LogStderr, LogBoth} {
		content, readErr := service.ReadRunLogs(t.Context(), job.ID.String(), stream, run.Number)
		if readErr != nil {
			t.Errorf("ReadRunLogs(%s) error = %v", stream, readErr)
		}
		if stream != LogStderr && string(content) != "captured\n" {
			t.Errorf("ReadRunLogs(%s) = %q", stream, content)
		}
	}
	if _, err := service.ReadRunLogs(t.Context(), job.ID.String(), LogStream("invalid"), run.Number); err == nil {
		t.Fatal("ReadRunLogs(invalid stream) error = nil")
	}
	if _, err := service.ReadRunLogs(t.Context(), job.ID.String(), LogBoth, run.Number+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadRunLogs(missing run) error = %v", err)
	}
	if err := service.FollowLogs(t.Context(), job.ID.String(), LogBoth, run.Number+1, &output); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FollowLogs(missing run) error = %v", err)
	}
	if err := service.FollowLogs(t.Context(), job.ID.String(), LogStream("invalid"), run.Number, &output); err == nil {
		t.Fatal("FollowLogs(invalid stream) error = nil")
	}
}

func TestServiceLiveInputValidationAndWaitCancellation(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	if _, err := service.SendInput(t.Context(), "missing", nil, false); err == nil {
		t.Fatal("SendInput(nil) error = nil")
	}
	job, err := service.Submit(t.Context(), SubmitRequest{
		Executable: "true", WorkingDirectory: t.TempDir(), StdinPolicy: model.StdinNull,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SendInput(t.Context(), job.ID.String(), bytes.NewReader([]byte("x")), false); !errors.Is(err, ErrConflict) {
		t.Fatalf("SendInput(non-live) error = %v, want ErrConflict", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Wait(ctx, job.ID.String()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait(canceled) error = %v", err)
	}
}

func TestServiceDeliversLiveInputAndDurableEOF(t *testing.T) {
	t.Parallel()

	service, _, job, runID, _ := newLiveInputService(t)
	var delivered bytes.Buffer
	service.sendInput = func(
		_ context.Context,
		endpoint string,
		gotRunID string,
		source io.Reader,
		eof bool,
	) (liveinput.Result, error) {
		if endpoint != "memory-endpoint" || gotRunID != runID.String() {
			return liveinput.Result{}, errors.New("wrong live-input identity")
		}
		content, readErr := io.ReadAll(source)
		if readErr != nil {
			return liveinput.Result{}, readErr
		}
		_, _ = delivered.Write(content)
		return liveinput.Result{Delivered: uint64(len(content)), EOF: eof}, nil
	}
	payload := bytes.Repeat([]byte("x"), 70*1024)
	result, err := service.SendInput(t.Context(), job.ID.String(), bytes.NewReader(payload), true)
	if err != nil || result.Delivered != uint64(len(payload)) || !result.EOF || !bytes.Equal(delivered.Bytes(), payload) {
		t.Fatalf("SendInput() = (%+v, %v), delivered %d", result, err, delivered.Len())
	}
	if _, err := service.SendInput(t.Context(), job.ID.String(), bytes.NewReader(nil), true); !errors.Is(err, ErrConflict) {
		t.Fatalf("SendInput(repeated EOF) error = %v", err)
	}
}

func TestServicePausesResumesAndCancelsActiveProcess(t *testing.T) {
	if os.Getenv("JOBMAN_APP_PROCESS_HELPER") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}

	service, clock := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{
		Executable: os.Args[0], WorkingDirectory: t.TempDir(),
		StopPolicy: model.StopPolicy{ForceAfterGrace: true}, StopPolicySet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := service.ids.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	capture, err := logstore.CreateRun(service.stateDir, job.ID.String(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = capture.Close() })
	paths := capture.Paths()
	logs := model.LogMetadata{
		StdoutPath: paths.Stdout, StderrPath: paths.Stderr, IndexPath: paths.Index,
		IndexVersion: capture.IndexVersion(), Integrity: model.LogIntegrityPending,
		RecordingHealth: model.RecordingHealthy,
	}
	if _, err := service.store.ReserveRun(t.Context(), job.ID, runID, 1, logs, clock.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestServicePausesResumesAndCancelsActiveProcess$")
	command.Env = append(os.Environ(), "JOBMAN_APP_PROCESS_HELPER=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	identity, err := platform.Inspect(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.MarkProcessStarted(
		t.Context(), job.ID, runID, os.Args[0],
		model.ProcessIdentity{
			PID: identity.PID, Platform: runtime.GOOS, CreationID: identity.Creation,
			BootID: identity.Boot, TreeID: strconv.Itoa(identity.PID),
		}, clock.now.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(3 * time.Second)
	if paused, err := service.Pause(t.Context(), job.ID.String()); err != nil || paused.Phase != model.JobPhasePaused {
		t.Fatalf("Pause(active) = (%+v, %v)", paused, err)
	}
	clock.now = clock.now.Add(time.Second)
	if resumed, err := service.Resume(t.Context(), job.ID.String()); err != nil || resumed.Phase != model.JobPhaseRunning {
		t.Fatalf("Resume(active) = (%+v, %v)", resumed, err)
	}
	clock.now = clock.now.Add(time.Second)
	if canceled, err := service.Cancel(t.Context(), job.ID.String()); err != nil || canceled.Phase != model.JobPhaseStopping {
		t.Fatalf("Cancel(active) = (%+v, %v)", canceled, err)
	}
	_ = stdin.Close()
	_ = command.Wait()
}

func TestRetentionAndErrorTranslationHelpers(t *testing.T) {
	t.Parallel()

	configuration := config.Retention{
		MaxJobs: config.NewIntegerLimit(2), MaxRunsPerJob: config.UnlimitedIntegerLimit(),
		MaxLogBytesPerJob: config.NewByteLimit(2048), MaxTotalLogBytes: config.UnlimitedByteLimit(),
	}
	policy := retentionPlanPolicy(configuration)
	if policy.MaxJobs.Unlimited || policy.MaxJobs.Maximum != 2 || !policy.MaxRunsPerJob.Unlimited ||
		policy.MaxBytesPerJob.Maximum != 2048 || !policy.MaxTotalBytes.Unlimited {
		t.Fatalf("retentionPlanPolicy() = %+v", policy)
	}
	if !integerRetentionLimit(config.IntegerLimit{}).Unlimited || !byteRetentionLimit(config.ByteLimit{}).Unlimited {
		t.Fatal("unset retention limits were not unlimited")
	}
	if waitForExit(t.Context(), platform.ProcessIdentity{}, 0) != nil {
		t.Fatal("waitForExit(zero grace) error != nil")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitForExit(ctx, platform.ProcessIdentity{}, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForExit(canceled) error = %v", err)
	}
	for source, target := range map[error]error{
		store.ErrNotFound: ErrNotFound, store.ErrAmbiguous: ErrAmbiguous, store.ErrConflict: ErrConflict,
	} {
		if err := translateStoreError("operation", source); !errors.Is(err, target) {
			t.Errorf("translateStoreError(%v) = %v", source, err)
		}
	}
}

func TestServicePolicyCleanupPlanningAndLogRecovery(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	firstJob, firstRun, firstPaths := completeCapturedRun(t, service, clock)
	_, _, _ = completeCapturedRun(t, service, clock)
	service.retention.MaxJobs = config.NewIntegerLimit(0)
	dryRun, err := service.Clean(t.Context(), CleanRequest{UsePolicy: true, DryRun: true})
	if err != nil || dryRun.Runs != 2 || dryRun.Bytes == 0 {
		t.Fatalf("Clean(policy dry run) = (%+v, %v)", dryRun, err)
	}
	removed, err := service.Clean(t.Context(), CleanRequest{UsePolicy: true})
	if err != nil || removed.Runs != 2 || removed.Files == 0 {
		t.Fatalf("Clean(policy) = (%+v, %v)", removed, err)
	}

	recovered := recoverLogMetadata(service.stateDir, firstJob.ID, firstRun)
	if recovered.RecordingHealth != model.RecordingDegraded {
		t.Fatalf("recoverLogMetadata(pruned) = %+v", recovered)
	}
	if err := os.MkdirAll(firstPaths.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstPaths.Stdout, []byte("tail"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered = recoverLogMetadata(service.stateDir, firstJob.ID, firstRun)
	if recovered.StdoutSize != 4 || recovered.DiagnosticCode != "supervisor_lease_expired" {
		t.Fatalf("recoverLogMetadata(partial) = %+v", recovered)
	}
}

func TestServicePropagatesClosedStoreFailures(t *testing.T) {
	service, _ := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{
		Executable: "true", WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()

	if err := service.ApplyConfig(ctx, config.Config{}); err == nil {
		t.Fatal("ApplyConfig(closed store) error = nil")
	}
	if _, err := service.Submit(ctx, SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()}); err == nil {
		t.Fatal("Submit(closed store) error = nil")
	}
	if _, err := service.List(ctx); err == nil {
		t.Fatal("List(closed store) error = nil")
	}
	if _, err := service.reconcileListedJobs(ctx, []model.JobState{job}); err == nil {
		t.Fatal("reconcileListedJobs(closed store) error = nil")
	}
	if _, err := service.Inspect(ctx, "missing"); err == nil {
		t.Fatal("Inspect(closed store) error = nil")
	}
	if _, err := service.ReadLogs(ctx, "missing", LogBoth); err == nil {
		t.Fatal("ReadLogs(closed store) error = nil")
	}
	if _, err := service.Cancel(ctx, "missing"); err == nil {
		t.Fatal("Cancel(closed store) error = nil")
	}
	if _, err := service.Pause(ctx, "missing"); err == nil {
		t.Fatal("Pause(closed store) error = nil")
	}
	if _, err := service.Resume(ctx, "missing"); err == nil {
		t.Fatal("Resume(closed store) error = nil")
	}
	if _, err := service.Wait(ctx, "missing"); err == nil {
		t.Fatal("Wait(closed store) error = nil")
	}
	if _, err := service.Rerun(ctx, "missing", RerunRequest{}); err == nil {
		t.Fatal("Rerun(closed store) error = nil")
	}
	if _, err := service.SendInput(ctx, "missing", bytes.NewReader(nil), false); err == nil {
		t.Fatal("SendInput(closed store) error = nil")
	}
	if err := service.FollowLogs(ctx, "missing", LogBoth, 0, io.Discard); err == nil {
		t.Fatal("FollowLogs(closed store) error = nil")
	}
	if _, err := service.Clean(ctx, CleanRequest{Selector: "missing"}); err == nil {
		t.Fatal("Clean(selector, closed store) error = nil")
	}
	if _, err := service.Clean(ctx, CleanRequest{}); err == nil {
		t.Fatal("Clean(all, closed store) error = nil")
	}
}

func TestServiceSubmissionFailureStages(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	if _, err := service.Submit(t.Context(), SubmitRequest{}); err == nil {
		t.Fatal("Submit(invalid specification) error = nil")
	}

	identifierFailure := errors.New("identifier entropy failed")
	ids, err := model.NewUUIDv7Generator(time.Now, &errorReader{err: identifierFailure})
	if err != nil {
		t.Fatal(err)
	}
	service.ids = ids
	if _, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()}); !errors.Is(err, identifierFailure) {
		t.Fatalf("Submit(identifier failure) error = %v", err)
	}

	service, _ = newTestService(t)
	credentialFailure := errors.New("credential entropy failed")
	service.random = &errorReader{err: credentialFailure}
	if _, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()}); !errors.Is(err, credentialFailure) {
		t.Fatalf("Submit(credential failure) error = %v", err)
	}

	service, _ = newTestService(t)
	launchFailure := errors.New("launch failed")
	service.launch = func(context.Context, supervisor.LaunchOptions) (supervisor.Acknowledgement, error) {
		return supervisor.Acknowledgement{}, launchFailure
	}
	if _, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()}); !errors.Is(err, launchFailure) {
		t.Fatalf("Submit(launch failure) error = %v", err)
	}

	service, _ = newTestService(t)
	service.launch = func(context.Context, supervisor.LaunchOptions) (supervisor.Acknowledgement, error) {
		if err := service.store.Close(); err != nil {
			return supervisor.Acknowledgement{}, err
		}
		return supervisor.Acknowledgement{}, nil
	}
	if _, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()}); err == nil {
		t.Fatal("Submit(post-launch load failure) error = nil")
	}
}

type errorReader struct{ err error }

func (reader *errorReader) Read([]byte) (int, error) { return 0, reader.err }

type dataErrorReader struct {
	data []byte
	err  error
}

func (reader *dataErrorReader) Read(destination []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	count := copy(destination, reader.data)
	reader.data = reader.data[count:]
	return count, reader.err
}

type hookEOFReader struct{ hook func() }

func (reader *hookEOFReader) Read([]byte) (int, error) {
	reader.hook()
	return 0, io.EOF
}

func TestServiceLiveInputFailureStages(t *testing.T) {
	t.Parallel()

	t.Run("transport", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputService(t)
		wantErr := errors.New("transport failed")
		service.sendInput = func(context.Context, string, string, io.Reader, bool) (liveinput.Result, error) {
			return liveinput.Result{Delivered: 2}, wantErr
		}
		result, err := service.SendInput(t.Context(), job.ID.String(), bytes.NewReader([]byte("data")), false)
		if !errors.Is(err, wantErr) || result.Delivered != 2 {
			t.Fatalf("SendInput(transport failure) = (%+v, %v)", result, err)
		}
	})

	t.Run("source", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputService(t)
		wantErr := errors.New("source failed")
		service.sendInput = successfulInputSender
		result, err := service.SendInput(
			t.Context(), job.ID.String(), &dataErrorReader{data: []byte("data"), err: wantErr}, false,
		)
		if !errors.Is(err, wantErr) || result.Delivered != 4 {
			t.Fatalf("SendInput(source failure) = (%+v, %v)", result, err)
		}
	})

	t.Run("active run changes", func(t *testing.T) {
		t.Parallel()
		service, clock, job, runID, capture := newLiveInputService(t)
		service.sendInput = func(_ context.Context, _, _ string, source io.Reader, _ bool) (liveinput.Result, error) {
			content, err := io.ReadAll(source)
			if err != nil {
				return liveinput.Result{}, err
			}
			if err := capture.Close(); err != nil {
				return liveinput.Result{}, err
			}
			logs := liveLogMetadata(capture)
			logs.Integrity = model.LogIntegrityValid
			exitCode := 0
			completedAt := clock.now.Add(4 * time.Second)
			if _, err := service.store.CompleteRunWithDisposition(
				t.Context(), job.ID, runID, model.RunOutcomeSuccess,
				&model.ExitInfo{ExitCode: &exitCode, ObservedAt: completedAt}, logs, "", completedAt,
				model.RunDisposition{TerminalOutcome: model.JobOutcomeSuccess},
			); err != nil {
				return liveinput.Result{}, err
			}
			return liveinput.Result{Delivered: uint64(len(content))}, nil
		}
		if _, err := service.SendInput(t.Context(), job.ID.String(), bytes.NewReader([]byte("data")), false); !errors.Is(err, ErrConflict) {
			t.Fatalf("SendInput(changed active run) error = %v", err)
		}
	})

	t.Run("record EOF", func(t *testing.T) {
		service, _, job, _, _ := newLiveInputService(t)
		service.sendInput = successfulInputSender
		reader := &hookEOFReader{hook: func() { _ = service.store.Close() }}
		if _, err := service.SendInput(t.Context(), job.ID.String(), reader, true); err == nil {
			t.Fatal("SendInput(EOF persistence failure) error = nil")
		}
	})

	t.Run("EOF transport race", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputService(t)
		wantErr := errors.New("EOF transport failed")
		service.sendInput = func(_ context.Context, _, _ string, source io.Reader, eof bool) (liveinput.Result, error) {
			content, _ := io.ReadAll(source)
			if eof {
				return liveinput.Result{}, wantErr
			}
			return liveinput.Result{Delivered: uint64(len(content))}, nil
		}
		result, err := service.SendInput(t.Context(), job.ID.String(), bytes.NewReader(nil), true)
		if err != nil || !result.EOF {
			t.Fatalf("SendInput(EOF transport race) = (%+v, %v)", result, err)
		}
	})

	if _, err := (&Service{}).sendLiveInput(t.Context(), "missing", "run", bytes.NewReader(nil), false); err == nil {
		t.Fatal("sendLiveInput(missing endpoint) error = nil")
	}
}

func successfulInputSender(
	_ context.Context,
	_ string,
	_ string,
	source io.Reader,
	eof bool,
) (liveinput.Result, error) {
	content, err := io.ReadAll(source)
	return liveinput.Result{Delivered: uint64(len(content)), EOF: eof}, err
}

func newLiveInputService(
	t *testing.T,
) (*Service, *testClock, model.JobState, model.RunID, *logstore.Run) {
	t.Helper()

	return newLiveInputServiceWithStopPolicy(t, model.StopPolicy{ForceAfterGrace: true})
}

func newLiveInputServiceWithStopPolicy(
	t *testing.T,
	stopPolicy model.StopPolicy,
) (*Service, *testClock, model.JobState, model.RunID, *logstore.Run) {
	t.Helper()

	service, clock := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{
		Executable: "true", WorkingDirectory: t.TempDir(), StdinPolicy: model.StdinLive,
		StopPolicy: stopPolicy, StopPolicySet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runID, err := service.ids.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	capture, err := logstore.CreateRun(service.stateDir, job.ID.String(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = capture.Close() })
	if _, err := service.store.ReserveRun(
		t.Context(), job.ID, runID, 1, liveLogMetadata(capture), clock.now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.MarkProcessStarted(
		t.Context(), job.ID, runID, "/bin/true",
		model.ProcessIdentity{PID: 1234, Platform: "test", CreationID: "live", BootID: "boot", TreeID: "tree"},
		clock.now.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.store.SetInputEndpoint(t.Context(), job.ID, "memory-endpoint", clock.now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(3 * time.Second)

	return service, clock, job, runID, capture
}

func liveLogMetadata(capture *logstore.Run) model.LogMetadata {
	paths := capture.Paths()
	return model.LogMetadata{
		StdoutPath: paths.Stdout, StderrPath: paths.Stderr, IndexPath: paths.Index,
		IndexVersion: capture.IndexVersion(), Integrity: model.LogIntegrityPending,
		RecordingHealth: model.RecordingHealthy,
	}
}

func TestServiceReconciliationAndLogSelectionEdges(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	details, err := service.Inspect(t.Context(), job.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := service.reconcileExpiredSubmission(t.Context(), details.Job); err != nil || changed {
		t.Fatalf("reconcileExpiredSubmission(active) = (%t, %v)", changed, err)
	}
	if changed, err := service.reconcileStaleOwnership(t.Context(), details.Job, details.Runs); err != nil || changed {
		t.Fatalf("reconcileStaleOwnership(live lease) = (%t, %v)", changed, err)
	}
	stopping, err := service.Cancel(t.Context(), job.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if stopping.Phase != model.JobPhaseStopping {
		t.Fatalf("Cancel(no run) phase = %s", stopping.Phase)
	}
	clock.now = clock.now.Add(time.Second)
	completedResult, err := service.store.FinalizeCancellationWithoutRun(t.Context(), job.ID, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if completedResult.Job.Phase != model.JobPhaseCompleted {
		t.Fatalf("FinalizeCancellationWithoutRun() phase = %s", completedResult.Job.Phase)
	}
	if err := service.FollowLogs(t.Context(), job.ID.String(), LogBoth, 0, io.Discard); !errors.Is(err, ErrConflict) {
		t.Fatalf("FollowLogs(completed without run) error = %v", err)
	}

	waiting, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.FollowLogs(ctx, waiting.ID.String(), LogBoth, 0, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("FollowLogs(canceled before first run) error = %v", err)
	}

	completedJob, completedRun, _ := completeCapturedRun(t, service, clock)
	if _, err := service.Clean(t.Context(), CleanRequest{Selector: completedJob.ID.String()}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadRunLogs(t.Context(), completedJob.ID.String(), LogBoth, completedRun.Number); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadRunLogs(pruned) error = %v", err)
	}
	if err := service.FollowLogs(t.Context(), completedJob.ID.String(), LogBoth, completedRun.Number, io.Discard); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FollowLogs(pruned) error = %v", err)
	}

	runs, err := service.store.ListRuns(t.Context(), completedJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if findRun(runs, completedRun.ID) == nil || findRun(runs, model.RunID("missing")) != nil {
		t.Fatal("findRun() did not distinguish present and missing run IDs")
	}
}

func TestServiceLogRecoveryIntegrityStates(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, run, paths := completeCapturedRun(t, service, clock)
	recovered := recoverLogMetadata(service.stateDir, job.ID, run)
	if recovered.Integrity != model.LogIntegrityValid || recovered.StdoutSize == 0 {
		t.Fatalf("recoverLogMetadata(valid) = %+v", recovered)
	}

	index, err := os.OpenFile(paths.Index, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.WriteAt([]byte{0}, 0); err != nil {
		_ = index.Close()
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	recovered = recoverLogMetadata(service.stateDir, job.ID, run)
	if recovered.Integrity != model.LogIntegrityCorrupt {
		t.Fatalf("recoverLogMetadata(corrupt) = %+v", recovered)
	}

	service, clock = newTestService(t)
	job, run, paths = completeCapturedRun(t, service, clock)
	index, err = os.OpenFile(paths.Index, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.WriteString("torn"); err != nil {
		_ = index.Close()
		t.Fatal(err)
	}
	if err := index.Close(); err != nil {
		t.Fatal(err)
	}
	recovered = recoverLogMetadata(service.stateDir, job.ID, run)
	if recovered.Integrity != model.LogIntegrityPartial {
		t.Fatalf("recoverLogMetadata(torn) = %+v", recovered)
	}
}

func TestServiceReportsMissingLogFiles(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, run, paths := completeCapturedRun(t, service, clock)
	if err := os.Rename(paths.Directory, paths.Directory+".missing"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadRunLogs(t.Context(), job.ID.String(), LogBoth, run.Number); err == nil {
		t.Fatal("ReadRunLogs(missing directory) error = nil")
	}
	if err := service.FollowLogs(t.Context(), job.ID.String(), LogBoth, run.Number, io.Discard); err == nil {
		t.Fatal("FollowLogs(missing directory) error = nil")
	}
}

func TestServiceCompletedLifecycleAndReconciliationConflicts(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, _, _ := completeCapturedRun(t, service, clock)
	if got, err := service.Wait(t.Context(), job.ID.String()); err != nil || got.Phase != model.JobPhaseCompleted {
		t.Fatalf("Wait(completed) = (%+v, %v)", got, err)
	}
	if got, err := service.Cancel(t.Context(), job.ID.String()); err != nil || got.Phase != model.JobPhaseCompleted {
		t.Fatalf("Cancel(completed) = (%+v, %v)", got, err)
	}
	if _, err := service.Pause(t.Context(), job.ID.String()); !errors.Is(err, ErrConflict) {
		t.Fatalf("Pause(completed) error = %v", err)
	}
	if _, err := service.Resume(t.Context(), job.ID.String()); !errors.Is(err, ErrConflict) {
		t.Fatalf("Resume(completed) error = %v", err)
	}

	service, clock = newTestService(t)
	service.launch = func(context.Context, supervisor.LaunchOptions) (supervisor.Acknowledgement, error) {
		return supervisor.Acknowledgement{}, errors.New("launch failed")
	}
	_, _ = service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()})
	jobs, err := service.store.ListJobs(t.Context(), store.ListJobsOptions{})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs() = (%+v, %v)", jobs, err)
	}
	expired := jobs[0]
	clock.now = clock.now.Add(defaultClaimWindow + time.Second)
	if _, err := service.store.MarkSubmissionFailed(t.Context(), expired.ID, "other-client", clock.now); err != nil {
		t.Fatal(err)
	}
	if changed, err := service.reconcileExpiredSubmission(t.Context(), expired); err != nil || !changed {
		t.Fatalf("reconcileExpiredSubmission(stale conflict) = (%t, %v)", changed, err)
	}

	service, clock = newTestService(t)
	service.launch = func(context.Context, supervisor.LaunchOptions) (supervisor.Acknowledgement, error) {
		return supervisor.Acknowledgement{}, errors.New("launch failed")
	}
	_, _ = service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()})
	jobs, err = service.store.ListJobs(t.Context(), store.ListJobsOptions{})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs() = (%+v, %v)", jobs, err)
	}
	expired = jobs[0]
	clock.now = clock.now.Add(defaultClaimWindow + time.Second)
	if err := service.store.Close(); err != nil {
		t.Fatal(err)
	}
	if changed, err := service.reconcileExpiredSubmission(t.Context(), expired); err == nil || changed {
		t.Fatalf("reconcileExpiredSubmission(store failure) = (%t, %v)", changed, err)
	}
}

func TestServiceLaunchRaceAndInputWaitCancellation(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	originalLaunch := service.launch
	wantErr := errors.New("acknowledgement lost")
	service.launch = func(ctx context.Context, options supervisor.LaunchOptions) (supervisor.Acknowledgement, error) {
		acknowledgement, err := originalLaunch(ctx, options)
		if err != nil {
			return acknowledgement, err
		}
		return acknowledgement, wantErr
	}
	job, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()})
	if err != nil || !job.SupervisorID.Valid() {
		t.Fatalf("Submit(lost acknowledgement) = (%+v, %v)", job, err)
	}

	service, _ = newTestService(t)
	waiting, err := service.Submit(t.Context(), SubmitRequest{
		Executable: "true", WorkingDirectory: t.TempDir(), StdinPolicy: model.StdinLive,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := service.waitForInputTarget(ctx, waiting.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForInputTarget(canceled) error = %v", err)
	}
}

func TestServiceProcessControlFailureHandling(t *testing.T) {
	t.Parallel()

	t.Run("pause unsupported", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputService(t)
		service.pauseResumeSupported = func() bool { return false }
		if _, err := service.Pause(t.Context(), job.ID.String()); !errors.Is(err, platform.ErrUnsupported) {
			t.Fatalf("Pause(unsupported) error = %v", err)
		}
	})

	t.Run("pause rollback", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputService(t)
		wantErr := errors.New("pause failed")
		service.pauseResumeSupported = func() bool { return true }
		service.processPause = func(platform.ProcessIdentity) error { return wantErr }
		got, err := service.Pause(t.Context(), job.ID.String())
		if !errors.Is(err, wantErr) || got.Phase != model.JobPhaseRunning {
			t.Fatalf("Pause(platform failure) = (%+v, %v)", got, err)
		}
	})

	t.Run("resume rollback", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputService(t)
		wantErr := errors.New("resume failed")
		service.pauseResumeSupported = func() bool { return true }
		service.processPause = func(platform.ProcessIdentity) error { return nil }
		if _, err := service.Pause(t.Context(), job.ID.String()); err != nil {
			t.Fatal(err)
		}
		service.processResume = func(platform.ProcessIdentity) error { return wantErr }
		got, err := service.Resume(t.Context(), job.ID.String())
		if !errors.Is(err, wantErr) || got.Phase != model.JobPhasePaused {
			t.Fatalf("Resume(platform failure) = (%+v, %v)", got, err)
		}
	})

	for _, test := range []struct {
		name      string
		terminate func(platform.ProcessIdentity, bool) error
		alive     func(platform.ProcessIdentity) (bool, error)
		want      error
	}{
		{
			name: "graceful termination", want: errors.New("terminate failed"),
			terminate: func(platform.ProcessIdentity, bool) error { return errors.New("terminate failed") },
		},
		{
			name: "liveness recheck", want: errors.New("alive failed"),
			terminate: func(platform.ProcessIdentity, bool) error { return nil },
			alive:     func(platform.ProcessIdentity) (bool, error) { return false, errors.New("alive failed") },
		},
		{
			name: "forced termination", want: errors.New("force failed"),
			terminate: func(_ platform.ProcessIdentity, force bool) error {
				if force {
					return errors.New("force failed")
				}
				return nil
			},
			alive: func(platform.ProcessIdentity) (bool, error) { return true, nil },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, _, job, _, _ := newLiveInputService(t)
			service.processTerminate = test.terminate
			service.processAlive = test.alive
			if _, err := service.Cancel(t.Context(), job.ID.String()); err == nil || err.Error() == "" {
				t.Fatalf("Cancel(%s) error = %v", test.name, err)
			}
		})
	}

	t.Run("force disabled", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputServiceWithStopPolicy(t, model.StopPolicy{})
		service.processTerminate = func(platform.ProcessIdentity, bool) error { return nil }
		if got, err := service.Cancel(t.Context(), job.ID.String()); err != nil || got.Phase != model.JobPhaseStopping {
			t.Fatalf("Cancel(force disabled) = (%+v, %v)", got, err)
		}
	})

	t.Run("paused resume failure", func(t *testing.T) {
		t.Parallel()
		service, _, job, _, _ := newLiveInputService(t)
		wantErr := errors.New("resume before cancel failed")
		service.pauseResumeSupported = func() bool { return true }
		service.processPause = func(platform.ProcessIdentity) error { return nil }
		if _, err := service.Pause(t.Context(), job.ID.String()); err != nil {
			t.Fatal(err)
		}
		service.processResume = func(platform.ProcessIdentity) error { return wantErr }
		if _, err := service.Cancel(t.Context(), job.ID.String()); !errors.Is(err, wantErr) {
			t.Fatalf("Cancel(paused resume failure) error = %v", err)
		}
	})
}

func TestServiceStaleOwnershipProcessResults(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{Executable: "true", WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.Add(20 * time.Second)
	service.processAlive = func(platform.ProcessIdentity) (bool, error) { return true, nil }
	if changed, err := service.reconcileStaleOwnership(t.Context(), job, nil); err != nil || changed {
		t.Fatalf("reconcileStaleOwnership(alive) = (%t, %v)", changed, err)
	}
	wantErr := errors.New("liveness failed")
	service.processAlive = func(platform.ProcessIdentity) (bool, error) { return false, wantErr }
	if changed, err := service.reconcileStaleOwnership(t.Context(), job, nil); !errors.Is(err, wantErr) || changed {
		t.Fatalf("reconcileStaleOwnership(liveness failure) = (%t, %v)", changed, err)
	}

	missingSupervisor, err := service.ids.NewSupervisorID()
	if err != nil {
		t.Fatal(err)
	}
	job.SupervisorID = missingSupervisor
	if changed, err := service.reconcileStaleOwnership(t.Context(), job, nil); !errors.Is(err, ErrNotFound) || changed {
		t.Fatalf("reconcileStaleOwnership(missing supervisor) = (%t, %v)", changed, err)
	}
}

func TestWaitForExitWithInjectedLiveness(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		alive func(platform.ProcessIdentity) (bool, error)
		want  error
	}{
		{name: "exited", alive: func(platform.ProcessIdentity) (bool, error) { return false, nil }},
		{name: "identity changed", alive: func(platform.ProcessIdentity) (bool, error) {
			return false, platform.ErrIdentityMismatch
		}},
		{name: "inspection failure", want: errors.New("inspect failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			alive := test.alive
			if alive == nil {
				alive = func(platform.ProcessIdentity) (bool, error) { return false, test.want }
			}
			err := waitForExitWithAlive(t.Context(), platform.ProcessIdentity{}, time.Second, alive)
			if test.want == nil && err != nil {
				t.Fatalf("waitForExitWithAlive() error = %v", err)
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("waitForExitWithAlive() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestInspectReportsComponentQueryFailures(t *testing.T) {
	t.Parallel()

	for _, table := range []string{
		"job_runtime",
		"job_dependencies",
		"wait_evaluations",
		"admissions",
		"notification_deliveries",
		"notification_attempts",
	} {
		t.Run(table, func(t *testing.T) {
			t.Parallel()
			service, _ := newTestService(t)
			job, err := service.Submit(t.Context(), SubmitRequest{
				Executable: "true", WorkingDirectory: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			database, err := sql.Open("sqlite", service.store.DatabasePath())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			if _, err := database.ExecContext(
				t.Context(), "ALTER TABLE "+table+" RENAME TO broken_"+table,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Inspect(t.Context(), job.ID.String()); err == nil {
				t.Fatalf("Inspect() error = nil after renaming %s", table)
			}
		})
	}
}
