package app

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
	"github.com/ryancswallace/jobman/internal/supervisor"
)

func TestServiceSubmitInspectAndCancel(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{
		Name:             "example",
		Executable:       "printf",
		Arguments:        []string{"%s", "a b"},
		WorkingDirectory: t.TempDir(),
		Environment:      map[string]string{"EXAMPLE": "value"},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if job.Phase != model.JobPhaseStarting || !job.SupervisorID.Valid() {
		t.Fatalf("Submit() job = %+v, want claimed starting job", job)
	}

	details, err := service.Inspect(t.Context(), job.ID.String()[:8])
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if details.Job.ID != job.ID || len(details.Runs) != 0 ||
		details.Job.Spec.Environment()["EXAMPLE"] != "value" {
		t.Fatalf("Inspect() = %+v, want submitted specification and no runs", details)
	}

	clock.now = clock.now.Add(2 * time.Second)
	canceled, err := service.Cancel(t.Context(), job.ID.String())
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if canceled.Phase != model.JobPhaseStopping || canceled.Cancellation == nil {
		t.Fatalf("Cancel() job = %+v, want durable stopping intent", canceled)
	}
	repeated, err := service.Cancel(t.Context(), job.ID.String())
	if err != nil {
		t.Fatalf("repeated Cancel() error = %v", err)
	}
	if repeated.Revision != canceled.Revision {
		t.Fatalf("repeated Cancel() revision = %d, want idempotent %d", repeated.Revision, canceled.Revision)
	}
}

func TestServiceSubmitPreservesExplicitZeroStopPolicy(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	job, err := service.Submit(t.Context(), SubmitRequest{
		Executable:       "true",
		WorkingDirectory: t.TempDir(),
		StopPolicy:       model.StopPolicy{},
		StopPolicySet:    true,
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got := job.Spec.StopPolicy(); got != (model.StopPolicy{}) {
		t.Fatalf("StopPolicy() = %#v, want explicit zero policy", got)
	}
}

func TestServiceSubmitCoalescesEquivalentDependencies(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	predecessor, err := service.Submit(t.Context(), SubmitRequest{
		Executable: "true", WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Submit(predecessor) error = %v", err)
	}
	job, err := service.Submit(t.Context(), SubmitRequest{
		Executable: "true", WorkingDirectory: t.TempDir(),
		Dependencies: []DependencyRequest{
			{Selector: predecessor.ID.String(), Predicate: string(store.DependencySuccess)},
			{Selector: predecessor.ID.String(), Predicate: string(store.DependencySuccess)},
		},
	})
	if err != nil {
		t.Fatalf("Submit(dependent) error = %v", err)
	}
	details, err := service.Inspect(t.Context(), job.ID.String())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if len(details.Dependencies) != 1 || len(details.Job.Spec.ExecutionPolicy().Dependencies) != 1 {
		t.Fatalf("coalesced dependencies = store %#v / spec %#v", details.Dependencies,
			details.Job.Spec.ExecutionPolicy().Dependencies)
	}
	if _, err := service.Submit(t.Context(), SubmitRequest{
		Executable: "true", WorkingDirectory: t.TempDir(),
		Dependencies: []DependencyRequest{
			{Selector: predecessor.ID.String(), Predicate: string(store.DependencySuccess)},
			{Selector: predecessor.ID.String(), Predicate: string(store.DependencyFailed)},
		},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Submit(contradictory dependency) error = %v, want ErrConflict", err)
	}
}

func TestServiceSelectorErrorsAndLogsWithoutRun(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	for range 2 {
		if _, err := service.Submit(t.Context(), SubmitRequest{
			Name:             "duplicate",
			Executable:       "true",
			WorkingDirectory: t.TempDir(),
		}); err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
	}

	if _, err := service.Inspect(t.Context(), "duplicate"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("Inspect(ambiguous) error = %v, want ErrAmbiguous", err)
	}
	if _, err := service.Inspect(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Inspect(missing) error = %v, want ErrNotFound", err)
	}
	jobs, err := service.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("List() count = %d, want 2", len(jobs))
	}
	if _, err := service.ReadLogs(t.Context(), jobs[0].ID.String(), LogBoth); !errors.Is(err, ErrConflict) {
		t.Fatalf("ReadLogs(no run) error = %v, want ErrConflict", err)
	}
}

func TestServiceCleanPersistsPrunedLogMetadata(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, run, paths := completeCapturedRun(t, service, clock)
	result, err := service.Clean(t.Context(), CleanRequest{Selector: job.ID.String()})
	if err != nil {
		t.Fatalf("Clean() error = %v", err)
	}
	if result.Runs != 1 || result.Files == 0 || result.Bytes == 0 {
		t.Fatalf("Clean() result = %+v, want one nonempty removed log set", result)
	}
	if _, statErr := os.Stat(paths.Directory); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cleaned log directory error = %v, want not exist", statErr)
	}

	details, err := service.Inspect(t.Context(), job.ID.String())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if len(details.Runs) != 1 || details.Runs[0].ID != run.ID {
		t.Fatalf("Inspect() runs = %+v, want run %s", details.Runs, run.ID)
	}
	logs := details.Runs[0].Logs
	if logs.Available() || logs.PrunedAt == nil {
		t.Fatalf("Inspect() logs = %+v, want durable pruning metadata", logs)
	}
	if logs.PrunedFiles != result.Files || logs.PrunedBytes != result.Bytes {
		t.Errorf("Inspect() removed counts = %d/%d, want %d/%d", logs.PrunedFiles, logs.PrunedBytes, result.Files, result.Bytes)
	}
	if _, readErr := service.ReadRunLogs(
		t.Context(), job.ID.String(), LogBoth, run.Number,
	); !errors.Is(readErr, ErrNotFound) {
		t.Fatalf("ReadRunLogs(pruned) error = %v, want ErrNotFound", readErr)
	}
	repeated, err := service.Clean(t.Context(), CleanRequest{Selector: job.ID.String()})
	if err != nil {
		t.Fatalf("repeated Clean() error = %v", err)
	}
	if repeated != (CleanResult{}) {
		t.Errorf("repeated Clean() result = %+v, want no already-pruned runs", repeated)
	}
}

func TestServiceCleanDoesNotRecordFailedFilesystemRemoval(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, run, paths := completeCapturedRun(t, service, clock)
	if err := os.WriteFile(filepath.Join(paths.Directory, "unexpected"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("write unexpected log entry: %v", err)
	}
	if _, err := service.Clean(t.Context(), CleanRequest{Selector: job.ID.String()}); err == nil {
		t.Fatal("Clean() error = nil, want unsafe-content failure")
	}
	persisted, err := service.store.GetRun(t.Context(), run.ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if !persisted.Logs.Available() || persisted.Logs.PrunedAt != nil {
		t.Fatalf("failed cleanup metadata = %+v, want logs still available", persisted.Logs)
	}
}

func TestServiceCleanResumesFilesystemRemovalBeforeMetadataCommit(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	job, run, paths := completeCapturedRun(t, service, clock)
	removed, err := logstore.CleanupRun(
		t.Context(),
		service.stateDir,
		job.ID.String(),
		run.Number,
		func(context.Context) (bool, error) { return true, nil },
	)
	if err != nil {
		t.Fatalf("simulate cleanup crash boundary: %v", err)
	}
	result, err := service.Clean(t.Context(), CleanRequest{Selector: job.ID.String()})
	if err != nil {
		t.Fatalf("Clean(resume) error = %v", err)
	}
	if result.Runs != 1 || result.Files != removed.Files || result.Bytes != removed.Bytes {
		t.Fatalf("Clean(resume) = %+v, want prior result %+v", result, removed)
	}
	persisted, err := service.store.GetRun(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Logs.Available() || persisted.Logs.PrunedAt == nil {
		t.Fatalf("resumed cleanup metadata = %+v", persisted.Logs)
	}
	if _, err := os.Stat(paths.Directory + ".deleting"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup handoff directory error = %v, want not exist", err)
	}
}

func TestServiceRejectsInvalidAdmissionBeforeJobBecomesVisible(t *testing.T) {
	t.Parallel()

	service, clock := newTestService(t)
	globalCapacity := uint64(1)
	if err := service.store.SetConcurrencyLimit(
		t.Context(), "", &globalCapacity, clock.now,
	); err != nil {
		t.Fatalf("SetConcurrencyLimit(global) error = %v", err)
	}
	launches := 0
	service.launch = func(
		context.Context,
		supervisor.LaunchOptions,
	) (supervisor.Acknowledgement, error) {
		launches++

		return supervisor.Acknowledgement{}, errors.New("launch must not be reached")
	}

	for _, test := range []struct {
		name        string
		concurrency model.ConcurrencyPolicy
	}{
		{
			name:        "unknown pool",
			concurrency: model.ConcurrencyPolicy{Pool: "not-configured", Slots: 1},
		},
		{
			name:        "request exceeds finite global limit",
			concurrency: model.ConcurrencyPolicy{Slots: 2},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			configuration := model.DefaultExecutionPolicy()
			configuration.Concurrency = test.concurrency
			_, err := service.Submit(t.Context(), SubmitRequest{
				Executable:       "true",
				WorkingDirectory: t.TempDir(),
				ExecutionPolicy:  configuration,
			})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("Submit() error = %v, want ErrConflict", err)
			}
		})
	}
	if launches != 0 {
		t.Fatalf("supervisor launches after rejected admissions = %d, want 0", launches)
	}
	jobs, err := service.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs after rejected admissions = %#v, want none", jobs)
	}
}

func TestServiceReconcilesExpiredSubmissionAndStaleSupervisor(t *testing.T) {
	t.Parallel()

	t.Run("expired submission", func(t *testing.T) {
		t.Parallel()

		service, clock := newTestService(t)
		service.launch = func(
			context.Context,
			supervisor.LaunchOptions,
		) (supervisor.Acknowledgement, error) {
			return supervisor.Acknowledgement{}, errors.New("injected launch failure")
		}
		_, err := service.Submit(t.Context(), SubmitRequest{
			Executable:       "true",
			WorkingDirectory: t.TempDir(),
		})
		if err == nil {
			t.Fatal("Submit() unexpectedly succeeded")
		}
		clock.now = clock.now.Add(defaultClaimWindow + time.Second)
		jobs, err := service.List(t.Context())
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(jobs) != 1 || jobs[0].Outcome != model.JobOutcomeSubmissionFailed {
			t.Fatalf("List() jobs = %+v, want reconciled submission failure", jobs)
		}
	})

	t.Run("stale supervisor", func(t *testing.T) {
		t.Parallel()

		service, clock := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable:       "true",
			WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		clock.now = clock.now.Add(20 * time.Second)
		details, err := service.Inspect(t.Context(), job.ID.String())
		if err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}
		if details.Job.Outcome != model.JobOutcomeLost || details.Job.Phase != model.JobPhaseCompleted {
			t.Fatalf("Inspect() job = %+v, want reconciled lost ownership", details.Job)
		}
	})

	t.Run("wait reconciles stale supervisor", func(t *testing.T) {
		t.Parallel()

		service, clock := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		clock.now = clock.now.Add(20 * time.Second)
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		completed, err := service.Wait(ctx, job.ID.String())
		if err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
		if completed.Outcome != model.JobOutcomeLost {
			t.Fatalf("Wait() job = %#v, want lost", completed)
		}
	})

	t.Run("cancel reconciles stale supervisor", func(t *testing.T) {
		t.Parallel()

		service, clock := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		clock.now = clock.now.Add(20 * time.Second)
		completed, err := service.Cancel(t.Context(), job.ID.String())
		if err != nil {
			t.Fatalf("Cancel() error = %v", err)
		}
		if completed.Outcome != model.JobOutcomeLost {
			t.Fatalf("Cancel() job = %#v, want lost", completed)
		}
	})
}

type testClock struct {
	now time.Time
}

func completeCapturedRun(
	t *testing.T,
	service *Service,
	clock *testClock,
) (model.JobState, model.RunState, logstore.Paths) {
	t.Helper()

	job, err := service.Submit(t.Context(), SubmitRequest{
		Executable:       "missing-test-executable",
		WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	runID, err := service.ids.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	capture, err := logstore.CreateRun(service.stateDir, job.ID.String(), 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	paths := capture.Paths()
	pending := model.LogMetadata{
		StdoutPath:      paths.Stdout,
		StderrPath:      paths.Stderr,
		IndexPath:       paths.Index,
		IndexVersion:    capture.IndexVersion(),
		Integrity:       model.LogIntegrityPending,
		RecordingHealth: model.RecordingHealthy,
	}
	reservedAt := clock.now.Add(2 * time.Millisecond)
	if _, reserveErr := service.store.ReserveRun(t.Context(), job.ID, runID, 1, pending, reservedAt); reserveErr != nil {
		t.Fatalf("ReserveRun() error = %v", reserveErr)
	}
	if _, appendErr := capture.Append(logstore.Stdout, []byte("captured\n"), reservedAt); appendErr != nil {
		t.Fatalf("Append() error = %v", appendErr)
	}
	if closeErr := capture.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	stdout, err := os.Stat(paths.Stdout)
	if err != nil {
		t.Fatalf("stat stdout log: %v", err)
	}
	stderr, err := os.Stat(paths.Stderr)
	if err != nil {
		t.Fatalf("stat stderr log: %v", err)
	}
	finalLogs := pending
	finalLogs.StdoutSize = stdout.Size()
	finalLogs.StderrSize = stderr.Size()
	finalLogs.Integrity = model.LogIntegrityValid
	completedAt := clock.now.Add(4 * time.Millisecond)
	completed, err := service.store.MarkStartFailed(
		t.Context(), job.ID, runID, finalLogs, "test_start_failure", completedAt,
	)
	if err != nil {
		t.Fatalf("MarkStartFailed() error = %v", err)
	}
	clock.now = completedAt.Add(time.Millisecond)

	return completed.Job, *completed.Run, paths
}

func newTestService(t *testing.T) (*Service, *testClock) {
	t.Helper()

	clock := &testClock{now: time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)}
	identifiers, err := model.NewUUIDv7Generator(func() time.Time { return clock.now }, rand.Reader)
	if err != nil {
		t.Fatalf("NewUUIDv7Generator() error = %v", err)
	}
	stateDir := filepath.Join(t.TempDir(), "state")
	database, err := store.Open(t.Context(), store.Options{
		StateDir:      stateDir,
		JobmanVersion: "test",
		Now:           func() time.Time { return clock.now },
		EventIDs:      identifiers,
	})
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			t.Errorf("Store.Close() error = %v", closeErr)
		}
	})

	service := &Service{
		store:      database,
		stateDir:   stateDir,
		executable: "jobman-test",
		now:        func() time.Time { return clock.now },
		random:     rand.Reader,
		ids:        identifiers,
	}
	service.launch = func(
		ctx context.Context,
		options supervisor.LaunchOptions,
	) (supervisor.Acknowledgement, error) {
		supervisorID, idErr := identifiers.NewSupervisorID()
		if idErr != nil {
			return supervisor.Acknowledgement{}, idErr
		}
		claimedAt := clock.now.Add(time.Millisecond)
		_, claimErr := options.Store.Claim(
			ctx,
			options.JobID,
			options.Credential,
			supervisorID,
			model.ProcessIdentity{
				PID:        1234,
				Platform:   runtime.GOOS,
				CreationID: "test-creation",
				BootID:     "test-boot",
				TreeID:     "test-tree",
			},
			claimedAt,
			claimedAt.Add(15*time.Second),
		)
		if claimErr != nil {
			return supervisor.Acknowledgement{}, claimErr
		}

		return supervisor.Acknowledgement{
			SchemaVersion: 1,
			JobID:         options.JobID,
			SupervisorID:  supervisorID,
		}, nil
	}

	return service, clock
}
