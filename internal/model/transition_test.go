package model

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestInitialLifecycleSuccess(t *testing.T) {
	t.Parallel()

	credential, hash := validCredential(t)
	submitted, err := NewSubmittedJob(
		testJobID,
		validSpec(t),
		hash,
		testTime,
		testTime.Add(30*time.Second),
	)
	if err != nil {
		t.Fatalf("NewSubmittedJob() error = %v", err)
	}
	assertTransition(t, submitted, JobPhaseSubmitting, "", 1, 1, []Effect{{Type: EffectLaunchSupervisor}})
	if submitted.Job.LaunchCredentialHash.Empty() || submitted.Job.ClaimDeadline == nil {
		t.Fatal("submission omitted claim material")
	}

	claimed, err := ClaimJob(
		submitted.Job,
		credential,
		testSupervisorID,
		validProcess(),
		testTime.Add(time.Second),
		testTime.Add(11*time.Second),
	)
	if err != nil {
		t.Fatalf("ClaimJob() error = %v", err)
	}
	assertTransition(t, claimed, JobPhaseStarting, "", 2, 2, nil)
	if claimed.Supervisor == nil || claimed.Supervisor.Revision != 1 {
		t.Fatalf("ClaimJob() supervisor = %#v", claimed.Supervisor)
	}
	if !claimed.Job.LaunchCredentialHash.Empty() || claimed.Job.ClaimDeadline != nil {
		t.Fatal("claim retained one-time credential material")
	}

	reserved, err := ReserveRun(
		claimed.Job,
		testRunID,
		1,
		validLogs(),
		testTime.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("ReserveRun() error = %v", err)
	}
	assertTransition(t, reserved, JobPhaseStarting, "", 3, 2, []Effect{{Type: EffectStartTarget}})
	if reserved.Run == nil || reserved.Run.Revision != 1 || reserved.Job.ActiveRunID != testRunID {
		t.Fatalf("ReserveRun() snapshots = job %#v, run %#v", reserved.Job, reserved.Run)
	}

	started, err := MarkProcessStarted(
		reserved.Job,
		*reserved.Run,
		"/usr/bin/example",
		validProcess(),
		testTime.Add(3*time.Second),
	)
	if err != nil {
		t.Fatalf("MarkProcessStarted() error = %v", err)
	}
	assertTransition(t, started, JobPhaseRunning, "", 4, 2, nil)
	if started.Run == nil || started.Run.Phase != RunPhaseRunning || started.Run.Process == nil {
		t.Fatalf("MarkProcessStarted() run = %#v", started.Run)
	}

	logs := started.Run.Logs
	logs.Integrity = LogIntegrityValid
	exitCode := 0
	exit := ExitInfo{ExitCode: &exitCode, ObservedAt: testTime.Add(4 * time.Second)}
	completed, err := FinalizeRun(
		started.Job,
		*started.Run,
		RunOutcomeSuccess,
		&exit,
		logs,
		testTime.Add(4*time.Second),
	)
	if err != nil {
		t.Fatalf("FinalizeRun() error = %v", err)
	}
	assertTransition(t, completed, JobPhaseCompleted, JobOutcomeSuccess, 5, 2, nil)
	if completed.Run == nil || completed.Run.Outcome != RunOutcomeSuccess || completed.Job.ActiveRunID != "" {
		t.Fatalf("FinalizeRun() snapshots = job %#v, run %#v", completed.Job, completed.Run)
	}

	released, event, err := ReleaseSupervisor(*claimed.Supervisor, testTime.Add(4*time.Second))
	if err != nil {
		t.Fatalf("ReleaseSupervisor() error = %v", err)
	}
	if released.Revision != 2 || released.ReleasedAt == nil || event.Type != EventSupervisorReleased {
		t.Fatalf("ReleaseSupervisor() = %#v, %#v", released, event)
	}
}

func TestClaimJobRejectsInvalidClaims(t *testing.T) {
	t.Parallel()

	job, credential := submittedJob(t)
	tests := map[string]struct {
		job        JobState
		credential []byte
		claimedAt  time.Time
	}{
		"bad credential": {
			job:        job,
			credential: bytes.Repeat([]byte{0x22}, 32),
			claimedAt:  testTime.Add(time.Second),
		},
		"expired": {
			job:        job,
			credential: credential,
			claimedAt:  *job.ClaimDeadline,
		},
	}

	claimed, err := ClaimJob(
		job,
		credential,
		testSupervisorID,
		validProcess(),
		testTime.Add(time.Second),
		testTime.Add(10*time.Second),
	)
	if err != nil {
		t.Fatalf("prepare claimed state: %v", err)
	}
	tests["replayed claim"] = struct {
		job        JobState
		credential []byte
		claimedAt  time.Time
	}{job: claimed.Job, credential: credential, claimedAt: testTime.Add(2 * time.Second)}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, claimError := ClaimJob(
				test.job,
				test.credential,
				testSupervisorID,
				validProcess(),
				test.claimedAt,
				test.claimedAt.Add(time.Second),
			)
			assertConflict(t, claimError)
		})
	}
}

func TestStartFailureCompletesJobAndRun(t *testing.T) {
	t.Parallel()

	job, run := reservedRun(t)
	logs := run.Logs
	logs.Integrity = LogIntegrityPartial
	logs.RecordingHealth = RecordingDegraded
	logs.DiagnosticCode = "log_flush_failed"

	result, err := MarkStartFailed(job, run, logs, "target_start_failed", testTime.Add(3*time.Second))
	if err != nil {
		t.Fatalf("MarkStartFailed() error = %v", err)
	}
	if result.Job.Phase != JobPhaseCompleted || result.Job.Outcome != JobOutcomeFailure {
		t.Fatalf("job = %#v", result.Job)
	}
	if result.Run == nil || result.Run.Outcome != RunOutcomeStartFailed || result.Run.Exit != nil {
		t.Fatalf("run = %#v", result.Run)
	}
	if result.Job.LastDiagnosticCode != "target_start_failed" ||
		result.Run.LastDiagnosticCode != "target_start_failed" {
		t.Fatal("diagnostic code was not preserved")
	}
}

func TestCancellationOfRunningJob(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	requestedAt := testTime.Add(4 * time.Second)
	requested, err := RequestCancellation(job, &run, requestedAt)
	if err != nil {
		t.Fatalf("RequestCancellation() error = %v", err)
	}
	if requested.Job.Phase != JobPhaseStopping || requested.Job.Cancellation == nil {
		t.Fatalf("canceling job = %#v", requested.Job)
	}
	if requested.Run == nil || requested.Run.Phase != RunPhaseStopping {
		t.Fatalf("canceling run = %#v", requested.Run)
	}
	if !reflect.DeepEqual(requested.Effects, []Effect{{Type: EffectStopTarget}}) {
		t.Fatalf("effects = %#v", requested.Effects)
	}

	repeated, err := RequestCancellation(requested.Job, requested.Run, requestedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("repeat RequestCancellation() error = %v", err)
	}
	if len(repeated.Events) != 0 || len(repeated.Effects) != 0 {
		t.Fatalf("idempotent cancellation emitted work: %#v", repeated)
	}
	if repeated.Job.Revision != requested.Job.Revision || repeated.Run.Revision != requested.Run.Revision {
		t.Fatal("idempotent cancellation changed revisions")
	}

	logs := requested.Run.Logs
	logs.Integrity = LogIntegrityValid
	exit := ExitInfo{Signal: "terminated", ObservedAt: requestedAt.Add(time.Second)}
	completed, err := FinalizeRun(
		requested.Job,
		*requested.Run,
		RunOutcomeCancelled,
		&exit,
		logs,
		requestedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("FinalizeRun() error = %v", err)
	}
	if completed.Job.Outcome != JobOutcomeCancelled || completed.Run.Outcome != RunOutcomeCancelled {
		t.Fatalf("completed cancellation = %#v", completed)
	}

	third, err := RequestCancellation(completed.Job, completed.Run, requestedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("completed idempotent cancellation error = %v", err)
	}
	if third.Job.Revision != completed.Job.Revision || len(third.Events) != 0 {
		t.Fatal("completed idempotent cancellation changed history")
	}
}

func TestCancellationBeforeRunReservation(t *testing.T) {
	t.Parallel()

	job, _ := claimedJob(t)
	requestedAt := testTime.Add(2 * time.Second)
	requested, err := RequestCancellation(job, nil, requestedAt)
	if err != nil {
		t.Fatalf("RequestCancellation() error = %v", err)
	}
	if requested.Run != nil || len(requested.Effects) != 0 || requested.Job.ActiveRunID != "" {
		t.Fatalf("pre-run cancellation emitted target work: %#v", requested)
	}

	completed, err := FinalizeCancellationWithoutRun(requested.Job, requestedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("FinalizeCancellationWithoutRun() error = %v", err)
	}
	if completed.Job.Outcome != JobOutcomeCancelled || completed.Job.Phase != JobPhaseCompleted {
		t.Fatalf("completed job = %#v", completed.Job)
	}
}

func TestCancellationWinsAfterDurableIntent(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	requested, err := RequestCancellation(job, &run, testTime.Add(4*time.Second))
	if err != nil {
		t.Fatalf("RequestCancellation() error = %v", err)
	}
	exitCode := 0
	exit := ExitInfo{ExitCode: &exitCode, ObservedAt: testTime.Add(5 * time.Second)}
	logs := requested.Run.Logs
	logs.Integrity = LogIntegrityValid

	_, err = FinalizeRun(
		requested.Job,
		*requested.Run,
		RunOutcomeSuccess,
		&exit,
		logs,
		testTime.Add(5*time.Second),
	)
	if err == nil {
		t.Fatal("FinalizeRun() accepted success after durable cancellation")
	}
}

func TestMarkSubmissionFailed(t *testing.T) {
	t.Parallel()

	job, _ := submittedJob(t)
	if _, err := MarkSubmissionFailed(
		job,
		"claim_timeout",
		job.ClaimDeadline.Add(-time.Nanosecond),
	); err == nil {
		t.Fatal("MarkSubmissionFailed() succeeded before deadline")
	}

	result, err := MarkSubmissionFailed(job, "claim_timeout", *job.ClaimDeadline)
	if err != nil {
		t.Fatalf("MarkSubmissionFailed() error = %v", err)
	}
	if result.Job.Outcome != JobOutcomeSubmissionFailed ||
		!result.Job.LaunchCredentialHash.Empty() ||
		result.Job.ClaimDeadline != nil {
		t.Fatalf("submission-failed state = %#v", result.Job)
	}
}

func TestMarkOwnershipLost(t *testing.T) {
	t.Parallel()

	t.Run("before run", func(t *testing.T) {
		t.Parallel()

		job, _ := claimedJob(t)
		result, err := MarkOwnershipLost(
			job,
			nil,
			nil,
			"supervisor_disappeared",
			testTime.Add(2*time.Second),
		)
		if err != nil {
			t.Fatalf("MarkOwnershipLost() error = %v", err)
		}
		if result.Job.Outcome != JobOutcomeLost || result.Run != nil {
			t.Fatalf("lost state = %#v", result)
		}
	})

	t.Run("canceled before run", func(t *testing.T) {
		t.Parallel()

		job, _ := claimedJob(t)
		canceling, err := RequestCancellation(job, nil, testTime.Add(2*time.Second))
		if err != nil {
			t.Fatalf("RequestCancellation() error = %v", err)
		}
		result, err := MarkOwnershipLost(
			canceling.Job,
			nil,
			nil,
			"supervisor_disappeared_during_cancellation",
			testTime.Add(3*time.Second),
		)
		if err != nil {
			t.Fatalf("MarkOwnershipLost() error = %v", err)
		}
		if result.Job.Outcome != JobOutcomeLost || result.Job.Cancellation == nil {
			t.Fatalf("lost cancellation state = %#v", result)
		}
	})

	t.Run("active run", func(t *testing.T) {
		t.Parallel()

		job, run := runningRun(t)
		logs := run.Logs
		logs.Integrity = LogIntegrityPartial
		logs.RecordingHealth = RecordingDegraded
		result, err := MarkOwnershipLost(
			job,
			&run,
			&logs,
			"target_identity_uncertain",
			testTime.Add(4*time.Second),
		)
		if err != nil {
			t.Fatalf("MarkOwnershipLost() error = %v", err)
		}
		if result.Job.Outcome != JobOutcomeLost || result.Run.Outcome != RunOutcomeLost {
			t.Fatalf("lost state = %#v", result)
		}
	})

	t.Run("cancellation remains uncertain", func(t *testing.T) {
		t.Parallel()

		job, run := runningRun(t)
		canceling, err := RequestCancellation(job, &run, testTime.Add(4*time.Second))
		if err != nil {
			t.Fatalf("RequestCancellation() error = %v", err)
		}
		logs := canceling.Run.Logs
		logs.Integrity = LogIntegrityPartial
		logs.RecordingHealth = RecordingDegraded
		result, err := MarkOwnershipLost(
			canceling.Job,
			canceling.Run,
			&logs,
			"supervisor_disappeared_during_cancellation",
			testTime.Add(5*time.Second),
		)
		if err != nil {
			t.Fatalf("MarkOwnershipLost() error = %v", err)
		}
		if result.Job.Outcome != JobOutcomeLost || result.Job.Cancellation == nil ||
			result.Run.Outcome != RunOutcomeLost {
			t.Fatalf("lost cancellation state = %#v", result)
		}
	})
}

func TestPauseJobRejectsProcessLaunchWindow(t *testing.T) {
	t.Parallel()

	job, _ := claimedJob(t)
	if _, _, err := PauseJob(job, nil, testTime.Add(2*time.Second)); err == nil {
		t.Fatal("PauseJob(starting) succeeded during process launch window")
	}
}

func TestSupervisorLeaseLifecycle(t *testing.T) {
	t.Parallel()

	_, supervisor := claimedJob(t)
	renewedAt := testTime.Add(5 * time.Second)
	renewed, event, err := RenewSupervisorLease(supervisor, renewedAt, renewedAt.Add(10*time.Second))
	if err != nil {
		t.Fatalf("RenewSupervisorLease() error = %v", err)
	}
	if renewed.Revision != supervisor.Revision+1 || event.Revision != renewed.Revision {
		t.Fatalf("renewed state/event = %#v %#v", renewed, event)
	}
	_, _, backwardError := RenewSupervisorLease(
		renewed,
		renewedAt.Add(-time.Second),
		renewedAt.Add(time.Second),
	)
	if backwardError == nil {
		t.Fatal("lease moved backward")
	}

	released, _, err := ReleaseSupervisor(renewed, renewedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("ReleaseSupervisor() error = %v", err)
	}
	repeated, event, err := ReleaseSupervisor(released, renewedAt.Add(2*time.Second))
	if err != nil {
		t.Fatalf("repeat ReleaseSupervisor() error = %v", err)
	}
	if repeated.Revision != released.Revision || event.Type != "" {
		t.Fatal("repeat release was not idempotent")
	}
	_, _, releasedError := RenewSupervisorLease(
		released,
		renewedAt.Add(2*time.Second),
		renewedAt.Add(3*time.Second),
	)
	if releasedError == nil {
		t.Fatal("renewed a released supervisor")
	}
}

func TestIllegalTransitionsReturnConflict(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	tests := map[string]func() error{
		"reserve while running": func() error {
			_, err := ReserveRun(job, testRunID, 2, validLogs(), testTime.Add(4*time.Second))

			return err
		},
		"start twice": func() error {
			_, err := MarkProcessStarted(
				job,
				run,
				"/usr/bin/example",
				validProcess(),
				testTime.Add(4*time.Second),
			)

			return err
		},
		"submission failure while running": func() error {
			_, err := MarkSubmissionFailed(job, "bad", testTime.Add(4*time.Second))

			return err
		},
	}

	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertConflict(t, operation())
		})
	}
}

func TestStateEventValidationAndDetailCopy(t *testing.T) {
	t.Parallel()

	job, _ := submittedJob(t)
	draft := jobEventWithDetails(
		job,
		EventJobSubmitted,
		"",
		string(JobPhaseSubmitting),
		testTime,
		[]byte(`{"value":1}`),
	)
	event, err := draft.WithID(testEventID)
	if err != nil {
		t.Fatalf("WithID() error = %v", err)
	}
	draft.Details[0] = '['
	if string(event.Details) != `{"value":1}` {
		t.Fatalf("event details were not copied: %s", event.Details)
	}
	if _, err := (EventDraft{
		JobID:      testJobID,
		Entity:     EntityJob,
		EntityID:   testJobID.String(),
		Type:       EventJobSubmitted,
		Revision:   1,
		OccurredAt: testTime,
		Details:    []byte(`{`),
	}).WithID(testEventID); err == nil {
		t.Fatal("WithID() accepted invalid detail JSON")
	}
}

func assertTransition(
	t *testing.T,
	result TransitionResult,
	phase JobPhase,
	outcome JobOutcome,
	revision uint64,
	eventCount int,
	effects []Effect,
) {
	t.Helper()

	if result.Job.Phase != phase || result.Job.Outcome != outcome || result.Job.Revision != revision {
		t.Fatalf("job state = phase %q outcome %q revision %d", result.Job.Phase, result.Job.Outcome, result.Job.Revision)
	}
	if len(result.Events) != eventCount {
		t.Fatalf("event count = %d, want %d", len(result.Events), eventCount)
	}
	if !reflect.DeepEqual(result.Effects, effects) {
		t.Fatalf("effects = %#v, want %#v", result.Effects, effects)
	}
	for _, event := range result.Events {
		if err := event.validate(); err != nil {
			t.Fatalf("invalid event draft: %v", err)
		}
	}
}

func assertConflict(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("operation succeeded, want conflict")
	}
	if !IsConflict(err) {
		t.Fatalf("error = %T %v, want conflict", err, err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("errors.As(%v) failed", err)
	}
}
