package model

import (
	"testing"
	"time"
)

// These tests exercise transition boundaries where every input snapshot is
// valid, but the requested timestamp or terminal disposition would make the
// resulting snapshot invalid. Keeping those checks covered is important: store
// callers may race with clocks or persisted cancellation intent even when the
// state they loaded was internally consistent.
func TestTransitionRejectsInvalidResultingTimestamps(t *testing.T) {
	t.Parallel()

	submitted, credential := submittedJob(t)
	if _, err := ClaimJob(
		submitted,
		credential,
		testSupervisorID,
		validProcess(),
		submitted.SubmittedAt.Add(-time.Second),
		submitted.SubmittedAt.Add(time.Second),
	); err == nil {
		t.Fatal("ClaimJob(claim before submission) error = nil")
	}

	claimed, _ := claimedJob(t)
	if _, err := CompleteWithoutRun(
		claimed,
		JobOutcomeAborted,
		"deadline",
		claimed.SubmittedAt.Add(-time.Second),
	); err == nil {
		t.Fatal("CompleteWithoutRun(completion before submission) error = nil")
	}
	if _, err := MarkOwnershipLost(
		claimed,
		nil,
		nil,
		"lease_expired",
		claimed.SubmittedAt.Add(-time.Second),
	); err == nil {
		t.Fatal("MarkOwnershipLost(completion before submission) error = nil")
	}

	runningJob, runningRun := runningRun(t)
	earlyStop := runningRun.ReservedAt.Add(-time.Nanosecond)
	if _, err := RequestCancellation(runningJob, &runningRun, earlyStop); err == nil {
		t.Fatal("RequestCancellation(stop before reservation) error = nil")
	}
	if _, err := RequestTimeout(runningJob, &runningRun, earlyStop); err == nil {
		t.Fatal("RequestTimeout(stop before reservation) error = nil")
	}
	if _, err := RequestRunTimeout(runningJob, runningRun, earlyStop); err == nil {
		t.Fatal("RequestRunTimeout(stop before reservation) error = nil")
	}

	logs := runningRun.Logs
	logs.Integrity = LogIntegrityValid
	earlyCompletion := runningRun.StartedAt.Add(-time.Nanosecond)
	exitCode := 0
	if _, completionErr := CompleteRun(
		runningJob,
		runningRun,
		RunOutcomeSuccess,
		&ExitInfo{ExitCode: &exitCode, ObservedAt: earlyCompletion},
		logs,
		"",
		earlyCompletion,
		RunDisposition{TerminalOutcome: JobOutcomeSuccess},
	); completionErr == nil {
		t.Fatal("CompleteRun(completion before process start) error = nil")
	}
}

func TestTransitionRejectsInconsistentTerminalDispositions(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	stopAt := run.StartedAt.Add(time.Second)
	stopping, err := RequestTimeout(job, &run, stopAt)
	if err != nil {
		t.Fatal(err)
	}
	logs := stopping.Run.Logs
	logs.Integrity = LogIntegrityValid
	exit := ExitInfo{PlatformReason: "timeout", ObservedAt: stopAt.Add(time.Second)}

	if _, completionErr := CompleteRun(
		stopping.Job,
		*stopping.Run,
		RunOutcomeTimedOut,
		&exit,
		logs,
		"job_timeout",
		exit.ObservedAt,
		RunDisposition{TerminalOutcome: JobOutcomeFailure},
	); completionErr == nil {
		t.Fatal("CompleteRun(timeout intent with failure outcome) error = nil")
	}

	if _, completionErr := CompleteRun(
		stopping.Job,
		*stopping.Run,
		RunOutcomeTimedOut,
		&exit,
		logs,
		"job_timeout",
		exit.ObservedAt,
		RunDisposition{NextPhase: JobPhaseQueued},
	); completionErr == nil {
		t.Fatal("CompleteRun(retry after whole-job timeout) error = nil")
	}

	job, run = runningRun(t)
	logs = run.Logs
	logs.Integrity = LogIntegrityValid
	if _, completionErr := CompleteRun(
		job,
		run,
		RunOutcomeSuccess,
		nil,
		logs,
		"",
		run.StartedAt.Add(time.Second),
		RunDisposition{NextPhase: JobPhaseQueued},
	); completionErr == nil {
		t.Fatal("CompleteRun(success without factual exit) error = nil")
	}

	// Exercise the timeout-specific cancellation outcome branch with a fully
	// consistent result as a counterpart to the rejection cases above.
	job, run = runningRun(t)
	stopping, err = RequestTimeout(job, &run, stopAt)
	if err != nil {
		t.Fatal(err)
	}
	logs = stopping.Run.Logs
	logs.Integrity = LogIntegrityValid
	completed, err := FinalizeRun(
		stopping.Job,
		*stopping.Run,
		RunOutcomeTimedOut,
		&exit,
		logs,
		exit.ObservedAt,
	)
	if err != nil || completed.Job.Outcome != JobOutcomeTimedOut {
		t.Fatalf("FinalizeRun(timeout) = (%+v, %v)", completed, err)
	}
}

func TestCancellationTransitionRejectsInvalidTargets(t *testing.T) {
	t.Parallel()

	completed := terminalRun(t)
	if _, err := RequestCancellation(completed.Job, &completed.Run, testTime.Add(time.Hour)); err == nil {
		t.Fatal("RequestCancellation(completed target) error = nil")
	}
	if _, err := MarkStartFailed(
		completed.Job,
		completed.Run,
		completed.Run.Logs,
		"",
		testTime.Add(time.Hour),
	); err == nil {
		t.Fatal("MarkStartFailed(empty diagnostic) error = nil")
	}

	reservedJob, _ := reservedRun(t)
	completedRun := completed.Run
	if err := validateCancellationTarget(reservedJob, &completedRun); err == nil {
		t.Fatal("validateCancellationTarget(completed run) error = nil")
	}
	mismatched := completedRun
	mismatched.ID = RunID(testEventID)
	if err := validateCancellationTarget(reservedJob, &mismatched); err == nil {
		t.Fatal("validateCancellationTarget(mismatched run) error = nil")
	}
}

func TestResumeRejectsPhaseThatNeedsAnActiveRun(t *testing.T) {
	t.Parallel()

	claimed, _ := claimedJob(t)
	queued, err := MoveJob(claimed, JobPhaseQueued, testTime.Add(time.Second), "ready")
	if err != nil {
		t.Fatal(err)
	}
	paused, _, err := PauseJob(queued.Job, nil, testTime.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResumeJob(paused.Job, nil, JobPhaseRunning, testTime.Add(3*time.Second)); err == nil {
		t.Fatal("ResumeJob(running without active run) error = nil")
	}
}
