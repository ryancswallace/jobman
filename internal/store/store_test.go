package store

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestStoreLifecycle(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, "lifecycle", newSequentialEventIDs(0x1000))
	jobID := mustJobID(t, 1, 1)
	runID := mustRunID(t, 1)
	supervisorID := mustSupervisorID(t, 1, 3)
	credential := bytes.Repeat([]byte{0x42}, 32)
	credentialHash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	submittedAt := storeTestTime()
	specification := testJobSpec(t, "lifecycle")

	submitted, err := store.Submit(
		t.Context(),
		jobID,
		specification,
		credentialHash,
		submittedAt,
		submittedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got := submitted.Effects; !reflect.DeepEqual(got, []model.Effect{{Type: model.EffectLaunchSupervisor}}) {
		t.Errorf("Submit() effects = %v, want launch supervisor", got)
	}
	assertJobSnapshot(t, store, submitted.Job)

	claimedAt := submittedAt.Add(time.Second)
	claimed, err := store.Claim(
		t.Context(),
		jobID,
		credential,
		supervisorID,
		testProcessIdentity(101, "supervisor"),
		claimedAt,
		claimedAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Supervisor == nil {
		t.Fatal("Claim() supervisor = nil, want ownership snapshot")
	}
	assertJobSnapshot(t, store, claimed.Job)
	assertSupervisorSnapshot(t, store, *claimed.Supervisor)

	logs := testLogs(store, jobID, model.LogIntegrityPending, model.RecordingHealthy)
	reservedAt := claimedAt.Add(time.Second)
	reserved, err := store.ReserveRun(t.Context(), jobID, runID, 1, logs, reservedAt)
	if err != nil {
		t.Fatalf("ReserveRun() error = %v", err)
	}
	if got := reserved.Effects; !reflect.DeepEqual(got, []model.Effect{{Type: model.EffectStartTarget}}) {
		t.Errorf("ReserveRun() effects = %v, want start target", got)
	}
	if reserved.Run == nil {
		t.Fatal("ReserveRun() run = nil")
	}
	assertJobSnapshot(t, store, reserved.Job)
	assertRunSnapshot(t, store, *reserved.Run)

	startedAt := reservedAt.Add(time.Second)
	started, err := store.MarkProcessStarted(
		t.Context(),
		jobID,
		runID,
		"/test/bin/worker",
		testProcessIdentity(202, "target"),
		startedAt,
	)
	if err != nil {
		t.Fatalf("MarkProcessStarted() error = %v", err)
	}
	assertJobSnapshot(t, store, started.Job)
	assertRunSnapshot(t, store, *started.Run)

	runs, err := store.ListRuns(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != runID {
		t.Errorf("ListRuns() = %v, want run %s", runs, runID)
	}
	inspectedJob, inspectedRuns, err := store.GetJobWithRuns(t.Context(), jobID.String()[:8])
	if err != nil {
		t.Fatalf("GetJobWithRuns() error = %v", err)
	}
	if inspectedJob.ID != jobID || len(inspectedRuns) != 1 || inspectedRuns[0].ID != runID {
		t.Errorf(
			"GetJobWithRuns() = (%s, %v), want job %s and run %s",
			inspectedJob.ID,
			inspectedRuns,
			jobID,
			runID,
		)
	}

	requestedAt := startedAt.Add(time.Second)
	canceled, err := store.RequestCancellation(t.Context(), jobID, requestedAt)
	if err != nil {
		t.Fatalf("RequestCancellation() error = %v", err)
	}
	if got := canceled.Effects; !reflect.DeepEqual(got, []model.Effect{{Type: model.EffectStopTarget}}) {
		t.Errorf("RequestCancellation() effects = %v, want stop target", got)
	}
	assertJobSnapshot(t, store, canceled.Job)
	assertRunSnapshot(t, store, *canceled.Run)

	completedAt := requestedAt.Add(time.Second)
	finalLogs := testLogs(store, jobID, model.LogIntegrityValid, model.RecordingHealthy)
	finalLogs.StdoutSize = 17
	finalLogs.StderrSize = 9
	exit := &model.ExitInfo{PlatformReason: "terminated", ObservedAt: completedAt}
	completed, err := store.FinalizeRun(
		t.Context(),
		jobID,
		runID,
		model.RunOutcomeCancelled,
		exit,
		finalLogs,
		completedAt,
	)
	if err != nil {
		t.Fatalf("FinalizeRun() error = %v", err)
	}
	assertJobSnapshot(t, store, completed.Job)
	assertRunSnapshot(t, store, *completed.Run)
	if completed.Supervisor == nil || completed.Supervisor.ReleasedAt == nil {
		t.Fatalf("FinalizeRun() supervisor = %+v, want released supervisor", completed.Supervisor)
	}
	assertSupervisorSnapshot(t, store, *completed.Supervisor)

	releasedAgain, err := store.ReleaseSupervisor(t.Context(), supervisorID, completedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("repeated ReleaseSupervisor() error = %v", err)
	}
	if releasedAgain.Revision != completed.Supervisor.Revision {
		t.Errorf("repeated release revision = %d, want unchanged %d", releasedAgain.Revision, completed.Supervisor.Revision)
	}

	assertEventCount(t, store, jobID, 12)
}

func TestFinalizeCancellationWithoutRun(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, "cancel-without-run", newSequentialEventIDs(0x2000))
	jobID := mustJobID(t, 2, 1)
	supervisorID := mustSupervisorID(t, 2, 2)
	credential := bytes.Repeat([]byte{0x21}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	at := storeTestTime()
	if _, submitErr := store.Submit(t.Context(), jobID, testJobSpec(t, "cancel"), hash, at, at.Add(time.Minute)); submitErr != nil {
		t.Fatalf("Submit() error = %v", submitErr)
	}
	if _, claimErr := store.Claim(
		t.Context(),
		jobID,
		credential,
		supervisorID,
		testProcessIdentity(301, "supervisor"),
		at.Add(time.Second),
		at.Add(time.Minute),
	); claimErr != nil {
		t.Fatalf("Claim() error = %v", claimErr)
	}
	if _, cancelErr := store.RequestCancellation(t.Context(), jobID, at.Add(2*time.Second)); cancelErr != nil {
		t.Fatalf("RequestCancellation() error = %v", cancelErr)
	}

	result, err := store.FinalizeCancellationWithoutRun(t.Context(), jobID, at.Add(3*time.Second))
	if err != nil {
		t.Fatalf("FinalizeCancellationWithoutRun() error = %v", err)
	}
	if result.Job.Outcome != model.JobOutcomeCancelled || result.Job.ActiveRunID != "" {
		t.Errorf("completed job = %+v, want canceled without run", result.Job)
	}
	if result.Supervisor == nil || result.Supervisor.ReleasedAt == nil {
		t.Errorf("completed supervisor = %+v, want released", result.Supervisor)
	}
}

func TestStartFailureAndLeaseLifecycle(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, "start-failure", newSequentialEventIDs(0x2500))
	jobID := mustJobID(t, 0x25, 1)
	runID := mustRunID(t, 0x25)
	supervisorID := mustSupervisorID(t, 0x25, 3)
	credential := bytes.Repeat([]byte{0x25}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	at := storeTestTime()
	if _, submitErr := store.Submit(
		t.Context(),
		jobID,
		testJobSpec(t, "start-failure"),
		hash,
		at,
		at.Add(time.Minute),
	); submitErr != nil {
		t.Fatalf("Submit() error = %v", submitErr)
	}
	if _, claimErr := store.Claim(
		t.Context(),
		jobID,
		credential,
		supervisorID,
		testProcessIdentity(351, "supervisor"),
		at.Add(time.Second),
		at.Add(time.Minute),
	); claimErr != nil {
		t.Fatalf("Claim() error = %v", claimErr)
	}

	renewed, err := store.RenewLease(
		t.Context(),
		supervisorID,
		at.Add(2*time.Second),
		at.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}
	assertSupervisorSnapshot(t, store, renewed)

	reservedLogs := testLogs(store, jobID, model.LogIntegrityPending, model.RecordingHealthy)
	if _, reserveErr := store.ReserveRun(
		t.Context(),
		jobID,
		runID,
		1,
		reservedLogs,
		at.Add(3*time.Second),
	); reserveErr != nil {
		t.Fatalf("ReserveRun() error = %v", reserveErr)
	}
	failedLogs := testLogs(store, jobID, model.LogIntegrityValid, model.RecordingDegraded)
	failedLogs.DiagnosticCode = "log_sync_failed"
	failed, err := store.MarkStartFailed(
		t.Context(),
		jobID,
		runID,
		failedLogs,
		"executable_not_found",
		at.Add(4*time.Second),
	)
	if err != nil {
		t.Fatalf("MarkStartFailed() error = %v", err)
	}
	if failed.Job.Outcome != model.JobOutcomeFailure || failed.Run == nil || failed.Run.Outcome != model.RunOutcomeStartFailed {
		t.Errorf("MarkStartFailed() = %+v, want failure/start_failed", failed)
	}
	if failed.Supervisor == nil || failed.Supervisor.ReleasedAt == nil {
		t.Errorf("MarkStartFailed() supervisor = %+v, want released", failed.Supervisor)
	}
	assertJobSnapshot(t, store, failed.Job)
	assertRunSnapshot(t, store, *failed.Run)
	assertSupervisorSnapshot(t, store, *failed.Supervisor)
	assertEventCount(t, store, jobID, 9)
}

func TestSubmissionFailure(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, "submission-failure", newSequentialEventIDs(0x2600))
	jobID := mustJobID(t, 0x26, 1)
	credential := bytes.Repeat([]byte{0x26}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	at := storeTestTime()
	deadline := at.Add(time.Minute)
	if _, submitErr := store.Submit(
		t.Context(),
		jobID,
		testJobSpec(t, "submission-failure"),
		hash,
		at,
		deadline,
	); submitErr != nil {
		t.Fatalf("Submit() error = %v", submitErr)
	}

	failed, err := store.MarkSubmissionFailed(t.Context(), jobID, "claim_timeout", deadline)
	if err != nil {
		t.Fatalf("MarkSubmissionFailed() error = %v", err)
	}
	if failed.Job.Outcome != model.JobOutcomeSubmissionFailed || !failed.Job.LaunchCredentialHash.Empty() {
		t.Errorf("MarkSubmissionFailed() job = %+v, want cleared submission failure", failed.Job)
	}
	assertJobSnapshot(t, store, failed.Job)
	assertEventCount(t, store, jobID, 2)
}

func TestBusyTimeoutIsClassified(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := openTestStoreAt(t, stateDir, newSequentialEventIDs(0x2700))
	second, err := Open(t.Context(), Options{
		StateDir:      stateDir,
		BusyTimeout:   20 * time.Millisecond,
		JobmanVersion: "store-test",
		Now:           storeTestTime,
		EventIDs:      newSequentialEventIDs(0x2800),
	})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Errorf("second Close() error = %v", closeErr)
		}
	})

	blocking, err := first.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin blocking transaction: %v", err)
	}
	t.Cleanup(func() {
		if rollbackErr := blocking.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			t.Errorf("rollback blocking transaction: %v", rollbackErr)
		}
	})

	credential := bytes.Repeat([]byte{0x27}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	at := storeTestTime()
	_, err = second.Submit(
		t.Context(),
		mustJobID(t, 0x27, 1),
		testJobSpec(t, "busy"),
		hash,
		at,
		at.Add(time.Minute),
	)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("Submit() error = %v, want ErrBusy", err)
	}
}

func TestTransitionRollsBackSnapshotWhenEventInsertFails(t *testing.T) {
	t.Parallel()

	duplicateEvents := &constantEventIDSource{id: mustEventID(t, 3, 1)}
	store := openTestStore(t, "event-rollback", duplicateEvents)
	jobID := mustJobID(t, 3, 2)
	supervisorID := mustSupervisorID(t, 3, 3)
	credential := bytes.Repeat([]byte{0x33}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	at := storeTestTime()
	if _, submitErr := store.Submit(t.Context(), jobID, testJobSpec(t, "rollback"), hash, at, at.Add(time.Minute)); submitErr != nil {
		t.Fatalf("Submit() error = %v", submitErr)
	}

	_, err = store.Claim(
		t.Context(),
		jobID,
		credential,
		supervisorID,
		testProcessIdentity(401, "supervisor"),
		at.Add(time.Second),
		at.Add(time.Minute),
	)
	if err == nil {
		t.Fatal("Claim() error = nil, want duplicate event failure")
	}

	job, err := store.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Phase != model.JobPhaseSubmitting || job.Revision != 1 || job.SupervisorID != "" {
		t.Errorf("job after rollback = %+v, want original submission", job)
	}
	if _, err := store.GetSupervisor(t.Context(), supervisorID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSupervisor() error = %v, want ErrNotFound", err)
	}
	assertEventCount(t, store, jobID, 1)
}

func TestConcurrentClaimUsesSnapshotCompareAndSwap(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := openTestStoreAt(t, stateDir, newSequentialEventIDs(0x4000))
	second := openTestStoreAt(t, stateDir, newSequentialEventIDs(0x5000))
	jobID := mustJobID(t, 4, 1)
	credential := bytes.Repeat([]byte{0x44}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	at := storeTestTime()
	if _, submitErr := first.Submit(t.Context(), jobID, testJobSpec(t, "cas"), hash, at, at.Add(time.Minute)); submitErr != nil {
		t.Fatalf("Submit() error = %v", submitErr)
	}
	snapshot, err := first.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	firstResult, err := model.ClaimJob(
		snapshot,
		credential,
		mustSupervisorID(t, 4, 2),
		testProcessIdentity(501, "first"),
		at.Add(time.Second),
		at.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("first ClaimJob() error = %v", err)
	}
	secondResult, err := model.ClaimJob(
		snapshot,
		credential,
		mustSupervisorID(t, 4, 3),
		testProcessIdentity(502, "second"),
		at.Add(time.Second),
		at.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("second ClaimJob() error = %v", err)
	}

	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		errorsFound <- first.commitTransition(t.Context(), firstResult)
	}()
	go func() {
		defer group.Done()
		<-start
		errorsFound <- second.commitTransition(t.Context(), secondResult)
	}()
	close(start)
	group.Wait()
	close(errorsFound)

	successes := 0
	conflicts := 0
	for err := range errorsFound {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Errorf("commitTransition() error = %v, want nil or ErrConflict", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("concurrent claims: successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}
}

func TestConcurrentCancellationUsesSnapshotCompareAndSwap(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := openTestStoreAt(t, stateDir, newSequentialEventIDs(0x7000))
	second := openTestStoreAt(t, stateDir, newSequentialEventIDs(0x8000))
	jobID := mustJobID(t, 7, 1)
	credential := bytes.Repeat([]byte{0x77}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	at := storeTestTime()
	if _, submitErr := first.Submit(
		t.Context(),
		jobID,
		testJobSpec(t, "cancel-cas"),
		hash,
		at,
		at.Add(time.Minute),
	); submitErr != nil {
		t.Fatalf("Submit() error = %v", submitErr)
	}
	claimed, err := first.Claim(
		t.Context(),
		jobID,
		credential,
		mustSupervisorID(t, 7, 2),
		testProcessIdentity(701, "cancel-cas"),
		at.Add(time.Second),
		at.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	firstRequestedAt := at.Add(2 * time.Second)
	secondRequestedAt := at.Add(3 * time.Second)
	firstResult, err := model.RequestCancellation(claimed.Job, nil, firstRequestedAt)
	if err != nil {
		t.Fatalf("first RequestCancellation() error = %v", err)
	}
	secondResult, err := model.RequestCancellation(claimed.Job, nil, secondRequestedAt)
	if err != nil {
		t.Fatalf("second RequestCancellation() error = %v", err)
	}

	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		errorsFound <- first.commitTransition(t.Context(), firstResult)
	}()
	go func() {
		defer group.Done()
		<-start
		errorsFound <- second.commitTransition(t.Context(), secondResult)
	}()
	close(start)
	group.Wait()
	close(errorsFound)

	successes := 0
	conflicts := 0
	for transitionErr := range errorsFound {
		switch {
		case transitionErr == nil:
			successes++
		case errors.Is(transitionErr, ErrConflict):
			conflicts++
		default:
			t.Errorf("commitTransition() error = %v, want nil or ErrConflict", transitionErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("concurrent cancellations: successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}

	job, err := first.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Phase != model.JobPhaseStopping || job.Revision != claimed.Job.Revision+1 || job.Cancellation == nil {
		t.Fatalf("job after concurrent cancellation = %+v", job)
	}
	if !job.Cancellation.RequestedAt.Equal(firstRequestedAt) &&
		!job.Cancellation.RequestedAt.Equal(secondRequestedAt) {
		t.Errorf("cancellation time = %s, want one of the competing requests", job.Cancellation.RequestedAt)
	}
	assertEventCount(t, first, jobID, 4)
}

func TestListAndResolveJobs(t *testing.T) {
	t.Parallel()

	store := openTestStore(t, "selectors", newSequentialEventIDs(0x6000))
	at := storeTestTime()
	jobs := []struct {
		id   model.JobID
		name string
	}{
		{id: mustJobID(t, 0x10, 1), name: "duplicate"},
		{id: mustJobID(t, 0x20, 2), name: "duplicate"},
		{id: mustJobID(t, 0x30, 3), name: "unique"},
		{id: mustJobID(t, 0x30, 4), name: "prefix-peer"},
	}
	for index, item := range jobs {
		credential := bytes.Repeat([]byte{byte(index + 1)}, 32)
		hash, err := model.NewCredentialHash(credential)
		if err != nil {
			t.Fatalf("NewCredentialHash() error = %v", err)
		}
		if _, err := store.Submit(
			t.Context(),
			item.id,
			testJobSpec(t, item.name),
			hash,
			at.Add(time.Duration(index)*time.Second),
			at.Add(time.Minute+time.Duration(index)*time.Second),
		); err != nil {
			t.Fatalf("Submit(%s) error = %v", item.id, err)
		}
	}

	listed, err := store.ListJobs(t.Context(), ListJobsOptions{})
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(listed) != len(jobs) {
		t.Fatalf("ListJobs() length = %d, want %d", len(listed), len(jobs))
	}
	if listed[0].ID != jobs[len(jobs)-1].id || listed[len(listed)-1].ID != jobs[0].id {
		t.Errorf("ListJobs() order = %v, want newest first", listed)
	}

	resolved, err := store.ResolveJob(t.Context(), jobs[2].id.String())
	if err != nil || resolved.ID != jobs[2].id {
		t.Errorf("ResolveJob(exact) = (%s, %v), want %s", resolved.ID, err, jobs[2].id)
	}
	resolved, err = store.ResolveJob(t.Context(), jobs[1].id.String()[:8])
	if err != nil || resolved.ID != jobs[1].id {
		t.Errorf("ResolveJob(prefix) = (%s, %v), want %s", resolved.ID, err, jobs[1].id)
	}
	if _, resolveErr := store.ResolveJob(t.Context(), jobs[2].id.String()[:8]); !errors.Is(resolveErr, ErrAmbiguous) {
		t.Errorf("ResolveJob(ambiguous prefix) error = %v, want ErrAmbiguous", resolveErr)
	}
	if _, resolveErr := store.ResolveJob(t.Context(), "duplicate"); !errors.Is(resolveErr, ErrAmbiguous) {
		t.Errorf("ResolveJob(ambiguous name) error = %v, want ErrAmbiguous", resolveErr)
	}
	resolved, err = store.ResolveJob(t.Context(), "unique")
	if err != nil || resolved.ID != jobs[2].id {
		t.Errorf("ResolveJob(name) = (%s, %v), want %s", resolved.ID, err, jobs[2].id)
	}
	if _, err := store.ResolveJob(t.Context(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ResolveJob(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListJobsAppliesFiltersBeforeLimit(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "filtered-list", newSequentialEventIDs(0x6100))
	at := storeTestTime()
	hash, err := model.NewCredentialHash(bytes.Repeat([]byte{0x61}, 32))
	if err != nil {
		t.Fatal(err)
	}
	olderID := mustJobID(t, 0x61, 1)
	newerID := mustJobID(t, 0x61, 2)
	if _, submitErr := database.Submit(
		t.Context(), olderID, testJobSpecWithGroups(t, "wanted", []string{"selected"}),
		hash, at, at.Add(time.Minute),
	); submitErr != nil {
		t.Fatal(submitErr)
	}
	if _, submitErr := database.Submit(
		t.Context(), newerID, testJobSpecWithGroups(t, "newest", []string{"other"}),
		hash, at.Add(time.Second), at.Add(time.Minute+time.Second),
	); submitErr != nil {
		t.Fatal(submitErr)
	}

	for name, options := range map[string]ListJobsOptions{
		"name":  {Name: "wanted", Limit: 1},
		"group": {Group: "selected", Limit: 1},
		"time":  {SubmittedBefore: at.Add(time.Second), Limit: 1},
	} {
		t.Run(name, func(t *testing.T) {
			listed, listErr := database.ListJobs(t.Context(), options)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(listed) != 1 || listed[0].ID != olderID {
				t.Fatalf("ListJobs(%s) = %+v, want older matching job %s", name, listed, olderID)
			}
		})
	}

	firstPage, err := database.ListJobs(t.Context(), ListJobsOptions{Limit: 1})
	if err != nil || len(firstPage) != 1 || firstPage[0].ID != newerID {
		t.Fatalf("ListJobs(first page) = %+v, %v", firstPage, err)
	}
	secondPage, err := database.ListJobs(t.Context(), ListJobsOptions{
		Cursor: &JobListCursor{SubmittedAt: firstPage[0].SubmittedAt, ID: firstPage[0].ID},
		Limit:  1,
	})
	if err != nil || len(secondPage) != 1 || secondPage[0].ID != olderID {
		t.Fatalf("ListJobs(second page) = %+v, %v, want %s", secondPage, err, olderID)
	}
	if _, err := database.ListJobs(t.Context(), ListJobsOptions{
		Cursor: &JobListCursor{SubmittedAt: firstPage[0].SubmittedAt}, Limit: 1,
	}); err == nil {
		t.Fatal("ListJobs(invalid cursor) error = nil")
	}

	extremes := []struct {
		name    string
		options ListJobsOptions
		want    int
	}{
		{name: "after pre-epoch", options: ListJobsOptions{SubmittedAfter: time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), Limit: 2}, want: 2},
		{name: "before pre-epoch", options: ListJobsOptions{SubmittedBefore: time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC), Limit: 2}, want: 0},
		{name: "after database range", options: ListJobsOptions{SubmittedAfter: time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), Limit: 2}, want: 0},
		{name: "before database range", options: ListJobsOptions{SubmittedBefore: time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC), Limit: 2}, want: 2},
	}
	for _, item := range extremes {
		t.Run(item.name, func(t *testing.T) {
			listed, listErr := database.ListJobs(t.Context(), item.options)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(listed) != item.want {
				t.Fatalf("ListJobs(%s) returned %d jobs, want %d", item.name, len(listed), item.want)
			}
		})
	}
}

type sequentialEventIDs struct {
	mu      sync.Mutex
	prefix  uint64
	counter uint64
}

func newSequentialEventIDs(prefix uint64) *sequentialEventIDs {
	return &sequentialEventIDs{prefix: prefix}
}

func (source *sequentialEventIDs) NewEventID() (model.EventID, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.counter++

	return model.ParseEventID(uuidText(source.prefix, source.counter))
}

type constantEventIDSource struct {
	id model.EventID
}

func (source *constantEventIDSource) NewEventID() (model.EventID, error) {
	return source.id, nil
}

func openTestStore(t *testing.T, name string, events EventIDSource) *Store {
	t.Helper()

	return openTestStoreAt(t, filepath.Join(t.TempDir(), name), events)
}

func openTestStoreAt(t *testing.T, stateDir string, events EventIDSource) *Store {
	t.Helper()

	store, err := Open(t.Context(), Options{
		StateDir:      stateDir,
		BusyTimeout:   time.Second,
		JobmanVersion: "store-test",
		Now:           storeTestTime,
		EventIDs:      events,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	return store
}

func testJobSpec(t *testing.T, name string) model.JobSpec {
	t.Helper()
	return testJobSpecWithGroups(t, name, nil)
}

func testJobSpecWithGroups(t *testing.T, name string, groups []string) model.JobSpec {
	t.Helper()

	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable:             "/test/bin/worker",
		Arguments:              []string{"--value", "with spaces"},
		WorkingDirectory:       filepath.Clean(t.TempDir()),
		Environment:            map[string]string{"JOBMAN_TEST": "1"},
		UnsetEnvironment:       []string{"UNSET_ME"},
		EnvironmentInheritance: model.EnvironmentInheritSubmission,
		Name:                   name,
		StopPolicy: model.StopPolicy{
			GracePeriod:     2 * time.Second,
			ForceAfterGrace: true,
		},
		StdinPolicy: model.StdinNull,
		ExecutionPolicy: model.ExecutionPolicy{
			Groups: groups,
		},
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}

	return specification
}

func testLogs(
	store *Store,
	jobID model.JobID,
	integrity model.LogIntegrity,
	health model.RecordingHealth,
) model.LogMetadata {
	directory := filepath.Join(store.StateDir(), "logs", jobID.String(), "1")

	return model.LogMetadata{
		StdoutPath:      filepath.Join(directory, "stdout.log"),
		StderrPath:      filepath.Join(directory, "stderr.log"),
		IndexPath:       filepath.Join(directory, "chunks.idx"),
		IndexVersion:    model.LogIndexVersion,
		Integrity:       integrity,
		RecordingHealth: health,
	}
}

func testProcessIdentity(pid int, creation string) model.ProcessIdentity {
	return model.ProcessIdentity{
		PID:        pid,
		Platform:   "test",
		CreationID: creation,
		BootID:     "test-boot",
		TreeID:     creation + "-tree",
	}
}

func assertJobSnapshot(t *testing.T, store *Store, want model.JobState) {
	t.Helper()

	got, err := store.GetJob(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetJob() = %#v, want %#v", got, want)
	}
}

func assertRunSnapshot(t *testing.T, store *Store, want model.RunState) {
	t.Helper()

	got, err := store.GetRun(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetRun() = %#v, want %#v", got, want)
	}
}

func assertSupervisorSnapshot(t *testing.T, store *Store, want model.SupervisorState) {
	t.Helper()

	got, err := store.GetSupervisor(t.Context(), want.ID)
	if err != nil {
		t.Fatalf("GetSupervisor() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GetSupervisor() = %#v, want %#v", got, want)
	}
}

func assertEventCount(t *testing.T, store *Store, jobID model.JobID, want int) {
	t.Helper()

	var got int
	if err := store.db.QueryRowContext(
		t.Context(),
		"SELECT count(*) FROM state_events WHERE job_id = ?",
		jobID.String(),
	).Scan(&got); err != nil {
		t.Fatalf("count state events: %v", err)
	}
	if got != want {
		t.Errorf("state event count = %d, want %d", got, want)
	}
}

func mustJobID(t *testing.T, prefix, suffix uint64) model.JobID {
	t.Helper()

	id, err := model.ParseJobID(uuidText(prefix, suffix))
	if err != nil {
		t.Fatalf("ParseJobID() error = %v", err)
	}

	return id
}

func mustRunID(t *testing.T, prefix uint64) model.RunID {
	t.Helper()

	id, err := model.ParseRunID(uuidText(prefix, 2))
	if err != nil {
		t.Fatalf("ParseRunID() error = %v", err)
	}

	return id
}

func mustSupervisorID(t *testing.T, prefix, suffix uint64) model.SupervisorID {
	t.Helper()

	id, err := model.ParseSupervisorID(uuidText(prefix, suffix))
	if err != nil {
		t.Fatalf("ParseSupervisorID() error = %v", err)
	}

	return id
}

func mustEventID(t *testing.T, prefix, suffix uint64) model.EventID {
	t.Helper()

	id, err := model.ParseEventID(uuidText(prefix, suffix))
	if err != nil {
		t.Fatalf("ParseEventID() error = %v", err)
	}

	return id
}

func uuidText(prefix, suffix uint64) string {
	return fmt.Sprintf("%08x-0000-7000-8000-%012x", prefix, suffix)
}

func storeTestTime() time.Time {
	return time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
}
