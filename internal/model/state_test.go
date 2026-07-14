package model

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCredentialHash(t *testing.T) {
	t.Parallel()

	credential, hash := validCredential(t)
	if hash.Empty() || !hash.Matches(credential) {
		t.Fatal("valid hash does not match its credential")
	}
	if hash.Matches(bytes.Repeat([]byte{0x01}, 31)) || hash.Matches(bytes.Repeat([]byte{0x01}, 32)) {
		t.Fatal("hash matched an invalid credential")
	}

	encoded := hash.Bytes()
	reconstructed, err := CredentialHashFromBytes(encoded)
	if err != nil {
		t.Fatalf("CredentialHashFromBytes() error = %v", err)
	}
	encoded[0] ^= 0xff
	if reconstructed != hash {
		t.Fatal("persisted hash retained caller-owned bytes")
	}
	if _, err := NewCredentialHash(make([]byte, 31)); err == nil {
		t.Fatal("NewCredentialHash() accepted 31 bytes")
	}
	if _, err := CredentialHashFromBytes(make([]byte, 31)); err == nil {
		t.Fatal("CredentialHashFromBytes() accepted 31 bytes")
	}
}

func TestProcessIdentityValidation(t *testing.T) {
	t.Parallel()

	valid := validProcess()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid identity: %v", err)
	}
	tests := map[string]func(*ProcessIdentity){
		"PID":         func(identity *ProcessIdentity) { identity.PID = 0 },
		"platform":    func(identity *ProcessIdentity) { identity.Platform = "" },
		"creation ID": func(identity *ProcessIdentity) { identity.CreationID = "" },
		"boot ID":     func(identity *ProcessIdentity) { identity.BootID = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			identity := valid
			mutate(&identity)
			if err := identity.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestExitInfoValidation(t *testing.T) {
	t.Parallel()

	zero := 0
	negative := -1
	tests := map[string]struct {
		information ExitInfo
		valid       bool
	}{
		"exit code": {
			information: ExitInfo{ExitCode: &zero, ObservedAt: testTime},
			valid:       true,
		},
		"signal": {
			information: ExitInfo{Signal: "terminated", ObservedAt: testTime},
			valid:       true,
		},
		"platform reason": {
			information: ExitInfo{PlatformReason: "status-control-c-exit", ObservedAt: testTime},
			valid:       true,
		},
		"zero time": {
			information: ExitInfo{ExitCode: &zero},
		},
		"negative code": {
			information: ExitInfo{ExitCode: &negative, ObservedAt: testTime},
		},
		"no result": {
			information: ExitInfo{ObservedAt: testTime},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := test.information.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %v", err, test.valid)
			}
		})
	}
}

func TestLogMetadataValidation(t *testing.T) {
	t.Parallel()

	valid := validLogs()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid logs: %v", err)
	}
	tests := map[string]func(*LogMetadata){
		"relative path": func(metadata *LogMetadata) { metadata.StdoutPath = "stdout.log" },
		"unclean path":  func(metadata *LogMetadata) { metadata.StdoutPath += "/../stdout.log" },
		"NUL path":      func(metadata *LogMetadata) { metadata.StdoutPath += "\x00" },
		"duplicate path": func(metadata *LogMetadata) {
			metadata.StderrPath = metadata.StdoutPath
		},
		"index version": func(metadata *LogMetadata) { metadata.IndexVersion++ },
		"negative size": func(metadata *LogMetadata) { metadata.StdoutSize = -1 },
		"integrity":     func(metadata *LogMetadata) { metadata.Integrity = "unknown" },
		"health":        func(metadata *LogMetadata) { metadata.RecordingHealth = "unknown" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			metadata := valid
			mutate(&metadata)
			if err := metadata.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestJobStateValidation(t *testing.T) {
	t.Parallel()

	valid, _ := submittedJob(t)
	tests := map[string]func(*JobState){
		"ID":         func(state *JobState) { state.ID = "invalid" },
		"phase":      func(state *JobState) { state.Phase = "unknown" },
		"revision":   func(state *JobState) { state.Revision = 0 },
		"submission": func(state *JobState) { state.SubmittedAt = time.Time{} },
		"credential": func(state *JobState) { state.LaunchCredentialHash = CredentialHash{} },
		"deadline":   func(state *JobState) { state.ClaimDeadline = nil },
		"deadline order": func(state *JobState) {
			state.ClaimDeadline = timePointer(state.SubmittedAt)
		},
		"premature outcome": func(state *JobState) { state.Outcome = JobOutcomeSuccess },
		"premature completion": func(state *JobState) {
			state.CompletedAt = timePointer(state.SubmittedAt.Add(time.Second))
		},
		"premature supervisor": func(state *JobState) { state.SupervisorID = testSupervisorID },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := valid
			mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestRunStateValidation(t *testing.T) {
	t.Parallel()

	_, valid := runningRun(t)
	tests := map[string]func(*RunState){
		"ID":       func(state *RunState) { state.ID = "invalid" },
		"job ID":   func(state *RunState) { state.JobID = "invalid" },
		"number":   func(state *RunState) { state.Number = 0 },
		"phase":    func(state *RunState) { state.Phase = "unknown" },
		"revision": func(state *RunState) { state.Revision = 0 },
		"reserved": func(state *RunState) { state.ReservedAt = time.Time{} },
		"process":  func(state *RunState) { state.Process = nil },
		"started":  func(state *RunState) { state.StartedAt = nil },
		"executable": func(state *RunState) {
			state.ResolvedExecutable = ""
		},
		"outcome": func(state *RunState) { state.Outcome = RunOutcomeSuccess },
		"logs":    func(state *RunState) { state.Logs.IndexVersion = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := valid
			mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestCompletedRunValidation(t *testing.T) {
	t.Parallel()

	job, run := runningRun(t)
	logs := run.Logs
	logs.Integrity = LogIntegrityValid
	exitCode := 0
	exit := ExitInfo{ExitCode: &exitCode, ObservedAt: testTime.Add(4 * time.Second)}
	result, err := FinalizeRun(job, run, RunOutcomeSuccess, &exit, logs, testTime.Add(4*time.Second))
	if err != nil {
		t.Fatalf("FinalizeRun() error = %v", err)
	}

	tests := map[string]func(*RunState){
		"pending logs": func(state *RunState) { state.Logs.Integrity = LogIntegrityPending },
		"success with nonzero exit": func(state *RunState) {
			code := 2
			state.Exit.ExitCode = &code
		},
		"exit observed after completion": func(state *RunState) {
			state.Exit.ObservedAt = state.CompletedAt.Add(time.Second)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := *result.Run
			exitCopy := *state.Exit
			state.Exit = &exitCopy
			mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestSupervisorStateValidation(t *testing.T) {
	t.Parallel()

	_, valid := claimedJob(t)
	tests := map[string]func(*SupervisorState){
		"ID":       func(state *SupervisorState) { state.ID = "invalid" },
		"job ID":   func(state *SupervisorState) { state.JobID = "invalid" },
		"revision": func(state *SupervisorState) { state.Revision = 0 },
		"process":  func(state *SupervisorState) { state.Process.CreationID = "" },
		"claimed":  func(state *SupervisorState) { state.ClaimedAt = time.Time{} },
		"renewal order": func(state *SupervisorState) {
			state.LeaseRenewedAt = state.ClaimedAt.Add(-time.Second)
		},
		"expiry order": func(state *SupervisorState) {
			state.LeaseExpiresAt = state.LeaseRenewedAt
		},
		"release order": func(state *SupervisorState) {
			state.ReleasedAt = timePointer(state.ClaimedAt.Add(-time.Second))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := valid
			mutate(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestEnumValidation(t *testing.T) {
	t.Parallel()

	if !EntityJob.Valid() || EntityKind("unknown").Valid() {
		t.Fatal("EntityKind.Valid() returned an incorrect result")
	}
	if !JobPhasePaused.Valid() || JobPhase("unknown").Valid() {
		t.Fatal("JobPhase.Valid() returned an incorrect result")
	}
	if !RunPhasePaused.Valid() || RunPhase("unknown").Valid() {
		t.Fatal("RunPhase.Valid() returned an incorrect result")
	}
	if !JobOutcomeAborted.Valid() || JobOutcome("unknown").Valid() {
		t.Fatal("JobOutcome.Valid() returned an incorrect result")
	}
	if !RunOutcomeTimedOut.Valid() || RunOutcome("unknown").Valid() {
		t.Fatal("RunOutcome.Valid() returned an incorrect result")
	}
	if !LogIntegrityCorrupt.Valid() || LogIntegrity("unknown").Valid() {
		t.Fatal("LogIntegrity.Valid() returned an incorrect result")
	}
	if !RecordingDegraded.Valid() || RecordingHealth("unknown").Valid() {
		t.Fatal("RecordingHealth.Valid() returned an incorrect result")
	}
}

func TestModelErrors(t *testing.T) {
	t.Parallel()

	validation := &ValidationError{Reason: "bad value"}
	if validation.Error() != "invalid model value: bad value" {
		t.Fatalf("ValidationError.Error() = %q", validation.Error())
	}
	conflict := &ConflictError{
		Entity:    EntityJob,
		ID:        testJobID.String(),
		Operation: "start",
		Actual:    string(JobPhaseCompleted),
		Allowed:   []string{string(JobPhaseStarting)},
	}
	if !strings.Contains(conflict.Error(), "expected starting") || !IsConflict(conflict) {
		t.Fatalf("conflict error = %q", conflict)
	}
	var target *ConflictError
	if !errors.As(conflict, &target) {
		t.Fatal("ConflictError does not support errors.As")
	}
}
