package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/liveinput"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/store"
)

func TestServiceReadSideFailureBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("doctor health", func(t *testing.T) {
		service, _ := newTestService(t)
		if err := service.store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Doctor(t.Context(), DoctorRequest{}); err == nil {
			t.Fatal("Doctor(closed store) error = nil")
		}
	})

	t.Run("doctor notification recovery", func(t *testing.T) {
		service, _ := newTestService(t)
		raw := openAppRawDatabase(t, service)
		if _, err := raw.ExecContext(
			t.Context(),
			`ALTER TABLE notification_deliveries RENAME TO unavailable_notification_deliveries`,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Doctor(t.Context(), DoctorRequest{Repair: true}); err == nil {
			t.Fatal("Doctor(unavailable notification queue) error = nil")
		}
	})

	t.Run("list reconciliation", func(t *testing.T) {
		service, clock := newTestService(t)
		if _, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		}); err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Minute)
		wantErr := errors.New("identity inspection failed")
		service.processAlive = func(platform.ProcessIdentity) (bool, error) { return false, wantErr }
		if _, err := service.ListJobs(t.Context(), ListRequest{Limit: 10}); !errors.Is(err, wantErr) {
			t.Fatalf("ListJobs(reconciliation failure) error = %v", err)
		}
	})

	t.Run("post-reconciliation list", func(t *testing.T) {
		service, clock := newTestService(t)
		if _, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		}); err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Minute)
		service.processAlive = func(platform.ProcessIdentity) (bool, error) {
			_ = service.store.Close()

			return true, nil
		}
		if _, err := service.ListJobs(t.Context(), ListRequest{Limit: 10}); err == nil {
			t.Fatal("ListJobs(store closed during reconciliation) error = nil")
		}
	})

	t.Run("inspect reconciliation", func(t *testing.T) {
		service, clock := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Minute)
		wantErr := errors.New("identity inspection failed")
		service.processAlive = func(platform.ProcessIdentity) (bool, error) { return false, wantErr }
		if _, err := service.Inspect(t.Context(), job.ID.String()); !errors.Is(err, wantErr) {
			t.Fatalf("Inspect(reconciliation failure) error = %v", err)
		}
	})

	t.Run("cancel reconciliation", func(t *testing.T) {
		service, clock := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = clock.now.Add(time.Minute)
		wantErr := errors.New("identity inspection failed")
		service.processAlive = func(platform.ProcessIdentity) (bool, error) { return false, wantErr }
		if _, err := service.Cancel(t.Context(), job.ID.String()); !errors.Is(err, wantErr) {
			t.Fatalf("Cancel(reconciliation failure) error = %v", err)
		}
	})
}

func TestServiceMutationAndInspectionBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("submit persistence", func(t *testing.T) {
		service, _ := newTestService(t)
		service.random = &closeStoreReader{store: service.store}
		if _, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		}); err == nil {
			t.Fatal("Submit(store closed after credential generation) error = nil")
		}
	})

	t.Run("admission inspection", func(t *testing.T) {
		service, clock := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, acquireErr := service.store.TryAcquireAdmission(
			t.Context(), job.ID, "", 1, clock.now.Add(time.Second), time.Minute,
		); acquireErr != nil {
			t.Fatal(acquireErr)
		}
		details, err := service.Inspect(t.Context(), job.ID.String())
		if err != nil || details.Admission == nil || details.Admission.JobID != job.ID {
			t.Fatalf("Inspect(admitted job) = (%+v, %v)", details, err)
		}
	})

	t.Run("rerun dependencies", func(t *testing.T) {
		service, _ := newTestService(t)
		dependency, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		source, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
			Dependencies: []DependencyRequest{{
				Selector: dependency.ID.String(), Predicate: string(store.DependencyFinish),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		clone, err := service.Rerun(t.Context(), source.ID.String(), RerunRequest{})
		if err != nil {
			t.Fatal(err)
		}
		details, err := service.Inspect(t.Context(), clone.ID.String())
		if err != nil || len(details.Dependencies) != 1 || details.Dependencies[0].DependsOn != dependency.ID {
			t.Fatalf("rerun dependencies = (%+v, %v)", details.Dependencies, err)
		}
	})

	t.Run("active resume unsupported", func(t *testing.T) {
		service, _, job, _, _ := newLiveInputService(t)
		service.pauseResumeSupported = func() bool { return false }
		if _, err := service.Resume(t.Context(), job.ID.String()); !errors.Is(err, platform.ErrUnsupported) {
			t.Fatalf("Resume(unsupported active target) error = %v", err)
		}
	})
}

func TestServiceLiveInputAdditionalFailureBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("post-delivery verification", func(t *testing.T) {
		service, _, job, _, _ := newLiveInputService(t)
		service.sendInput = func(
			_ context.Context,
			_ string,
			_ string,
			source io.Reader,
			_ bool,
		) (liveinput.Result, error) {
			contents, err := io.ReadAll(source)
			if err != nil {
				return liveinput.Result{}, err
			}
			_ = service.store.Close()

			return liveinput.Result{Delivered: uint64(len(contents))}, nil
		}
		if _, err := service.SendInput(
			t.Context(), job.ID.String(), bytes.NewReader([]byte("payload")), false,
		); err == nil {
			t.Fatal("SendInput(store closed after delivery) error = nil")
		}
	})

	t.Run("runtime unavailable", func(t *testing.T) {
		service, _ := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(), StdinPolicy: model.StdinLive,
		})
		if err != nil {
			t.Fatal(err)
		}
		raw := openAppRawDatabase(t, service)
		if _, err := raw.ExecContext(t.Context(), `ALTER TABLE job_runtime RENAME TO unavailable_job_runtime`); err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.waitForInputTarget(t.Context(), job.ID); err == nil {
			t.Fatal("waitForInputTarget(unavailable runtime) error = nil")
		}
	})
}

func TestFiniteRetentionAndTerminationDeadline(t *testing.T) {
	t.Parallel()

	age, err := config.NewDurationLimit(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	policy := retentionPlanPolicy(config.Retention{CompletedLogMaxAge: age})
	if policy.MaxAge.Unlimited || policy.MaxAge.Maximum != 24*time.Hour {
		t.Fatalf("finite retention age = %+v", policy.MaxAge)
	}

	if err := waitForExitWithAlive(
		t.Context(),
		platform.ProcessIdentity{},
		time.Millisecond,
		func(platform.ProcessIdentity) (bool, error) { return true, nil },
	); err != nil {
		t.Fatalf("waitForExitWithAlive(deadline) error = %v", err)
	}
}

func TestServiceBlockingAndReconciliationFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		operation func(context.Context, *Service, model.JobID) error
	}{
		{
			name: "doctor",
			operation: func(ctx context.Context, service *Service, _ model.JobID) error {
				_, err := service.Doctor(ctx, DoctorRequest{Repair: true})
				return err
			},
		},
		{
			name: "list",
			operation: func(ctx context.Context, service *Service, _ model.JobID) error {
				_, err := service.List(ctx)
				return err
			},
		},
		{
			name: "wait",
			operation: func(ctx context.Context, service *Service, jobID model.JobID) error {
				_, err := service.Wait(ctx, jobID.String())
				return err
			},
		},
	} {
		t.Run(test.name+" stale-owner inspection", func(t *testing.T) {
			t.Parallel()

			service, clock := newTestService(t)
			job, err := service.Submit(t.Context(), SubmitRequest{
				Executable: "true", WorkingDirectory: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			clock.now = clock.now.Add(time.Minute)
			wantErr := errors.New("identity inspection failed")
			service.processAlive = func(platform.ProcessIdentity) (bool, error) { return false, wantErr }
			if err := test.operation(t.Context(), service, job.ID); !errors.Is(err, wantErr) {
				t.Fatalf("operation error = %v, want %v", err, wantErr)
			}
		})
	}

	t.Run("wait cancellation", func(t *testing.T) {
		t.Parallel()

		service, _ := newTestService(t)
		job, err := service.Submit(t.Context(), SubmitRequest{
			Executable: "true", WorkingDirectory: t.TempDir(),
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := service.Wait(ctx, job.ID.String()); !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait(canceled context) error = %v", err)
		}
	})

	t.Run("termination grace liveness", func(t *testing.T) {
		t.Parallel()

		service, _, job, _, _ := newLiveInputServiceWithStopPolicy(t, model.StopPolicy{
			GracePeriod: time.Second, ForceAfterGrace: true,
		})
		wantErr := errors.New("liveness failed during grace period")
		service.processTerminate = func(platform.ProcessIdentity, bool) error { return nil }
		service.processAlive = func(platform.ProcessIdentity) (bool, error) { return false, wantErr }
		if _, err := service.Cancel(t.Context(), job.ID.String()); !errors.Is(err, wantErr) {
			t.Fatalf("Cancel(grace-period liveness failure) error = %v", err)
		}
	})
}

func TestServiceReadLogsPropagatesCorruptIndex(t *testing.T) {
	t.Parallel()

	service, _, job, _, capture := newLiveInputService(t)
	paths := capture.Paths()
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
	// One complete v1/v2 index record is 52 bytes. An all-zero record is a
	// complete corrupt record rather than an accepted torn tail.
	if err := os.WriteFile(paths.Index, make([]byte, 52), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadLogs(t.Context(), job.ID.String(), LogBoth); err == nil {
		t.Fatal("ReadLogs(corrupt index) error = nil")
	}
}

type closeStoreReader struct {
	store *store.Store
}

func (reader *closeStoreReader) Read(destination []byte) (int, error) {
	for index := range destination {
		destination[index] = byte(index)
	}
	_ = reader.store.Close()

	return len(destination), nil
}

var _ io.Reader = (*closeStoreReader)(nil)
