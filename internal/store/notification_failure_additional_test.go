//nolint:gosec // Failure-injection tests derive deterministic identifiers from bounded table indexes.
package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestNotificationQueueDatabaseFailureStages(t *testing.T) {
	t.Parallel()

	t.Run("state event query", func(t *testing.T) {
		database, input, _ := queuedNotificationFixture(t, 0x13d00, false)
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE state_events RENAME TO unavailable_state_events`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.QueueNotificationDeliveries(t.Context(), []QueueNotificationDeliveryInput{input}); err == nil {
			t.Fatal("QueueNotificationDeliveries() error = nil")
		}
	})

	t.Run("delivery insert", func(t *testing.T) {
		database, input, _ := queuedNotificationFixture(t, 0x13d20, false)
		installAbortTrigger(t, database, "fail_delivery_insert", "INSERT", "notification_deliveries")
		if _, err := database.QueueNotificationDeliveries(t.Context(), []QueueNotificationDeliveryInput{input}); err == nil {
			t.Fatal("QueueNotificationDeliveries() error = nil")
		}
	})
}

func TestNotificationClaimAndRenewDatabaseFailures(t *testing.T) {
	t.Parallel()

	t.Run("claim update", func(t *testing.T) {
		database, input, now := queuedNotificationFixture(t, 0x13e00, true)
		installAbortTrigger(t, database, "fail_delivery_claim", "UPDATE", "notification_deliveries")
		if _, err := database.ClaimNotificationDelivery(t.Context(), input.EventID, now, now.Add(time.Minute)); err == nil {
			t.Fatal("ClaimNotificationDelivery() error = nil")
		}
	})

	t.Run("corrupt ready row", func(t *testing.T) {
		database, input, now := queuedNotificationFixture(t, 0x13e20, true)
		ignoreCheckConstraints(t, database)
		if _, err := database.db.ExecContext(t.Context(), `
			UPDATE notification_deliveries SET max_attempts = 0 WHERE event_id = ?`, input.EventID.String()); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ClaimNotificationDelivery(t.Context(), input.EventID, now, now.Add(time.Minute)); err == nil {
			t.Fatal("ClaimNotificationDelivery(corrupt row) error = nil")
		}
	})

	t.Run("renew update", func(t *testing.T) {
		database, input, now := queuedNotificationFixture(t, 0x13e40, true)
		claimed, err := database.ClaimNotificationDelivery(t.Context(), input.EventID, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE notification_deliveries RENAME TO unavailable_deliveries`); err != nil {
			t.Fatal(err)
		}
		if err := database.RenewNotificationDelivery(
			t.Context(), input.EventID, input.NotifierName, claimed.ClaimToken, now, now.Add(2*time.Minute),
		); err == nil {
			t.Fatal("RenewNotificationDelivery() error = nil")
		}
	})
}

func TestNotificationCompletionDatabaseFailureStages(t *testing.T) {
	t.Parallel()

	for index, stage := range []struct {
		name, operation, table string
	}{
		{name: "attempt insert", operation: "INSERT", table: "notification_attempts"},
		{name: "delivery update", operation: "UPDATE", table: "notification_deliveries"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			database, input, now := queuedNotificationFixture(t, uint64(0x13f00+index*0x20), true)
			claimed, err := database.ClaimNotificationDelivery(t.Context(), input.EventID, now, now.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			installAbortTrigger(t, database, fmt.Sprintf("fail_notification_completion_%d", index), stage.operation, stage.table)
			if _, err := database.CompleteNotificationDelivery(t.Context(), CompleteNotificationDeliveryInput{
				EventID: input.EventID, ClaimToken: claimed.ClaimToken, NotifierName: input.NotifierName,
				AttemptNumber: 1, StartedAt: now, CompletedAt: now.Add(time.Second), Succeeded: true,
			}); err == nil {
				t.Fatal("CompleteNotificationDelivery() error = nil")
			}
		})
	}

	t.Run("attempt identifier", func(t *testing.T) {
		database, input, now := queuedNotificationFixture(t, 0x13f60, true)
		claimed, err := database.ClaimNotificationDelivery(t.Context(), input.EventID, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		database.eventIDs = failingEventIDSource{err: errors.New("entropy failed")}
		if _, err := database.CompleteNotificationDelivery(t.Context(), CompleteNotificationDeliveryInput{
			EventID: input.EventID, ClaimToken: claimed.ClaimToken, NotifierName: input.NotifierName,
			AttemptNumber: 1, StartedAt: now, CompletedAt: now.Add(time.Second), Succeeded: true,
		}); err == nil {
			t.Fatal("CompleteNotificationDelivery(identifier failure) error = nil")
		}
	})
}

func TestNotificationReadersRejectDatabaseFailures(t *testing.T) {
	t.Parallel()

	database, input, _ := queuedNotificationFixture(t, 0x13fa0, true)
	ignoreCheckConstraints(t, database)
	if _, err := database.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `
		UPDATE notification_deliveries SET event_id = 'invalid' WHERE event_id = ?`, input.EventID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ListNotificationDeliveries(t.Context(), input.JobID); err == nil {
		t.Fatal("ListNotificationDeliveries(corrupt row) error = nil")
	}
	if _, err := database.db.ExecContext(t.Context(), `ALTER TABLE notification_deliveries RENAME TO unavailable_deliveries`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.NextNotificationDeliveryAt(t.Context(), model.EventID("")); err == nil {
		t.Fatal("NextNotificationDeliveryAt() error = nil")
	}
}

func queuedNotificationFixture(
	t *testing.T,
	prefix uint64,
	queue bool,
) (*Store, QueueNotificationDeliveryInput, time.Time) {
	t.Helper()
	database := openTestStore(t, "notification-failure", newSequentialEventIDs(prefix))
	now := storeTestTime()
	jobID := mustJobID(t, prefix+1, 1)
	submitRuntimeJob(t, database, jobID, now)
	event := notificationJobEvent(t, database, jobID)
	input := QueueNotificationDeliveryInput{
		JobID: jobID, EventID: event.ID, NotifierName: "ops", EventType: "job_started", MaxAttempts: 2,
	}
	if queue {
		if _, err := database.QueueNotificationDeliveries(t.Context(), []QueueNotificationDeliveryInput{input}); err != nil {
			t.Fatal(err)
		}
	}

	return database, input, now
}
