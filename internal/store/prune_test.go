package store

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestPruneCompletedJobMetadataDryRunAndDelete(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "metadata-pruning", newSequentialEventIDs(0xa100))
	jobID := mustJobID(t, 0xa1, 1)
	supervisorID := mustSupervisorID(t, 0xa1, 2)
	credential := bytes.Repeat([]byte{0xa1}, 32)
	hash, hashErr := model.NewCredentialHash(credential)
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	at := storeTestTime()
	if _, submitErr := database.Submit(t.Context(), jobID, testJobSpec(t, "prune"), hash, at, at.Add(time.Minute)); submitErr != nil {
		t.Fatal(submitErr)
	}
	if _, claimErr := database.Claim(
		t.Context(), jobID, credential, supervisorID, testProcessIdentity(401, "prune"),
		at.Add(time.Second), at.Add(time.Minute),
	); claimErr != nil {
		t.Fatal(claimErr)
	}
	if _, cancelErr := database.RequestCancellation(t.Context(), jobID, at.Add(2*time.Second)); cancelErr != nil {
		t.Fatal(cancelErr)
	}
	completedAt := at.Add(3 * time.Second)
	if _, finalizeErr := database.FinalizeCancellationWithoutRun(t.Context(), jobID, completedAt); finalizeErr != nil {
		t.Fatal(finalizeErr)
	}

	eligible, err := database.PruneCompletedJobMetadata(
		t.Context(), jobID, completedAt.Add(time.Second), true,
	)
	if err != nil || !eligible {
		t.Fatalf("dry-run pruning = (%t, %v), want eligible", eligible, err)
	}
	if _, getErr := database.GetJob(t.Context(), jobID); getErr != nil {
		t.Fatalf("dry-run deleted job: %v", getErr)
	}
	eligible, err = database.PruneCompletedJobMetadata(
		t.Context(), jobID, completedAt.Add(time.Second), false,
	)
	if err != nil || !eligible {
		t.Fatalf("pruning = (%t, %v), want eligible", eligible, err)
	}
	if _, getErr := database.GetJob(t.Context(), jobID); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("GetJob() after pruning = %v, want ErrNotFound", getErr)
	}
}

func TestStoreHealthAndConsistentBackup(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "doctor", newSequentialEventIDs(0xa200))
	report, err := database.CheckHealth(t.Context(), true)
	if err != nil {
		t.Fatalf("CheckHealth() error = %v", err)
	}
	if !report.Healthy || !report.WALCheckpointed || report.SchemaVersion != currentSchemaVersion {
		t.Fatalf("health report = %+v", report)
	}
	destination := filepath.Join(t.TempDir(), "backup.db")
	if err := database.Backup(t.Context(), destination); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	if err := validateDatabaseFile(destination); err != nil {
		t.Fatalf("backup validation = %v", err)
	}
}

func TestSynchronizeConcurrencyLimitsReplacesUnreferencedPools(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "synchronize-limits", newSequentialEventIDs(0xa300))
	now := storeTestTime()
	global := uint64(3)
	alpha := uint64(2)
	beta := uint64(1)
	if err := database.SynchronizeConcurrencyLimits(t.Context(), &global, map[string]*uint64{
		"alpha": &alpha,
		"beta":  &beta,
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := database.SynchronizeConcurrencyLimits(t.Context(), &global, map[string]*uint64{
		"alpha": &alpha,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.SynchronizeConcurrencyLimits(t.Context(), nil, map[string]*uint64{
		"alpha": nil,
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var pools int
	if err := database.db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM concurrency_limits WHERE scope_kind = 'pool'`).Scan(&pools); err != nil {
		t.Fatal(err)
	}
	if pools != 1 {
		t.Fatalf("retained pools = %d, want 1", pools)
	}

	zero := uint64(0)
	tooLarge := uint64(math.MaxUint64)
	for _, test := range []struct {
		global *uint64
		pools  map[string]*uint64
	}{
		{global: &zero},
		{pools: map[string]*uint64{" bad ": &alpha}},
		{pools: map[string]*uint64{"bad": &zero}},
		{global: &tooLarge},
	} {
		if err := database.SynchronizeConcurrencyLimits(t.Context(), test.global, test.pools, now); err == nil {
			t.Errorf("SynchronizeConcurrencyLimits(%v, %v) error = nil", test.global, test.pools)
		}
	}
}

func TestSynchronizeConcurrencyLimitsRejectsQueuedRequestsThatNoLongerFit(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "synchronize-queued", newSequentialEventIDs(0xa350))
	now := storeTestTime()
	capacity := uint64(2)
	if err := database.SynchronizeConcurrencyLimits(t.Context(), &capacity, nil, now); err != nil {
		t.Fatal(err)
	}

	activeJob := mustJobID(t, 0xa3, 0x51)
	submitRuntimeJob(t, database, activeJob, now)
	if admission, err := database.TryAcquireAdmission(
		t.Context(), activeJob, "", 2, now.Add(time.Second), time.Minute,
	); err != nil || admission.JobID != activeJob {
		t.Fatalf("active admission = %+v, %v", admission, err)
	}
	queuedJob := mustJobID(t, 0xa3, 0x52)
	submitRuntimeJob(t, database, queuedJob, now.Add(2*time.Second))
	if admission, err := database.TryAcquireAdmission(
		t.Context(), queuedJob, "", 2, now.Add(3*time.Second), time.Minute,
	); !errors.Is(err, ErrCapacity) || admission.JobID != "" {
		t.Fatalf("queued admission = %+v, %v", admission, err)
	}
	lowered := uint64(1)
	if err := database.SetConcurrencyLimit(
		t.Context(), "", &lowered, now.Add(4*time.Second),
	); !errors.Is(err, ErrAdmissionImpossible) {
		t.Fatalf("SetConcurrencyLimit() error = %v, want ErrAdmissionImpossible", err)
	}
	if err := database.SynchronizeConcurrencyLimits(
		t.Context(), &capacity, nil, now.Add(5*time.Second),
	); err != nil {
		t.Fatalf("retain capacity with fitting queued request: %v", err)
	}

	if err := database.SynchronizeConcurrencyLimits(
		t.Context(), &lowered, nil, now.Add(6*time.Second),
	); !errors.Is(err, ErrAdmissionImpossible) {
		t.Fatalf("lower capacity error = %v, want ErrAdmissionImpossible", err)
	}
}

func TestSynchronizeConcurrencyLimitsRejectsOversizedQueuedPoolRequest(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "synchronize-pool-queued", newSequentialEventIDs(0xa360))
	now := storeTestTime()
	capacity := uint64(2)
	if err := database.SynchronizeConcurrencyLimits(
		t.Context(), nil, map[string]*uint64{"build": &capacity}, now,
	); err != nil {
		t.Fatal(err)
	}
	for index := uint64(1); index <= 2; index++ {
		jobID := mustJobID(t, 0xa3, 0x60+index)
		submitRuntimeJob(t, database, jobID, now.Add(time.Duration(index)*time.Second))
		_, err := database.TryAcquireAdmission(
			t.Context(), jobID, "build", 2, now.Add(time.Duration(index+2)*time.Second), time.Minute,
		)
		if index == 1 && err != nil {
			t.Fatal(err)
		}
		if index == 2 && !errors.Is(err, ErrCapacity) {
			t.Fatalf("queued pool admission error = %v, want ErrCapacity", err)
		}
	}

	lowered := uint64(1)
	if err := database.SynchronizeConcurrencyLimits(
		t.Context(), nil, map[string]*uint64{"build": &lowered}, now.Add(5*time.Second),
	); !errors.Is(err, ErrAdmissionImpossible) {
		t.Fatalf("lower pool capacity error = %v, want ErrAdmissionImpossible", err)
	}
}

func TestMigrationBackupRejectsDuplicateDestination(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "duplicate-migration-backup", newSequentialEventIDs(0xa370))
	if _, err := database.db.ExecContext(t.Context(), "PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := database.db.ExecContext(
			t.Context(), "PRAGMA user_version = "+strconv.Itoa(currentSchemaVersion),
		); err != nil {
			t.Errorf("restore schema version: %v", err)
		}
	}()
	if err := database.backupBeforeUpgrade(t.Context()); err != nil {
		t.Fatalf("first backupBeforeUpgrade() error = %v", err)
	}
	if err := database.backupBeforeUpgrade(t.Context()); err == nil {
		t.Fatal("second backupBeforeUpgrade() error = nil")
	}
}

func TestSynchronizeConcurrencyLimitsProtectsReferencedPool(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "synchronize-referenced", newSequentialEventIDs(0xa400))
	now := storeTestTime()
	capacity := uint64(1)
	if err := database.SynchronizeConcurrencyLimits(
		t.Context(), nil, map[string]*uint64{"build": &capacity}, now,
	); err != nil {
		t.Fatal(err)
	}
	jobID := mustJobID(t, 0xa4, 1)
	submitRuntimeJob(t, database, jobID, now)
	if _, err := database.TryAcquireAdmission(
		t.Context(), jobID, "build", 1, now.Add(time.Second), time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.SynchronizeConcurrencyLimits(
		t.Context(), nil, map[string]*uint64{}, now.Add(2*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("remove referenced pool error = %v, want conflict", err)
	}
	if err := database.ReleaseAdmission(t.Context(), jobID, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.SynchronizeConcurrencyLimits(
		t.Context(), nil, map[string]*uint64{}, now.Add(4*time.Second),
	); err != nil {
		t.Fatal(err)
	}
}

func TestBackupAndPruningValidation(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "backup-errors", newSequentialEventIDs(0xa500))
	if err := database.Backup(t.Context(), ""); err == nil {
		t.Fatal("Backup(empty) error = nil")
	}
	existing := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(existing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := database.Backup(t.Context(), existing); err == nil {
		t.Fatal("Backup(existing) error = nil")
	}
	report, err := database.CheckHealth(t.Context(), false)
	if err != nil || !report.Healthy || report.WALCheckpointed {
		t.Fatalf("CheckHealth(false) = %+v, %v", report, err)
	}
	if _, pruneErr := database.PruneCompletedJobMetadata(
		t.Context(), model.JobID("invalid"), storeTestTime(), false,
	); pruneErr == nil {
		t.Fatal("PruneCompletedJobMetadata(invalid) error = nil")
	}
	jobID := mustJobID(t, 0xa5, 1)
	submitRuntimeJob(t, database, jobID, storeTestTime())
	eligible, err := database.PruneCompletedJobMetadata(
		t.Context(), jobID, storeTestTime().Add(time.Hour), false,
	)
	if err != nil || eligible {
		t.Fatalf("prune active job = %t, %v", eligible, err)
	}
}

func TestPruningBlocksUnprunedRunsAndUnresolvedDependents(t *testing.T) {
	t.Parallel()
	database := openTestStore(t, "pruning-blockers", newSequentialEventIDs(0xa700))
	now := storeTestTime()

	withRun := mustJobID(t, 0xa7, 1)
	credential := submitRuntimeJob(t, database, withRun, now)
	claimRuntimeJob(t, database, withRun, mustSupervisorID(t, 0xa7, 2), credential, now)
	runID := mustRunID(t, 0xa7)
	logs := testLogs(database, withRun, model.LogIntegrityPending, model.RecordingHealthy)
	if _, err := database.ReserveRun(t.Context(), withRun, runID, 1, logs, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	logs.Integrity = model.LogIntegrityValid
	if _, err := database.MarkStartFailed(
		t.Context(), withRun, runID, logs, "test", now.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	eligible, err := database.PruneCompletedJobMetadata(
		t.Context(), withRun, now.Add(time.Hour), false,
	)
	if err != nil || eligible {
		t.Fatalf("prune unpruned run = %t, %v", eligible, err)
	}

	predecessor := mustJobID(t, 0xa7, 4)
	completeJobWithoutRun(t, database, predecessor, now.Add(4*time.Second))
	dependent := mustJobID(t, 0xa7, 5)
	hash, err := model.NewCredentialHash(bytes.Repeat([]byte{0x55}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, submitErr := database.SubmitWithDependencies(
		t.Context(), dependent, testJobSpec(t, "dependent"), hash,
		now.Add(8*time.Second), now.Add(time.Minute),
		[]Dependency{{JobID: dependent, DependsOn: predecessor, Predicate: DependencyFinish}},
	); submitErr != nil {
		t.Fatal(submitErr)
	}
	eligible, err = database.PruneCompletedJobMetadata(
		t.Context(), predecessor, now.Add(time.Hour), false,
	)
	if err != nil || eligible {
		t.Fatalf("prune unresolved dependency = %t, %v", eligible, err)
	}
	missing := mustJobID(t, 0xa7, 6)
	if _, err := database.PruneCompletedJobMetadata(
		t.Context(), missing, now.Add(time.Hour), false,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("prune missing error = %v, want not found", err)
	}
}

func completeJobWithoutRun(t *testing.T, database *Store, jobID model.JobID, now time.Time) {
	t.Helper()
	credential := submitRuntimeJob(t, database, jobID, now)
	claimRuntimeJob(t, database, jobID, mustSupervisorID(t, 0xa8, 1), credential, now)
	if _, err := database.RequestCancellation(t.Context(), jobID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.FinalizeCancellationWithoutRun(t.Context(), jobID, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
}
