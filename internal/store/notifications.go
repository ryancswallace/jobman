package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ryancswallace/jobman/internal/model"
)

// NotificationAttemptStatus identifies durable notification work state.
type NotificationAttemptStatus string

// Persisted notification attempt states.
const (
	NotificationAttemptPending    NotificationAttemptStatus = "pending"
	NotificationAttemptDelivering NotificationAttemptStatus = "delivering"
	NotificationAttemptSucceeded  NotificationAttemptStatus = "succeeded"
	NotificationAttemptFailed     NotificationAttemptStatus = "failed"
)

// RecordNotificationAttemptInput contains one completed delivery attempt. It
// intentionally accepts only structured, non-secret result metadata; response
// bodies and command output must be redacted elsewhere and are not persisted.
type RecordNotificationAttemptInput struct {
	JobID              model.JobID
	EventID            model.EventID
	NotifierName       string
	EventType          string
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

// NotificationAttempt is one durable delivery-attempt snapshot.
type NotificationAttempt struct {
	ID                 model.EventID
	JobID              model.JobID
	EventID            model.EventID
	NotifierName       string
	EventType          string
	AttemptNumber      int
	Status             NotificationAttemptStatus
	CreatedAt          time.Time
	StartedAt          *time.Time
	CompletedAt        *time.Time
	NextAttemptAt      *time.Time
	DiagnosticCode     string
	Retryable          bool
	ResponseStatusCode *int
	CommandExitCode    *int
	MessageID          string
	ResponseTruncated  bool
}

const notificationAttemptColumns = `
	id, job_id, event_id, notifier_name, event_type, attempt_number, status,
	created_at_ns, started_at_ns, completed_at_ns, next_attempt_at_ns,
	diagnostic_code, retryable, response_status_code, command_exit_code,
	message_id, response_truncated`

// RecordNotificationAttempt durably records a completed delivery. Repeating
// the same event/notifier/attempt tuple with identical data is idempotent; a
// conflicting replay returns ErrConflict.
func (s *Store) RecordNotificationAttempt(
	ctx context.Context,
	input RecordNotificationAttemptInput,
) (NotificationAttempt, error) {
	if err := validateNotificationAttemptInput(input); err != nil {
		return NotificationAttempt{}, fmt.Errorf("record notification attempt: %w", err)
	}
	id, err := s.eventIDs.NewEventID()
	if err != nil {
		return NotificationAttempt{}, fmt.Errorf("record notification attempt: generate ID: %w", err)
	}
	expected := notificationAttemptFromInput(id, input)
	var recorded NotificationAttempt
	err = s.writeTransaction(ctx, "notification attempt", func(tx *sql.Tx) error {
		var recordErr error
		recorded, recordErr = recordNotificationAttemptTx(ctx, tx, expected)

		return recordErr
	})
	if err != nil {
		return NotificationAttempt{}, err
	}

	return recorded, nil
}

func recordNotificationAttemptTx(
	ctx context.Context,
	tx *sql.Tx,
	expected NotificationAttempt,
) (NotificationAttempt, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO notification_attempts (
		id, job_id, event_id, notifier_name, event_type, attempt_number, status,
		created_at_ns, started_at_ns, completed_at_ns, next_attempt_at_ns,
		diagnostic_code, retryable, response_status_code, command_exit_code,
		message_id, response_truncated
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(event_id, notifier_name, attempt_number) DO NOTHING`,
		expected.ID.String(), expected.JobID.String(), expected.EventID.String(),
		expected.NotifierName, expected.EventType, expected.AttemptNumber, string(expected.Status),
		expected.CreatedAt.UnixNano(), nullableTime(expected.StartedAt), nullableTime(expected.CompletedAt),
		nullableTime(expected.NextAttemptAt), nullableString(expected.DiagnosticCode), boolInt(expected.Retryable),
		nullableInt(expected.ResponseStatusCode), nullableInt(expected.CommandExitCode),
		nullableString(expected.MessageID), boolInt(expected.ResponseTruncated),
	)
	if err != nil {
		return NotificationAttempt{}, fmt.Errorf(
			"insert notification attempt: %w",
			classifySQLite("insert notification attempt", err),
		)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return NotificationAttempt{}, fmt.Errorf("read notification attempt insert result: %w", err)
	}
	recorded, err := scanNotificationAttempt(tx.QueryRowContext(
		ctx,
		"SELECT "+notificationAttemptColumns+` FROM notification_attempts
		 WHERE event_id = ? AND notifier_name = ? AND attempt_number = ?`,
		expected.EventID.String(), expected.NotifierName, expected.AttemptNumber,
	))
	if err != nil {
		return NotificationAttempt{}, fmt.Errorf("read notification attempt after insert: %w", err)
	}
	if rows == 0 && !sameNotificationAttempt(recorded, expected) {
		return NotificationAttempt{}, fmt.Errorf(
			"record notification attempt %s/%s/%d: %w",
			expected.EventID,
			expected.NotifierName,
			expected.AttemptNumber,
			ErrConflict,
		)
	}

	return recorded, nil
}

// ListNotificationAttempts returns every retained attempt for a job in stable
// event/attempt order.
func (s *Store) ListNotificationAttempts(
	ctx context.Context,
	jobID model.JobID,
) ([]NotificationAttempt, error) {
	if !jobID.Valid() {
		return nil, errors.New("list notification attempts: invalid job ID")
	}
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT "+notificationAttemptColumns+` FROM notification_attempts
		 WHERE job_id = ? ORDER BY created_at_ns, event_id, notifier_name, attempt_number`,
		jobID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("list notification attempts for job %s: %w",
			jobID, classifySQLite("list notification attempts", err))
	}
	defer rows.Close()

	attempts := make([]NotificationAttempt, 0)
	for rows.Next() {
		attempt, scanErr := scanNotificationAttempt(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list notification attempts for job %s: decode attempt: %w", jobID, scanErr)
		}
		attempts = append(attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list notification attempts for job %s: iterate attempts: %w", jobID, err)
	}

	return attempts, nil
}

//nolint:gocognit,cyclop // Validation intentionally reports the first violated durable-record invariant.
func validateNotificationAttemptInput(input RecordNotificationAttemptInput) error {
	if !input.JobID.Valid() {
		return errors.New("invalid job ID")
	}
	if !input.EventID.Valid() {
		return errors.New("invalid event ID")
	}
	if input.NotifierName == "" || len(input.NotifierName) > 128 ||
		strings.TrimSpace(input.NotifierName) != input.NotifierName || containsControlText(input.NotifierName) {
		return errors.New("invalid notifier name")
	}
	if !model.ValidNotificationEvent(input.EventType) {
		return errors.New("invalid event type")
	}
	if input.AttemptNumber < 1 || input.AttemptNumber > 100 {
		return errors.New("attempt number must be between 1 and 100")
	}
	if input.StartedAt.IsZero() || input.CompletedAt.Before(input.StartedAt) ||
		input.StartedAt.UnixNano() < 0 || input.CompletedAt.UnixNano() < 0 {
		return errors.New("invalid attempt timestamps")
	}
	if input.NextAttemptAt != nil && (input.NextAttemptAt.Before(input.CompletedAt) || input.NextAttemptAt.UnixNano() < 0) {
		return errors.New("next attempt time must not precede completion")
	}
	if input.Succeeded && (input.DiagnosticCode != "" || input.Retryable) {
		return errors.New("successful attempt must not have a diagnostic or be retryable")
	}
	if !input.Succeeded && !validNotificationDiagnostic(input.DiagnosticCode) {
		return errors.New("failed attempt requires a valid diagnostic code")
	}
	if input.ResponseStatusCode != nil && (*input.ResponseStatusCode < 100 || *input.ResponseStatusCode > 999) {
		return errors.New("response status code must be between 100 and 999")
	}
	if len(input.MessageID) > 1024 || containsControlText(input.MessageID) {
		return errors.New("invalid message ID")
	}

	return nil
}

func notificationAttemptFromInput(id model.EventID, input RecordNotificationAttemptInput) NotificationAttempt {
	status := NotificationAttemptFailed
	if input.Succeeded {
		status = NotificationAttemptSucceeded
	}
	startedAt := input.StartedAt.UTC()
	completedAt := input.CompletedAt.UTC()

	return NotificationAttempt{
		ID: id, JobID: input.JobID, EventID: input.EventID,
		NotifierName: input.NotifierName, EventType: input.EventType, AttemptNumber: input.AttemptNumber,
		Status: status, CreatedAt: startedAt, StartedAt: &startedAt, CompletedAt: &completedAt,
		NextAttemptAt: cloneTime(input.NextAttemptAt), DiagnosticCode: input.DiagnosticCode,
		Retryable: input.Retryable, ResponseStatusCode: cloneInt(input.ResponseStatusCode),
		CommandExitCode: cloneInt(input.CommandExitCode), MessageID: input.MessageID,
		ResponseTruncated: input.ResponseTruncated,
	}
}

//nolint:cyclop // Decoding validates every independently nullable persisted result field.
func scanNotificationAttempt(row rowScanner) (NotificationAttempt, error) {
	var (
		idText, jobIDText, eventIDText      string
		notifierName, eventType, statusText string
		attemptNumber, createdAt            int64
		startedAt, completedAt, nextAt      sql.NullInt64
		diagnostic, messageID               sql.NullString
		retryable, truncated                int64
		responseStatus, commandExit         sql.NullInt64
	)
	if err := row.Scan(
		&idText, &jobIDText, &eventIDText, &notifierName, &eventType, &attemptNumber, &statusText,
		&createdAt, &startedAt, &completedAt, &nextAt, &diagnostic, &retryable,
		&responseStatus, &commandExit, &messageID, &truncated,
	); err != nil {
		return NotificationAttempt{}, err
	}
	id, err := model.ParseEventID(idText)
	if err != nil {
		return NotificationAttempt{}, fmt.Errorf("parse attempt ID: %w", err)
	}
	jobID, err := model.ParseJobID(jobIDText)
	if err != nil {
		return NotificationAttempt{}, fmt.Errorf("parse attempt job ID: %w", err)
	}
	eventID, err := model.ParseEventID(eventIDText)
	if err != nil {
		return NotificationAttempt{}, fmt.Errorf("parse notification event ID: %w", err)
	}
	status := NotificationAttemptStatus(statusText)
	if status != NotificationAttemptPending && status != NotificationAttemptDelivering &&
		status != NotificationAttemptSucceeded && status != NotificationAttemptFailed {
		return NotificationAttempt{}, errors.New("invalid persisted notification attempt status")
	}
	if attemptNumber < 1 || attemptNumber > 100 || retryable < 0 || retryable > 1 || truncated < 0 || truncated > 1 {
		return NotificationAttempt{}, errors.New("invalid persisted notification attempt metadata")
	}

	attempt := NotificationAttempt{
		ID: id, JobID: jobID, EventID: eventID, NotifierName: notifierName, EventType: eventType,
		AttemptNumber: int(attemptNumber), Status: status, CreatedAt: timeFromDatabase(createdAt),
		StartedAt: optionalTime(startedAt), CompletedAt: optionalTime(completedAt), NextAttemptAt: optionalTime(nextAt),
		DiagnosticCode: diagnostic.String, Retryable: retryable == 1,
		ResponseStatusCode: optionalInt(responseStatus), CommandExitCode: optionalInt(commandExit),
		MessageID: messageID.String, ResponseTruncated: truncated == 1,
	}
	if err := validatePersistedNotificationAttempt(attempt); err != nil {
		return NotificationAttempt{}, err
	}

	return attempt, nil
}

//nolint:gocognit,cyclop // Status-specific persisted invariants are kept together for auditability.
func validatePersistedNotificationAttempt(attempt NotificationAttempt) error {
	if !attempt.ID.Valid() || !attempt.JobID.Valid() || !attempt.EventID.Valid() {
		return errors.New("invalid persisted notification attempt identifier")
	}
	if attempt.NotifierName == "" || len(attempt.NotifierName) > 128 ||
		strings.TrimSpace(attempt.NotifierName) != attempt.NotifierName || containsControlText(attempt.NotifierName) ||
		!model.ValidNotificationEvent(attempt.EventType) || attempt.AttemptNumber < 1 || attempt.AttemptNumber > 100 {
		return errors.New("invalid persisted notification attempt identity")
	}
	if attempt.CreatedAt.IsZero() || attempt.CreatedAt.UnixNano() < 0 ||
		attempt.StartedAt != nil && attempt.StartedAt.Before(attempt.CreatedAt) ||
		attempt.CompletedAt != nil && attempt.CompletedAt.Before(attempt.CreatedAt) ||
		attempt.StartedAt != nil && attempt.CompletedAt != nil && attempt.CompletedAt.Before(*attempt.StartedAt) ||
		attempt.NextAttemptAt != nil && attempt.NextAttemptAt.Before(attempt.CreatedAt) {
		return errors.New("invalid persisted notification attempt timestamps")
	}
	switch attempt.Status {
	case NotificationAttemptPending:
		if attempt.StartedAt != nil || attempt.CompletedAt != nil {
			return errors.New("invalid persisted pending notification attempt")
		}
	case NotificationAttemptDelivering:
		if attempt.StartedAt == nil || attempt.CompletedAt != nil {
			return errors.New("invalid persisted delivering notification attempt")
		}
	case NotificationAttemptSucceeded:
		if attempt.StartedAt == nil || attempt.CompletedAt == nil || attempt.DiagnosticCode != "" || attempt.Retryable {
			return errors.New("invalid persisted successful notification attempt")
		}
	case NotificationAttemptFailed:
		if attempt.StartedAt == nil || attempt.CompletedAt == nil || !validNotificationDiagnostic(attempt.DiagnosticCode) {
			return errors.New("invalid persisted failed notification attempt")
		}
	default:
		return errors.New("invalid persisted notification attempt status")
	}
	if attempt.ResponseStatusCode != nil && (*attempt.ResponseStatusCode < 100 || *attempt.ResponseStatusCode > 999) ||
		len(attempt.MessageID) > 1024 || containsControlText(attempt.MessageID) {
		return errors.New("invalid persisted notification response metadata")
	}

	return nil
}

//nolint:cyclop // Equality covers every persisted field for conflict-safe idempotency.
func sameNotificationAttempt(first, second NotificationAttempt) bool {
	return first.JobID == second.JobID && first.EventID == second.EventID &&
		first.NotifierName == second.NotifierName && first.EventType == second.EventType &&
		first.AttemptNumber == second.AttemptNumber && first.Status == second.Status &&
		first.CreatedAt.Equal(second.CreatedAt) && sameOptionalTime(first.StartedAt, second.StartedAt) &&
		sameOptionalTime(first.CompletedAt, second.CompletedAt) && sameOptionalTime(first.NextAttemptAt, second.NextAttemptAt) &&
		first.DiagnosticCode == second.DiagnosticCode && first.Retryable == second.Retryable &&
		sameOptionalInt(first.ResponseStatusCode, second.ResponseStatusCode) &&
		sameOptionalInt(first.CommandExitCode, second.CommandExitCode) && first.MessageID == second.MessageID &&
		first.ResponseTruncated == second.ResponseTruncated
}

func validNotificationDiagnostic(value string) bool {
	switch value {
	case "invalid", "canceled", "timeout", "transport", "rejected", "internal":
		return true
	default:
		return false
	}
}

func containsControlText(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}

	return false
}

func boolInt(value bool) int {
	if value {
		return 1
	}

	return 0
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}

	return *value
}

func optionalInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)

	return &converted
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value

	return &cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()

	return &cloned
}

func sameOptionalTime(first, second *time.Time) bool {
	return first == nil && second == nil || first != nil && second != nil && first.Equal(*second)
}

func sameOptionalInt(first, second *int) bool {
	return first == nil && second == nil || first != nil && second != nil && *first == *second
}
