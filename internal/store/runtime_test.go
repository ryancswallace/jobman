package store

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestCompleteRunWithDispositionSchedulesAnotherRun(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "runtime-retry", newSequentialEventIDs(0xa000))
	now := storeTestTime()
	jobID := mustJobID(t, 0xa001, 1)
	runID := mustRunID(t, 0xa001)
	supervisorID := mustSupervisorID(t, 0xa001, 3)
	credential := submitRuntimeJob(t, database, jobID, now)
	claimRuntimeJob(t, database, jobID, supervisorID, credential, now)
	if _, err := database.TryAcquireAdmission(
		t.Context(), jobID, "", 1, now.Add(1500*time.Millisecond), time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission() error = %v", err)
	}
	logs := testLogs(database, jobID, model.LogIntegrityPending, model.RecordingHealthy)
	if _, err := database.ReserveRun(t.Context(), jobID, runID, 1, logs, now.Add(2*time.Second)); err != nil {
		t.Fatalf("ReserveRun() error = %v", err)
	}
	if _, err := database.MarkProcessStarted(
		t.Context(), jobID, runID, "/test/bin/worker", testProcessIdentity(1234, "runtime"), now.Add(3*time.Second),
	); err != nil {
		t.Fatalf("MarkProcessStarted() error = %v", err)
	}
	exitCode := 1
	exit := &model.ExitInfo{ExitCode: &exitCode, ObservedAt: now.Add(4 * time.Second)}
	logs.Integrity = model.LogIntegrityValid
	next := now.Add(9 * time.Second)
	result, err := database.CompleteRunWithDisposition(
		t.Context(),
		jobID,
		runID,
		model.RunOutcomeFailure,
		exit,
		logs,
		"exit_nonzero",
		now.Add(4*time.Second),
		model.RunDisposition{NextPhase: model.JobPhaseBackoff, NextRunAt: &next, Reason: "retryable_failure"},
	)
	if err != nil {
		t.Fatalf("CompleteRunWithDisposition() error = %v", err)
	}
	if result.Job.Phase != model.JobPhaseBackoff || result.Job.Outcome != "" || result.Job.ActiveRunID != "" {
		t.Fatalf("scheduled job = %#v", result.Job)
	}
	runtime, err := database.GetRuntime(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetRuntime() error = %v", err)
	}
	if runtime.RunCount != 1 || runtime.SuccessCount != 0 || runtime.FailureCount != 1 {
		t.Fatalf("runtime counts = %#v", runtime)
	}
	if runtime.NextRunAt == nil || !runtime.NextRunAt.Equal(next) {
		t.Fatalf("runtime next run = %v, want %v", runtime.NextRunAt, next)
	}
	owner, err := database.GetSupervisor(t.Context(), supervisorID)
	if err != nil {
		t.Fatalf("GetSupervisor() error = %v", err)
	}
	if owner.ReleasedAt != nil {
		t.Fatalf("retry released supervisor at %v", owner.ReleasedAt)
	}
	if used, err := activeSlots(t.Context(), database.db, "", false); err != nil {
		t.Fatalf("activeSlots() error = %v", err)
	} else if used != 0 {
		t.Fatalf("active admission slots after run completion = %d, want 0", used)
	}
}

func TestTerminalCompletionSerializesWithLeaseRenewal(t *testing.T) {
	t.Parallel()

	database, jobID, runID, logs, now := runningRuntimeFixture(t, 0xa050)
	job, err := database.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	exit := &model.ExitInfo{ExitCode: &exitCode, ObservedAt: now.Add(5 * time.Second)}

	database.supervisorLeaseMu.Lock()
	locked := true
	ctx := t.Context()
	t.Cleanup(func() {
		if locked {
			database.supervisorLeaseMu.Unlock()
		}
	})

	type operationResult struct {
		name string
		err  error
	}
	started := make(chan struct{}, 2)
	results := make(chan operationResult, 2)
	go func() {
		started <- struct{}{}
		_, renewErr := database.RenewLease(
			ctx, job.SupervisorID, now.Add(4*time.Second), now.Add(time.Minute),
		)
		results <- operationResult{name: "renew", err: renewErr}
	}()
	go func() {
		started <- struct{}{}
		_, completionErr := database.CompleteRunWithDisposition(
			ctx, jobID, runID, model.RunOutcomeSuccess, exit, logs, "", now.Add(5*time.Second),
			model.RunDisposition{TerminalOutcome: model.JobOutcomeSuccess},
		)
		results <- operationResult{name: "complete", err: completionErr}
	}()
	<-started
	<-started
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case result := <-results:
		t.Fatalf("%s operation bypassed supervisor lease serialization: %v", result.name, result.err)
	case <-timer.C:
	}

	database.supervisorLeaseMu.Unlock()
	locked = false
	for range 2 {
		result := <-results
		if result.name == "complete" && result.err != nil {
			t.Fatalf("terminal completion raced with lease renewal: %v", result.err)
		}
		if result.name == "renew" && result.err != nil && !model.IsConflict(result.err) && !errors.Is(result.err, ErrConflict) {
			t.Fatalf("lease renewal error = %v", result.err)
		}
	}
}

func TestMarkOwnershipLostAtomicallyReleasesAdmissionAndCountsRun(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "ownership-lost-admission", newSequentialEventIDs(0xa080))
	now := storeTestTime()
	jobID := mustJobID(t, 0xa081, 1)
	runID := mustRunID(t, 0xa081)
	supervisorID := mustSupervisorID(t, 0xa081, 3)
	credential := submitRuntimeJob(t, database, jobID, now)
	claimRuntimeJob(t, database, jobID, supervisorID, credential, now)
	if _, err := database.TryAcquireAdmission(
		t.Context(), jobID, "", 1, now.Add(time.Second), time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission() error = %v", err)
	}
	logs := testLogs(database, jobID, model.LogIntegrityPending, model.RecordingHealthy)
	if _, err := database.ReserveRun(
		t.Context(), jobID, runID, 1, logs, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("ReserveRun() error = %v", err)
	}
	if _, err := database.MarkProcessStarted(
		t.Context(), jobID, runID, "/test/bin/worker", testProcessIdentity(1234, "lost"),
		now.Add(3*time.Second),
	); err != nil {
		t.Fatalf("MarkProcessStarted() error = %v", err)
	}
	logs.Integrity = model.LogIntegrityPartial
	logs.RecordingHealth = model.RecordingDegraded
	result, err := database.MarkOwnershipLost(
		t.Context(), jobID, &logs, "owner_disappeared", now.Add(4*time.Second),
	)
	if err != nil {
		t.Fatalf("MarkOwnershipLost() error = %v", err)
	}
	if result.Job.Outcome != model.JobOutcomeLost || result.Run == nil ||
		result.Run.Outcome != model.RunOutcomeLost {
		t.Fatalf("MarkOwnershipLost() = %#v", result)
	}
	runtimeState, err := database.GetRuntime(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetRuntime() error = %v", err)
	}
	if runtimeState.RunCount != 1 || runtimeState.SuccessCount != 0 || runtimeState.FailureCount != 1 {
		t.Fatalf("runtime after ownership loss = %#v", runtimeState)
	}
	admission, found, err := database.GetAdmission(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetAdmission() error = %v", err)
	}
	if !found || admission.ReleasedAt == nil {
		t.Fatalf("admission after ownership loss = %#v, found=%t", admission, found)
	}
	assertActiveAdmissionSlots(t, database, 0)
}

func TestRecordInputEOFIsBoundToOneActiveRun(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "input-eof-run", newSequentialEventIDs(0xa090))
	now := storeTestTime()
	jobID := mustJobID(t, 0xa091, 1)
	runID := mustRunID(t, 0xa091)
	otherRunID := mustRunID(t, 0xa092)
	supervisorID := mustSupervisorID(t, 0xa091, 3)
	credential := submitRuntimeJob(t, database, jobID, now)
	claimRuntimeJob(t, database, jobID, supervisorID, credential, now)
	logs := testLogs(database, jobID, model.LogIntegrityPending, model.RecordingHealthy)
	if _, err := database.ReserveRun(
		t.Context(), jobID, runID, 1, logs, now.Add(time.Second),
	); err != nil {
		t.Fatalf("ReserveRun() error = %v", err)
	}
	if err := database.RecordInputEOF(
		t.Context(), jobID, otherRunID, now.Add(2*time.Second),
	); err == nil {
		t.Fatal("RecordInputEOF(other run) succeeded")
	}
	runtimeState, err := database.GetRuntime(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetRuntime() error = %v", err)
	}
	if runtimeState.InputEOFRequested {
		t.Fatal("wrong-run EOF mutated durable intent")
	}
	if err := database.RecordInputEOF(
		t.Context(), jobID, runID, now.Add(3*time.Second),
	); err != nil {
		t.Fatalf("RecordInputEOF(active run) error = %v", err)
	}
	if err := database.RecordInputEOF(
		t.Context(), jobID, runID, now.Add(4*time.Second),
	); err == nil {
		t.Fatal("RecordInputEOF(repeated) succeeded")
	}
}

func TestListWaitEvaluationsReturnsBoundedDiagnosticsInSpecificationOrder(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "wait-observability", newSequentialEventIDs(0xa100))
	now := storeTestTime()
	jobID := mustJobID(t, 0xa101, 1)
	submitRuntimeJob(t, database, jobID, now)
	if err := database.RecordWaitEvaluation(
		t.Context(), jobID, 1, model.WaitProbe, false, "probe_exit_nonzero", now.Add(time.Second),
	); err != nil {
		t.Fatalf("RecordWaitEvaluation(probe) error = %v", err)
	}
	if err := database.RecordWaitEvaluation(
		t.Context(), jobID, 0, model.WaitDelay, true, "", now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("RecordWaitEvaluation(delay) error = %v", err)
	}
	if err := database.RecordWaitEvaluation(
		t.Context(), jobID, 1, model.WaitProbe, true, "", now.Add(3*time.Second),
	); err != nil {
		t.Fatalf("RecordWaitEvaluation(probe satisfied) error = %v", err)
	}

	evaluations, err := database.ListWaitEvaluations(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListWaitEvaluations() error = %v", err)
	}
	if len(evaluations) != 2 || evaluations[0].ConditionIndex != 0 ||
		evaluations[1].ConditionIndex != 1 {
		t.Fatalf("ListWaitEvaluations() = %#v, want conditions 0 then 1", evaluations)
	}
	probe := evaluations[1]
	if probe.ConditionKind != model.WaitProbe || probe.AttemptCount != 2 ||
		probe.SatisfiedAt == nil || !probe.SatisfiedAt.Equal(now.Add(3*time.Second)) ||
		probe.LastDiagnosticCode != "" {
		t.Fatalf("probe evaluation = %#v", probe)
	}
}

func TestDependencyEvaluationPersistsTerminalObservation(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "dependencies", newSequentialEventIDs(0xb000))
	now := storeTestTime()
	dependencyID := mustJobID(t, 0xb001, 1)
	dependentID := mustJobID(t, 0xb001, 2)
	submitRuntimeJob(t, database, dependencyID, now)
	submitRuntimeJob(t, database, dependentID, now)
	if err := database.SetDependencies(t.Context(), dependentID, []Dependency{{
		JobID: dependentID, DependsOn: dependencyID, Predicate: DependencySubmissionFailed,
	}}); err != nil {
		t.Fatalf("SetDependencies() error = %v", err)
	}
	status, err := database.EvaluateDependencies(t.Context(), dependentID, now.Add(time.Second))
	if err != nil {
		t.Fatalf("EvaluateDependencies(pending) error = %v", err)
	}
	if status.Ready || status.Impossible || status.Pending != 1 {
		t.Fatalf("pending dependency status = %#v", status)
	}
	if _, completionErr := database.MarkSubmissionFailed(
		t.Context(), dependencyID, "test_expiry", now.Add(31*time.Second),
	); completionErr != nil {
		t.Fatalf("MarkSubmissionFailed() error = %v", completionErr)
	}
	status, err = database.EvaluateDependencies(t.Context(), dependentID, now.Add(32*time.Second))
	if err != nil {
		t.Fatalf("EvaluateDependencies(ready) error = %v", err)
	}
	if !status.Ready || status.Impossible || status.Pending != 0 {
		t.Fatalf("ready dependency status = %#v", status)
	}
	edges, err := database.ListDependencies(t.Context(), dependentID)
	if err != nil {
		t.Fatalf("ListDependencies() error = %v", err)
	}
	if len(edges) != 1 || edges[0].ObservedRevision == 0 || edges[0].SatisfiedAt == nil {
		t.Fatalf("observed dependencies = %#v", edges)
	}
	repeated, err := database.EvaluateDependencies(t.Context(), dependentID, now.Add(33*time.Second))
	if err != nil {
		t.Fatalf("EvaluateDependencies(repeated) error = %v", err)
	}
	if !repeated.Ready || repeated.Impossible || repeated.Pending != 0 {
		t.Fatalf("repeated dependency status = %#v", repeated)
	}
}

func TestDependencyEvaluationReportsImpossiblePredicate(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "dependency-impossible", newSequentialEventIDs(0xb080))
	now := storeTestTime()
	dependencyID := mustJobID(t, 0xb081, 1)
	dependentID := mustJobID(t, 0xb081, 2)
	submitRuntimeJob(t, database, dependencyID, now)
	submitRuntimeJob(t, database, dependentID, now)
	if err := database.SetDependencies(t.Context(), dependentID, []Dependency{{
		JobID: dependentID, DependsOn: dependencyID, Predicate: DependencySuccess,
	}}); err != nil {
		t.Fatalf("SetDependencies() error = %v", err)
	}
	if _, err := database.MarkSubmissionFailed(
		t.Context(), dependencyID, "test_failure", now.Add(31*time.Second),
	); err != nil {
		t.Fatalf("MarkSubmissionFailed() error = %v", err)
	}

	status, err := database.EvaluateDependencies(t.Context(), dependentID, now.Add(32*time.Second))
	if err != nil {
		t.Fatalf("EvaluateDependencies() error = %v", err)
	}
	if status.Ready || !status.Impossible || status.Pending != 0 || len(status.Failed) != 1 {
		t.Fatalf("dependency status = %#v, want one impossible dependency", status)
	}
	failed := status.Failed[0]
	if failed.DependsOn != dependencyID || failed.ObservedOutcome != model.JobOutcomeSubmissionFailed ||
		failed.SatisfiedAt != nil {
		t.Fatalf("failed dependency = %#v", failed)
	}

	repeated, err := database.EvaluateDependencies(t.Context(), dependentID, now.Add(33*time.Second))
	if err != nil {
		t.Fatalf("EvaluateDependencies(repeated) error = %v", err)
	}
	if repeated.Ready || !repeated.Impossible || len(repeated.Failed) != 1 {
		t.Fatalf("repeated dependency status = %#v", repeated)
	}
}

func TestDependencyOutcomeSetIsCanonicalAndMatchesAnySelectedOutcome(t *testing.T) {
	t.Parallel()

	predicate, err := OutcomeSetPredicate([]model.JobOutcome{
		model.JobOutcomeSuccess,
		model.JobOutcomeFailure,
		model.JobOutcomeSuccess,
	})
	if err != nil {
		t.Fatalf("OutcomeSetPredicate() error = %v", err)
	}
	if predicate != "outcomes:failure,success" {
		t.Fatalf("OutcomeSetPredicate() = %q, want canonical sorted set", predicate)
	}
	if !predicate.Valid() || !predicate.Matches(model.JobOutcomeSuccess) ||
		!predicate.Matches(model.JobOutcomeFailure) || predicate.Matches(model.JobOutcomeTimedOut) {
		t.Fatalf("outcome-set matching is inconsistent for %q", predicate)
	}
	for _, invalid := range []DependencyPredicate{
		"outcomes:",
		"outcomes:success,failure",
		"outcomes:success,success",
		"outcomes:unknown",
	} {
		if invalid.Valid() {
			t.Errorf("DependencyPredicate(%q).Valid() = true", invalid)
		}
	}

	database := openTestStore(t, "dependency-outcome-set", newSequentialEventIDs(0xb100))
	now := storeTestTime()
	dependencyID := mustJobID(t, 0xb101, 1)
	dependentID := mustJobID(t, 0xb101, 2)
	supervisorID := mustSupervisorID(t, 0xb101, 3)
	credential := submitRuntimeJob(t, database, dependencyID, now)
	submitRuntimeJob(t, database, dependentID, now)
	if dependencyErr := database.SetDependencies(t.Context(), dependentID, []Dependency{{
		JobID: dependentID, DependsOn: dependencyID, Predicate: predicate,
	}}); dependencyErr != nil {
		t.Fatalf("SetDependencies() error = %v", dependencyErr)
	}
	claimRuntimeJob(t, database, dependencyID, supervisorID, credential, now)
	completed, err := database.CompleteWithoutRun(
		t.Context(), dependencyID, model.JobOutcomeFailure, "test_failure", now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("CompleteWithoutRun() error = %v", err)
	}

	status, err := database.EvaluateDependencies(t.Context(), dependentID, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("EvaluateDependencies() error = %v", err)
	}
	if !status.Ready || status.Impossible || status.Pending != 0 || len(status.Failed) != 0 {
		t.Fatalf("dependency status = %#v, want ready", status)
	}
	edges, err := database.ListDependencies(t.Context(), dependentID)
	if err != nil {
		t.Fatalf("ListDependencies() error = %v", err)
	}
	if len(edges) != 1 || edges[0].ObservedRevision != completed.Job.Revision ||
		edges[0].ObservedOutcome != model.JobOutcomeFailure || edges[0].SatisfiedAt == nil {
		t.Fatalf("observed outcome-set dependency = %#v", edges)
	}
}

func TestSubmitWithDependenciesRollsBackJobAndEdgesAtomically(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "dependency-submit-rollback", newSequentialEventIDs(0xb200))
	now := storeTestTime()
	dependencyID := mustJobID(t, 0xb201, 1)
	dependentID := mustJobID(t, 0xb201, 2)
	submitRuntimeJob(t, database, dependencyID, now)
	credential := bytes.Repeat([]byte{0xb2}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}

	_, err = database.SubmitWithDependencies(
		t.Context(),
		dependentID,
		testJobSpec(t, "atomic-dependencies"),
		hash,
		now.Add(time.Second),
		now.Add(time.Minute),
		[]Dependency{
			{JobID: dependentID, DependsOn: dependencyID, Predicate: DependencySuccess},
			{JobID: dependentID, DependsOn: dependencyID, Predicate: DependencyFailed},
		},
	)
	if err == nil {
		t.Fatal("SubmitWithDependencies() accepted contradictory predicates")
	}
	if _, getErr := database.GetJob(t.Context(), dependentID); !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("GetJob() after failed atomic submission error = %v, want ErrNotFound", getErr)
	}

	var jobs, runtimes, dependencies, events int
	if queryErr := database.db.QueryRowContext(t.Context(), `
		SELECT
			(SELECT COUNT(*) FROM jobs WHERE id = ?),
			(SELECT COUNT(*) FROM job_runtime WHERE job_id = ?),
			(SELECT COUNT(*) FROM job_dependencies WHERE job_id = ?),
			(SELECT COUNT(*) FROM state_events WHERE job_id = ?)`,
		dependentID.String(),
		dependentID.String(),
		dependentID.String(),
		dependentID.String(),
	).Scan(&jobs, &runtimes, &dependencies, &events); queryErr != nil {
		t.Fatalf("query rows after rollback: %v", queryErr)
	}
	if jobs != 0 || runtimes != 0 || dependencies != 0 || events != 0 {
		t.Fatalf(
			"rows after failed atomic submission = jobs:%d runtime:%d dependencies:%d events:%d, want all zero",
			jobs,
			runtimes,
			dependencies,
			events,
		)
	}
}

func TestAdmissionAppliesGlobalAndNamedPoolLimits(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission", newSequentialEventIDs(0xc000))
	now := storeTestTime()
	first := mustJobID(t, 0xc001, 1)
	second := mustJobID(t, 0xc001, 2)
	submitRuntimeJob(t, database, first, now)
	submitRuntimeJob(t, database, second, now)
	global := uint64(2)
	pool := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &global, now); err != nil {
		t.Fatalf("SetConcurrencyLimit(global) error = %v", err)
	}
	if err := database.SetConcurrencyLimit(t.Context(), "network", &pool, now); err != nil {
		t.Fatalf("SetConcurrencyLimit(pool) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), first, "network", 1, now, time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission(first) error = %v", err)
	}
	admission, found, getErr := database.GetAdmission(t.Context(), first)
	if getErr != nil {
		t.Fatalf("GetAdmission(active) error = %v", getErr)
	}
	if !found || admission.JobID != first || admission.Pool != "network" ||
		admission.Slots != 1 || admission.ReleasedAt != nil {
		t.Fatalf("GetAdmission(active) = %#v, found %v", admission, found)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), second, "network", 1, now, time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(second) error = %v, want ErrCapacity", err)
	}
	if err := database.ReleaseAdmission(t.Context(), first, now.Add(time.Second)); err != nil {
		t.Fatalf("ReleaseAdmission() error = %v", err)
	}
	admission, found, getErr = database.GetAdmission(t.Context(), first)
	if getErr != nil {
		t.Fatalf("GetAdmission(released) error = %v", getErr)
	}
	if !found || admission.ReleasedAt == nil || !admission.ReleasedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("GetAdmission(released) = %#v, found %v", admission, found)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), second, "network", 1, now.Add(2*time.Second), time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission(after release) error = %v", err)
	}
}

func TestAdmissionRejectsCompletedJobsAndChangedRequestParameters(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-immutable", newSequentialEventIDs(0xc080))
	now := storeTestTime()
	capacity := uint64(3)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatal(err)
	}

	completedJob := mustJobID(t, 0xc081, 1)
	credential := submitRuntimeJob(t, database, completedJob, now)
	claimRuntimeJob(t, database, completedJob, mustSupervisorID(t, 0xc081, 2), credential, now)
	if _, err := database.CompleteWithoutRun(
		t.Context(), completedJob, model.JobOutcomeFailure, "test", now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), completedJob, "", 1, now.Add(2*time.Second), time.Minute,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed job admission error = %v, want ErrConflict", err)
	}

	activeJob := mustJobID(t, 0xc081, 3)
	submitRuntimeJob(t, database, activeJob, now)
	if _, err := database.TryAcquireAdmission(
		t.Context(), activeJob, "", 1, now.Add(3*time.Second), time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), activeJob, "", 2, now.Add(4*time.Second), time.Minute,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed active admission error = %v, want ErrConflict", err)
	}

	blockerJob := mustJobID(t, 0xc081, 4)
	submitRuntimeJob(t, database, blockerJob, now)
	if _, err := database.TryAcquireAdmission(
		t.Context(), blockerJob, "", 2, now.Add(5*time.Second), time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	queuedJob := mustJobID(t, 0xc081, 5)
	submitRuntimeJob(t, database, queuedJob, now)
	if _, err := database.TryAcquireAdmission(
		t.Context(), queuedJob, "", 1, now.Add(6*time.Second), time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("queued admission error = %v, want ErrCapacity", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), queuedJob, "", 2, now.Add(7*time.Second), time.Minute,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed queued admission error = %v, want ErrConflict", err)
	}
}

func TestValidateAdmissionRequestRejectsOversizedNamedPoolRequest(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-pool-size", newSequentialEventIDs(0xc090))
	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "build", &capacity, storeTestTime()); err != nil {
		t.Fatal(err)
	}
	if err := database.ValidateAdmissionRequest(t.Context(), "build", 2); !errors.Is(err, ErrAdmissionImpossible) {
		t.Fatalf("ValidateAdmissionRequest() error = %v, want ErrAdmissionImpossible", err)
	}
}

func TestAdmissionDoesNotBypassOlderRequestThatFits(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-fifo", newSequentialEventIDs(0xc100))
	now := storeTestTime()
	blockerOne := mustJobID(t, 0xc101, 1)
	blockerTwo := mustJobID(t, 0xc101, 2)
	older := mustJobID(t, 0xc101, 3)
	younger := mustJobID(t, 0xc101, 4)
	for _, jobID := range []model.JobID{blockerOne, blockerTwo, older, younger} {
		submitRuntimeJob(t, database, jobID, now)
	}
	capacity := uint64(3)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	for _, jobID := range []model.JobID{blockerOne, blockerTwo} {
		if _, err := database.TryAcquireAdmission(t.Context(), jobID, "", 1, now, time.Minute); err != nil {
			t.Fatalf("TryAcquireAdmission(blocker) error = %v", err)
		}
	}
	if _, err := database.TryAcquireAdmission(t.Context(), older, "", 2, now, time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(older blocked) error = %v, want ErrCapacity", err)
	}
	if err := database.ReleaseAdmission(t.Context(), blockerOne, now.Add(time.Second)); err != nil {
		t.Fatalf("ReleaseAdmission(blocker) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), younger, "", 1, now.Add(2*time.Second), time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(younger) error = %v, want older request priority", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), older, "", 2, now.Add(3*time.Second), time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission(older ready) error = %v", err)
	}
	assertActiveAdmissionSlots(t, database, 3)
	assertAdmissionRequest(t, database, older, false, 0)
	assertAdmissionRequest(t, database, younger, true, 0)
}

func TestAdmissionBreaksEqualEligibilityByJobID(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-job-id-order", newSequentialEventIDs(0xc180))
	now := storeTestTime()
	blocker := mustJobID(t, 0xc181, 1)
	lower := mustJobID(t, 0xc181, 2)
	higher := mustJobID(t, 0xc181, 3)
	if lower.String() >= higher.String() {
		t.Fatalf("fixture IDs are not ordered: %s >= %s", lower, higher)
	}
	for _, jobID := range []model.JobID{blocker, lower, higher} {
		submitRuntimeJob(t, database, jobID, now)
	}
	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), blocker, "", 1, now, time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission(blocker) error = %v", err)
	}
	eligibleAt := now.Add(time.Second)
	// Enqueue the lexically higher ID first; the durable eligibility key, not
	// SQLite insertion order, must still prioritize the lower ID.
	if _, err := database.TryAcquireAdmission(
		t.Context(), higher, "", 1, eligibleAt, time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(higher queued) error = %v, want ErrCapacity", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), lower, "", 1, eligibleAt, time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(lower queued) error = %v, want ErrCapacity", err)
	}
	if err := database.ReleaseAdmission(t.Context(), blocker, now.Add(2*time.Second)); err != nil {
		t.Fatalf("ReleaseAdmission(blocker) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), higher, "", 1, now.Add(3*time.Second), time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(higher before lower) error = %v, want ErrCapacity", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), lower, "", 1, now.Add(3*time.Second), time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission(lower) error = %v", err)
	}
}

func TestAdmissionBoundsBypassesOfBlockedOlderRequest(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-bypass", newSequentialEventIDs(0xc200))
	now := storeTestTime()
	blocker := mustJobID(t, 0xc201, 1)
	older := mustJobID(t, 0xc201, 2)
	younger := []model.JobID{
		mustJobID(t, 0xc202, 1),
		mustJobID(t, 0xc202, 2),
		mustJobID(t, 0xc202, 3),
		mustJobID(t, 0xc202, 4),
	}
	jobIDs := append([]model.JobID{blocker, older}, younger...)
	for _, jobID := range jobIDs {
		submitRuntimeJob(t, database, jobID, now)
	}
	capacity := uint64(4)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), blocker, "", 3, now, time.Minute); err != nil {
		t.Fatalf("TryAcquireAdmission(blocker) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), older, "", 2, now, time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(older) error = %v, want ErrCapacity", err)
	}

	elapsed := time.Second
	expectedBypasses := uint64(1)
	for index := range maxAdmissionBypasses {
		jobID := younger[index]
		if _, err := database.TryAcquireAdmission(
			t.Context(), jobID, "", 1, now.Add(elapsed), time.Minute,
		); err != nil {
			t.Fatalf("TryAcquireAdmission(bypass %d) error = %v", expectedBypasses, err)
		}
		assertAdmissionRequest(t, database, older, true, expectedBypasses)
		if err := database.ReleaseAdmission(t.Context(), jobID, now.Add(elapsed)); err != nil {
			t.Fatalf("ReleaseAdmission(bypass %d) error = %v", expectedBypasses, err)
		}
		elapsed += time.Second
		expectedBypasses++
	}

	blockedYounger := younger[maxAdmissionBypasses]
	if _, err := database.TryAcquireAdmission(
		t.Context(), blockedYounger, "", 1, now.Add(10*time.Second), time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(after bypass budget) error = %v, want ErrCapacity", err)
	}
	if err := database.ReleaseAdmission(t.Context(), blocker, now.Add(11*time.Second)); err != nil {
		t.Fatalf("ReleaseAdmission(blocker) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), older, "", 2, now.Add(12*time.Second), time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission(prioritized older) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), blockedYounger, "", 1, now.Add(13*time.Second), time.Minute,
	); err != nil {
		t.Fatalf("TryAcquireAdmission(younger after older) error = %v", err)
	}
	assertActiveAdmissionSlots(t, database, 3)
}

func TestAdmissionIgnoresOlderRequestWithoutSharedFiniteScope(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-independent-pools", newSequentialEventIDs(0xc300))
	now := storeTestTime()
	blocker := mustJobID(t, 0xc301, 1)
	older := mustJobID(t, 0xc301, 2)
	younger := mustJobID(t, 0xc301, 3)
	for _, jobID := range []model.JobID{blocker, older, younger} {
		submitRuntimeJob(t, database, jobID, now)
	}
	capacity := uint64(1)
	for _, pool := range []string{"one", "two"} {
		if err := database.SetConcurrencyLimit(t.Context(), pool, &capacity, now); err != nil {
			t.Fatalf("SetConcurrencyLimit(%q) error = %v", pool, err)
		}
	}
	if _, err := database.TryAcquireAdmission(t.Context(), blocker, "one", 1, now, time.Minute); err != nil {
		t.Fatalf("TryAcquireAdmission(blocker) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), older, "one", 1, now, time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(older) error = %v, want ErrCapacity", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), younger, "two", 1, now, time.Minute); err != nil {
		t.Fatalf("TryAcquireAdmission(independent younger) error = %v", err)
	}
	assertAdmissionRequest(t, database, older, true, 0)
}

func TestAdmissionDoesNotExpireCapacityWithoutRelease(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-expiry", newSequentialEventIDs(0xc400))
	now := storeTestTime()
	first := mustJobID(t, 0xc401, 1)
	second := mustJobID(t, 0xc401, 2)
	for _, jobID := range []model.JobID{first, second} {
		submitRuntimeJob(t, database, jobID, now)
	}
	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), first, "", 1, now, time.Second); err != nil {
		t.Fatalf("TryAcquireAdmission(first) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), second, "", 1, now.Add(time.Hour), time.Second,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(after lease expiry) error = %v, want ErrCapacity", err)
	}
	assertActiveAdmissionSlots(t, database, 1)
}

func TestAdmissionRejectsPermanentlyOversizedRequest(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-impossible", newSequentialEventIDs(0xc450))
	now := storeTestTime()
	jobID := mustJobID(t, 0xc451, 1)
	submitRuntimeJob(t, database, jobID, now)
	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), jobID, "", 2, now, time.Minute,
	); !errors.Is(err, ErrAdmissionImpossible) {
		t.Fatalf("TryAcquireAdmission() error = %v, want ErrAdmissionImpossible", err)
	}
	assertAdmissionRequest(t, database, jobID, false, 0)
}

func TestAdmissionRequestKeepsDurableQueuePosition(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-durable-order", newSequentialEventIDs(0xc480))
	now := storeTestTime()
	blocker := mustJobID(t, 0xc481, 1)
	pending := mustJobID(t, 0xc481, 2)
	for _, jobID := range []model.JobID{blocker, pending} {
		submitRuntimeJob(t, database, jobID, now)
	}
	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), blocker, "", 1, now, time.Minute); err != nil {
		t.Fatalf("TryAcquireAdmission(blocker) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), pending, "", 1, now, time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(pending) error = %v, want ErrCapacity", err)
	}
	sequence, enqueuedAt := admissionRequestPosition(t, database, pending)
	if _, err := database.TryAcquireAdmission(
		t.Context(), pending, "", 1, now.Add(time.Hour), time.Minute,
	); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(retry) error = %v, want ErrCapacity", err)
	}
	gotSequence, gotEnqueuedAt := admissionRequestPosition(t, database, pending)
	if gotSequence != sequence || gotEnqueuedAt != enqueuedAt {
		t.Fatalf(
			"retried request position = (%d, %d), want (%d, %d)",
			gotSequence,
			gotEnqueuedAt,
			sequence,
			enqueuedAt,
		)
	}

	reopened, err := Open(t.Context(), Options{
		StateDir: database.StateDir(), EventIDs: newSequentialEventIDs(0xc490),
	})
	if err != nil {
		t.Fatalf("Open(second handle) error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := reopened.Close(); closeErr != nil {
			t.Errorf("Close(second handle) error = %v", closeErr)
		}
	})
	gotSequence, gotEnqueuedAt = admissionRequestPosition(t, reopened, pending)
	if gotSequence != sequence || gotEnqueuedAt != enqueuedAt {
		t.Fatalf(
			"reopened request position = (%d, %d), want (%d, %d)",
			gotSequence,
			gotEnqueuedAt,
			sequence,
			enqueuedAt,
		)
	}
}

func TestConcurrentAdmissionNeverExceedsCapacity(t *testing.T) {
	t.Parallel()

	firstStore := openTestStore(t, "admission-contention", newSequentialEventIDs(0xc500))
	secondStore, err := Open(t.Context(), Options{
		StateDir: firstStore.StateDir(), EventIDs: newSequentialEventIDs(0xc600),
	})
	if err != nil {
		t.Fatalf("Open(second store) error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := secondStore.Close(); closeErr != nil {
			t.Errorf("Close(second store) error = %v", closeErr)
		}
	})
	now := storeTestTime()
	first := mustJobID(t, 0xc501, 1)
	second := mustJobID(t, 0xc501, 2)
	for _, jobID := range []model.JobID{first, second} {
		submitRuntimeJob(t, firstStore, jobID, now)
	}
	capacity := uint64(1)
	if err := firstStore.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	type admissionAttempt struct {
		store *Store
		jobID model.JobID
		at    time.Time
	}
	for _, item := range []admissionAttempt{
		{store: firstStore, jobID: first, at: now},
		{store: secondStore, jobID: second, at: now.Add(time.Nanosecond)},
	} {
		wait.Add(1)
		go func(item admissionAttempt) {
			defer wait.Done()
			<-start
			_, acquireErr := item.store.TryAcquireAdmission(
				t.Context(), item.jobID, "", 1, item.at, time.Minute,
			)
			results <- acquireErr
		}(item)
	}
	close(start)
	wait.Wait()
	close(results)

	succeeded := 0
	blocked := 0
	for acquireErr := range results {
		switch {
		case acquireErr == nil:
			succeeded++
		case errors.Is(acquireErr, ErrCapacity):
			blocked++
		default:
			t.Fatalf("TryAcquireAdmission() unexpected error = %v", acquireErr)
		}
	}
	if succeeded != 1 || blocked != 1 {
		t.Fatalf("concurrent acquisitions: succeeded=%d blocked=%d, want 1 each", succeeded, blocked)
	}
	assertActiveAdmissionSlots(t, firstStore, 1)
}

func TestTerminalJobRemovesQueuedAdmissionRequest(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "admission-terminal-cleanup", newSequentialEventIDs(0xc700))
	now := storeTestTime()
	blocker := mustJobID(t, 0xc701, 1)
	pending := mustJobID(t, 0xc701, 2)
	submitRuntimeJob(t, database, blocker, now)
	credential := submitRuntimeJob(t, database, pending, now)
	claimRuntimeJob(t, database, pending, mustSupervisorID(t, 0xc701, 3), credential, now)
	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), blocker, "", 1, now, time.Minute); err != nil {
		t.Fatalf("TryAcquireAdmission(blocker) error = %v", err)
	}
	if _, err := database.TryAcquireAdmission(t.Context(), pending, "", 1, now, time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("TryAcquireAdmission(pending) error = %v, want ErrCapacity", err)
	}
	assertAdmissionRequest(t, database, pending, true, 0)
	if _, err := database.RequestCancellation(t.Context(), pending, now.Add(2*time.Second)); err != nil {
		t.Fatalf("RequestCancellation() error = %v", err)
	}
	if _, err := database.FinalizeCancellationWithoutRun(t.Context(), pending, now.Add(3*time.Second)); err != nil {
		t.Fatalf("FinalizeCancellationWithoutRun() error = %v", err)
	}
	assertAdmissionRequest(t, database, pending, false, 0)
}

func assertActiveAdmissionSlots(t *testing.T, database *Store, want uint64) {
	t.Helper()
	got, err := activeSlots(t.Context(), database.db, "", false)
	if err != nil {
		t.Fatalf("activeSlots() error = %v", err)
	}
	if got != want {
		t.Fatalf("active admission slots = %d, want %d", got, want)
	}
}

func assertAdmissionRequest(
	t *testing.T,
	database *Store,
	jobID model.JobID,
	wantFound bool,
	wantBypasses uint64,
) {
	t.Helper()
	var count, bypasses int64
	if err := database.db.QueryRowContext(t.Context(), `
		SELECT COUNT(*), COALESCE(MAX(bypass_count), 0)
		FROM admission_requests WHERE job_id = ?`, jobID.String()).Scan(&count, &bypasses); err != nil {
		t.Fatalf("query admission request: %v", err)
	}
	found := count != 0
	if found != wantFound {
		t.Fatalf("admission request found = %t, want %t", found, wantFound)
	}
	gotBypasses, err := nonnegativeUintFromDatabase("admission bypass count", bypasses)
	if err != nil {
		t.Fatalf("decode admission bypass count: %v", err)
	}
	if found && gotBypasses != wantBypasses {
		t.Fatalf("admission request bypass count = %d, want %d", bypasses, wantBypasses)
	}
}

func admissionRequestPosition(
	t *testing.T,
	database *Store,
	jobID model.JobID,
) (sequence, enqueuedAt int64) {
	t.Helper()
	if err := database.db.QueryRowContext(t.Context(), `
		SELECT sequence, enqueued_at_ns
		FROM admission_requests WHERE job_id = ?`, jobID.String()).Scan(&sequence, &enqueuedAt); err != nil {
		t.Fatalf("query admission request position: %v", err)
	}

	return sequence, enqueuedAt
}

func TestPauseAndResumeAccountElapsedPause(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "pause-resume", newSequentialEventIDs(0xd000))
	now := storeTestTime()
	jobID := mustJobID(t, 0xd001, 1)
	supervisorID := mustSupervisorID(t, 0xd001, 2)
	credential := submitRuntimeJob(t, database, jobID, now)
	claimRuntimeJob(t, database, jobID, supervisorID, credential, now)
	if _, err := database.MoveJob(
		t.Context(), jobID, model.JobPhaseQueued, now.Add(time.Second), "test_admission_wait",
	); err != nil {
		t.Fatalf("MoveJob(queued) error = %v", err)
	}
	paused, err := database.Pause(t.Context(), jobID, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if paused.Job.Phase != model.JobPhasePaused || len(paused.Effects) != 0 {
		t.Fatalf("Pause() = %#v", paused)
	}
	resumed, err := database.Resume(t.Context(), jobID, now.Add(7*time.Second))
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Job.Phase != model.JobPhaseQueued {
		t.Fatalf("Resume().Job.Phase = %q", resumed.Job.Phase)
	}
	runtime, err := database.GetRuntime(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetRuntime() error = %v", err)
	}
	if runtime.TotalPaused != 5*time.Second || runtime.PausedAt != nil || runtime.PausedFrom != "" {
		t.Fatalf("resumed runtime = %#v", runtime)
	}
}

func submitRuntimeJob(t *testing.T, database *Store, jobID model.JobID, now time.Time) []byte {
	t.Helper()
	credential := bytes.Repeat([]byte{jobID.String()[0]}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	if _, err := database.Submit(
		t.Context(), jobID, testJobSpec(t, "runtime"), hash, now, now.Add(30*time.Second),
	); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	return credential
}

func claimRuntimeJob(
	t *testing.T,
	database *Store,
	jobID model.JobID,
	supervisorID model.SupervisorID,
	credential []byte,
	now time.Time,
) {
	t.Helper()
	if _, err := database.Claim(
		t.Context(), jobID, credential, supervisorID, testProcessIdentity(4321, "supervisor"),
		now.Add(time.Second), now.Add(time.Minute),
	); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
}
