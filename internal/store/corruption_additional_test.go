package store

import (
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestRuntimeReadersRejectCorruptPersistedValues(t *testing.T) {
	t.Parallel()

	fields := []string{"revision", "run_count", "success_count", "failure_count"}
	for index, field := range fields {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			database := openTestStore(t, "corrupt-runtime-"+field, newSequentialEventIDs(uint64(0x12000+index*0x10)))
			ignoreCheckConstraints(t, database)
			jobID := mustJobID(t, uint64(0x12001+index*0x10), 1)
			submitRuntimeJob(t, database, jobID, storeTestTime())
			if _, err := database.db.ExecContext(t.Context(), "UPDATE job_runtime SET "+field+" = -1 WHERE job_id = ?", jobID.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := database.GetRuntime(t.Context(), jobID); err == nil {
				t.Fatalf("GetRuntime() accepted negative %s", field)
			}
		})
	}
}

func TestWaitEvaluationReaderRejectsCorruptRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		column string
		value  any
	}{
		{column: "condition_index", value: -1},
		{column: "attempt_count", value: -1},
		{column: "condition_kind", value: "unknown"},
	}
	for index, test := range tests {
		t.Run(test.column, func(t *testing.T) {
			t.Parallel()
			database := openTestStore(t, "corrupt-wait", newSequentialEventIDs(uint64(0x12100+index*0x10)))
			ignoreCheckConstraints(t, database)
			jobID := mustJobID(t, uint64(0x12101+index*0x10), 1)
			now := storeTestTime()
			submitRuntimeJob(t, database, jobID, now)
			if err := database.RecordWaitEvaluation(t.Context(), jobID, 0, model.WaitDelay, false, "", now); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(t.Context(), "UPDATE wait_evaluations SET "+test.column+" = ? WHERE job_id = ?", test.value, jobID.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ListWaitEvaluations(t.Context(), jobID); err == nil {
				t.Fatalf("ListWaitEvaluations() accepted corrupt %s", test.column)
			}
		})
	}
}

func TestDependencyReadersRejectCorruptRows(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "corrupt-dependencies", newSequentialEventIDs(0x12200))
	now := storeTestTime()
	jobID := mustJobID(t, 0x12201, 1)
	dependencyID := mustJobID(t, 0x12201, 2)
	submitRuntimeJob(t, database, jobID, now)
	submitRuntimeJob(t, database, dependencyID, now)
	if err := database.SetDependencies(t.Context(), jobID, []Dependency{{
		JobID: jobID, DependsOn: dependencyID, Predicate: DependencySuccess,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `UPDATE job_dependencies SET dependency_job_id = 'invalid' WHERE job_id = ?`, jobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ListDependencies(t.Context(), jobID); err == nil {
		t.Fatal("ListDependencies() accepted invalid dependency ID")
	}
}

func TestAdmissionReadersRejectCorruptRows(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "corrupt-admission", newSequentialEventIDs(0x12300))
	ignoreCheckConstraints(t, database)
	now := storeTestTime()
	jobID := mustJobID(t, 0x12301, 1)
	credential := submitRuntimeJob(t, database, jobID, now)
	claimRuntimeJob(t, database, jobID, mustSupervisorID(t, 0x12301, 2), credential, now)
	if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `UPDATE admissions SET slots = -1 WHERE job_id = ?`, jobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ListExpiredAdmissions(t.Context(), now.Add(time.Second)); err == nil {
		t.Fatal("ListExpiredAdmissions() accepted negative slots")
	}
}

func TestAdmissionReadersRejectCorruptIdentifiers(t *testing.T) {
	t.Parallel()

	for name, statement := range map[string]string{
		"job ID": `UPDATE admissions SET job_id = 'invalid'`,
		"run ID": `UPDATE admissions SET run_id = 'invalid'`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database, jobID, now := claimedRuntimeFixture(t, 0x12340+uint64(len(name))*0x10)
			ignoreCheckConstraints(t, database)
			if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			if _, err := database.ListExpiredAdmissions(t.Context(), now.Add(time.Second)); err == nil {
				t.Fatal("ListExpiredAdmissions() accepted corrupt identifier")
			}
		})
	}
}

func TestAdmissionQueueRejectsCorruptPersistedCounters(t *testing.T) {
	t.Parallel()

	for index, column := range []string{"slots", "bypass_count"} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()
			database, jobID, now := claimedRuntimeFixture(t, uint64(0x12380+index*0x10))
			ignoreCheckConstraints(t, database)
			if _, err := database.db.ExecContext(t.Context(), `
				INSERT INTO admission_requests(job_id, pool_name, slots, enqueued_at_ns)
				VALUES (?, NULL, 1, ?)`, jobID.String(), now.UnixNano()); err != nil {
				t.Fatal(err)
			}
			if _, err := database.db.ExecContext(t.Context(), "UPDATE admission_requests SET "+column+" = -1 WHERE job_id = ?", jobID.String()); err != nil {
				t.Fatal(err)
			}
			if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err == nil {
				t.Fatalf("TryAcquireAdmission() accepted negative %s", column)
			}
		})
	}
}

func TestExpiredOwnerReaderRejectsCorruptJobID(t *testing.T) {
	t.Parallel()

	database, jobID, now := claimedRuntimeFixture(t, 0x123b0)
	ignoreCheckConstraints(t, database)
	if _, err := database.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `UPDATE jobs SET id = 'invalid' WHERE id = ?`, jobID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ListExpiredOwnedJobs(t.Context(), now.Add(time.Hour)); err == nil {
		t.Fatal("ListExpiredOwnedJobs() accepted invalid job ID")
	}
}

func ignoreCheckConstraints(t *testing.T, database *Store) {
	t.Helper()
	database.db.SetMaxOpenConns(1)
	if _, err := database.db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeMutationValidationEdges(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "runtime-validation-edges", newSequentialEventIDs(0x12400))
	now := storeTestTime()
	jobID := mustJobID(t, 0x12401, 1)
	submitRuntimeJob(t, database, jobID, now)
	if err := database.RecordWaitEvaluation(t.Context(), jobID, -1, model.WaitDelay, false, "", now); err == nil {
		t.Fatal("RecordWaitEvaluation() accepted a negative index")
	}
	if err := database.RecordInputEOF(t.Context(), "invalid", mustRunID(t, 0x12401), now); err == nil {
		t.Fatal("RecordInputEOF() accepted an invalid job ID")
	}
	if err := database.ValidateAdmissionRequest(t.Context(), "", 0); err == nil {
		t.Fatal("ValidateAdmissionRequest() accepted zero slots")
	}
	if err := database.ValidateAdmissionRequest(t.Context(), " padded ", 1); err == nil {
		t.Fatal("ValidateAdmissionRequest() accepted a padded pool")
	}
	if err := database.SetDependencies(t.Context(), "invalid", nil); err == nil {
		t.Fatal("SetDependencies() accepted an invalid job ID")
	}
}
