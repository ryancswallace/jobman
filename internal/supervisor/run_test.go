package supervisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/policy"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	targetEnvironment = "JOBMAN_SUPERVISOR_TEST_TARGET"
	targetBlock       = "JOBMAN_SUPERVISOR_TEST_BLOCK"
	targetExitCode    = "JOBMAN_SUPERVISOR_TEST_EXIT_CODE"
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

func TestJitteredSchedulerPollIsSymmetricallyBounded(t *testing.T) {
	t.Parallel()

	if got := jitteredSchedulerPoll(100*time.Millisecond, fixedJitterSource(0)); got != 90*time.Millisecond {
		t.Fatalf("lower jittered poll = %s, want 90ms", got)
	}
	if got := jitteredSchedulerPoll(
		100*time.Millisecond,
		fixedJitterSource(uint64(20*time.Millisecond)),
	); got != 110*time.Millisecond {
		t.Fatalf("upper jittered poll = %s, want 110ms", got)
	}
}

type fixedJitterSource uint64

func (source fixedJitterSource) Uint64N(upperBound uint64) uint64 {
	if upperBound == 0 {
		return 0
	}

	return min(uint64(source), upperBound-1)
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

func TestRunPersistsConfiguredExitClassification(t *testing.T) {
	tests := []struct {
		name         string
		exitCode     int
		successCodes []int
		wantRun      model.RunOutcome
		wantJob      model.JobOutcome
		wantSuccess  uint64
		wantFailure  uint64
	}{
		{
			name: "nonzero configured success", exitCode: 42, successCodes: []int{0, 42},
			wantRun: model.RunOutcomeSuccess, wantJob: model.JobOutcomeSuccess, wantSuccess: 1,
		},
		{
			name: "zero excluded from success", exitCode: 0, successCodes: []int{42},
			wantRun: model.RunOutcomeFailure, wantJob: model.JobOutcomeFailure, wantFailure: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := model.DefaultExecutionPolicy()
			configuration.Classification.SuccessExitCodes = test.successCodes
			fixture := submitSupervisorFixtureWithPolicyAndExit(
				t,
				configuration,
				test.exitCode,
			)
			if err := Run(
				t.Context(), fixture.stateDir, fixture.jobID.String(),
				bytes.NewReader(fixture.credential), new(closingBuffer),
			); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			database := openSupervisorStore(t, fixture.stateDir)
			defer closeSupervisorStore(t, database)
			job, err := database.GetJob(t.Context(), fixture.jobID)
			if err != nil {
				t.Fatalf("GetJob() error = %v", err)
			}
			runs, err := database.ListRuns(t.Context(), fixture.jobID)
			if err != nil {
				t.Fatalf("ListRuns() error = %v", err)
			}
			runtimeState, err := database.GetRuntime(t.Context(), fixture.jobID)
			if err != nil {
				t.Fatalf("GetRuntime() error = %v", err)
			}
			if job.Outcome != test.wantJob || len(runs) != 1 || runs[0].Outcome != test.wantRun {
				t.Fatalf("classification persisted job=%q runs=%#v", job.Outcome, runs)
			}
			if runtimeState.SuccessCount != test.wantSuccess || runtimeState.FailureCount != test.wantFailure {
				t.Fatalf("runtime counts = %#v", runtimeState)
			}
		})
	}
}

func TestRunDurablyCompletesSubscribedNotifications(t *testing.T) {
	executable, executableErr := os.Executable()
	if executableErr != nil {
		t.Fatalf("locate test executable: %v", executableErr)
	}
	configuration := model.DefaultExecutionPolicy()
	configuration.NotifierDefinitions = []model.NotifierDefinition{{
		Name: "audit", Kind: model.NotifierCommand, Timeout: 5 * time.Second,
		Retry: model.NotifierRetryPolicy{MaxAttempts: 1},
		Command: &model.CommandNotifierDefinition{
			Executable: executable,
			Arguments:  []string{"-test.run=^TestSupervisorNotificationHelper$"},
		},
	}}
	configuration.Notifications = []model.NotificationSubscription{{
		Notifier: "audit",
		Events: []string{
			"job_started", "run_started", "run_succeeded", "job_succeeded",
		},
	}}
	fixture := submitSupervisorFixtureWithPolicy(t, true, false, configuration)
	if err := Run(
		t.Context(), fixture.stateDir, fixture.jobID.String(),
		bytes.NewReader(fixture.credential), new(closingBuffer),
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	deliveries, err := database.ListNotificationDeliveries(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListNotificationDeliveries() error = %v", err)
	}
	attempts, err := database.ListNotificationAttempts(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListNotificationAttempts() error = %v", err)
	}
	if len(deliveries) != 4 || len(attempts) != 4 {
		t.Fatalf("notification history = %d deliveries / %d attempts", len(deliveries), len(attempts))
	}
	seen := make(map[model.EventID]struct{}, len(deliveries))
	for _, delivery := range deliveries {
		if delivery.Status != store.NotificationDeliverySucceeded || delivery.AttemptCount != 1 {
			t.Errorf("delivery = %#v, want one successful attempt", delivery)
		}
		if _, duplicate := seen[delivery.EventID]; duplicate {
			t.Errorf("duplicate stable notification event ID %s", delivery.EventID)
		}
		seen[delivery.EventID] = struct{}{}
	}
}

func TestWholeJobTimeoutBoundsNotificationRetrySchedule(t *testing.T) {
	configuration := model.DefaultExecutionPolicy()
	configuration.JobTimeout = 30 * time.Second
	configuration.NotifierDefinitions = []model.NotifierDefinition{{
		Name: "slow-retry", Kind: model.NotifierCommand, Timeout: time.Second,
		Retry: model.NotifierRetryPolicy{MaxAttempts: 3, Delay: time.Hour, MaxDelay: time.Hour},
		Command: &model.CommandNotifierDefinition{
			Executable: filepath.Join(
				string(filepath.Separator), "definitely-missing", "jobman-notifier",
			),
		},
	}}
	configuration.Notifications = []model.NotificationSubscription{{
		Notifier: "slow-retry", Events: []string{"job_started"},
	}}
	fixture := submitSupervisorFixtureWithPolicy(t, true, false, configuration)

	started := time.Now()
	if runErr := Run(
		t.Context(), fixture.stateDir, fixture.jobID.String(),
		bytes.NewReader(fixture.credential), new(closingBuffer),
	); runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("notification retry ignored whole-job timeout: %s", elapsed)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	deliveries, deliveriesErr := database.ListNotificationDeliveries(t.Context(), fixture.jobID)
	attempts, attemptsErr := database.ListNotificationAttempts(t.Context(), fixture.jobID)
	if job.Outcome != model.JobOutcomeSuccess || deliveriesErr != nil || attemptsErr != nil ||
		len(deliveries) != 1 || deliveries[0].Status != store.NotificationDeliveryFailed ||
		len(attempts) != 1 || attempts[0].Retryable {
		t.Fatalf(
			"bounded notification job=%q; deliveries=%#v (%v), attempts=%#v (%v)",
			job.Outcome, deliveries, deliveriesErr, attempts, attemptsErr,
		)
	}
}

func TestRecoverNotificationsReplaysExpiredClaimAfterReopen(t *testing.T) {
	executable, executableErr := os.Executable()
	if executableErr != nil {
		t.Fatalf("locate test executable: %v", executableErr)
	}
	configuration := model.DefaultExecutionPolicy()
	configuration.NotifierDefinitions = []model.NotifierDefinition{{
		Name: "audit", Kind: model.NotifierCommand, Timeout: 5 * time.Second,
		Retry: model.NotifierRetryPolicy{MaxAttempts: 2},
		Command: &model.CommandNotifierDefinition{
			Executable: executable,
			Arguments:  []string{"-test.run=^TestSupervisorNotificationHelper$"},
		},
	}}
	configuration.Notifications = []model.NotificationSubscription{{
		Notifier: "audit", Events: []string{"job_started"},
	}}
	fixture := submitSupervisorFixtureWithPolicy(t, true, false, configuration)

	first := openSupervisorStore(t, fixture.stateDir)
	job, err := first.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("GetJob() error = %v", err)
	}
	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("create supervisor ID generator: %v", err)
	}
	supervisorID, err := ids.NewSupervisorID()
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("create supervisor ID: %v", err)
	}
	process, err := platform.Inspect(os.Getpid())
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("inspect recovery test process: %v", err)
	}
	claimedAt := time.Now().UTC()
	claim, err := first.Claim(
		t.Context(), job.ID, fixture.credential, supervisorID, modelIdentity(process),
		claimedAt, claimedAt.Add(time.Minute),
	)
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("Claim() error = %v", err)
	}
	event, err := first.TransitionEvent(
		t.Context(), model.EntityJob, claim.Job.ID.String(), claim.Job.Revision,
	)
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("TransitionEvent() error = %v", err)
	}
	queuedBeforeDelivery, err := first.ListNotificationDeliveries(t.Context(), fixture.jobID)
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("ListNotificationDeliveries(before delivery) error = %v", err)
	}
	if len(queuedBeforeDelivery) != 1 || queuedBeforeDelivery[0].EventID != event.ID ||
		queuedBeforeDelivery[0].Status != store.NotificationDeliveryPending {
		closeSupervisorStore(t, first)
		t.Fatalf("transactionally queued notification = %#v", queuedBeforeDelivery)
	}
	if _, queueErr := first.QueueNotificationDeliveries(t.Context(), []store.QueueNotificationDeliveryInput{{
		JobID: job.ID, EventID: event.ID, NotifierName: "audit",
		EventType: "job_started", MaxAttempts: 2,
	}}); queueErr != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("QueueNotificationDeliveries() error = %v", queueErr)
	}
	deliveryClaimedAt := time.Now().UTC()
	firstClaim, err := first.ClaimNotificationDelivery(
		t.Context(), event.ID, deliveryClaimedAt, deliveryClaimedAt.Add(time.Microsecond),
	)
	if err != nil {
		closeSupervisorStore(t, first)
		t.Fatalf("ClaimNotificationDelivery() error = %v", err)
	}
	closeSupervisorStore(t, first)

	second := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, second)
	if recoveryErr := RecoverNotifications(t.Context(), second); recoveryErr != nil {
		t.Fatalf("RecoverNotifications() error = %v", recoveryErr)
	}
	deliveries, err := second.ListNotificationDeliveries(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListNotificationDeliveries() error = %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != store.NotificationDeliverySucceeded ||
		deliveries[0].EventID != firstClaim.EventID || deliveries[0].AttemptCount != 1 ||
		deliveries[0].ClaimToken == firstClaim.ClaimToken {
		t.Fatalf("recovered delivery = %#v, first claim = %#v", deliveries, firstClaim)
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
	if err != nil {
		t.Fatalf("Run() error = %v", err)
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

func TestRunRetriesStartFailureToConfiguredLimit(t *testing.T) {
	completion := policy.CompletionPolicy{
		MaxRuns:       policy.Limit{Value: 2},
		SuccessTarget: policy.Limit{Value: 1},
		FailureLimit:  policy.Limit{Value: 2},
	}
	configuration := model.DefaultExecutionPolicy()
	configuration.Completion = completion
	configuration.Classification.RetryStartFailure = true
	fixture := submitSupervisorFixtureWithPolicy(t, false, false, configuration)

	if err := Run(
		t.Context(),
		fixture.stateDir,
		fixture.jobID.String(),
		bytes.NewReader(fixture.credential),
		new(closingBuffer),
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 || runs[0].Outcome != model.RunOutcomeStartFailed ||
		runs[1].Outcome != model.RunOutcomeStartFailed {
		t.Fatalf("retry runs = %#v", runs)
	}
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Outcome != model.JobOutcomeFailure {
		t.Fatalf("job outcome = %q, want failure", job.Outcome)
	}
}

func TestRunTimeoutIsRunFailureNotWholeJobTimeout(t *testing.T) {
	configuration := model.DefaultExecutionPolicy()
	configuration.RunTimeout = 100 * time.Millisecond
	fixture := submitSupervisorFixtureWithPolicy(t, true, true, configuration)

	if err := Run(
		t.Context(),
		fixture.stateDir,
		fixture.jobID.String(),
		bytes.NewReader(fixture.credential),
		new(closingBuffer),
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Outcome != model.JobOutcomeFailure {
		t.Fatalf("job outcome = %q, want failure", job.Outcome)
	}
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].Outcome != model.RunOutcomeTimedOut {
		t.Fatalf("runs = %#v, want one timed-out run", runs)
	}
}

func TestRunTimeoutCanRetry(t *testing.T) {
	configuration := model.DefaultExecutionPolicy()
	configuration.RunTimeout = 50 * time.Millisecond
	configuration.Classification.RetryTimeout = true
	two, limitErr := policy.FiniteLimit(2)
	if limitErr != nil {
		t.Fatalf("FiniteLimit() error = %v", limitErr)
	}
	configuration.Completion.MaxRuns = two
	configuration.Completion.FailureLimit = two
	fixture := submitSupervisorFixtureWithPolicy(t, true, true, configuration)

	if err := Run(t.Context(), fixture.stateDir, fixture.jobID.String(),
		bytes.NewReader(fixture.credential), new(closingBuffer)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 2 || runs[0].Outcome != model.RunOutcomeTimedOut || runs[1].Outcome != model.RunOutcomeTimedOut {
		t.Fatalf("runs = %#v, want two timed-out attempts", runs)
	}
}

func TestWholeJobTimeoutRemainsTimedOut(t *testing.T) {
	configuration := model.DefaultExecutionPolicy()
	configuration.JobTimeout = 50 * time.Millisecond
	fixture := submitSupervisorFixtureWithPolicy(t, true, true, configuration)

	if err := Run(t.Context(), fixture.stateDir, fixture.jobID.String(),
		bytes.NewReader(fixture.credential), new(closingBuffer)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Outcome != model.JobOutcomeTimedOut {
		t.Fatalf("job outcome = %q, want timed_out", job.Outcome)
	}
}

func TestRetryableRunTimeoutsStopAtWholeJobTimeout(t *testing.T) {
	configuration := model.DefaultExecutionPolicy()
	configuration.RunTimeout = 40 * time.Millisecond
	// Leave enough whole-job budget for multiple instrumented process launches;
	// race and coverage builds can add hundreds of milliseconds per helper run.
	configuration.JobTimeout = 2 * time.Second
	configuration.Classification.RetryTimeout = true
	configuration.Completion = policy.CompletionPolicy{
		MaxRuns:       policy.UnlimitedLimit(),
		SuccessTarget: policy.Limit{Value: 1},
		FailureLimit:  policy.UnlimitedLimit(),
	}
	fixture := submitSupervisorFixtureWithPolicy(t, true, true, configuration)

	if err := Run(
		t.Context(),
		fixture.stateDir,
		fixture.jobID.String(),
		bytes.NewReader(fixture.credential),
		new(closingBuffer),
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Outcome != model.JobOutcomeTimedOut || job.LastDiagnosticCode != "job_timeout" {
		t.Fatalf("job after timeout retries = %#v, want timed_out", job)
	}
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) < 2 {
		t.Fatalf("runs = %#v, want multiple run-timeout attempts before whole-job timeout", runs)
	}
	for index, run := range runs {
		if run.Outcome != model.RunOutcomeTimedOut {
			t.Errorf("run %d outcome = %q, want timed_out", index+1, run.Outcome)
		}
	}
	runtimeState, err := database.GetRuntime(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetRuntime() error = %v", err)
	}
	if runtimeState.RunCount != uint64(len(runs)) || runtimeState.FailureCount != uint64(len(runs)) {
		t.Fatalf("runtime counts = %#v, want %d completed failures", runtimeState, len(runs))
	}
}

func TestWholeJobTimeoutBoundsRetryBackoff(t *testing.T) {
	configuration := model.DefaultExecutionPolicy()
	configuration.JobTimeout = 250 * time.Millisecond
	configuration.FailureDelay = policy.DelayPolicy{Base: time.Hour, Backoff: policy.BackoffConstant}
	two, err := policy.FiniteLimit(2)
	if err != nil {
		t.Fatalf("FiniteLimit() error = %v", err)
	}
	configuration.Completion.MaxRuns = two
	configuration.Completion.FailureLimit = two
	fixture := submitSupervisorFixtureWithPolicyAndExit(t, configuration, 1)

	started := time.Now()
	if runErr := Run(
		t.Context(), fixture.stateDir, fixture.jobID.String(),
		bytes.NewReader(fixture.credential), new(closingBuffer),
	); runErr != nil {
		t.Fatalf("Run() error = %v", runErr)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("whole-job timeout took %s during one-hour backoff", elapsed)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if job.Outcome != model.JobOutcomeTimedOut || len(runs) != 1 {
		t.Fatalf("backoff timeout job=%#v runs=%#v", job, runs)
	}
}

func TestSchedulingDeadlinesBoundAdmissionWait(t *testing.T) {
	tests := []struct {
		name          string
		configuration func(string) model.ExecutionPolicy
		wantOutcome   model.JobOutcome
		wantCode      string
	}{
		{
			name: "whole job timeout",
			configuration: func(string) model.ExecutionPolicy {
				configuration := model.DefaultExecutionPolicy()
				configuration.JobTimeout = 250 * time.Millisecond
				return configuration
			},
			wantOutcome: model.JobOutcomeTimedOut,
			wantCode:    "job_timeout",
		},
		{
			name: "wait abort after prerequisite satisfaction",
			configuration: func(ready string) model.ExecutionPolicy {
				configuration := model.DefaultExecutionPolicy()
				configuration.WaitConditions = []model.WaitCondition{{
					Kind: model.WaitFileExists, Path: ready, PollInterval: 10 * time.Millisecond,
					AbortAt: time.Now().UTC().Add(250 * time.Millisecond),
				}}
				return configuration
			},
			wantOutcome: model.JobOutcomeAborted,
			wantCode:    "wait_abort_deadline",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
				t.Fatalf("create ready file: %v", err)
			}
			fixture := submitSupervisorFixtureWithPolicy(t, true, false, test.configuration(ready))
			installAdmissionBlocker(t, fixture.stateDir)

			if err := Run(
				t.Context(), fixture.stateDir, fixture.jobID.String(),
				bytes.NewReader(fixture.credential), new(closingBuffer),
			); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			database := openSupervisorStore(t, fixture.stateDir)
			defer closeSupervisorStore(t, database)
			job, err := database.GetJob(t.Context(), fixture.jobID)
			if err != nil {
				t.Fatalf("GetJob() error = %v", err)
			}
			runs, err := database.ListRuns(t.Context(), fixture.jobID)
			if err != nil {
				t.Fatalf("ListRuns() error = %v", err)
			}
			if job.Outcome != test.wantOutcome || job.LastDiagnosticCode != test.wantCode || len(runs) != 0 {
				t.Fatalf("admission deadline job=%#v runs=%#v", job, runs)
			}
		})
	}
}

func TestReconcileExpiredAdmissionsReleasesDeadOwner(t *testing.T) {
	stateDir := t.TempDir()
	// State directories need owner traversal in addition to read and write.
	//nolint:gosec // G302's file-oriented recommendation does not apply to directories.
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("make state directory private: %v", err)
	}
	database := openSupervisorStore(t, stateDir)
	defer closeSupervisorStore(t, database)
	now := time.Now().UTC()
	jobID := submitOwnedAdmissionFixture(t, database, now)

	reconciled, err := reconcileExpiredOwnership(t.Context(), database, now.Add(time.Second))
	if err != nil {
		t.Fatalf("reconcileExpiredOwnership() error = %v", err)
	}
	if !reconciled {
		t.Fatal("reconcileExpiredOwnership() did not reconcile dead owner")
	}
	job, err := database.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	admission, found, err := database.GetAdmission(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetAdmission() error = %v", err)
	}
	if job.Outcome != model.JobOutcomeLost || !found || admission.ReleasedAt == nil {
		t.Fatalf("reconciled job=%#v admission=%#v found=%t", job, admission, found)
	}
}

func TestRunWaitConditionsRespectAllAndAnyModes(t *testing.T) {
	tests := []struct {
		name           string
		mode           policy.WaitMode
		createBlocked  bool
		wantWaitPhase  bool
		wantRunOutcome model.RunOutcome
	}{
		{
			name:           "all waits for every condition",
			mode:           policy.WaitModeAll,
			createBlocked:  true,
			wantWaitPhase:  true,
			wantRunOutcome: model.RunOutcomeSuccess,
		},
		{
			name:           "any starts after one condition",
			mode:           policy.WaitModeAny,
			wantRunOutcome: model.RunOutcomeSuccess,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			waitDirectory := t.TempDir()
			ready := filepath.Join(waitDirectory, "ready")
			blocked := filepath.Join(waitDirectory, "blocked")
			if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
				t.Fatalf("create satisfied wait fixture: %v", err)
			}
			configuration := model.DefaultExecutionPolicy()
			configuration.WaitMode = test.mode
			configuration.WaitConditions = []model.WaitCondition{
				{
					Kind: model.WaitFileExists, Path: ready,
					PollInterval: 10 * time.Millisecond,
				},
				{
					Kind: model.WaitFileExists, Path: blocked,
					PollInterval: 10 * time.Millisecond,
				},
			}
			fixture := submitSupervisorFixtureWithPolicy(t, true, false, configuration)

			done := make(chan error, 1)
			go func() {
				done <- Run(
					t.Context(),
					fixture.stateDir,
					fixture.jobID.String(),
					bytes.NewReader(fixture.credential),
					new(closingBuffer),
				)
			}()
			database := openSupervisorStore(t, fixture.stateDir)
			defer closeSupervisorStore(t, database)
			if test.wantWaitPhase {
				waitForSupervisorJobPhase(t, database, fixture.jobID, model.JobPhaseWaiting)
				runs, err := database.ListRuns(t.Context(), fixture.jobID)
				if err != nil {
					t.Fatalf("ListRuns(waiting) error = %v", err)
				}
				if len(runs) != 0 {
					t.Fatalf("runs while an all-mode condition is pending = %#v", runs)
				}
				runtimeState, err := database.GetRuntime(t.Context(), fixture.jobID)
				if err != nil {
					t.Fatalf("GetRuntime(waiting) error = %v", err)
				}
				if runtimeState.PrerequisitesSatisfiedAt != nil {
					t.Fatalf("prerequisites marked satisfied while wait is pending: %v",
						runtimeState.PrerequisitesSatisfiedAt)
				}
			}
			if test.createBlocked {
				if err := os.WriteFile(blocked, []byte("ready"), 0o600); err != nil {
					t.Fatalf("satisfy blocked wait fixture: %v", err)
				}
			}

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Run() error = %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Run() did not complete after wait conditions were satisfied")
			}
			runs, err := database.ListRuns(t.Context(), fixture.jobID)
			if err != nil {
				t.Fatalf("ListRuns() error = %v", err)
			}
			if len(runs) != 1 || runs[0].Outcome != test.wantRunOutcome {
				t.Fatalf("runs = %#v, want one %q run", runs, test.wantRunOutcome)
			}
			if test.createBlocked {
				runtimeState, err := database.GetRuntime(t.Context(), fixture.jobID)
				if err != nil {
					t.Fatalf("GetRuntime() error = %v", err)
				}
				if runtimeState.PrerequisitesSatisfiedAt == nil {
					t.Fatal("prerequisites were not marked satisfied after final wait became ready")
				}
			}
		})
	}
}

func TestRunWaitAbortCompletesWithoutStartingTarget(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	configuration := model.DefaultExecutionPolicy()
	configuration.WaitConditions = []model.WaitCondition{{
		Kind:         model.WaitFileExists,
		Path:         missing,
		PollInterval: 10 * time.Millisecond,
		AbortAt:      time.Now().UTC().Add(200 * time.Millisecond),
	}}
	fixture := submitSupervisorFixtureWithPolicy(t, true, false, configuration)

	if err := Run(
		t.Context(),
		fixture.stateDir,
		fixture.jobID.String(),
		bytes.NewReader(fixture.credential),
		new(closingBuffer),
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	database := openSupervisorStore(t, fixture.stateDir)
	defer closeSupervisorStore(t, database)
	job, err := database.GetJob(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Outcome != model.JobOutcomeAborted || job.LastDiagnosticCode != "wait_abort_deadline" {
		t.Fatalf("aborted wait job = %#v", job)
	}
	runs, err := database.ListRuns(t.Context(), fixture.jobID)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs after wait abort = %#v, want none", runs)
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
	if encoded := os.Getenv(targetExitCode); encoded != "" {
		code, err := strconv.Atoi(encoded)
		if err != nil {
			t.Fatalf("parse helper exit code: %v", err)
		}
		os.Exit(code) //nolint:revive // This test is intentionally re-executed as a subprocess target with a configured exit status.
	}
}

// TestSupervisorNotificationHelper is executed as a direct command notifier.
// Successful process exit is sufficient; the parent test verifies the durable
// queue and attempt history instead of relying on helper output.
func TestSupervisorNotificationHelper(*testing.T) {}

func submitSupervisorFixture(t *testing.T, executable bool) supervisorFixture {
	t.Helper()

	return submitSupervisorFixtureWithOptions(t, executable, false)
}

func submitSupervisorFixtureWithOptions(t *testing.T, executable, block bool) supervisorFixture {
	t.Helper()

	return submitSupervisorFixtureWithPolicy(t, executable, block, model.DefaultExecutionPolicy())
}

func submitSupervisorFixtureWithPolicy(
	t *testing.T,
	executable,
	block bool,
	executionPolicy model.ExecutionPolicy,
) supervisorFixture {
	t.Helper()

	return submitSupervisorFixtureWithPolicyOptions(t, executable, block, executionPolicy, "")
}

func submitSupervisorFixtureWithPolicyAndExit(
	t *testing.T,
	executionPolicy model.ExecutionPolicy,
	exitCode int,
) supervisorFixture {
	t.Helper()

	return submitSupervisorFixtureWithPolicyOptions(
		t,
		true,
		false,
		executionPolicy,
		strconv.Itoa(exitCode),
	)
}

func submitSupervisorFixtureWithPolicyOptions(
	t *testing.T,
	executable,
	block bool,
	executionPolicy model.ExecutionPolicy,
	exitCode string,
) supervisorFixture {
	t.Helper()

	return submitSupervisorFixtureWithPolicyOptionsAndStdin(
		t, executable, block, executionPolicy, exitCode, model.StdinNull,
	)
}

func submitSupervisorFixtureWithPolicyOptionsAndStdin(
	t *testing.T,
	executable,
	block bool,
	executionPolicy model.ExecutionPolicy,
	exitCode string,
	stdinPolicy model.StdinPolicy,
) supervisorFixture {
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
		if exitCode != "" {
			environment[targetExitCode] = exitCode
		}
	}

	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable:       target,
		Arguments:        arguments,
		WorkingDirectory: workingDirectory,
		Environment:      environment,
		StdinPolicy:      stdinPolicy,
		StopPolicy: model.StopPolicy{
			GracePeriod:     time.Second,
			ForceAfterGrace: true,
		},
		ExecutionPolicy: executionPolicy,
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

func installAdmissionBlocker(t *testing.T, stateDir string) {
	t.Helper()

	database := openSupervisorStore(t, stateDir)
	defer closeSupervisorStore(t, database)
	now := time.Now().UTC()
	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		t.Fatalf("create blocker IDs: %v", err)
	}
	jobID, err := ids.NewJobID()
	if err != nil {
		t.Fatalf("create blocker job ID: %v", err)
	}
	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable: "blocker", WorkingDirectory: t.TempDir(),
		ExecutionPolicy: model.DefaultExecutionPolicy(),
	})
	if err != nil {
		t.Fatalf("create blocker specification: %v", err)
	}
	credential := bytes.Repeat([]byte{0x5a}, credentialSize)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("hash blocker credential: %v", err)
	}
	if _, err := database.Submit(
		t.Context(), jobID, specification, hash, now, now.Add(time.Minute),
	); err != nil {
		t.Fatalf("submit blocker: %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), jobID, "", 1, now, time.Hour,
	); err != nil {
		t.Fatalf("acquire blocker admission: %v", err)
	}
}

func submitOwnedAdmissionFixture(t *testing.T, database *store.Store, now time.Time) model.JobID {
	t.Helper()

	capacity := uint64(1)
	if err := database.SetConcurrencyLimit(t.Context(), "", &capacity, now); err != nil {
		t.Fatalf("SetConcurrencyLimit() error = %v", err)
	}
	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		t.Fatalf("create expired-owner IDs: %v", err)
	}
	jobID, err := ids.NewJobID()
	if err != nil {
		t.Fatalf("create expired-owner job ID: %v", err)
	}
	supervisorID, err := ids.NewSupervisorID()
	if err != nil {
		t.Fatalf("create expired-owner supervisor ID: %v", err)
	}
	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable: "expired-owner", WorkingDirectory: t.TempDir(),
		ExecutionPolicy: model.DefaultExecutionPolicy(),
	})
	if err != nil {
		t.Fatalf("create expired-owner specification: %v", err)
	}
	credential := bytes.Repeat([]byte{0x6b}, credentialSize)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("hash expired-owner credential: %v", err)
	}
	if _, err := database.Submit(
		t.Context(), jobID, specification, hash, now, now.Add(time.Minute),
	); err != nil {
		t.Fatalf("submit expired owner: %v", err)
	}
	if _, err := database.Claim(
		t.Context(),
		jobID,
		credential,
		supervisorID,
		model.ProcessIdentity{
			PID: 2_000_000_000, Platform: runtime.GOOS, CreationID: "missing-owner",
			BootID: "missing-boot", TreeID: "missing-tree",
		},
		now,
		now.Add(time.Millisecond),
	); err != nil {
		t.Fatalf("claim expired owner: %v", err)
	}
	if _, err := database.TryAcquireAdmission(
		t.Context(), jobID, "", 1, now, time.Millisecond,
	); err != nil {
		t.Fatalf("acquire expired admission: %v", err)
	}

	return jobID
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
