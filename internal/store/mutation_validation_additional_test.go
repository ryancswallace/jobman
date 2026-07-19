package store

import (
	"math"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestPersistenceValueEncodersRejectInvalidStates(t *testing.T) {
	t.Parallel()

	job, _, _ := persistenceFixtures(t)
	invalidJob := job
	invalidJob.Spec = model.JobSpec{}
	if _, err := jobValues(invalidJob); err == nil {
		t.Fatal("jobValues() accepted an invalid specification")
	}
	invalidJob = job
	invalidJob.Revision = math.MaxUint64
	if _, err := jobValues(invalidJob); err == nil {
		t.Fatal("jobValues() accepted an unrepresentable revision")
	}

	_, run, supervisor := persistenceFixtures(t)
	invalidRun := run
	invalidRun.Number = math.MaxUint64
	if _, err := runValues(invalidRun); err == nil {
		t.Fatal("runValues() accepted an unrepresentable number")
	}
	invalidRun = run
	invalidRun.Revision = math.MaxUint64
	if _, err := runValues(invalidRun); err == nil {
		t.Fatal("runValues() accepted an unrepresentable revision")
	}
	invalidSupervisor := supervisor
	invalidSupervisor.Revision = math.MaxUint64
	if _, err := supervisorValues(invalidSupervisor); err == nil {
		t.Fatal("supervisorValues() accepted an unrepresentable revision")
	}

	invalidRun.Phase = "invalid"
	invalidResult := model.TransitionResult{Job: job, Run: &invalidRun}
	if err := validateTransition(invalidResult); err == nil {
		t.Fatal("validateTransition() accepted an invalid run")
	}
	invalidSupervisor.Process.PID = -1
	invalidResult = model.TransitionResult{Job: job, Supervisor: &invalidSupervisor}
	if err := validateTransition(invalidResult); err == nil {
		t.Fatal("validateTransition() accepted an invalid supervisor")
	}
}

func persistenceFixtures(t *testing.T) (model.JobState, model.RunState, model.SupervisorState) {
	t.Helper()
	database, jobID, runID, _, _ := runningRuntimeFixture(t, 0x16300)
	job, err := database.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := database.GetRun(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	supervisor, err := database.GetSupervisor(t.Context(), job.SupervisorID)
	if err != nil {
		t.Fatal(err)
	}

	return job, run, supervisor
}

func TestStoreTransitionModelFailureBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("cancellation time", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x16400)
		if _, err := database.RequestCancellation(t.Context(), jobID, now.Add(-time.Second)); err == nil {
			t.Fatal("RequestCancellation() accepted an early timestamp")
		}
	})

	t.Run("cancellation finalization", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x16500)
		if _, err := database.FinalizeCancellationWithoutRun(t.Context(), jobID, now); err == nil {
			t.Fatal("FinalizeCancellationWithoutRun() accepted a job without cancellation intent")
		}
	})

	t.Run("terminal supervisor unavailable", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x16600)
		if _, err := database.RequestCancellation(t.Context(), jobID, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE supervisors RENAME TO unavailable_supervisors`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.FinalizeCancellationWithoutRun(t.Context(), jobID, now.Add(2*time.Second)); err == nil {
			t.Fatal("FinalizeCancellationWithoutRun() error = nil")
		}
	})

	t.Run("submission deadline", func(t *testing.T) {
		t.Parallel()
		database := openTestStore(t, "submission-model-failure", newSequentialEventIDs(0x16700))
		jobID := mustJobID(t, 0x16701, 1)
		now := storeTestTime()
		submitRuntimeJob(t, database, jobID, now)
		if _, err := database.MarkSubmissionFailed(t.Context(), jobID, "expired", now); err == nil {
			t.Fatal("MarkSubmissionFailed() accepted an unexpired claim")
		}
	})

	t.Run("lease timestamps", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x16800)
		job, err := database.GetJob(t.Context(), jobID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.RenewLease(t.Context(), job.SupervisorID, now.Add(-time.Second), now.Add(time.Minute)); err == nil {
			t.Fatal("RenewLease() accepted a backward timestamp")
		}
		if _, err := database.ReleaseSupervisor(t.Context(), job.SupervisorID, now.Add(-time.Second)); err == nil {
			t.Fatal("ReleaseSupervisor() accepted an early timestamp")
		}
	})
}
