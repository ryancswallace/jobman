package model

import (
	"reflect"
	"testing"
	"time"
)

func TestMoveJobLifecycle(t *testing.T) {
	t.Parallel()

	job, _ := claimedJob(t)
	for index, step := range []struct {
		phase JobPhase
		event EventType
	}{
		{phase: JobPhaseWaiting, event: EventJobWaiting},
		{phase: JobPhaseQueued, event: EventJobQueued},
		{phase: JobPhaseStarting, event: EventJobStarting},
	} {
		result, err := MoveJob(job, step.phase, testTime.Add(time.Duration(index+1)*time.Second), "policy")
		if err != nil {
			t.Fatalf("MoveJob(%s) error = %v", step.phase, err)
		}
		if result.Job.Phase != step.phase || len(result.Events) != 1 || result.Events[0].Type != step.event {
			t.Fatalf("MoveJob(%s) = %#v", step.phase, result)
		}
		job = result.Job
	}
	if _, err := MoveJob(job, JobPhaseRunning, testTime, ""); err == nil {
		t.Fatal("MoveJob(unsupported target) error = nil")
	}
	active, _ := runningRun(t)
	if _, err := MoveJob(active, JobPhaseWaiting, testTime, ""); !IsConflict(err) {
		t.Fatalf("MoveJob(active run) error = %v, want conflict", err)
	}
}

func TestCompleteRunTerminalAndRetry(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	logs := run.Logs
	logs.Integrity = LogIntegrityValid
	exitCode := 0
	exit := ExitInfo{ExitCode: &exitCode, ObservedAt: testTime.Add(5 * time.Second)}
	terminal, err := CompleteRun(
		job, run, RunOutcomeSuccess, &exit, logs, "", testTime.Add(5*time.Second),
		RunDisposition{TerminalOutcome: JobOutcomeSuccess},
	)
	if err != nil {
		t.Fatalf("CompleteRun(terminal) error = %v", err)
	}
	if terminal.Job.Outcome != JobOutcomeSuccess || terminal.Run == nil || terminal.Run.Outcome != RunOutcomeSuccess {
		t.Fatalf("CompleteRun(terminal) = %#v", terminal)
	}

	for _, phase := range []JobPhase{JobPhaseQueued, JobPhaseBackoff} {
		job, run = runningRun(t)
		logs = run.Logs
		logs.Integrity = LogIntegrityValid
		exitCode = 1
		exit = ExitInfo{ExitCode: &exitCode, ObservedAt: testTime.Add(5 * time.Second)}
		nextRunAt := testTime.Add(time.Minute)
		retry, retryErr := CompleteRun(
			job, run, RunOutcomeFailure, &exit, logs, "nonzero_exit", testTime.Add(5*time.Second),
			RunDisposition{NextPhase: phase, NextRunAt: &nextRunAt, Reason: "retryable"},
		)
		if retryErr != nil {
			t.Fatalf("CompleteRun(retry %s) error = %v", phase, retryErr)
		}
		if retry.Job.Phase != phase || retry.Job.ActiveRunID != "" || retry.Run == nil ||
			retry.Run.Outcome != RunOutcomeFailure || len(retry.Events) != 2 {
			t.Fatalf("CompleteRun(retry %s) = %#v", phase, retry)
		}
	}

	job, run = runningRun(t)
	if _, err := CompleteRun(
		job, run, RunOutcomeFailure, nil, run.Logs, "", testTime,
		RunDisposition{NextPhase: JobPhaseWaiting},
	); err == nil {
		t.Fatal("CompleteRun(invalid retry phase) error = nil")
	}
	if _, err := CompleteRun(
		job, run, RunOutcomeFailure, nil, run.Logs, "", testTime,
		RunDisposition{TerminalOutcome: JobOutcome("unknown")},
	); err == nil {
		t.Fatal("CompleteRun(invalid terminal outcome) error = nil")
	}
}

func TestTimeoutLifecycle(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	requestedAt := testTime.Add(5 * time.Second)
	wholeJob, err := RequestTimeout(job, &run, requestedAt)
	if err != nil {
		t.Fatalf("RequestTimeout(active) error = %v", err)
	}
	if wholeJob.Job.Phase != JobPhaseStopping || wholeJob.Run == nil ||
		wholeJob.Run.StopReason != StopReasonTimeout ||
		!reflect.DeepEqual(wholeJob.Effects, []Effect{{Type: EffectStopTarget}}) {
		t.Fatalf("RequestTimeout(active) = %#v", wholeJob)
	}
	repeatedWholeJob, err := RequestTimeout(wholeJob.Job, wholeJob.Run, requestedAt.Add(time.Second))
	if err != nil || len(repeatedWholeJob.Events) != 0 || repeatedWholeJob.Job.Revision != wholeJob.Job.Revision {
		t.Fatalf("RequestTimeout(repeated) = %#v, %v", repeatedWholeJob, err)
	}

	job, _ = claimedJob(t)
	withoutRun, err := RequestTimeout(job, nil, requestedAt)
	if err != nil || withoutRun.Run != nil || len(withoutRun.Effects) != 0 {
		t.Fatalf("RequestTimeout(without run) = %#v, %v", withoutRun, err)
	}

	job, run = runningRun(t)
	runTimeout, err := RequestRunTimeout(job, run, requestedAt)
	if err != nil {
		t.Fatalf("RequestRunTimeout() error = %v", err)
	}
	if runTimeout.Run == nil || runTimeout.Run.StopReason != StopReasonTimeout || len(runTimeout.Events) != 2 {
		t.Fatalf("RequestRunTimeout() = %#v", runTimeout)
	}
	repeatedRun, err := RequestRunTimeout(runTimeout.Job, *runTimeout.Run, requestedAt.Add(time.Second))
	if err != nil || len(repeatedRun.Events) != 0 || repeatedRun.Job.Revision != runTimeout.Job.Revision {
		t.Fatalf("RequestRunTimeout(repeated) = %#v, %v", repeatedRun, err)
	}
	completed := terminalRun(t)
	unchangedResult, err := RequestRunTimeout(completed.Job, completed.Run, requestedAt)
	if err != nil || len(unchangedResult.Events) != 0 {
		t.Fatalf("RequestRunTimeout(completed) = %#v, %v", unchangedResult, err)
	}
}

func TestCompleteWithoutRun(t *testing.T) {
	t.Parallel()

	for _, outcome := range []JobOutcome{JobOutcomeAborted, JobOutcomeTimedOut} {
		job, _ := claimedJob(t)
		result, err := CompleteWithoutRun(job, outcome, "prerequisite_failed", testTime.Add(5*time.Second))
		if err != nil {
			t.Fatalf("CompleteWithoutRun(%s) error = %v", outcome, err)
		}
		if result.Job.Outcome != outcome || result.Job.CompletedAt == nil || len(result.Events) != 1 {
			t.Fatalf("CompleteWithoutRun(%s) = %#v", outcome, result)
		}
		wantEvent := EventJobAborted
		if outcome == JobOutcomeTimedOut {
			wantEvent = EventTimeout
		}
		if result.Events[0].Type != wantEvent {
			t.Fatalf("CompleteWithoutRun(%s) event = %s", outcome, result.Events[0].Type)
		}
	}
	job, _ := claimedJob(t)
	if _, err := CompleteWithoutRun(job, JobOutcomeSuccess, "", testTime); err == nil {
		t.Fatal("CompleteWithoutRun(success) error = nil")
	}
	active, _ := runningRun(t)
	if _, err := CompleteWithoutRun(active, JobOutcomeAborted, "", testTime); !IsConflict(err) {
		t.Fatalf("CompleteWithoutRun(active) error = %v, want conflict", err)
	}
}

func TestPauseAndResumeLifecycle(t *testing.T) {
	t.Parallel()

	job, _ := claimedJob(t)
	queued, err := MoveJob(job, JobPhaseWaiting, testTime.Add(2*time.Second), "wait")
	if err != nil {
		t.Fatalf("MoveJob(waiting) error = %v", err)
	}
	paused, prior, err := PauseJob(queued.Job, nil, testTime.Add(3*time.Second))
	if err != nil || prior != JobPhaseWaiting {
		t.Fatalf("PauseJob(waiting) = %#v, %s, %v", paused, prior, err)
	}
	resumed, err := ResumeJob(paused.Job, nil, prior, testTime.Add(4*time.Second))
	if err != nil || resumed.Job.Phase != JobPhaseWaiting {
		t.Fatalf("ResumeJob(waiting) = %#v, %v", resumed, err)
	}

	job, run := runningRun(t)
	paused, prior, err = PauseJob(job, &run, testTime.Add(5*time.Second))
	if err != nil || paused.Run == nil || paused.Run.Phase != RunPhasePaused ||
		!reflect.DeepEqual(paused.Effects, []Effect{{Type: EffectPauseTarget}}) {
		t.Fatalf("PauseJob(running) = %#v, %s, %v", paused, prior, err)
	}
	repeated, repeatedPrior, err := PauseJob(paused.Job, paused.Run, testTime.Add(6*time.Second))
	if err != nil || repeatedPrior != JobPhasePaused || len(repeated.Events) != 0 {
		t.Fatalf("PauseJob(repeated) = %#v, %s, %v", repeated, repeatedPrior, err)
	}
	resumed, err = ResumeJob(paused.Job, paused.Run, prior, testTime.Add(7*time.Second))
	if err != nil || resumed.Run == nil || resumed.Run.Phase != RunPhaseRunning ||
		!reflect.DeepEqual(resumed.Effects, []Effect{{Type: EffectResumeTarget}}) {
		t.Fatalf("ResumeJob(running) = %#v, %v", resumed, err)
	}
	if _, err := ResumeJob(resumed.Job, resumed.Run, prior, testTime); !IsConflict(err) {
		t.Fatalf("ResumeJob(not paused) error = %v", err)
	}
	if _, err := ResumeJob(paused.Job, paused.Run, JobPhasePaused, testTime); err == nil {
		t.Fatal("ResumeJob(invalid prior) error = nil")
	}
}

func terminalRun(t *testing.T) struct {
	Job JobState
	Run RunState
} {
	t.Helper()
	job, run := runningRun(t)
	logs := run.Logs
	logs.Integrity = LogIntegrityValid
	exitCode := 0
	result, err := FinalizeRun(
		job,
		run,
		RunOutcomeSuccess,
		&ExitInfo{ExitCode: &exitCode, ObservedAt: testTime.Add(5 * time.Second)},
		logs,
		testTime.Add(5*time.Second),
	)
	if err != nil {
		t.Fatalf("FinalizeRun() error = %v", err)
	}
	if result.Run == nil {
		t.Fatal("FinalizeRun() run = nil")
	}

	return struct {
		Job JobState
		Run RunState
	}{Job: result.Job, Run: *result.Run}
}

func TestLifecycleRejectsInvalidState(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	invalidJob := job
	invalidJob.Revision = 0
	operations := []func() error{
		func() error { _, err := MoveJob(invalidJob, JobPhaseWaiting, testTime, ""); return err },
		func() error { _, err := RequestTimeout(invalidJob, &run, testTime); return err },
		func() error { _, err := CompleteWithoutRun(invalidJob, JobOutcomeAborted, "", testTime); return err },
		func() error { _, _, err := PauseJob(invalidJob, &run, testTime); return err },
	}
	for _, operation := range operations {
		if err := operation(); err == nil {
			t.Fatal("operation accepted invalid state")
		}
	}
	if _, err := RequestRunTimeout(job, RunState{}, testTime); err == nil {
		t.Fatal("RequestRunTimeout(mismatched run) error = nil")
	}
}

func TestLifecycleRejectsInvalidRunCompletionAndPairing(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	logs := run.Logs
	logs.Integrity = LogIntegrityValid
	validDisposition := RunDisposition{NextPhase: JobPhaseQueued}

	wrongRun := run
	wrongRun.ID = RunID("01890f4e-4c00-7000-8000-000000000099")
	if _, err := CompleteRun(
		job, wrongRun, RunOutcomeFailure, nil, logs, "", testTime.Add(5*time.Second), validDisposition,
	); err == nil {
		t.Fatal("CompleteRun(mismatched run) error = nil")
	}
	if _, err := CompleteRun(
		job, run, RunOutcome("unknown"), nil, logs, "", testTime.Add(5*time.Second), validDisposition,
	); err == nil {
		t.Fatal("CompleteRun(invalid outcome) error = nil")
	}
	invalidLogs := logs
	invalidLogs.Integrity = LogIntegrity("unknown")
	if _, err := CompleteRun(
		job, run, RunOutcomeFailure, nil, invalidLogs, "", testTime.Add(5*time.Second), validDisposition,
	); err == nil {
		t.Fatal("CompleteRun(invalid logs) error = nil")
	}
	if _, err := CompleteRun(
		job, run, RunOutcomeFailure, nil, logs, "", run.ReservedAt.Add(-time.Second), validDisposition,
	); err == nil {
		t.Fatal("CompleteRun(early completion) error = nil")
	}
	exitCode := -1
	invalidExit := ExitInfo{ExitCode: &exitCode, ObservedAt: testTime.Add(5 * time.Second)}
	if _, err := CompleteRun(
		job, run, RunOutcomeFailure, &invalidExit, logs, "", testTime.Add(5*time.Second), validDisposition,
	); err == nil {
		t.Fatal("CompleteRun(invalid exit) error = nil")
	}

	completed := terminalRun(t)
	unchangedResult, unchangedErr := RequestTimeout(completed.Job, &completed.Run, testTime.Add(time.Hour))
	if unchangedErr != nil || len(unchangedResult.Events) != 0 {
		t.Fatalf("RequestTimeout(completed) = %#v, %v", unchangedResult, unchangedErr)
	}
	if _, err := RequestTimeout(job, &wrongRun, testTime); err == nil {
		t.Fatal("RequestTimeout(mismatched run) error = nil")
	}
	if _, _, err := PauseJob(job, &wrongRun, testTime); err == nil {
		t.Fatal("PauseJob(mismatched run) error = nil")
	}

	pausedWithoutRun, prior, err := PauseJob(job, nil, testTime.Add(5*time.Second))
	if err != nil {
		t.Fatalf("PauseJob(without supplied run) error = %v", err)
	}
	if _, err := ResumeJob(pausedWithoutRun.Job, &run, prior, testTime.Add(6*time.Second)); err == nil {
		t.Fatal("ResumeJob(non-paused run) error = nil")
	}
}
