package app

import (
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
}

type testClock struct {
	now time.Time
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
