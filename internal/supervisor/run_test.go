package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	targetEnvironment = "JOBMAN_SUPERVISOR_TEST_TARGET"
	targetBlock       = "JOBMAN_SUPERVISOR_TEST_BLOCK"
	stdoutMarker      = "jobman-supervisor-stdout"
	stderrMarker      = "jobman-supervisor-stderr"
)

func TestRunForwardsContextCancellation(t *testing.T) {
	fixture := submitSupervisorFixtureWithOptions(t, true, true)
	acknowledgement := new(closingBuffer)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Run(
			ctx,
			fixture.stateDir,
			fixture.jobID.String(),
			bytes.NewReader(fixture.credential),
			acknowledgement,
		)
	}()

	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	waitForSupervisorJobPhase(t, database, fixture.jobID, model.JobPhaseRunning)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() after cancellation error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not stop its target after context cancellation")
	}

	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Phase != model.JobPhaseCompleted || job.Outcome != model.JobOutcomeCancelled {
		t.Fatalf("canceled supervisor job = %#v", job)
	}
}

type supervisorFixture struct {
	stateDir   string
	jobID      model.JobID
	credential []byte
}

func TestRunCompletesStoreBackedJob(t *testing.T) {
	fixture := submitSupervisorFixture(t, true)
	acknowledgement := new(closingBuffer)

	if err := Run(
		t.Context(),
		fixture.stateDir,
		fixture.jobID.String(),
		bytes.NewReader(fixture.credential),
		acknowledgement,
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	ack := decodeWrittenAcknowledgement(t, acknowledgement)
	if ack.JobID != fixture.jobID || !ack.SupervisorID.Valid() {
		t.Fatalf("acknowledgement = %#v", ack)
	}

	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Phase != model.JobPhaseCompleted || job.Outcome != model.JobOutcomeSuccess {
		t.Fatalf("completed job = %#v", job)
	}
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != model.RunOutcomeSuccess {
		t.Fatalf("runs = %#v", runs)
	}
	supervisor, err := database.GetSupervisorForJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetSupervisorForJob() error = %v", err)
	}
	if supervisor.ID != ack.SupervisorID || supervisor.ReleasedAt == nil {
		t.Fatalf("released supervisor = %#v", supervisor)
	}

	reader, err := logstore.OpenRun(fixture.stateDir, fixture.jobID.String(), 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}
	var stdout bytes.Buffer
	if _, err := reader.CopyStream(&stdout, logstore.Stdout); err != nil {
		t.Fatalf("CopyStream(stdout) error = %v", err)
	}
	var stderr bytes.Buffer
	if _, err := reader.CopyStream(&stderr, logstore.Stderr); err != nil {
		t.Fatalf("CopyStream(stderr) error = %v", err)
	}
	if !strings.Contains(stdout.String(), stdoutMarker) || !strings.Contains(stderr.String(), stderrMarker) {
		t.Fatalf("captured streams = stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

func TestRunRecordsStartFailure(t *testing.T) {
	fixture := submitSupervisorFixture(t, false)
	acknowledgement := new(closingBuffer)

	err := Run(
		t.Context(),
		fixture.stateDir,
		fixture.jobID.String(),
		bytes.NewReader(fixture.credential),
		acknowledgement,
	)
	if err == nil || !strings.Contains(err.Error(), "start target") {
		t.Fatalf("Run() error = %v, want start failure", err)
	}
	ack := decodeWrittenAcknowledgement(t, acknowledgement)

	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Phase != model.JobPhaseCompleted || job.Outcome != model.JobOutcomeFailure {
		t.Fatalf("failed job = %#v", job)
	}
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != model.RunOutcomeStartFailed {
		t.Fatalf("runs = %#v", runs)
	}
	if runs[0].Logs.RecordingHealth != model.RecordingHealthy ||
		runs[0].Logs.Integrity != model.LogIntegrityValid {
		t.Fatalf("failed-run log health = %#v", runs[0].Logs)
	}
	supervisor, err := database.GetSupervisorForJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetSupervisorForJob() error = %v", err)
	}
	if supervisor.ID != ack.SupervisorID || supervisor.ReleasedAt == nil {
		t.Fatalf("released supervisor = %#v", supervisor)
	}
}

func TestRunRejectsWrongCredential(t *testing.T) {
	fixture := submitSupervisorFixture(t, true)
	wrongCredential := bytes.Repeat([]byte{0xff}, credentialSize)
	acknowledgement := new(closingBuffer)

	err := Run(
		t.Context(),
		fixture.stateDir,
		fixture.jobID.String(),
		bytes.NewReader(wrongCredential),
		acknowledgement,
	)
	if err == nil || !model.IsConflict(err) {
		t.Fatalf("Run() error = %T %v, want model conflict", err, err)
	}
	if acknowledgement.Len() != 0 || acknowledgement.closed {
		t.Fatalf("invalid claim wrote acknowledgement: %q, closed=%v", acknowledgement.String(), acknowledgement.closed)
	}

	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Phase != model.JobPhaseSubmitting || job.SupervisorID != "" {
		t.Fatalf("wrong credential mutated job = %#v", job)
	}
}

func TestRunRejectsInvalidIdentityAndCredentialLength(t *testing.T) {
	t.Parallel()

	if err := Run(t.Context(), t.TempDir(), "invalid", strings.NewReader(""), new(bytes.Buffer)); err == nil {
		t.Fatal("Run() accepted an invalid job ID")
	}
	if err := Run(
		t.Context(),
		t.TempDir(),
		protocolJobID.String(),
		bytes.NewReader(make([]byte, credentialSize-1)),
		new(bytes.Buffer),
	); err == nil || !strings.Contains(err.Error(), "read supervisor credential") {
		// io.ReadFull returns io.ErrUnexpectedEOF. Match the stable operation
		// context rather than the exact wrapped sentinel.
		t.Fatalf("Run() short credential error = %v", err)
	}
}

func TestSupervisorTargetHelper(t *testing.T) {
	if os.Getenv(targetEnvironment) != "1" {
		return
	}
	if _, err := fmt.Fprint(os.Stdout, stdoutMarker); err != nil {
		t.Fatalf("write helper stdout: %v", err)
	}
	if _, err := fmt.Fprint(os.Stderr, stderrMarker); err != nil {
		t.Fatalf("write helper stderr: %v", err)
	}
	if os.Getenv(targetBlock) == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
}

func submitSupervisorFixture(t *testing.T, executable bool) supervisorFixture {
	t.Helper()

	return submitSupervisorFixtureWithOptions(t, executable, false)
}

func submitSupervisorFixtureWithOptions(t *testing.T, executable, block bool) supervisorFixture {
	t.Helper()

	stateDir := t.TempDir()
	// State directories need owner traversal in addition to read and write.
	//nolint:gosec // G302's file-oriented recommendation does not apply to directories.
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("make state directory private: %v", err)
	}
	workingDirectory := t.TempDir()
	target := filepath.Join(string(filepath.Separator), "definitely-missing", "jobman-test-target")
	arguments := []string(nil)
	environment := map[string]string(nil)
	if executable {
		var err error
		target, err = os.Executable()
		if err != nil {
			t.Fatalf("locate test executable: %v", err)
		}
		arguments = []string{"-test.run=^TestSupervisorTargetHelper$"}
		environment = map[string]string{targetEnvironment: "1"}
		if block {
			environment[targetBlock] = "1"
		}
	}

	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable:       target,
		Arguments:        arguments,
		WorkingDirectory: workingDirectory,
		Environment:      environment,
		StopPolicy: model.StopPolicy{
			GracePeriod:     time.Second,
			ForceAfterGrace: true,
		},
	})
	if err != nil {
		t.Fatalf("create job specification: %v", err)
	}

	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		t.Fatalf("create ID generator: %v", err)
	}
	jobID, err := ids.NewJobID()
	if err != nil {
		t.Fatalf("create job ID: %v", err)
	}
	credential := bytes.Repeat([]byte{0x7c}, credentialSize)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("hash launch credential: %v", err)
	}

	database := openSupervisorStore(t, stateDir)
	submittedAt := time.Now().UTC()
	_, err = database.Submit(
		t.Context(),
		jobID,
		specification,
		hash,
		submittedAt,
		submittedAt.Add(time.Minute),
	)
	if err != nil {
		closeSupervisorStore(t, database)
		t.Fatalf("submit job: %v", err)
	}
	closeSupervisorStore(t, database)

	return supervisorFixture{stateDir: stateDir, jobID: jobID, credential: credential}
}

func waitForSupervisorJobPhase(
	t *testing.T,
	database *store.Store,
	jobID model.JobID,
	want model.JobPhase,
) {
	t.Helper()

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := database.GetJob(t.Context(), jobID)
		if err == nil && job.Phase == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("job %s did not reach phase %s: last error %v", jobID, want, err)
		case <-ticker.C:
		}
	}
}

func openSupervisorStore(t *testing.T, stateDir string) *store.Store {
	t.Helper()

	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		t.Fatalf("create event ID generator: %v", err)
	}
	database, err := store.Open(t.Context(), store.Options{
		StateDir:      stateDir,
		JobmanVersion: "test",
		Now:           time.Now,
		EventIDs:      ids,
	})
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}

	return database
}

func closeSupervisorStore(t *testing.T, database *store.Store) {
	t.Helper()

	if err := database.Close(); err != nil {
		t.Fatalf("close test store: %v", err)
	}
}

func decodeWrittenAcknowledgement(t *testing.T, writer *closingBuffer) Acknowledgement {
	t.Helper()

	if !writer.closed {
		t.Fatal("Run() did not close the acknowledgement writer")
	}
	var acknowledgement Acknowledgement
	decoder := json.NewDecoder(bytes.NewReader(writer.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&acknowledgement); err != nil {
		t.Fatalf("decode written acknowledgement: %v", err)
	}

	return acknowledgement
}

type closingBuffer struct {
	bytes.Buffer
	closed bool
}

func (buffer *closingBuffer) Close() error {
	buffer.closed = true

	return nil
}
