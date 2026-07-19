package model

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestJobInvariantValidationBranches(t *testing.T) {
	t.Parallel()
	credential, hash := validCredential(t)
	submitted, _ := submittedJob(t)
	claimed, _ := claimedJob(t)
	running, _ := runningRun(t)
	terminal := terminalRun(t).Job

	tests := map[string]func() error{
		"invalid active run ID": func() error {
			value := claimed
			value.ActiveRunID = "invalid"
			return validateOptionalJobIDs(value)
		},
		"invalid supervisor ID": func() error {
			value := claimed
			value.SupervisorID = "invalid"
			return validateOptionalJobIDs(value)
		},
		"completed fields missing": func() error {
			value := terminal
			value.Outcome = ""
			return validateJobTerminalFields(value)
		},
		"completed active run": func() error {
			value := terminal
			value.ActiveRunID = testRunID
			return validateJobTerminalFields(value)
		},
		"active terminal fields": func() error {
			value := claimed
			value.Outcome = JobOutcomeFailure
			return validateJobTerminalFields(value)
		},
		"submitting without claim material": func() error {
			value := submitted
			value.LaunchCredentialHash = CredentialHash{}
			return validateJobClaimFields(value)
		},
		"submitting with owner": func() error {
			value := submitted
			value.SupervisorID = testSupervisorID
			return validateJobClaimFields(value)
		},
		"claimed with credential": func() error {
			value := claimed
			value.LaunchCredentialHash = hash
			return validateJobClaimFields(value)
		},
		"running without active run": func() error {
			value := running
			value.ActiveRunID = ""
			return validateJobOwnershipFields(value)
		},
		"stopping without stop target": func() error {
			value := claimed
			value.Phase = JobPhaseStopping
			return validateJobOwnershipFields(value)
		},
		"owned without supervisor": func() error {
			value := claimed
			value.SupervisorID = ""
			return validateJobOwnershipFields(value)
		},
		"running without start": func() error {
			value := running
			value.StartedAt = nil
			return validateJobOwnershipFields(value)
		},
		"stop intent without time": func() error {
			value := claimed
			value.Phase = JobPhaseStopping
			value.Cancellation = &CancellationIntent{Reason: StopReasonCancellation}
			return validateJobCancellationFields(value)
		},
		"stop intent with invalid reason": func() error {
			value := claimed
			value.Phase = JobPhaseStopping
			value.Cancellation = &CancellationIntent{RequestedAt: testTime, Reason: "invalid"}
			return validateJobCancellationFields(value)
		},
		"stop intent in active phase": func() error {
			value := claimed
			value.Cancellation = &CancellationIntent{RequestedAt: testTime, Reason: StopReasonCancellation}
			return validateJobCancellationFields(value)
		},
		"timestamp before submission": func() error {
			value := claimed
			value.ClaimedAt = timePointer(value.SubmittedAt.Add(-time.Second))
			return validateJobTimes(value)
		},
		"claim deadline before submission": func() error {
			value := submitted
			value.ClaimDeadline = timePointer(value.SubmittedAt)
			return validateJobTimes(value)
		},
		"start before claim": func() error {
			value := running
			value.StartedAt = timePointer(value.ClaimedAt.Add(-time.Second))
			return validateJobTimes(value)
		},
		"completion before start": func() error {
			value := terminal
			value.CompletedAt = timePointer(value.StartedAt.Add(-time.Second))
			return validateJobTimes(value)
		},
		"invalid specification": func() error {
			value := submitted
			value.Spec = JobSpec{}
			return value.Validate()
		},
		"invalid outcome": func() error {
			value := submitted
			value.Outcome = "invalid"
			return value.Validate()
		},
		"valid cancellation absence": func() error {
			return validateJobCancellationFields(claimed)
		},
	}
	_ = credential
	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := operation()
			if name == "valid cancellation absence" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("validation error = nil")
			}
		})
	}
}

func TestJobSpecificationStrictJSONEdges(t *testing.T) {
	t.Parallel()

	specification := validSpec(t)
	canonical, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, parseErr := ParseJobSpecJSON(append(append([]byte(nil), canonical...), []byte(` {}`)...)); parseErr == nil {
		t.Fatal("ParseJobSpecJSON() accepted trailing data")
	}
	var decoded map[string]any
	if unmarshalErr := json.Unmarshal(canonical, &decoded); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	delete(decoded, "execution_policy")
	missingPolicy, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseJobSpecJSON(missingPolicy); err == nil {
		t.Fatal("ParseJobSpecJSON() accepted a current specification without an execution policy")
	}
	var unmarshaled JobSpec
	if err := json.Unmarshal(canonical, &unmarshaled); err != nil {
		t.Fatal(err)
	}
	if unmarshaled.Executable() != specification.Executable() {
		t.Fatalf("unmarshaled executable = %q", unmarshaled.Executable())
	}
	if err := requireJSONEnd(json.NewDecoder(bytes.NewBufferString(`]`))); err == nil {
		t.Fatal("requireJSONEnd() accepted malformed trailing JSON")
	}

	wire := executionPolicyToWire(DefaultExecutionPolicy())
	wire.LogRetentionMaxAge = ""
	if _, err := executionPolicyFromWire(wire); err != nil {
		t.Fatalf("executionPolicyFromWire(blank legacy retention) = %v", err)
	}
	if validHTTPHeaderName("") {
		t.Fatal("validHTTPHeaderName() accepted an empty name")
	}
	job, _ := submittedJob(t)
	if err := validateEventEntity(EventDraft{Entity: "unknown", JobID: job.ID}); err == nil {
		t.Fatal("validateEventEntity() accepted an unknown entity")
	}
}

func TestRunInvariantValidationBranches(t *testing.T) {
	t.Parallel()
	_, reserved := reservedRun(t)
	_, running := runningRun(t)
	completed := terminalRun(t).Run
	stoppingJob, stoppingRun := runningRun(t)
	stopping, err := RequestCancellation(stoppingJob, &stoppingRun, testTime.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func() error{
		"completed missing fields": func() error {
			value := completed
			value.Outcome = ""
			return validateRunTerminalFields(value)
		},
		"completed pending logs": func() error {
			value := completed
			value.Logs.Integrity = LogIntegrityPending
			return validateRunTerminalFields(value)
		},
		"active terminal fields": func() error {
			value := reserved
			value.Outcome = RunOutcomeFailure
			return validateRunTerminalFields(value)
		},
		"active process fields": func() error {
			value := running
			value.Process = nil
			return validateRunProcessFields(value)
		},
		"stopping incomplete published process": func() error {
			value := *stopping.Run
			value.StartedAt = nil
			return validateRunProcessFields(value)
		},
		"invalid process": func() error {
			value := running
			value.Process = processPointer(ProcessIdentity{PID: -1})
			return validateRunProcessFields(value)
		},
		"stopping without intent": func() error {
			value := *stopping.Run
			value.StopRequestedAt = nil
			return validateRunStopFields(value)
		},
		"invalid exit": func() error {
			value := completed
			negative := -1
			value.Exit = &ExitInfo{ExitCode: &negative, ObservedAt: *value.CompletedAt}
			return validateRunExitFields(value)
		},
		"exit on active run": func() error {
			value := running
			zero := 0
			value.Exit = &ExitInfo{ExitCode: &zero, ObservedAt: *value.StartedAt}
			return validateRunExitFields(value)
		},
		"completed missing exit": func() error {
			value := completed
			value.Exit = nil
			return validateRunExitFields(value)
		},
		"success missing exit code": func() error {
			value := completed
			value.Exit = &ExitInfo{Signal: "terminated", ObservedAt: *value.CompletedAt}
			return validateRunExitFields(value)
		},
		"timestamp before reservation": func() error {
			value := running
			value.StartedAt = timePointer(value.ReservedAt.Add(-time.Second))
			return validateRunTimes(value)
		},
		"completion before start": func() error {
			value := completed
			value.CompletedAt = timePointer(value.StartedAt.Add(-time.Second))
			return validateRunTimes(value)
		},
		"exit after completion": func() error {
			value := completed
			exit := *value.Exit
			exit.ObservedAt = value.CompletedAt.Add(time.Second)
			value.Exit = &exit
			return validateRunTimes(value)
		},
		"pruned active run": func() error {
			value := running
			value.Logs.PrunedAt = timePointer(testTime.Add(time.Hour))
			return validateRunTimes(value)
		},
		"pruned before completion": func() error {
			value := completed
			value.Logs.PrunedAt = timePointer(value.CompletedAt.Add(-time.Second))
			return validateRunTimes(value)
		},
	}
	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); err == nil {
				t.Fatal("validation error = nil")
			}
		})
	}
}

func TestRemainingLifecycleTransitionContracts(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	completedAt := run.ReservedAt.Add(time.Second)
	exitCode := 0
	exit := &ExitInfo{ExitCode: &exitCode, ObservedAt: completedAt}
	logs := run.Logs
	logs.Integrity = LogIntegrityValid

	terminal, err := CompleteRun(job, run, RunOutcomeSuccess, exit, logs, "", completedAt, RunDisposition{
		TerminalOutcome: JobOutcomeSuccess,
	})
	if err != nil || terminal.Job.Outcome != JobOutcomeSuccess {
		t.Fatalf("CompleteRun(terminal) = (%+v, %v)", terminal, err)
	}
	if _, completionErr := CompleteRun(job, run, RunOutcomeSuccess, exit, logs, "", completedAt, RunDisposition{
		TerminalOutcome: JobOutcome("unknown"),
	}); completionErr == nil {
		t.Fatal("CompleteRun(invalid terminal outcome) error = nil")
	}

	timedOut, err := RequestTimeout(job, &run, completedAt)
	if err != nil || len(timedOut.Effects) != 1 {
		t.Fatalf("RequestTimeout(running) = (%+v, %v)", timedOut, err)
	}
	unchangedTimeout, err := RequestTimeout(timedOut.Job, timedOut.Run, completedAt.Add(time.Second))
	if err != nil || len(unchangedTimeout.Events) != 0 {
		t.Fatalf("RequestTimeout(repeated) = (%+v, %v)", unchangedTimeout, err)
	}
	mismatchedRun := run
	mismatchedRun.JobID = JobID(testEventID)
	if _, timeoutErr := RequestTimeout(job, &mismatchedRun, completedAt); timeoutErr == nil {
		t.Fatal("RequestTimeout(mismatched run) error = nil")
	}
	if _, timeoutErr := RequestRunTimeout(job, mismatchedRun, completedAt); timeoutErr == nil {
		t.Fatal("RequestRunTimeout(mismatched run) error = nil")
	}

	claimed, _ := claimedJob(t)
	if _, completionErr := CompleteWithoutRun(claimed, JobOutcomeSuccess, "invalid", completedAt); completionErr == nil {
		t.Fatal("CompleteWithoutRun(success) error = nil")
	}
	canceled, err := RequestCancellation(claimed, nil, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FinalizeCancellationWithoutRun(canceled.Job, completedAt.Add(-time.Second)); err == nil {
		t.Fatal("FinalizeCancellationWithoutRun(early) error = nil")
	}
	invalidJob := claimed
	invalidJob.ID = "invalid"
	if _, err := MarkSubmissionFailed(invalidJob, "expired", completedAt); err == nil {
		t.Fatal("MarkSubmissionFailed(invalid job) error = nil")
	}
	if _, err := MarkOwnershipLost(claimed, &run, nil, "lost", completedAt); err == nil {
		t.Fatal("MarkOwnershipLost(missing logs) error = nil")
	}
	if _, err := MarkOwnershipLost(claimed, nil, nil, "", completedAt); err == nil {
		t.Fatal("MarkOwnershipLost(empty diagnostic) error = nil")
	}

	_, supervisor := claimedJob(t)
	invalidSupervisor := supervisor
	invalidSupervisor.ID = "invalid"
	if _, _, err := RenewSupervisorLease(invalidSupervisor, completedAt, completedAt.Add(time.Second)); err == nil {
		t.Fatal("RenewSupervisorLease(invalid state) error = nil")
	}
	if _, _, err := ReleaseSupervisor(invalidSupervisor, completedAt); err == nil {
		t.Fatal("ReleaseSupervisor(invalid state) error = nil")
	}
	if _, _, err := ReleaseSupervisor(supervisor, supervisor.ClaimedAt.Add(-time.Second)); err == nil {
		t.Fatal("ReleaseSupervisor(early) error = nil")
	}
}

func TestRemainingCompletionAndCancellationValidation(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	completedAt := run.ReservedAt.Add(time.Second)
	logs := run.Logs
	logs.Integrity = LogIntegrityValid
	invalidLogs := logs
	invalidLogs.StdoutSize = -1
	if _, err := completeRunTransition(job, run, RunOutcomeSuccess, nil, invalidLogs, "", completedAt, EventRunCompleted); err == nil {
		t.Fatal("completeRunTransition(invalid logs) error = nil")
	}
	if _, err := completeRunTransition(job, run, RunOutcomeSuccess, nil, logs, "", run.ReservedAt.Add(-time.Second), EventRunCompleted); err == nil {
		t.Fatal("completeRunTransition(early completion) error = nil")
	}
	badExitCode := -1
	if _, err := completeRunTransition(job, run, RunOutcomeSuccess, &ExitInfo{
		ExitCode: &badExitCode, ObservedAt: completedAt,
	}, logs, "", completedAt, EventRunCompleted); err == nil {
		t.Fatal("completeRunTransition(invalid exit) error = nil")
	}
	pausedJob, _, err := PauseJob(job, &run, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := completeRunTransition(pausedJob.Job, *pausedJob.Run, RunOutcomeSuccess, nil, logs, "", completedAt, EventRunCompleted); err == nil {
		t.Fatal("completeRunTransition(paused pair) error = nil")
	}

	claimed, _ := claimedJob(t)
	claimed.ActiveRunID = testRunID
	if err := validateCancellationTarget(claimed, nil); err == nil {
		t.Fatal("validateCancellationTarget(missing run) error = nil")
	}
	completed := terminalRun(t)
	if err := validateCancellationTarget(completed.Job, nil); err == nil {
		t.Fatal("validateCancellationTarget(completed job) error = nil")
	}
}

func TestEventInvariantValidationBranches(t *testing.T) {
	t.Parallel()
	base := EventDraft{
		JobID: testJobID, Entity: EntityJob, EntityID: testJobID.String(),
		Type: EventJobSubmitted, Revision: 1, OccurredAt: testTime,
	}
	tests := map[string]func(*EventDraft){
		"job ID":        func(value *EventDraft) { value.JobID = "invalid" },
		"identity":      func(value *EventDraft) { value.EntityID = "" },
		"revision":      func(value *EventDraft) { value.Revision = 0 },
		"run ID":        func(value *EventDraft) { value.RunID = "invalid" },
		"supervisor ID": func(value *EventDraft) { value.SupervisorID = "invalid" },
		"details":       func(value *EventDraft) { value.Details = json.RawMessage(`{`) },
		"job linkage":   func(value *EventDraft) { value.EntityID = testRunID.String() },
		"run linkage": func(value *EventDraft) {
			value.Entity = EntityRun
			value.EntityID = testRunID.String()
		},
		"supervisor linkage": func(value *EventDraft) {
			value.Entity = EntitySupervisor
			value.EntityID = testSupervisorID.String()
		},
		"unknown entity": func(value *EventDraft) {
			value.Entity = EntityKind("unknown")
			value.EntityID = "unknown"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			value := base
			mutate(&value)
			if _, err := value.WithID(testEventID); err == nil {
				t.Fatal("WithID() error = nil")
			}
		})
	}
	if _, err := base.WithID("invalid"); err == nil {
		t.Fatal("WithID(invalid event ID) error = nil")
	}
}

func TestTransitionGuardBranches(t *testing.T) {
	t.Parallel()
	job, credential := submittedJob(t)
	claimed, supervisor := claimedJob(t)
	reservedJob, reserved := reservedRun(t)
	runningJob, running := runningRun(t)
	invalidJob := runningJob
	invalidJob.Revision = 0
	invalidRun := running
	invalidRun.Revision = 0

	operations := map[string]func() error{
		"new invalid submission": func() error {
			_, err := NewSubmittedJob(testJobID, JobSpec{}, CredentialHash{}, testTime, testTime)
			return err
		},
		"claim invalid job": func() error {
			_, err := ClaimJob(invalidJob, credential, testSupervisorID, validProcess(), testTime, testTime.Add(time.Second))
			return err
		},
		"claim invalid supervisor": func() error {
			_, err := ClaimJob(job, credential, "invalid", validProcess(), testTime.Add(time.Second), testTime.Add(2*time.Second))
			return err
		},
		"claim invalid process": func() error {
			_, err := ClaimJob(job, credential, testSupervisorID, ProcessIdentity{}, testTime.Add(time.Second), testTime.Add(2*time.Second))
			return err
		},
		"claim invalid lease": func() error {
			_, err := ClaimJob(job, credential, testSupervisorID, validProcess(), testTime.Add(time.Second), testTime.Add(time.Second))
			return err
		},
		"reserve invalid job": func() error {
			_, err := ReserveRun(invalidJob, testRunID, 1, validLogs(), testTime)
			return err
		},
		"reserve active run": func() error {
			_, err := ReserveRun(reservedJob, testRunID, 2, validLogs(), testTime.Add(time.Second))
			return err
		},
		"reserve invalid ID": func() error {
			_, err := ReserveRun(claimed, "invalid", 1, validLogs(), testTime.Add(2*time.Second))
			return err
		},
		"reserve zero number": func() error {
			_, err := ReserveRun(claimed, testRunID, 0, validLogs(), testTime.Add(2*time.Second))
			return err
		},
		"reserve invalid logs": func() error {
			_, err := ReserveRun(claimed, testRunID, 1, LogMetadata{}, testTime.Add(2*time.Second))
			return err
		},
		"reserve before claim": func() error {
			_, err := ReserveRun(claimed, testRunID, 1, validLogs(), testTime)
			return err
		},
		"start invalid job": func() error {
			_, err := MarkProcessStarted(invalidJob, running, "/bin/true", validProcess(), testTime)
			return err
		},
		"start invalid run": func() error {
			_, err := MarkProcessStarted(runningJob, invalidRun, "/bin/true", validProcess(), testTime)
			return err
		},
		"start empty executable": func() error {
			_, err := MarkProcessStarted(reservedJob, reserved, "", validProcess(), testTime.Add(3*time.Second))
			return err
		},
		"start invalid process": func() error {
			_, err := MarkProcessStarted(reservedJob, reserved, "/bin/true", ProcessIdentity{}, testTime.Add(3*time.Second))
			return err
		},
		"start before reservation": func() error {
			_, err := MarkProcessStarted(reservedJob, reserved, "/bin/true", validProcess(), reserved.ReservedAt.Add(-time.Second))
			return err
		},
		"cancel invalid job": func() error {
			_, err := RequestCancellation(invalidJob, &running, testTime)
			return err
		},
		"cancel before submission": func() error {
			_, err := RequestCancellation(claimed, nil, claimed.SubmittedAt.Add(-time.Second))
			return err
		},
		"finalize start failure outcome": func() error {
			_, err := FinalizeRun(runningJob, running, RunOutcomeStartFailed, nil, running.Logs, testTime)
			return err
		},
		"finalize invalid outcome": func() error {
			_, err := FinalizeRun(runningJob, running, "invalid", nil, running.Logs, testTime)
			return err
		},
		"finalize mismatched stop outcome": func() error {
			stopping, stopErr := RequestCancellation(runningJob, &running, testTime.Add(4*time.Second))
			if stopErr != nil {
				return stopErr
			}
			_, err := FinalizeRun(stopping.Job, *stopping.Run, RunOutcomeFailure, nil, stopping.Run.Logs, testTime.Add(5*time.Second))
			return err
		},
		"finalize cancellation invalid job": func() error {
			_, err := FinalizeCancellationWithoutRun(invalidJob, testTime)
			return err
		},
		"submission failure invalid job": func() error {
			_, err := MarkSubmissionFailed(invalidJob, "failure", testTime)
			return err
		},
		"submission failure empty diagnostic": func() error {
			_, err := MarkSubmissionFailed(job, "", *job.ClaimDeadline)
			return err
		},
		"ownership loss empty diagnostic": func() error {
			_, err := MarkOwnershipLost(claimed, nil, nil, "", testTime)
			return err
		},
		"ownership loss invalid job": func() error {
			_, err := MarkOwnershipLost(invalidJob, nil, nil, "lost", testTime)
			return err
		},
		"ownership loss needs run": func() error {
			_, err := MarkOwnershipLost(runningJob, nil, nil, "lost", testTime)
			return err
		},
		"ownership loss needs logs": func() error {
			_, err := MarkOwnershipLost(runningJob, &running, nil, "lost", testTime)
			return err
		},
		"renew invalid supervisor": func() error {
			value := supervisor
			value.Revision = 0
			_, _, err := RenewSupervisorLease(value, testTime, testTime.Add(time.Second))
			return err
		},
		"renew invalid expiry": func() error {
			_, _, err := RenewSupervisorLease(supervisor, supervisor.LeaseRenewedAt, supervisor.LeaseRenewedAt)
			return err
		},
		"release invalid supervisor": func() error {
			value := supervisor
			value.Revision = 0
			_, _, err := ReleaseSupervisor(value, testTime)
			return err
		},
		"release before claim": func() error {
			_, _, err := ReleaseSupervisor(supervisor, supervisor.ClaimedAt.Add(-time.Second))
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); err == nil {
				t.Fatal("operation error = nil")
			}
		})
	}
}

func TestTransitionDetailHelpers(t *testing.T) {
	t.Parallel()
	if reasonDetails("") != nil || len(reasonDetails("waiting")) == 0 {
		t.Fatal("reasonDetails() returned unexpected data")
	}
	if diagnosticDetails("") != nil || len(diagnosticDetails("failed")) == 0 {
		t.Fatal("diagnosticDetails() returned unexpected data")
	}
	if _, err := jobOutcomeForRun(RunOutcome("invalid")); err == nil {
		t.Fatal("jobOutcomeForRun(invalid) error = nil")
	}
}

func TestJobSpecDefensiveParsingAndValidation(t *testing.T) {
	t.Parallel()

	valid := validSpec(t)
	tests := map[string]func() error{
		"trailing JSON": func() error {
			encoded, err := valid.CanonicalJSON()
			if err != nil {
				return err
			}
			_, err = ParseJobSpecJSON(append(encoded, []byte(` {}`)...))
			return err
		},
		"missing execution policy": func() error {
			encoded, err := valid.CanonicalJSON()
			if err != nil {
				return err
			}
			var wire map[string]any
			if unmarshalErr := json.Unmarshal(encoded, &wire); unmarshalErr != nil {
				return unmarshalErr
			}
			delete(wire, "execution_policy")
			encoded, err = json.Marshal(wire)
			if err != nil {
				return err
			}
			_, err = ParseJobSpecJSON(encoded)
			return err
		},
		"empty working directory": func() error {
			value := valid
			value.workingDirectory = ""
			return value.Validate()
		},
		"working directory NUL": func() error {
			value := valid
			value.workingDirectory += "\x00"
			return value.Validate()
		},
		"invalid execution policy": func() error {
			value := valid
			value.executionPolicy.Concurrency.Slots = 0
			return value.Validate()
		},
		"invalid canonical representation": func() error {
			value := valid
			value.executable = ""
			_, err := value.CanonicalJSON()
			return err
		},
		"invalid unmarshal": func() error {
			value := valid
			return value.UnmarshalJSON([]byte(`{"schema_version":2}`))
		},
		"nested malformed JSON": func() error {
			_, err := ParseJobSpecJSON([]byte(`{"a":[{"b":`))
			return err
		},
	}
	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); err == nil {
				t.Fatal("error = nil")
			}
		})
	}
}

func TestLifecycleDefensiveBranches(t *testing.T) {
	t.Parallel()

	invalidJob, invalidRun := runningRun(t)
	invalidJob.Revision = 0
	invalidRun.Revision = 0
	runningJob, running := runningRun(t)
	completed := terminalRun(t)
	const otherJobID = JobID("01890f4e-4c00-7000-8000-000000000099")

	operations := map[string]func() error{
		"move invalid job": func() error {
			_, err := MoveJob(invalidJob, JobPhaseQueued, testTime, "test")
			return err
		},
		"complete invalid terminal outcome": func() error {
			_, err := CompleteRun(runningJob, running, RunOutcomeSuccess, nil, validLogs(), "", testTime, RunDisposition{TerminalOutcome: "invalid"})
			return err
		},
		"complete invalid pair": func() error {
			_, err := CompleteRun(invalidJob, invalidRun, RunOutcomeSuccess, nil, validLogs(), "", testTime, RunDisposition{TerminalOutcome: JobOutcomeSuccess})
			return err
		},
		"retry inactive pair": func() error {
			_, err := CompleteRun(completed.Job, completed.Run, RunOutcomeSuccess, completed.Run.Exit, completed.Run.Logs, "", testTime, RunDisposition{NextPhase: JobPhaseQueued})
			return err
		},
		"retry completion before reservation": func() error {
			_, err := CompleteRun(runningJob, running, RunOutcomeFailure, nil, validLogs(), "", running.ReservedAt.Add(-time.Second), RunDisposition{NextPhase: JobPhaseBackoff})
			return err
		},
		"timeout invalid job": func() error {
			_, err := RequestTimeout(invalidJob, nil, testTime)
			return err
		},
		"timeout mismatched run": func() error {
			other := running
			other.JobID = otherJobID
			_, err := RequestTimeout(runningJob, &other, testTime)
			return err
		},
		"run timeout invalid completed job": func() error {
			job := completed.Job
			job.Revision = 0
			_, err := RequestRunTimeout(job, completed.Run, testTime)
			return err
		},
		"run timeout invalid completed run": func() error {
			run := completed.Run
			run.Revision = 0
			_, err := RequestRunTimeout(completed.Job, run, testTime)
			return err
		},
		"run timeout completed mismatch": func() error {
			run := completed.Run
			run.JobID = otherJobID
			_, err := RequestRunTimeout(completed.Job, run, testTime)
			return err
		},
		"complete without run invalid job": func() error {
			_, err := CompleteWithoutRun(invalidJob, JobOutcomeFailure, "failed", testTime)
			return err
		},
		"pause invalid job": func() error {
			_, _, err := PauseJob(invalidJob, nil, testTime)
			return err
		},
		"pause mismatched run": func() error {
			other := running
			other.JobID = otherJobID
			_, _, err := PauseJob(runningJob, &other, testTime)
			return err
		},
		"resume invalid job": func() error {
			_, err := ResumeJob(invalidJob, nil, JobPhaseRunning, testTime)
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); err == nil {
				t.Fatal("error = nil")
			}
		})
	}
}

func TestNotifierDefinitionCloneAndValidationBranches(t *testing.T) {
	t.Parallel()

	for _, definition := range []NotifierDefinition{
		{Name: "command", Kind: NotifierCommand},
		{Name: "webhook", Kind: NotifierWebhook},
		{Name: "smtp", Kind: NotifierSMTP},
	} {
		if err := definition.validateKind(); err == nil {
			t.Errorf("validateKind(%s) error = nil", definition.Kind)
		}
	}
	if _, err := validateNotifierDefinitionSet([]NotifierDefinition{{Name: "broken", Kind: "unknown"}}); err == nil {
		t.Fatal("validateNotifierDefinitionSet() accepted invalid definition")
	}
	command := cloneCommandNotifier(&CommandNotifierDefinition{Executable: "/bin/true"})
	if command.Arguments == nil || command.Environment == nil {
		t.Fatalf("cloneCommandNotifier() = %+v", command)
	}
	webhook := cloneWebhookNotifier(&WebhookNotifierDefinition{URL: "https://example.test"})
	if webhook.Headers == nil {
		t.Fatalf("cloneWebhookNotifier() = %+v", webhook)
	}
	smtp := cloneSMTPNotifier(&SMTPNotifierDefinition{Address: "smtp.example.test:25"})
	if smtp.To == nil {
		t.Fatalf("cloneSMTPNotifier() = %+v", smtp)
	}
	if validHTTPHeaderName("X-Test-Invalid-\u00e9") {
		t.Fatal("validHTTPHeaderName() accepted non-ASCII name")
	}
	if optionalSecretReferenceToWire(nil) != nil || optionalSecretReferenceFromWire(nil) != nil {
		t.Fatal("nil optional secret reference was not preserved")
	}
}

func TestPublicValidationAndErrorFormattingBranches(t *testing.T) {
	t.Parallel()

	if text := (&ConflictError{Operation: "update"}).Error(); text == "" {
		t.Fatal("ConflictError.Error() returned empty text")
	}
	for name, operation := range map[string]func() error{
		"parse run":        func() error { _, err := ParseRunID("invalid"); return err },
		"parse supervisor": func() error { _, err := ParseSupervisorID("invalid"); return err },
		"parse event":      func() error { _, err := ParseEventID("invalid"); return err },
		"job optional ID": func() error {
			job, _ := claimedJob(t)
			job.SupervisorID = "invalid"
			return job.Validate()
		},
		"run outcome": func() error {
			_, run := runningRun(t)
			run.Outcome = "invalid"
			return run.Validate()
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); err == nil {
				t.Fatal("error = nil")
			}
		})
	}
}
