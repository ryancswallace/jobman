package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestRuntimeReadersReportColumnTypeCorruption(t *testing.T) {
	t.Parallel()

	t.Run("wait evaluation", func(t *testing.T) {
		t.Parallel()

		database := openTestStore(t, "wait-scan-corruption", newSequentialEventIDs(0x17000))
		jobID := mustJobID(t, 0x17001, 1)
		now := storeTestTime()
		submitRuntimeJob(t, database, jobID, now)
		if err := database.RecordWaitEvaluation(t.Context(), jobID, 0, model.WaitDelay, false, "", now); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(t.Context(), `
			ALTER TABLE wait_evaluations RENAME TO stored_wait_evaluations;
			CREATE VIEW wait_evaluations AS
			SELECT job_id, 'not-an-integer' AS condition_index, condition_kind,
			       evaluated_at_ns, satisfied_at_ns, attempt_count, last_diagnostic_code
			FROM stored_wait_evaluations`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ListWaitEvaluations(t.Context(), jobID); err == nil {
			t.Fatal("ListWaitEvaluations() accepted a non-integer condition index")
		}
	})

	t.Run("dependency observation", func(t *testing.T) {
		t.Parallel()

		database, jobID, _ := dependencyFixture(t, 0x17100)
		if _, err := database.db.ExecContext(t.Context(), `
			ALTER TABLE job_dependencies RENAME TO stored_job_dependencies;
			CREATE VIEW job_dependencies AS
			SELECT job_id, dependency_job_id, predicate, 'not-an-integer' AS observed_revision,
			       observed_outcome, satisfied_at_ns
			FROM stored_job_dependencies`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ListDependencies(t.Context(), jobID); err == nil {
			t.Fatal("ListDependencies() accepted a non-integer observed revision")
		}
	})
}

func TestConcurrencySynchronizationReportsLateDatabaseFailures(t *testing.T) {
	t.Parallel()

	t.Run("pool upsert", func(t *testing.T) {
		t.Parallel()

		database := openTestStore(t, "pool-upsert-failure", newSequentialEventIDs(0x17200))
		if _, err := database.db.ExecContext(t.Context(), `
			CREATE TEMP TRIGGER fail_pool_upsert BEFORE INSERT ON concurrency_limits
			WHEN NEW.scope_kind = 'pool'
			BEGIN SELECT RAISE(ABORT, 'injected pool failure'); END`); err != nil {
			t.Fatal(err)
		}
		capacity := uint64(2)
		if err := database.SynchronizeConcurrencyLimits(
			t.Context(), nil, map[string]*uint64{"build": &capacity}, storeTestTime(),
		); err == nil {
			t.Fatal("SynchronizeConcurrencyLimits(pool upsert failure) error = nil")
		}
	})

	t.Run("omitted pool reference query", func(t *testing.T) {
		t.Parallel()

		database := openTestStore(t, "pool-reference-failure", newSequentialEventIDs(0x17300))
		if err := database.SetConcurrencyLimit(t.Context(), "obsolete", nil, storeTestTime()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE admissions RENAME TO unavailable_admissions`); err != nil {
			t.Fatal(err)
		}
		if err := database.SynchronizeConcurrencyLimits(
			t.Context(), nil, map[string]*uint64{}, storeTestTime(),
		); err == nil {
			t.Fatal("SynchronizeConcurrencyLimits(pool reference query failure) error = nil")
		}
	})

	t.Run("pool listing decode", func(t *testing.T) {
		t.Parallel()

		database := openTestStore(t, "pool-list-decode-failure", newSequentialEventIDs(0x17400))
		if err := database.SetConcurrencyLimit(t.Context(), "build", nil, storeTestTime()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(t.Context(), `
			ALTER TABLE concurrency_limits RENAME TO stored_concurrency_limits;
			CREATE VIEW concurrency_limits AS
			SELECT scope_kind, NULL AS scope_name, capacity, revision, updated_at_ns
			FROM stored_concurrency_limits`); err != nil {
			t.Fatal(err)
		}
		if err := database.SynchronizeConcurrencyLimits(
			t.Context(), nil, map[string]*uint64{}, storeTestTime(),
		); err == nil {
			t.Fatal("SynchronizeConcurrencyLimits(corrupt pool name) error = nil")
		}
	})
}

func TestAdmissionQueueReportsCorruptOlderRequests(t *testing.T) {
	t.Parallel()

	t.Run("negative slots", func(t *testing.T) {
		t.Parallel()

		database, olderID, now := claimedRuntimeFixture(t, 0x17500)
		newerID := mustJobID(t, 0x17501, 3)
		credential := submitRuntimeJob(t, database, newerID, now.Add(time.Nanosecond))
		claimRuntimeJob(t, database, newerID, mustSupervisorID(t, 0x17501, 4), credential, now)
		capacity := uint64(2)
		if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
			t.Fatal(err)
		}
		ignoreCheckConstraints(t, database)
		if _, err := database.db.ExecContext(t.Context(), `
			INSERT INTO admission_requests(job_id, pool_name, slots, enqueued_at_ns)
			VALUES (?, NULL, -1, ?)`, olderID.String(), now.UnixNano()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.TryAcquireAdmission(t.Context(), newerID, "", 1, now.Add(time.Second), time.Minute); err == nil {
			t.Fatal("TryAcquireAdmission() accepted corrupt older slots")
		}
	})

	t.Run("undeclared pool", func(t *testing.T) {
		t.Parallel()

		database, olderID, now := claimedRuntimeFixture(t, 0x17600)
		newerID := mustJobID(t, 0x17601, 3)
		credential := submitRuntimeJob(t, database, newerID, now.Add(time.Nanosecond))
		claimRuntimeJob(t, database, newerID, mustSupervisorID(t, 0x17601, 4), credential, now)
		capacity := uint64(2)
		if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(t.Context(), `
			INSERT INTO admission_requests(job_id, pool_name, slots, enqueued_at_ns)
			VALUES (?, 'missing', 1, ?)`, olderID.String(), now.UnixNano()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.TryAcquireAdmission(t.Context(), newerID, "", 1, now.Add(time.Second), time.Minute); err == nil {
			t.Fatal("TryAcquireAdmission() accepted an older request for an undeclared pool")
		}
	})
}

func TestAdmissionCapacityErrorPropagation(t *testing.T) {
	t.Parallel()

	database, jobID, now := claimedRuntimeFixture(t, 0x17700)
	if _, err := database.TryAcquireAdmission(
		t.Context(), jobID, "", math.MaxUint64, now, time.Minute,
	); err == nil {
		t.Fatal("TryAcquireAdmission() accepted unrepresentable slots")
	}

	one := uint64(1)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := admissionFits(canceled, database.db, "", 1, &one, nil); err == nil {
		t.Fatal("admissionFits(global query failure) error = nil")
	}
	if _, err := admissionFits(canceled, database.db, "build", 1, nil, &one); err == nil {
		t.Fatal("admissionFits(pool query failure) error = nil")
	}
}
