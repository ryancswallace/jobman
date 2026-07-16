package model

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testJobID        = JobID("01890f4e-4c00-7000-8000-000000000001")
	testRunID        = RunID("01890f4e-4c00-7000-8000-000000000002")
	testSupervisorID = SupervisorID("01890f4e-4c00-7000-8000-000000000003")
	testEventID      = EventID("01890f4e-4c00-7000-8000-000000000004")
)

var testTime = time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)

func testAbsolutePath(elements ...string) string {
	root := filepath.VolumeName(os.TempDir()) + string(filepath.Separator)
	return filepath.Join(append([]string{root}, elements...)...)
}

func validSpec(tb testing.TB) JobSpec {
	tb.Helper()

	specification, err := NewJobSpec(JobSpecInput{
		Executable:             "/usr/bin/example",
		Arguments:              []string{"first", "second value"},
		WorkingDirectory:       testAbsolutePath("tmp", "jobman-work"),
		Environment:            map[string]string{"JOBMAN_TEST": "true"},
		UnsetEnvironment:       []string{"REMOVE_ME"},
		EnvironmentInheritance: EnvironmentInheritSubmission,
		Name:                   "example",
		StopPolicy: StopPolicy{
			GracePeriod:     5 * time.Second,
			ForceAfterGrace: true,
		},
		StdinPolicy: StdinNull,
	})
	if err != nil {
		tb.Fatalf("create valid job specification: %v", err)
	}

	return specification
}

func validCredential(t *testing.T) ([]byte, CredentialHash) {
	t.Helper()

	credential := bytes.Repeat([]byte{0x5a}, 32)
	hash, err := NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("hash valid credential: %v", err)
	}

	return credential, hash
}

func validLogs() LogMetadata {
	root := testAbsolutePath("tmp", "jobman-state", testJobID.String(), "1")

	return LogMetadata{
		StdoutPath:      filepath.Join(root, "stdout.log"),
		StderrPath:      filepath.Join(root, "stderr.log"),
		IndexPath:       filepath.Join(root, "chunks.idx"),
		IndexVersion:    LogIndexVersion,
		Integrity:       LogIntegrityPending,
		RecordingHealth: RecordingHealthy,
	}
}

func validProcess() ProcessIdentity {
	return ProcessIdentity{
		PID:        1234,
		Platform:   "linux",
		CreationID: "start-time-42",
		BootID:     "boot-7",
		TreeID:     "pgid-1234",
	}
}

func submittedJob(t *testing.T) (job JobState, credential []byte) {
	t.Helper()

	credential, hash := validCredential(t)
	result, err := NewSubmittedJob(
		testJobID,
		validSpec(t),
		hash,
		testTime,
		testTime.Add(30*time.Second),
	)
	if err != nil {
		t.Fatalf("submit valid job: %v", err)
	}

	return result.Job, credential
}

func claimedJob(t *testing.T) (JobState, SupervisorState) {
	t.Helper()

	job, credential := submittedJob(t)
	result, err := ClaimJob(
		job,
		credential,
		testSupervisorID,
		validProcess(),
		testTime.Add(time.Second),
		testTime.Add(11*time.Second),
	)
	if err != nil {
		t.Fatalf("claim valid job: %v", err)
	}

	return result.Job, *result.Supervisor
}

func reservedRun(t *testing.T) (JobState, RunState) {
	t.Helper()

	job, _ := claimedJob(t)
	result, err := ReserveRun(job, testRunID, 1, validLogs(), testTime.Add(2*time.Second))
	if err != nil {
		t.Fatalf("reserve valid run: %v", err)
	}

	return result.Job, *result.Run
}

func runningRun(t *testing.T) (JobState, RunState) {
	t.Helper()

	job, run := reservedRun(t)
	result, err := MarkProcessStarted(
		job,
		run,
		"/usr/bin/example",
		validProcess(),
		testTime.Add(3*time.Second),
	)
	if err != nil {
		t.Fatalf("mark valid process started: %v", err)
	}

	return result.Job, *result.Run
}
