package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

// NotificationDeliveryStatus identifies the durable state of one event and
// notifier pair. A delivering row is recoverable after its claim lease
// expires; successful and permanently failed rows are terminal history.
type NotificationDeliveryStatus string

// Durable notification delivery states.
const (
	NotificationDeliveryPending    NotificationDeliveryStatus = "pending"
	NotificationDeliveryDelivering NotificationDeliveryStatus = "delivering"
	NotificationDeliverySucceeded  NotificationDeliveryStatus = "succeeded"
	NotificationDeliveryFailed     NotificationDeliveryStatus = "failed"
)

// QueueNotificationDeliveryInput identifies one subscribed notifier. Event
// envelope fields are loaded from the immutable state event so an idempotent
// replay cannot change its timestamp or run identity.
type QueueNotificationDeliveryInput struct {
	JobID        model.JobID
	EventID      model.EventID
	NotifierName string
	EventType    string
	MaxAttempts  int
}

// NotificationDelivery is durable pending or completed work. It deliberately
// contains no destination, credential, payload body, or raw error text.
type NotificationDelivery struct {
	JobID          model.JobID
	EventID        model.EventID
	RunID          model.RunID
	ClaimToken     model.EventID
	NotifierName   string
	EventType      string
	Status         NotificationDeliveryStatus
	OccurredAt     time.Time
	CreatedAt      time.Time
	NextAttemptAt  *time.Time
	ClaimedAt      *time.Time
	ClaimExpiresAt *time.Time
	CompletedAt    *time.Time
	MaxAttempts    int
	AttemptCount   int
}

// NextAttemptNumber returns the attempt number reserved by a current claim.
func (delivery NotificationDelivery) NextAttemptNumber() int {
	return delivery.AttemptCount + 1
}

const notificationDeliveryColumns = `
	job_id, event_id, notifier_name, event_type, run_id,
	occurred_at_ns, created_at_ns, max_attempts, attempt_count, status,
	next_attempt_at_ns, claim_token, claimed_at_ns, claim_expires_at_ns,
	completed_at_ns`

// QueueNotificationDeliveries atomically creates every subscribed
// event/notifier pair before any external delivery begins. Repeating an
// identical batch is idempotent; changing immutable work returns ErrConflict.
func (s *Store) QueueNotificationDeliveries(
	ctx context.Context,
	inputs []QueueNotificationDeliveryInput,
) ([]NotificationDelivery, error) {
	if len(inputs) == 0 {
		return []NotificationDelivery{}, nil
	}
	for _, input := range inputs {
		if err := validateQueueNotificationInput(input); err != nil {
			return nil, fmt.Errorf("queue notification delivery: %w", err)
		}
	}
	createdAt := s.now().UTC()
	if createdAt.IsZero() || createdAt.UnixNano() < 0 {
		return nil, errors.New("queue notification delivery: invalid store time")
	}

	queued := make([]NotificationDelivery, 0, len(inputs))
	err := s.writeTransaction(ctx, "notification delivery queue", func(tx *sql.Tx) error {
		for _, input := range inputs {
			recorded, queueErr := queueNotificationDeliveryTx(ctx, tx, input, createdAt)
			if queueErr != nil {
				return queueErr
			}
			queued = append(queued, recorded)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return queued, nil
}

func queueNotificationDeliveryTx(
	ctx context.Context,
	tx *sql.Tx,
	input QueueNotificationDeliveryInput,
	createdAt time.Time,
) (NotificationDelivery, error) {
	var runID sql.NullString
	var occurredAt int64
	err := tx.QueryRowContext(ctx, `
		SELECT run_id, occurred_at_ns
		FROM state_events
		WHERE id = ? AND job_id = ?`, input.EventID.String(), input.JobID.String()).Scan(&runID, &occurredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationDelivery{}, fmt.Errorf("load notification state event %s: %w", input.EventID, ErrNotFound)
	}
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf(
			"load notification state event %s: %w",
			input.EventID,
			classifySQLite("load notification state event", err),
		)
	}
	expected := NotificationDelivery{
		JobID: input.JobID, EventID: input.EventID, NotifierName: input.NotifierName,
		EventType: input.EventType, RunID: model.RunID(runID.String),
		Status: NotificationDeliveryPending, OccurredAt: timeFromDatabase(occurredAt),
		CreatedAt: createdAt, NextAttemptAt: cloneTime(&createdAt),
		MaxAttempts: input.MaxAttempts,
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO notification_deliveries (
			job_id, event_id, notifier_name, event_type, run_id,
			occurred_at_ns, created_at_ns, max_attempts, attempt_count,
			status, next_attempt_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 'pending', ?)
		ON CONFLICT(event_id, notifier_name) DO NOTHING`,
		expected.JobID.String(), expected.EventID.String(), expected.NotifierName,
		expected.EventType, nullableString(expected.RunID.String()), expected.OccurredAt.UnixNano(),
		expected.CreatedAt.UnixNano(), expected.MaxAttempts, expected.CreatedAt.UnixNano(),
	)
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf(
			"insert notification delivery: %w",
			classifySQLite("insert notification delivery", err),
		)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("read notification delivery insert result: %w", err)
	}
	recorded, err := scanNotificationDelivery(tx.QueryRowContext(
		ctx,
		"SELECT "+notificationDeliveryColumns+` FROM notification_deliveries
		 WHERE event_id = ? AND notifier_name = ?`,
		expected.EventID.String(), expected.NotifierName,
	))
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("read queued notification delivery: %w", err)
	}
	if rows == 0 && !sameNotificationDeliveryIdentity(recorded, expected) {
		return NotificationDelivery{}, fmt.Errorf(
			"queue notification delivery %s/%s: %w",
			expected.EventID,
			expected.NotifierName,
			ErrConflict,
		)
	}

	return recorded, nil
}

// ClaimNotificationDelivery leases the oldest ready delivery, optionally
// restricted to one state event. An expired delivering claim is resumed with
// the same attempt number and stable event ID.
func (s *Store) ClaimNotificationDelivery(
	ctx context.Context,
	eventID model.EventID,
	now time.Time,
	leaseExpiresAt time.Time,
) (NotificationDelivery, error) {
	if eventID != "" && !eventID.Valid() {
		return NotificationDelivery{}, errors.New("claim notification delivery: invalid event ID")
	}
	now = now.UTC()
	leaseExpiresAt = leaseExpiresAt.UTC()
	if now.IsZero() || now.UnixNano() < 0 || !leaseExpiresAt.After(now) {
		return NotificationDelivery{}, errors.New("claim notification delivery: invalid lease")
	}
	var claimed NotificationDelivery
	err := s.writeTransaction(ctx, "notification delivery claim", func(tx *sql.Tx) error {
		selected, selectErr := selectReadyNotificationDelivery(ctx, tx, eventID, now)
		if selectErr != nil {
			return selectErr
		}
		token, tokenErr := s.eventIDs.NewEventID()
		if tokenErr != nil {
			return fmt.Errorf("generate notification claim token: %w", tokenErr)
		}
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET status = 'delivering', next_attempt_at_ns = NULL,
				claim_token = ?, claimed_at_ns = ?, claim_expires_at_ns = ?
			WHERE event_id = ? AND notifier_name = ? AND
				((status = 'pending' AND next_attempt_at_ns <= ?) OR
				 (status = 'delivering' AND claim_expires_at_ns <= ?))`,
			token.String(), now.UnixNano(), leaseExpiresAt.UnixNano(),
			selected.EventID.String(), selected.NotifierName, now.UnixNano(), now.UnixNano(),
		)
		if updateErr != nil {
			return fmt.Errorf(
				"lease notification delivery: %w",
				classifySQLite("lease notification delivery", updateErr),
			)
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("read notification delivery lease result: %w", rowsErr)
		}
		if rows != 1 {
			return fmt.Errorf("lease notification delivery: %w", ErrConflict)
		}
		var readErr error
		claimed, readErr = scanNotificationDelivery(tx.QueryRowContext(
			ctx,
			"SELECT "+notificationDeliveryColumns+` FROM notification_deliveries
			 WHERE event_id = ? AND notifier_name = ?`,
			selected.EventID.String(), selected.NotifierName,
		))
		if readErr != nil {
			return fmt.Errorf("read claimed notification delivery: %w", readErr)
		}

		return nil
	})
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("claim notification delivery: %w", err)
	}

	return claimed, nil
}

func selectReadyNotificationDelivery(
	ctx context.Context,
	tx *sql.Tx,
	eventID model.EventID,
	now time.Time,
) (NotificationDelivery, error) {
	query := "SELECT " + notificationDeliveryColumns + ` FROM notification_deliveries
		WHERE ((status = 'pending' AND next_attempt_at_ns <= ?) OR
		       (status = 'delivering' AND claim_expires_at_ns <= ?))`
	arguments := []any{now.UnixNano(), now.UnixNano()}
	if eventID != "" {
		query += " AND event_id = ?"
		arguments = append(arguments, eventID.String())
	}
	query += ` ORDER BY
		CASE WHEN status = 'pending' THEN next_attempt_at_ns ELSE claim_expires_at_ns END,
		created_at_ns, event_id, notifier_name
		LIMIT 1`
	selected, err := scanNotificationDelivery(tx.QueryRowContext(ctx, query, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationDelivery{}, ErrNotFound
	}
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("select ready notification delivery: %w", err)
	}

	return selected, nil
}

// RenewNotificationDelivery extends a live claim. The token comparison keeps
// a stale worker from reclaiming ownership after another process resumes it.
func (s *Store) RenewNotificationDelivery(
	ctx context.Context,
	eventID model.EventID,
	notifierName string,
	claimToken model.EventID,
	now time.Time,
	leaseExpiresAt time.Time,
) error {
	if !eventID.Valid() || !validNotifierName(notifierName) || !claimToken.Valid() {
		return errors.New("renew notification delivery: invalid identity")
	}
	now = now.UTC()
	leaseExpiresAt = leaseExpiresAt.UTC()
	if now.IsZero() || now.UnixNano() < 0 || !leaseExpiresAt.After(now) {
		return errors.New("renew notification delivery: invalid lease")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET claim_expires_at_ns = ?
		WHERE event_id = ? AND notifier_name = ? AND status = 'delivering' AND claim_token = ?`,
		leaseExpiresAt.UnixNano(), eventID.String(), notifierName, claimToken.String(),
	)
	if err != nil {
		return fmt.Errorf(
			"renew notification delivery: %w",
			classifySQLite("renew notification delivery", err),
		)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("renew notification delivery: read update result: %w", err)
	}
	if rows != 1 {
		return fmt.Errorf("renew notification delivery: %w", ErrConflict)
	}

	return nil
}

// CompleteNotificationDeliveryInput records one external attempt and either
// schedules the next attempt or makes the delivery terminal. AttemptNumber is
// explicit so an exact completion replay remains idempotent after a later
// attempt has already been claimed.
type CompleteNotificationDeliveryInput struct {
	EventID            model.EventID
	ClaimToken         model.EventID
	NotifierName       string
	AttemptNumber      int
	StartedAt          time.Time
	CompletedAt        time.Time
	NextAttemptAt      *time.Time
	DiagnosticCode     string
	Retryable          bool
	Succeeded          bool
	ResponseStatusCode *int
	CommandExitCode    *int
	MessageID          string
	ResponseTruncated  bool
}

// CompleteNotificationDelivery atomically records an attempt and advances its
// queue row. Identical completion replay is a no-op; conflicting replay is
// rejected.
//
//nolint:gocognit,cyclop // Claim validation, replay, attempt insert, and advancement form one atomic state machine.
func (s *Store) CompleteNotificationDelivery(
	ctx context.Context,
	input CompleteNotificationDeliveryInput,
) (NotificationDelivery, error) {
	if !input.EventID.Valid() || !input.ClaimToken.Valid() || !validNotifierName(input.NotifierName) ||
		input.AttemptNumber < 1 || input.AttemptNumber > 100 {
		return NotificationDelivery{}, errors.New("complete notification delivery: invalid identity")
	}
	var completed NotificationDelivery
	err := s.writeTransaction(ctx, "notification delivery completion", func(tx *sql.Tx) error {
		delivery, scanErr := scanNotificationDelivery(tx.QueryRowContext(
			ctx,
			"SELECT "+notificationDeliveryColumns+` FROM notification_deliveries
			 WHERE event_id = ? AND notifier_name = ?`,
			input.EventID.String(), input.NotifierName,
		))
		if errors.Is(scanErr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if scanErr != nil {
			return fmt.Errorf("load notification delivery for completion: %w", scanErr)
		}
		attemptInput := RecordNotificationAttemptInput{
			JobID: delivery.JobID, EventID: delivery.EventID, NotifierName: delivery.NotifierName,
			EventType: delivery.EventType, AttemptNumber: input.AttemptNumber,
			StartedAt: input.StartedAt, CompletedAt: input.CompletedAt,
			NextAttemptAt: input.NextAttemptAt, DiagnosticCode: input.DiagnosticCode,
			Retryable: input.Retryable, Succeeded: input.Succeeded,
			ResponseStatusCode: input.ResponseStatusCode, CommandExitCode: input.CommandExitCode,
			MessageID: input.MessageID, ResponseTruncated: input.ResponseTruncated,
		}
		if validateErr := validateNotificationAttemptInput(attemptInput); validateErr != nil {
			return fmt.Errorf("validate notification delivery attempt: %w", validateErr)
		}
		expectedAttempt := notificationAttemptFromInput("", attemptInput)

		owned := delivery.Status == NotificationDeliveryDelivering &&
			delivery.ClaimToken == input.ClaimToken &&
			delivery.NextAttemptNumber() == input.AttemptNumber
		if !owned {
			recorded, existingErr := scanNotificationAttempt(tx.QueryRowContext(
				ctx,
				"SELECT "+notificationAttemptColumns+` FROM notification_attempts
				 WHERE event_id = ? AND notifier_name = ? AND attempt_number = ?`,
				input.EventID.String(), input.NotifierName, input.AttemptNumber,
			))
			if existingErr == nil && sameNotificationAttempt(recorded, expectedAttempt) {
				completed = delivery

				return nil
			}
			if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
				return fmt.Errorf("read prior notification completion: %w", existingErr)
			}

			return fmt.Errorf("complete notification delivery: %w", ErrConflict)
		}

		shouldRetry := !input.Succeeded && input.Retryable && input.AttemptNumber < delivery.MaxAttempts
		if shouldRetry != (input.NextAttemptAt != nil) {
			return errors.New("complete notification delivery: retry schedule does not match outcome")
		}
		attemptID, idErr := s.eventIDs.NewEventID()
		if idErr != nil {
			return fmt.Errorf("generate notification attempt ID: %w", idErr)
		}
		expectedAttempt.ID = attemptID
		if _, recordErr := recordNotificationAttemptTx(ctx, tx, expectedAttempt); recordErr != nil {
			return recordErr
		}

		status := NotificationDeliveryFailed
		var nextAttemptAt any
		var completedAt any = input.CompletedAt.UTC().UnixNano()
		if input.Succeeded {
			status = NotificationDeliverySucceeded
		} else if shouldRetry {
			status = NotificationDeliveryPending
			nextAttemptAt = input.NextAttemptAt.UTC().UnixNano()
			completedAt = nil
		}
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET attempt_count = ?, status = ?, next_attempt_at_ns = ?,
				claim_token = NULL, claimed_at_ns = NULL, claim_expires_at_ns = NULL,
				completed_at_ns = ?
			WHERE event_id = ? AND notifier_name = ? AND status = 'delivering' AND
				claim_token = ? AND attempt_count = ?`,
			input.AttemptNumber, string(status), nextAttemptAt, completedAt,
			input.EventID.String(), input.NotifierName, input.ClaimToken.String(),
			input.AttemptNumber-1,
		)
		if updateErr != nil {
			return fmt.Errorf(
				"advance notification delivery: %w",
				classifySQLite("advance notification delivery", updateErr),
			)
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return fmt.Errorf("read notification delivery completion result: %w", rowsErr)
		}
		if rows != 1 {
			return fmt.Errorf("advance notification delivery: %w", ErrConflict)
		}
		completed, scanErr = scanNotificationDelivery(tx.QueryRowContext(
			ctx,
			"SELECT "+notificationDeliveryColumns+` FROM notification_deliveries
			 WHERE event_id = ? AND notifier_name = ?`,
			input.EventID.String(), input.NotifierName,
		))
		if scanErr != nil {
			return fmt.Errorf("read completed notification delivery: %w", scanErr)
		}

		return nil
	})
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("complete notification delivery: %w", err)
	}

	return completed, nil
}

// NextNotificationDeliveryAt returns the earliest time unfinished work for an
// event can be claimed. A zero event ID considers all events.
func (s *Store) NextNotificationDeliveryAt(
	ctx context.Context,
	eventID model.EventID,
) (time.Time, bool, error) {
	if eventID != "" && !eventID.Valid() {
		return time.Time{}, false, errors.New("next notification delivery: invalid event ID")
	}
	query := `SELECT MIN(
		CASE WHEN status = 'pending' THEN next_attempt_at_ns ELSE claim_expires_at_ns END
	) FROM notification_deliveries WHERE status IN ('pending', 'delivering')`
	arguments := []any{}
	if eventID != "" {
		query += " AND event_id = ?"
		arguments = append(arguments, eventID.String())
	}
	var encoded sql.NullInt64
	if err := s.db.QueryRowContext(ctx, query, arguments...).Scan(&encoded); err != nil {
		return time.Time{}, false, fmt.Errorf(
			"next notification delivery: %w",
			classifySQLite("next notification delivery", err),
		)
	}
	if !encoded.Valid {
		return time.Time{}, false, nil
	}
	parsed := timeFromDatabase(encoded.Int64)

	return parsed, true, nil
}

// ListNotificationDeliveries returns durable notification work for inspection
// and recovery tests in stable creation order.
func (s *Store) ListNotificationDeliveries(
	ctx context.Context,
	jobID model.JobID,
) ([]NotificationDelivery, error) {
	if !jobID.Valid() {
		return nil, errors.New("list notification deliveries: invalid job ID")
	}
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT "+notificationDeliveryColumns+` FROM notification_deliveries
		 WHERE job_id = ? ORDER BY created_at_ns, event_id, notifier_name`,
		jobID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"list notification deliveries: %w",
			classifySQLite("list notification deliveries", err),
		)
	}
	defer rows.Close()

	deliveries := make([]NotificationDelivery, 0)
	for rows.Next() {
		delivery, scanErr := scanNotificationDelivery(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list notification deliveries: decode: %w", scanErr)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notification deliveries: iterate: %w", err)
	}

	return deliveries, nil
}

func validateQueueNotificationInput(input QueueNotificationDeliveryInput) error {
	if !input.JobID.Valid() || !input.EventID.Valid() {
		return errors.New("invalid event identity")
	}
	if !validNotifierName(input.NotifierName) {
		return errors.New("invalid notifier name")
	}
	if !model.ValidNotificationEvent(input.EventType) {
		return errors.New("invalid event type")
	}
	if input.MaxAttempts < 1 || input.MaxAttempts > 100 {
		return errors.New("maximum attempts must be between 1 and 100")
	}

	return nil
}

func validNotifierName(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && !containsControlText(value)
}

func scanNotificationDelivery(row rowScanner) (NotificationDelivery, error) {
	var (
		jobIDText, eventIDText, notifierName, eventType, statusText string
		runIDText, claimTokenText                                   sql.NullString
		occurredAt, createdAt, maxAttempts, attemptCount            int64
		nextAt, claimedAt, claimExpiresAt, completedAt              sql.NullInt64
	)
	if err := row.Scan(
		&jobIDText, &eventIDText, &notifierName, &eventType, &runIDText,
		&occurredAt, &createdAt, &maxAttempts, &attemptCount, &statusText,
		&nextAt, &claimTokenText, &claimedAt, &claimExpiresAt, &completedAt,
	); err != nil {
		return NotificationDelivery{}, err
	}
	jobID, err := model.ParseJobID(jobIDText)
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("parse notification delivery job ID: %w", err)
	}
	eventID, err := model.ParseEventID(eventIDText)
	if err != nil {
		return NotificationDelivery{}, fmt.Errorf("parse notification delivery event ID: %w", err)
	}
	var runID model.RunID
	if runIDText.Valid {
		runID, err = model.ParseRunID(runIDText.String)
		if err != nil {
			return NotificationDelivery{}, fmt.Errorf("parse notification delivery run ID: %w", err)
		}
	}
	var claimToken model.EventID
	if claimTokenText.Valid {
		claimToken, err = model.ParseEventID(claimTokenText.String)
		if err != nil {
			return NotificationDelivery{}, fmt.Errorf("parse notification claim token: %w", err)
		}
	}
	delivery := NotificationDelivery{
		JobID: jobID, EventID: eventID, RunID: runID, ClaimToken: claimToken,
		NotifierName: notifierName, EventType: eventType, Status: NotificationDeliveryStatus(statusText),
		OccurredAt: timeFromDatabase(occurredAt), CreatedAt: timeFromDatabase(createdAt),
		NextAttemptAt: optionalTime(nextAt), ClaimedAt: optionalTime(claimedAt),
		ClaimExpiresAt: optionalTime(claimExpiresAt), CompletedAt: optionalTime(completedAt),
		MaxAttempts: int(maxAttempts), AttemptCount: int(attemptCount),
	}
	if err := validatePersistedNotificationDelivery(delivery); err != nil {
		return NotificationDelivery{}, err
	}

	return delivery, nil
}

func validatePersistedNotificationDelivery(delivery NotificationDelivery) error {
	if err := validatePersistedNotificationDeliveryEnvelope(delivery); err != nil {
		return err
	}
	switch delivery.Status {
	case NotificationDeliveryPending:
		return validatePendingNotificationDelivery(delivery)
	case NotificationDeliveryDelivering:
		return validateDeliveringNotificationDelivery(delivery)
	case NotificationDeliverySucceeded, NotificationDeliveryFailed:
		return validateTerminalNotificationDelivery(delivery)
	default:
		return errors.New("invalid persisted notification delivery status")
	}
}

func validatePersistedNotificationDeliveryEnvelope(delivery NotificationDelivery) error {
	validRun := delivery.RunID == "" || delivery.RunID.Valid()
	validAttempts := delivery.MaxAttempts >= 1 && delivery.MaxAttempts <= 100 &&
		delivery.AttemptCount >= 0 && delivery.AttemptCount <= delivery.MaxAttempts
	if !delivery.JobID.Valid() || !delivery.EventID.Valid() || !validRun ||
		!validNotifierName(delivery.NotifierName) || !model.ValidNotificationEvent(delivery.EventType) ||
		delivery.OccurredAt.IsZero() || delivery.CreatedAt.IsZero() || !validAttempts {
		return errors.New("invalid persisted notification delivery")
	}

	return nil
}

func validatePendingNotificationDelivery(delivery NotificationDelivery) error {
	if delivery.NextAttemptAt == nil || delivery.ClaimToken != "" || delivery.ClaimedAt != nil ||
		delivery.ClaimExpiresAt != nil || delivery.CompletedAt != nil {
		return errors.New("invalid persisted pending notification delivery")
	}

	return nil
}

func validateDeliveringNotificationDelivery(delivery NotificationDelivery) error {
	validLease := delivery.ClaimedAt != nil && delivery.ClaimExpiresAt != nil &&
		delivery.ClaimExpiresAt.After(*delivery.ClaimedAt)
	if delivery.NextAttemptAt != nil || !delivery.ClaimToken.Valid() || !validLease || delivery.CompletedAt != nil {
		return errors.New("invalid persisted delivering notification delivery")
	}

	return nil
}

func validateTerminalNotificationDelivery(delivery NotificationDelivery) error {
	if delivery.NextAttemptAt != nil || delivery.ClaimToken != "" || delivery.ClaimedAt != nil ||
		delivery.ClaimExpiresAt != nil || delivery.CompletedAt == nil || delivery.AttemptCount == 0 {
		return errors.New("invalid persisted terminal notification delivery")
	}

	return nil
}

func sameNotificationDeliveryIdentity(first, second NotificationDelivery) bool {
	return first.JobID == second.JobID && first.EventID == second.EventID && first.RunID == second.RunID &&
		first.NotifierName == second.NotifierName && first.EventType == second.EventType &&
		first.OccurredAt.Equal(second.OccurredAt) && first.MaxAttempts == second.MaxAttempts
}

// queueNotificationsForStateEvents is part of the same SQLite transaction as
// the lifecycle snapshots and events. This closes the otherwise unavoidable
// crash window between committing a transition and making its subscribed
// notification work durable.
func queueNotificationsForStateEvents(
	ctx context.Context,
	tx *sql.Tx,
	events []model.StateEvent,
) error {
	policies := make(map[model.JobID]model.ExecutionPolicy)
	for _, event := range events {
		eventType := notificationTypeForStateEvent(event)
		if eventType == "" {
			continue
		}
		policy, found := policies[event.JobID]
		if !found {
			var err error
			policy, err = loadNotificationPolicy(ctx, tx, event.JobID)
			if err != nil {
				return err
			}
			policies[event.JobID] = policy
		}
		if len(policy.Notifications) == 0 {
			continue
		}
		if err := queueStateEventNotifications(ctx, tx, event, eventType, policy); err != nil {
			return err
		}
	}

	return nil
}

func loadNotificationPolicy(
	ctx context.Context,
	tx *sql.Tx,
	jobID model.JobID,
) (model.ExecutionPolicy, error) {
	var specificationJSON string
	if err := tx.QueryRowContext(
		ctx,
		"SELECT spec_json FROM jobs WHERE id = ?",
		jobID.String(),
	).Scan(&specificationJSON); err != nil {
		return model.ExecutionPolicy{}, fmt.Errorf("load notification job specification: %w", err)
	}
	specification, err := model.ParseJobSpecJSON([]byte(specificationJSON))
	if err != nil {
		return model.ExecutionPolicy{}, fmt.Errorf("parse notification job specification: %w", err)
	}

	return specification.ExecutionPolicy(), nil
}

func queueStateEventNotifications(
	ctx context.Context,
	tx *sql.Tx,
	event model.StateEvent,
	eventType string,
	policy model.ExecutionPolicy,
) error {
	definitions := make(map[string]model.NotifierDefinition, len(policy.NotifierDefinitions))
	for _, definition := range policy.NotifierDefinitions {
		definitions[definition.Name] = definition
	}
	for _, subscription := range policy.Notifications {
		if !containsNotificationEvent(subscription.Events, eventType) {
			continue
		}
		maxAttempts := 1
		if definition, exists := definitions[subscription.Notifier]; exists {
			maxAttempts = definition.Retry.MaxAttempts
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notification_deliveries (
				job_id, event_id, notifier_name, event_type, run_id,
				occurred_at_ns, created_at_ns, max_attempts, attempt_count,
				status, next_attempt_at_ns
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 'pending', ?)
			ON CONFLICT(event_id, notifier_name) DO NOTHING`,
			event.JobID.String(), event.ID.String(), subscription.Notifier, eventType,
			nullableString(event.RunID.String()), event.OccurredAt.UnixNano(),
			event.OccurredAt.UnixNano(), maxAttempts, event.OccurredAt.UnixNano(),
		); err != nil {
			return fmt.Errorf(
				"queue notification with state transition: %w",
				classifySQLite("queue notification with state transition", err),
			)
		}
	}

	return nil
}

func containsNotificationEvent(events []string, wanted string) bool {
	for _, event := range events {
		if event == wanted {
			return true
		}
	}

	return false
}

func notificationTypeForStateEvent(event model.StateEvent) string {
	switch event.Entity {
	case model.EntityRun:
		return notificationTypeForRunEvent(event)
	case model.EntityJob:
		return notificationTypeForJobEvent(event)
	case model.EntitySupervisor:
		return ""
	default:
		return ""
	}
}

func notificationTypeForRunEvent(event model.StateEvent) string {
	switch event.Type {
	case model.EventProcessStarted:
		return "run_started"
	case model.EventRunCompleted, model.EventStartFailed, model.EventOwnershipLost:
		return notificationTypeForRunOutcome(model.RunOutcome(event.ToOutcome))
	default:
		return ""
	}
}

func notificationTypeForRunOutcome(outcome model.RunOutcome) string {
	switch outcome {
	case model.RunOutcomeSuccess:
		return "run_succeeded"
	case model.RunOutcomeTimedOut:
		return "run_timed_out"
	case model.RunOutcomeCancelled:
		return "run_cancelled" //nolint:misspell // Persisted event vocabulary uses this spelling.
	case model.RunOutcomeLost:
		return "run_lost"
	case model.RunOutcomeFailure, model.RunOutcomeStartFailed:
		return "run_failed"
	default:
		return ""
	}
}

func notificationTypeForJobEvent(event model.StateEvent) string {
	switch event.Type {
	case model.EventSupervisorClaimed:
		return "job_started"
	case model.EventRetryScheduled:
		return "retry_scheduled"
	case model.EventSubmissionFailed:
		return "job_submission_failed"
	case model.EventOwnershipLost:
		return "job_lost"
	case model.EventJobCompleted, model.EventJobAborted:
		return notificationTypeForJobOutcome(model.JobOutcome(event.ToOutcome))
	default:
		return ""
	}
}

func notificationTypeForJobOutcome(outcome model.JobOutcome) string {
	switch outcome {
	case model.JobOutcomeSuccess:
		return "job_succeeded"
	case model.JobOutcomeTimedOut:
		return "job_timed_out"
	case model.JobOutcomeCancelled:
		return "job_cancelled" //nolint:misspell // Persisted event vocabulary uses this spelling.
	case model.JobOutcomeAborted:
		return "job_aborted"
	case model.JobOutcomeLost:
		return "job_lost"
	case model.JobOutcomeSubmissionFailed:
		return "job_submission_failed"
	case model.JobOutcomeFailure:
		return "job_failed"
	case model.JobOutcomeNone:
		return ""
	default:
		return ""
	}
}
