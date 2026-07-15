package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestNotificationAttemptsRecordRepeatedEventsAndRetries(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "notification-attempts", newSequentialEventIDs(0xe000))
	now := storeTestTime()
	jobID := mustJobID(t, 0xe001, 1)
	submitRuntimeJob(t, database, jobID, now)
	firstEvent := mustEventID(t, 0xe001, 2)
	secondEvent := mustEventID(t, 0xe001, 3)
	statusUnavailable := 503
	next := now.Add(5 * time.Second)
	firstInput := RecordNotificationAttemptInput{
		JobID: jobID, EventID: firstEvent, NotifierName: "webhook", EventType: "run_failed",
		AttemptNumber: 1, StartedAt: now.Add(time.Second), CompletedAt: now.Add(2 * time.Second),
		NextAttemptAt: &next, DiagnosticCode: "transport", Retryable: true,
		ResponseStatusCode: &statusUnavailable, ResponseTruncated: true,
	}
	first, err := database.RecordNotificationAttempt(t.Context(), firstInput)
	if err != nil {
		t.Fatalf("RecordNotificationAttempt(first) error = %v", err)
	}
	if first.Status != NotificationAttemptFailed || first.EventID != firstEvent || first.DiagnosticCode != "transport" ||
		first.ResponseStatusCode == nil || *first.ResponseStatusCode != statusUnavailable || !first.ResponseTruncated {
		t.Fatalf("first attempt = %#v", first)
	}

	statusNoContent := 204
	secondAttempt, err := database.RecordNotificationAttempt(t.Context(), RecordNotificationAttemptInput{
		JobID: jobID, EventID: firstEvent, NotifierName: "webhook", EventType: "run_failed",
		AttemptNumber: 2, StartedAt: now.Add(5 * time.Second), CompletedAt: now.Add(6 * time.Second),
		Succeeded: true, ResponseStatusCode: &statusNoContent,
	})
	if err != nil {
		t.Fatalf("RecordNotificationAttempt(retry) error = %v", err)
	}
	if secondAttempt.Status != NotificationAttemptSucceeded || secondAttempt.AttemptNumber != 2 {
		t.Fatalf("second attempt = %#v", secondAttempt)
	}

	// A later run can emit the same event type and start its own attempt
	// numbering because uniqueness is scoped by the stable event ID.
	if _, recordErr := database.RecordNotificationAttempt(t.Context(), RecordNotificationAttemptInput{
		JobID: jobID, EventID: secondEvent, NotifierName: "webhook", EventType: "run_failed",
		AttemptNumber: 1, StartedAt: now.Add(7 * time.Second), CompletedAt: now.Add(8 * time.Second),
		Succeeded: true, ResponseStatusCode: &statusNoContent,
	}); recordErr != nil {
		t.Fatalf("RecordNotificationAttempt(repeated event type) error = %v", recordErr)
	}

	attempts, err := database.ListNotificationAttempts(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListNotificationAttempts() error = %v", err)
	}
	if len(attempts) != 3 || attempts[0].EventID != firstEvent || attempts[1].AttemptNumber != 2 ||
		attempts[2].EventID != secondEvent || attempts[2].AttemptNumber != 1 {
		t.Fatalf("notification attempts = %#v", attempts)
	}
}

func TestRecordNotificationAttemptIsIdempotentAndDetectsConflict(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "notification-attempt-idempotency", newSequentialEventIDs(0xe100))
	now := storeTestTime()
	jobID := mustJobID(t, 0xe101, 1)
	submitRuntimeJob(t, database, jobID, now)
	input := RecordNotificationAttemptInput{
		JobID: jobID, EventID: mustEventID(t, 0xe101, 2), NotifierName: "mail", EventType: "job_failed",
		AttemptNumber: 1, StartedAt: now.Add(time.Second), CompletedAt: now.Add(2 * time.Second),
		DiagnosticCode: "rejected", Retryable: true, MessageID: "event-1@example.test",
	}
	first, err := database.RecordNotificationAttempt(t.Context(), input)
	if err != nil {
		t.Fatalf("RecordNotificationAttempt(first) error = %v", err)
	}
	replayed, err := database.RecordNotificationAttempt(t.Context(), input)
	if err != nil {
		t.Fatalf("RecordNotificationAttempt(replay) error = %v", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("replay ID = %s, want %s", replayed.ID, first.ID)
	}

	input.Retryable = false
	if _, conflictErr := database.RecordNotificationAttempt(t.Context(), input); !errors.Is(conflictErr, ErrConflict) {
		t.Fatalf("RecordNotificationAttempt(conflict) error = %v, want ErrConflict", conflictErr)
	}
}

func TestRecordNotificationAttemptRejectsInvalidOrSecretLikeMetadata(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "notification-attempt-validation", newSequentialEventIDs(0xe200))
	now := storeTestTime()
	jobID := mustJobID(t, 0xe201, 1)
	submitRuntimeJob(t, database, jobID, now)
	base := RecordNotificationAttemptInput{
		JobID: jobID, EventID: mustEventID(t, 0xe201, 2), NotifierName: "hook", EventType: "job_failed",
		AttemptNumber: 1, StartedAt: now.Add(time.Second), CompletedAt: now.Add(2 * time.Second),
		DiagnosticCode: "internal",
	}
	tests := map[string]func(*RecordNotificationAttemptInput){
		"unknown event":           func(input *RecordNotificationAttemptInput) { input.EventType = "arbitrary" },
		"unstructured diagnostic": func(input *RecordNotificationAttemptInput) { input.DiagnosticCode = "token=secret" },
		"invalid chronology":      func(input *RecordNotificationAttemptInput) { input.CompletedAt = input.StartedAt.Add(-time.Second) },
		"success with diagnostic": func(input *RecordNotificationAttemptInput) { input.Succeeded = true },
		"control in message ID":   func(input *RecordNotificationAttemptInput) { input.MessageID = "ok\r\nBcc: bad" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := base
			mutate(&input)
			if _, err := database.RecordNotificationAttempt(t.Context(), input); err == nil {
				t.Fatal("RecordNotificationAttempt() error = nil")
			}
		})
	}

	var unsafeColumnCount int
	if err := database.db.QueryRowContext(
		t.Context(),
		"SELECT count(*) FROM pragma_table_info('notification_attempts') WHERE name = 'response_excerpt'",
	).Scan(&unsafeColumnCount); err != nil {
		t.Fatalf("inspect notification schema: %v", err)
	}
	if unsafeColumnCount != 0 {
		t.Fatal("notification_attempts retains an unsafe response excerpt column")
	}
}

func TestListNotificationAttemptsRejectsInvalidJobID(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "notification-attempt-list", newSequentialEventIDs(0xe300))
	if _, err := database.ListNotificationAttempts(t.Context(), model.JobID("")); err == nil {
		t.Fatal("ListNotificationAttempts() error = nil")
	}
}

func TestNotificationDeliveryReplaysExpiredClaimAfterReopen(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := openNotificationRecoveryStore(t, stateDir, newSequentialEventIDs(0xe400))
	now := storeTestTime()
	jobID := mustJobID(t, 0xe401, 1)
	submitRuntimeJob(t, first, jobID, now)
	event := notificationJobEvent(t, first, jobID)
	queued, err := first.QueueNotificationDeliveries(t.Context(), []QueueNotificationDeliveryInput{
		{JobID: jobID, EventID: event.ID, NotifierName: "webhook", EventType: "job_started", MaxAttempts: 3},
	})
	if err != nil {
		t.Fatalf("QueueNotificationDeliveries() error = %v", err)
	}
	if len(queued) != 1 || queued[0].Status != NotificationDeliveryPending ||
		!queued[0].OccurredAt.Equal(event.OccurredAt) {
		t.Fatalf("queued delivery = %#v, event = %#v", queued, event)
	}

	claimedAt := now.Add(time.Second)
	leaseExpiresAt := claimedAt.Add(10 * time.Second)
	firstClaim, err := first.ClaimNotificationDelivery(t.Context(), event.ID, claimedAt, leaseExpiresAt)
	if err != nil {
		t.Fatalf("ClaimNotificationDelivery() error = %v", err)
	}
	if firstClaim.Status != NotificationDeliveryDelivering || firstClaim.NextAttemptNumber() != 1 {
		t.Fatalf("first claim = %#v", firstClaim)
	}
	if closeErr := first.Close(); closeErr != nil {
		t.Fatalf("close first store: %v", closeErr)
	}

	second := openNotificationRecoveryStore(t, stateDir, newSequentialEventIDs(0xe500))
	if _, claimErr := second.ClaimNotificationDelivery(
		t.Context(), event.ID, leaseExpiresAt.Add(-time.Nanosecond), leaseExpiresAt.Add(time.Minute),
	); !errors.Is(claimErr, ErrNotFound) {
		t.Fatalf("claim before lease expiry error = %v, want ErrNotFound", claimErr)
	}
	replayed, err := second.ClaimNotificationDelivery(
		t.Context(), event.ID, leaseExpiresAt, leaseExpiresAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("claim expired delivery error = %v", err)
	}
	if replayed.EventID != firstClaim.EventID || replayed.ClaimToken == firstClaim.ClaimToken ||
		replayed.NextAttemptNumber() != firstClaim.NextAttemptNumber() {
		t.Fatalf("replayed claim = %#v, first = %#v", replayed, firstClaim)
	}
	completedAt := leaseExpiresAt.Add(time.Second)
	completed, err := second.CompleteNotificationDelivery(t.Context(), CompleteNotificationDeliveryInput{
		EventID: replayed.EventID, ClaimToken: replayed.ClaimToken, NotifierName: replayed.NotifierName,
		AttemptNumber: replayed.NextAttemptNumber(), StartedAt: leaseExpiresAt,
		CompletedAt: completedAt, Succeeded: true,
	})
	if err != nil {
		t.Fatalf("CompleteNotificationDelivery() error = %v", err)
	}
	if completed.Status != NotificationDeliverySucceeded || completed.AttemptCount != 1 {
		t.Fatalf("completed delivery = %#v", completed)
	}
	if closeErr := second.Close(); closeErr != nil {
		t.Fatalf("close second store: %v", closeErr)
	}

	third := openNotificationRecoveryStore(t, stateDir, newSequentialEventIDs(0xe600))
	if _, claimErr := third.ClaimNotificationDelivery(
		t.Context(), event.ID, completedAt.Add(time.Second), completedAt.Add(time.Minute),
	); !errors.Is(claimErr, ErrNotFound) {
		t.Fatalf("claim completed delivery error = %v, want ErrNotFound", claimErr)
	}
	attempts, err := third.ListNotificationAttempts(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListNotificationAttempts() error = %v", err)
	}
	if len(attempts) != 1 || attempts[0].EventID != event.ID || attempts[0].AttemptNumber != 1 {
		t.Fatalf("recovered attempts = %#v", attempts)
	}
	if closeErr := third.Close(); closeErr != nil {
		t.Fatalf("close third store: %v", closeErr)
	}
}

func TestNotificationDeliveryRetryAndCompletionAreIdempotent(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "notification-delivery-idempotency", newSequentialEventIDs(0xe700))
	now := storeTestTime()
	jobID := mustJobID(t, 0xe701, 1)
	submitRuntimeJob(t, database, jobID, now)
	event := notificationJobEvent(t, database, jobID)
	if _, err := database.QueueNotificationDeliveries(t.Context(), []QueueNotificationDeliveryInput{
		{JobID: jobID, EventID: event.ID, NotifierName: "mail", EventType: "job_started", MaxAttempts: 2},
	}); err != nil {
		t.Fatalf("QueueNotificationDeliveries() error = %v", err)
	}

	first, err := database.ClaimNotificationDelivery(
		t.Context(), event.ID, now.Add(time.Second), now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("claim first attempt: %v", err)
	}
	nextAttemptAt := now.Add(10 * time.Second)
	firstCompletion := CompleteNotificationDeliveryInput{
		EventID: first.EventID, ClaimToken: first.ClaimToken, NotifierName: first.NotifierName,
		AttemptNumber: 1, StartedAt: now.Add(2 * time.Second), CompletedAt: now.Add(3 * time.Second),
		NextAttemptAt: &nextAttemptAt, DiagnosticCode: "transport", Retryable: true,
	}
	pending, err := database.CompleteNotificationDelivery(t.Context(), firstCompletion)
	if err != nil {
		t.Fatalf("complete retryable attempt: %v", err)
	}
	if pending.Status != NotificationDeliveryPending || pending.AttemptCount != 1 ||
		pending.NextAttemptAt == nil || !pending.NextAttemptAt.Equal(nextAttemptAt) {
		t.Fatalf("pending delivery = %#v", pending)
	}
	replayedPending, err := database.CompleteNotificationDelivery(t.Context(), firstCompletion)
	if err != nil {
		t.Fatalf("replay first completion: %v", err)
	}
	if replayedPending.Status != NotificationDeliveryPending || replayedPending.AttemptCount != 1 {
		t.Fatalf("replayed first completion = %#v", replayedPending)
	}
	if _, claimErr := database.ClaimNotificationDelivery(
		t.Context(), event.ID, nextAttemptAt.Add(-time.Nanosecond), nextAttemptAt.Add(time.Minute),
	); !errors.Is(claimErr, ErrNotFound) {
		t.Fatalf("claim before retry time error = %v, want ErrNotFound", claimErr)
	}

	second, err := database.ClaimNotificationDelivery(
		t.Context(), event.ID, nextAttemptAt, nextAttemptAt.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("claim second attempt: %v", err)
	}
	secondCompletion := CompleteNotificationDeliveryInput{
		EventID: second.EventID, ClaimToken: second.ClaimToken, NotifierName: second.NotifierName,
		AttemptNumber: 2, StartedAt: nextAttemptAt, CompletedAt: nextAttemptAt.Add(time.Second),
		Succeeded: true, MessageID: "stable-message@example.test",
	}
	succeeded, err := database.CompleteNotificationDelivery(t.Context(), secondCompletion)
	if err != nil {
		t.Fatalf("complete successful retry: %v", err)
	}
	replayedSuccess, err := database.CompleteNotificationDelivery(t.Context(), secondCompletion)
	if err != nil {
		t.Fatalf("replay successful completion: %v", err)
	}
	if succeeded.Status != NotificationDeliverySucceeded || replayedSuccess.Status != NotificationDeliverySucceeded ||
		succeeded.AttemptCount != 2 || replayedSuccess.AttemptCount != 2 {
		t.Fatalf("successful delivery = %#v, replay = %#v", succeeded, replayedSuccess)
	}
	attempts, err := database.ListNotificationAttempts(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListNotificationAttempts() error = %v", err)
	}
	if len(attempts) != 2 || attempts[0].AttemptNumber != 1 || attempts[1].AttemptNumber != 2 {
		t.Fatalf("attempts after completion replay = %#v", attempts)
	}
}

func TestQueueNotificationDeliveriesIsAtomicAndSecretFree(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "notification-delivery-queue", newSequentialEventIDs(0xe800))
	now := storeTestTime()
	jobID := mustJobID(t, 0xe801, 1)
	submitRuntimeJob(t, database, jobID, now)
	event := notificationJobEvent(t, database, jobID)
	inputs := []QueueNotificationDeliveryInput{
		{JobID: jobID, EventID: event.ID, NotifierName: "command", EventType: "job_started", MaxAttempts: 1},
		{JobID: jobID, EventID: event.ID, NotifierName: "webhook", EventType: "job_started", MaxAttempts: 4},
	}
	queued, err := database.QueueNotificationDeliveries(t.Context(), inputs)
	if err != nil {
		t.Fatalf("QueueNotificationDeliveries() error = %v", err)
	}
	if len(queued) != 2 {
		t.Fatalf("queued deliveries = %#v", queued)
	}
	replayed, err := database.QueueNotificationDeliveries(t.Context(), inputs)
	if err != nil || len(replayed) != 2 {
		t.Fatalf("QueueNotificationDeliveries(replay) = %#v, %v", replayed, err)
	}
	inputs[1].MaxAttempts++
	if _, conflictErr := database.QueueNotificationDeliveries(t.Context(), inputs); !errors.Is(conflictErr, ErrConflict) {
		t.Fatalf("QueueNotificationDeliveries(conflict) error = %v, want ErrConflict", conflictErr)
	}
	deliveries, err := database.ListNotificationDeliveries(t.Context(), jobID)
	if err != nil {
		t.Fatalf("ListNotificationDeliveries() error = %v", err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("durable deliveries = %#v", deliveries)
	}

	unsafeColumns := []string{"payload", "body", "url", "credential", "secret", "response_body"}
	for _, column := range unsafeColumns {
		var count int
		if queryErr := database.db.QueryRowContext(
			t.Context(),
			"SELECT count(*) FROM pragma_table_info('notification_deliveries') WHERE name = ?",
			column,
		).Scan(&count); queryErr != nil {
			t.Fatalf("inspect notification delivery column %q: %v", column, queryErr)
		}
		if count != 0 {
			t.Errorf("notification_deliveries contains unsafe column %q", column)
		}
	}
}

func openNotificationRecoveryStore(t *testing.T, stateDir string, source EventIDSource) *Store {
	t.Helper()
	database, err := Open(t.Context(), Options{
		StateDir: stateDir, BusyTimeout: time.Second, JobmanVersion: "notification-recovery-test",
		Now: storeTestTime, EventIDs: source,
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	return database
}

func notificationJobEvent(t *testing.T, database *Store, jobID model.JobID) TransitionEvent {
	t.Helper()
	job, err := database.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	event, err := database.TransitionEvent(t.Context(), model.EntityJob, jobID.String(), job.Revision)
	if err != nil {
		t.Fatalf("TransitionEvent() error = %v", err)
	}

	return event
}
