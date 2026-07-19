package store

import (
	"math"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestRuntimePolicyTransitionsRejectInvalidModelInputs(t *testing.T) {
	t.Parallel()

	t.Run("completion outcome", func(t *testing.T) {
		t.Parallel()
		database, jobID, runID, logs, now := runningRuntimeFixture(t, 0x15000)
		if _, err := database.CompleteRunWithDisposition(
			t.Context(), jobID, runID, model.RunOutcome("invalid"), nil, logs, "", now.Add(4*time.Second),
			model.RunDisposition{TerminalOutcome: model.JobOutcomeFailure},
		); err == nil {
			t.Fatal("CompleteRunWithDisposition() accepted an invalid outcome")
		}
	})

	for name, operation := range map[string]func(*Store, model.JobID, time.Time) error{
		"job timeout": func(database *Store, jobID model.JobID, before time.Time) error {
			_, err := database.RequestTimeout(t.Context(), jobID, before)
			return err
		},
		"run timeout": func(database *Store, jobID model.JobID, before time.Time) error {
			_, err := database.RequestRunTimeout(t.Context(), jobID, before)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database, jobID, _, _, now := runningRuntimeFixture(t, 0x15100+uint64(len(name))*0x20)
			if err := operation(database, jobID, now.Add(-time.Second)); err == nil {
				t.Fatal("timeout operation accepted a time before submission")
			}
		})
	}
}

func TestResumePropagatesPersistedStateFailures(t *testing.T) {
	t.Parallel()

	t.Run("runtime unavailable", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x15200)
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE job_runtime RENAME TO unavailable_runtime`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Resume(t.Context(), jobID, now); err == nil {
			t.Fatal("Resume() error = nil")
		}
	})

	t.Run("run unavailable", func(t *testing.T) {
		t.Parallel()
		database, jobID, _, _, now := runningRuntimeFixture(t, 0x15300)
		if _, err := database.Pause(t.Context(), jobID, now.Add(4*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE runs RENAME TO unavailable_runs`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Resume(t.Context(), jobID, now.Add(5*time.Second)); err == nil {
			t.Fatal("Resume() error = nil")
		}
	})

	t.Run("invalid prior phase", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x15400)
		if _, err := database.MoveJob(t.Context(), jobID, model.JobPhaseQueued, now.Add(time.Second), "queue"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Pause(t.Context(), jobID, now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		ignoreCheckConstraints(t, database)
		if _, err := database.db.ExecContext(t.Context(), `UPDATE job_runtime SET paused_from_phase = 'completed' WHERE job_id = ?`, jobID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Resume(t.Context(), jobID, now.Add(3*time.Second)); err == nil {
			t.Fatal("Resume() accepted an invalid prior phase")
		}
	})
}

func TestDependencyEvaluationRejectsCorruptObservations(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		column string
		value  any
	}{
		{name: "observed revision", column: "observed_revision", value: -1},
		{name: "dependency identifier", column: "dependency_job_id", value: "invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database, jobID, dependencyID := dependencyFixture(t, 0x15500+uint64(len(test.name))*0x20)
			ignoreCheckConstraints(t, database)
			if _, err := database.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatal(err)
			}
			if test.column == "dependency_job_id" {
				if _, err := database.db.ExecContext(t.Context(), `UPDATE jobs SET id = 'invalid' WHERE id = ?`, dependencyID.String()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := database.db.ExecContext(t.Context(), "UPDATE job_dependencies SET "+test.column+" = ? WHERE job_id = ?", test.value, jobID.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := database.EvaluateDependencies(t.Context(), jobID, storeTestTime()); err == nil {
				t.Fatal("EvaluateDependencies() accepted corrupt persisted data")
			}
		})
	}

	t.Run("current revision", func(t *testing.T) {
		t.Parallel()
		database, jobID, dependencyID := dependencyFixture(t, 0x15600)
		ignoreCheckConstraints(t, database)
		if _, err := database.db.ExecContext(t.Context(), `UPDATE jobs SET revision = -1 WHERE id = ?`, dependencyID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.EvaluateDependencies(t.Context(), jobID, storeTestTime()); err == nil {
			t.Fatal("EvaluateDependencies() accepted a negative current revision")
		}
	})

	t.Run("snapshot update", func(t *testing.T) {
		t.Parallel()
		database, jobID, _ := dependencyFixture(t, 0x15700)
		installAbortTrigger(t, database, "fail_dependency_snapshot", "UPDATE", "job_dependencies")
		if _, err := database.EvaluateDependencies(t.Context(), jobID, storeTestTime()); err == nil {
			t.Fatal("EvaluateDependencies() error = nil")
		}
	})
}

func dependencyFixture(t *testing.T, prefix uint64) (*Store, model.JobID, model.JobID) {
	t.Helper()
	database := openTestStore(t, "dependency-failure", newSequentialEventIDs(prefix))
	now := storeTestTime()
	jobID := mustJobID(t, prefix+1, 1)
	dependencyID := mustJobID(t, prefix+1, 2)
	submitRuntimeJob(t, database, jobID, now)
	credential := submitRuntimeJob(t, database, dependencyID, now)
	if err := database.SetDependencies(t.Context(), jobID, []Dependency{{
		JobID: jobID, DependsOn: dependencyID, Predicate: DependencySuccess,
	}}); err != nil {
		t.Fatal(err)
	}
	claimRuntimeJob(t, database, dependencyID, mustSupervisorID(t, prefix+1, 3), credential, now)
	if _, err := database.CompleteWithoutRun(
		t.Context(), dependencyID, model.JobOutcomeAborted, "test", now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}

	return database, jobID, dependencyID
}

func TestConcurrencyLimitsRejectUnrepresentablePersistedValues(t *testing.T) {
	t.Parallel()

	t.Run("capacity overflow", func(t *testing.T) {
		t.Parallel()
		database := openTestStore(t, "capacity-overflow", newSequentialEventIDs(0x15800))
		capacity := uint64(math.MaxUint64)
		if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, storeTestTime()); err == nil {
			t.Fatal("SetConcurrencyLimit() accepted an unrepresentable capacity")
		}
	})

	t.Run("negative queued slots", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x15900)
		ignoreCheckConstraints(t, database)
		if _, err := database.db.ExecContext(t.Context(), `
			INSERT INTO admission_requests(job_id, pool_name, slots, enqueued_at_ns)
			VALUES (?, NULL, -1, ?)`, jobID.String(), now.UnixNano()); err != nil {
			t.Fatal(err)
		}
		capacity := uint64(1)
		if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err == nil {
			t.Fatal("SetConcurrencyLimit() accepted negative queued slots")
		}
	})
}

func TestConcurrencySynchronizationPropagatesDatabaseFailures(t *testing.T) {
	t.Parallel()

	t.Run("queued request query", func(t *testing.T) {
		t.Parallel()
		database := openTestStore(t, "queued-query-failure", newSequentialEventIDs(0x15c00))
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE admission_requests RENAME TO unavailable_requests`); err != nil {
			t.Fatal(err)
		}
		capacity := uint64(1)
		if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, storeTestTime()); err == nil {
			t.Fatal("SetConcurrencyLimit() error = nil")
		}
	})

	t.Run("pool listing", func(t *testing.T) {
		t.Parallel()
		database := openTestStore(t, "pool-list-failure", newSequentialEventIDs(0x15d00))
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE concurrency_limits RENAME TO unavailable_limits`); err != nil {
			t.Fatal(err)
		}
		if err := database.SynchronizeConcurrencyLimits(t.Context(), nil, nil, storeTestTime()); err == nil {
			t.Fatal("SynchronizeConcurrencyLimits() error = nil")
		}
	})

	t.Run("pool removal", func(t *testing.T) {
		t.Parallel()
		database := openTestStore(t, "pool-removal-failure", newSequentialEventIDs(0x15e00))
		if err := database.SetConcurrencyLimit(t.Context(), "obsolete", nil, storeTestTime()); err != nil {
			t.Fatal(err)
		}
		installAbortTrigger(t, database, "fail_pool_removal", "DELETE", "concurrency_limits")
		if err := database.SynchronizeConcurrencyLimits(t.Context(), nil, map[string]*uint64{}, storeTestTime()); err == nil {
			t.Fatal("SynchronizeConcurrencyLimits() error = nil")
		}
	})

	t.Run("limit upsert", func(t *testing.T) {
		t.Parallel()
		database := openTestStore(t, "limit-upsert-failure", newSequentialEventIDs(0x15f00))
		installAbortTrigger(t, database, "fail_limit_upsert", "INSERT", "concurrency_limits")
		if err := database.SynchronizeConcurrencyLimits(t.Context(), nil, nil, storeTestTime()); err == nil {
			t.Fatal("SynchronizeConcurrencyLimits() error = nil")
		}
	})
}

func TestAdmissionValidationPropagatesLookupFailures(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-validation-failures", newSequentialEventIDs(0x16000))
	if _, err := database.TryAcquireAdmission(
		t.Context(), mustJobID(t, 0x16001, 1), "", 1, storeTestTime(), time.Minute,
	); err == nil {
		t.Fatal("TryAcquireAdmission() accepted a missing job")
	}
	if err := database.ValidateAdmissionRequest(t.Context(), "", math.MaxUint64); err == nil {
		t.Fatal("ValidateAdmissionRequest() accepted unrepresentable slots")
	}
	if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE concurrency_limits RENAME TO unavailable_limits`); err != nil {
		t.Fatal(err)
	}
	if err := database.ValidateAdmissionRequest(t.Context(), "", 1); err == nil {
		t.Fatal("ValidateAdmissionRequest() error = nil")
	}
}

func TestDependencyLookupAndListConversionFailures(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "dependency-lookup-failures", newSequentialEventIDs(0x16100))
	if err := database.SetDependencies(t.Context(), mustJobID(t, 0x16101, 1), nil); err == nil {
		t.Fatal("SetDependencies() accepted a missing job")
	}
	database, jobID, _ := dependencyFixture(t, 0x16200)
	ignoreCheckConstraints(t, database)
	if _, err := database.db.ExecContext(t.Context(), `UPDATE job_dependencies SET observed_revision = -1 WHERE job_id = ?`, jobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ListDependencies(t.Context(), jobID); err == nil {
		t.Fatal("ListDependencies() accepted a negative observed revision")
	}
}

func TestAdmissionPersistencePropagatesEachDatabaseFailure(t *testing.T) {
	t.Parallel()

	for index, stage := range []struct {
		name, operation, table string
	}{
		{name: "admission insert", operation: "INSERT", table: "admissions"},
		{name: "request delete", operation: "DELETE", table: "admission_requests"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			t.Parallel()
			database, jobID, now := claimedRuntimeFixture(t, 0x15a00+uint64(index)*0x20)
			installAbortTrigger(t, database, "fail_admission_persist_"+stage.table, stage.operation, stage.table)
			if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err == nil {
				t.Fatal("TryAcquireAdmission() error = nil")
			}
		})
	}

	t.Run("corrupt active admission", func(t *testing.T) {
		t.Parallel()
		database, jobID, now := claimedRuntimeFixture(t, 0x15b00)
		if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err != nil {
			t.Fatal(err)
		}
		ignoreCheckConstraints(t, database)
		if _, err := database.db.ExecContext(t.Context(), `UPDATE admissions SET slots = -1 WHERE job_id = ?`, jobID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err == nil {
			t.Fatal("TryAcquireAdmission() accepted a corrupt active admission")
		}
	})
}
