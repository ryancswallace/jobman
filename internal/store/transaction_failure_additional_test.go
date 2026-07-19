package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func installAbortTrigger(t *testing.T, database *Store, name, timing, table string) {
	t.Helper()
	database.db.SetMaxOpenConns(1)
	statement := fmt.Sprintf(
		`CREATE TEMP TRIGGER %s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, 'injected failure'); END`,
		name, timing, table,
	)
	if _, err := database.db.ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeTransitionsRollbackInjectedFailures(t *testing.T) {
	t.Parallel()

	t.Run("move job runtime", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x13000)
		installAbortTrigger(t, database, "fail_move_runtime", "UPDATE", "job_runtime")
		if _, err := database.MoveJob(t.Context(), jobID, model.JobPhaseQueued, now.Add(time.Second), "queue"); err == nil {
			t.Fatal("MoveJob() error = nil")
		}
		assertJobPhase(t, database, jobID, model.JobPhaseStarting)
	})

	t.Run("complete without run runtime", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x13100)
		installAbortTrigger(t, database, "fail_complete_runtime", "UPDATE", "job_runtime")
		if _, err := database.CompleteWithoutRun(t.Context(), jobID, model.JobOutcomeAborted, "failed", now.Add(time.Second)); err == nil {
			t.Fatal("CompleteWithoutRun() error = nil")
		}
		assertJobPhase(t, database, jobID, model.JobPhaseStarting)
	})

	t.Run("pause runtime", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x13200)
		if _, err := database.MoveJob(t.Context(), jobID, model.JobPhaseQueued, now.Add(time.Second), "queue"); err != nil {
			t.Fatal(err)
		}
		installAbortTrigger(t, database, "fail_pause_runtime", "UPDATE", "job_runtime")
		if _, err := database.Pause(t.Context(), jobID, now.Add(2*time.Second)); err == nil {
			t.Fatal("Pause() error = nil")
		}
		assertJobPhase(t, database, jobID, model.JobPhaseQueued)
	})

	t.Run("resume runtime", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x13300)
		if _, err := database.MoveJob(t.Context(), jobID, model.JobPhaseQueued, now.Add(time.Second), "queue"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Pause(t.Context(), jobID, now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		installAbortTrigger(t, database, "fail_resume_runtime", "UPDATE", "job_runtime")
		if _, err := database.Resume(t.Context(), jobID, now.Add(3*time.Second)); err == nil {
			t.Fatal("Resume() error = nil")
		}
		assertJobPhase(t, database, jobID, model.JobPhasePaused)
	})
}

func TestDirectRuntimeMutationsPropagateInjectedFailures(t *testing.T) {
	t.Parallel()

	for index, operation := range []struct {
		name string
		run  func(*Store, model.JobID, time.Time) error
	}{
		{name: "prerequisites", run: func(database *Store, jobID model.JobID, now time.Time) error {
			return database.MarkPrerequisitesSatisfied(t.Context(), jobID, now)
		}},
		{name: "input endpoint", run: func(database *Store, jobID model.JobID, now time.Time) error {
			return database.SetInputEndpoint(t.Context(), jobID, "/tmp/input.sock", now)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			database := openTestStore(t, "direct-runtime-failure", newSequentialEventIDs(uint64(0x13400+index*0x10)))
			jobID := mustJobID(t, uint64(0x13401+index*0x10), 1)
			now := storeTestTime()
			submitRuntimeJob(t, database, jobID, now)
			installAbortTrigger(t, database, fmt.Sprintf("fail_direct_runtime_%d", index), "UPDATE", "job_runtime")
			if err := operation.run(database, jobID, now.Add(time.Second)); err == nil {
				t.Fatal("operation error = nil")
			}
		})
	}
}

func TestAdmissionMutationsPropagateInjectedFailures(t *testing.T) {
	t.Parallel()

	t.Run("set limit", func(t *testing.T) {
		t.Parallel()
		database := openTestStore(t, "limit-failure", newSequentialEventIDs(0x13500))
		installAbortTrigger(t, database, "fail_limit", "INSERT", "concurrency_limits")
		capacity := uint64(2)
		if err := database.SetConcurrencyLimit(t.Context(), "build", &capacity, storeTestTime()); err == nil {
			t.Fatal("SetConcurrencyLimit() error = nil")
		}
	})

	t.Run("acquire request", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x13600)
		installAbortTrigger(t, database, "fail_request", "INSERT", "admission_requests")
		if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err == nil {
			t.Fatal("TryAcquireAdmission() error = nil")
		}
	})

	for index, operation := range []struct {
		name string
		run  func(*Store, model.JobID, model.RunID, time.Time) error
	}{
		{name: "bind", run: func(database *Store, jobID model.JobID, runID model.RunID, _ time.Time) error {
			return database.BindAdmissionToRun(t.Context(), jobID, runID)
		}},
		{name: "renew", run: func(database *Store, jobID model.JobID, _ model.RunID, now time.Time) error {
			return database.RenewAdmission(t.Context(), jobID, now, time.Minute)
		}},
		{name: "release", run: func(database *Store, jobID model.JobID, _ model.RunID, now time.Time) error {
			return database.ReleaseAdmission(t.Context(), jobID, now)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			database, jobID, now := claimedRuntimeFixture(t, uint64(0x13700+index*0x10))
			if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err != nil {
				t.Fatal(err)
			}
			installAbortTrigger(t, database, fmt.Sprintf("fail_admission_%d", index), "UPDATE", "admissions")
			if err := operation.run(database, jobID, mustRunID(t, uint64(0x13701+index*0x10)), now); err == nil {
				t.Fatal("operation error = nil")
			}
		})
	}
}

func TestRunCompletionRollsBackEachTransactionalStage(t *testing.T) {
	t.Parallel()

	stages := []struct {
		name, operation, table string
	}{
		{name: "job transition", operation: "UPDATE", table: "jobs"},
		{name: "run transition", operation: "UPDATE", table: "runs"},
		{name: "supervisor release", operation: "UPDATE", table: "supervisors"},
		{name: "admission release", operation: "UPDATE", table: "admissions"},
		{name: "request cleanup", operation: "DELETE", table: "admission_requests"},
	}
	for index, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			t.Parallel()
			database, jobID, runID, logs, now := runningRuntimeFixture(t, uint64(0x13800+index*0x20))
			if _, err := database.db.ExecContext(t.Context(), `
				INSERT INTO admission_requests(job_id, pool_name, slots, enqueued_at_ns)
				VALUES (?, NULL, 1, ?)`, jobID.String(), now.UnixNano()); err != nil {
				t.Fatal(err)
			}
			installAbortTrigger(t, database, fmt.Sprintf("fail_completion_%d", index), stage.operation, stage.table)
			exitCode := 1
			if _, err := database.CompleteRunWithDisposition(
				t.Context(), jobID, runID, model.RunOutcomeFailure,
				&model.ExitInfo{ExitCode: &exitCode, ObservedAt: now.Add(4 * time.Second)},
				logs, "failed", now.Add(4*time.Second),
				model.RunDisposition{TerminalOutcome: model.JobOutcomeFailure, Reason: "run_limit"},
			); err == nil {
				t.Fatal("CompleteRunWithDisposition() error = nil")
			}
			assertJobPhase(t, database, jobID, model.JobPhaseRunning)
		})
	}
}

func TestCompletionWithoutRunRollsBackAdmissionCleanup(t *testing.T) {
	t.Parallel()

	for index, stage := range []struct {
		operation, table string
	}{
		{operation: "UPDATE", table: "admissions"},
		{operation: "DELETE", table: "admission_requests"},
	} {
		t.Run(stage.table, func(t *testing.T) {
			t.Parallel()
			database, jobID, now := claimedRuntimeFixture(t, uint64(0x13900+index*0x20))
			if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(t.Context(), `
				INSERT INTO admission_requests(job_id, pool_name, slots, enqueued_at_ns)
				VALUES (?, NULL, 1, ?)`, jobID.String(), now.UnixNano()); err != nil {
				t.Fatal(err)
			}
			installAbortTrigger(t, database, fmt.Sprintf("fail_without_run_%d", index), stage.operation, stage.table)
			if _, err := database.CompleteWithoutRun(
				t.Context(), jobID, model.JobOutcomeAborted, "failed", now.Add(time.Second),
			); err == nil {
				t.Fatal("CompleteWithoutRun() error = nil")
			}
			assertJobPhase(t, database, jobID, model.JobPhaseStarting)
		})
	}
}

func TestActiveRunLookupFailuresPropagate(t *testing.T) {
	t.Parallel()

	database, jobID, _, _, now := runningRuntimeFixture(t, 0x13a00)
	if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE runs RENAME TO unavailable_runs`); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func() error{
		"cancel": func() error {
			_, err := database.RequestCancellation(t.Context(), jobID, now.Add(4*time.Second))
			return err
		},
		"ownership lost": func() error {
			_, err := database.MarkOwnershipLost(t.Context(), jobID, nil, "lost", now.Add(4*time.Second))
			return err
		},
		"job timeout": func() error {
			_, err := database.RequestTimeout(t.Context(), jobID, now.Add(4*time.Second))
			return err
		},
		"run timeout": func() error {
			_, err := database.RequestRunTimeout(t.Context(), jobID, now.Add(4*time.Second))
			return err
		},
		"pause": func() error {
			_, err := database.Pause(t.Context(), jobID, now.Add(4*time.Second))
			return err
		},
	}
	for name, operation := range operations {
		if err := operation(); err == nil {
			t.Errorf("%s with unavailable runs error = nil", name)
		}
	}
}

func TestSupervisorLookupFailuresPropagateFromTerminalTransitions(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*Store, model.JobID, model.RunID, model.LogMetadata, time.Time) error{
		"start failure": func(database *Store, jobID model.JobID, runID model.RunID, logs model.LogMetadata, now time.Time) error {
			logs.Integrity = model.LogIntegrityPartial
			logs.RecordingHealth = model.RecordingDegraded
			_, err := database.MarkStartFailed(t.Context(), jobID, runID, logs, "start_failed", now.Add(4*time.Second))
			return err
		},
		"finalize run": func(database *Store, jobID model.JobID, runID model.RunID, logs model.LogMetadata, now time.Time) error {
			exitCode := 0
			_, err := database.FinalizeRun(t.Context(), jobID, runID, model.RunOutcomeSuccess,
				&model.ExitInfo{ExitCode: &exitCode, ObservedAt: now.Add(4 * time.Second)}, logs, now.Add(4*time.Second))
			return err
		},
		"complete run policy": func(database *Store, jobID model.JobID, runID model.RunID, logs model.LogMetadata, now time.Time) error {
			exitCode := 0
			_, err := database.CompleteRunWithDisposition(t.Context(), jobID, runID, model.RunOutcomeSuccess,
				&model.ExitInfo{ExitCode: &exitCode, ObservedAt: now.Add(4 * time.Second)}, logs, "", now.Add(4*time.Second),
				model.RunDisposition{TerminalOutcome: model.JobOutcomeSuccess})
			return err
		},
		"ownership lost": func(database *Store, jobID model.JobID, _ model.RunID, logs model.LogMetadata, now time.Time) error {
			_, err := database.MarkOwnershipLost(t.Context(), jobID, &logs, "lost", now.Add(4*time.Second))
			return err
		},
	}
	index := uint64(0)
	for name, operation := range tests {
		name, operation := name, operation
		index++
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database, jobID, runID, logs, now := runningRuntimeFixture(t, 0x13b00+index*0x20)
			if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE supervisors RENAME TO unavailable_supervisors`); err != nil {
				t.Fatal(err)
			}
			if err := operation(database, jobID, runID, logs, now); err == nil {
				t.Fatal("terminal transition with unavailable supervisor error = nil")
			}
		})
	}
}

func TestDependencyMutationDatabaseFailures(t *testing.T) {
	t.Parallel()

	for index, stage := range []struct {
		name, operation string
	}{
		{name: "delete", operation: "DELETE"},
		{name: "insert", operation: "INSERT"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			t.Parallel()
			database := openTestStore(t, "dependency-mutation-failure", newSequentialEventIDs(uint64(0x13c00+index*0x20)))
			now := storeTestTime()
			jobID := mustJobID(t, uint64(0x13c01+index*0x20), 1)
			dependencyID := mustJobID(t, uint64(0x13c02+index*0x20), 1)
			submitRuntimeJob(t, database, jobID, now)
			submitRuntimeJob(t, database, dependencyID, now)
			if stage.operation == "DELETE" {
				if err := database.SetDependencies(t.Context(), jobID, []Dependency{{
					JobID: jobID, DependsOn: dependencyID, Predicate: DependencySuccess,
				}}); err != nil {
					t.Fatal(err)
				}
			}
			installAbortTrigger(t, database, fmt.Sprintf("fail_dependency_%d", index), stage.operation, "job_dependencies")
			if err := database.SetDependencies(t.Context(), jobID, []Dependency{{
				JobID: jobID, DependsOn: dependencyID, Predicate: DependencySuccess,
			}}); err == nil {
				t.Fatal("SetDependencies() error = nil")
			}
		})
	}
}

func runningRuntimeFixture(
	t *testing.T,
	prefix uint64,
) (*Store, model.JobID, model.RunID, model.LogMetadata, time.Time) {
	t.Helper()
	database, jobID, now := claimedRuntimeFixture(t, prefix)
	if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	runID := mustRunID(t, prefix+2)
	logs := testLogs(database, jobID, model.LogIntegrityPending, model.RecordingHealthy)
	if _, err := database.ReserveRun(t.Context(), jobID, runID, 1, logs, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.MarkProcessStarted(
		t.Context(), jobID, runID, "/bin/true", testProcessIdentity(8100+int(prefix%100), "transaction"), now.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	logs.Integrity = model.LogIntegrityValid

	return database, jobID, runID, logs, now
}

func claimedRuntimeFixture(t *testing.T, prefix uint64) (*Store, model.JobID, time.Time) {
	t.Helper()
	database := openTestStore(t, "claimed-runtime", newSequentialEventIDs(prefix))
	now := storeTestTime()
	jobID := mustJobID(t, prefix+1, 1)
	credential := submitRuntimeJob(t, database, jobID, now)
	claimRuntimeJob(t, database, jobID, mustSupervisorID(t, prefix+1, 2), credential, now)

	return database, jobID, now
}

func assertJobPhase(t *testing.T, database *Store, jobID model.JobID, want model.JobPhase) {
	t.Helper()
	job, err := database.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Phase != want {
		t.Fatalf("job phase = %q, want %q", job.Phase, want)
	}
}
