// Package notify delivers bounded, versioned job events to configured local
// commands, HTTP webhooks, and SMTP servers.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// EventSchemaVersion is the JSON schema emitted by all notifier types.
	EventSchemaVersion = 1
	defaultTimeout     = 10 * time.Second
	defaultByteLimit   = int64(64 * 1024)
	maximumByteLimit   = int64(1024 * 1024)
)

// EventType identifies a lifecycle transition that can be delivered.
type EventType string

// Notification event types defined by the v1 specification.
const (
	EventJobStarted       EventType = "job_started"
	EventRunStarted       EventType = "run_started"
	EventRunSucceeded     EventType = "run_succeeded"
	EventRunFailed        EventType = "run_failed"
	EventRunTimedOut      EventType = "run_timed_out"
	EventRunCancelled     EventType = "run_cancelled" //nolint:misspell // The event spelling is fixed by the specification.
	EventRunLost          EventType = "run_lost"
	EventRetryScheduled   EventType = "retry_scheduled"
	EventJobSucceeded     EventType = "job_succeeded"
	EventJobFailed        EventType = "job_failed"
	EventJobTimedOut      EventType = "job_timed_out"
	EventJobCancelled     EventType = "job_cancelled" //nolint:misspell // The event spelling is fixed by the specification.
	EventJobAborted       EventType = "job_aborted"
	EventJobLost          EventType = "job_lost"
	EventSubmissionFailed EventType = "job_submission_failed"
)

// Event is the common, versioned payload delivered by every notifier. Detail
// must already be redacted; notifier implementations never add command lines,
// environment values, credentials, or target output to it.
type Event struct {
	OccurredAt    time.Time      `json:"occurred_at"`
	Detail        map[string]any `json:"detail,omitempty"`
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	Type          EventType      `json:"type"`
	JobID         string         `json:"job_id"`
	RunID         string         `json:"run_id,omitempty"`
}

// Validate checks the stable event envelope.
func (event Event) Validate() error {
	if event.SchemaVersion != EventSchemaVersion {
		return fmt.Errorf("validate notification event: schema version must be %d", EventSchemaVersion)
	}
	if strings.TrimSpace(event.ID) == "" {
		return errors.New("validate notification event: ID is required")
	}
	if strings.TrimSpace(event.JobID) == "" {
		return errors.New("validate notification event: job ID is required")
	}
	if !event.Type.valid() {
		return fmt.Errorf("validate notification event: unsupported event type %q", event.Type)
	}
	if event.OccurredAt.IsZero() {
		return errors.New("validate notification event: occurrence time is required")
	}

	return nil
}

func (eventType EventType) valid() bool {
	switch eventType {
	case EventJobStarted,
		EventRunStarted,
		EventRunSucceeded,
		EventRunFailed,
		EventRunTimedOut,
		EventRunCancelled,
		EventRunLost,
		EventRetryScheduled,
		EventJobSucceeded,
		EventJobFailed,
		EventJobTimedOut,
		EventJobCancelled,
		EventJobAborted,
		EventJobLost,
		EventSubmissionFailed:
		return true
	default:
		return false
	}
}

func marshalEvent(event Event) ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, errors.New("encode notification event: unsupported detail value")
	}
	if int64(len(payload)) > maximumByteLimit {
		return nil, errors.New("encode notification event: payload exceeds maximum size")
	}

	return payload, nil
}

// Notifier delivers an event to one configured destination.
type Notifier interface {
	Name() string
	Deliver(context.Context, Event) (Result, error)
}

// Result contains bounded delivery metadata. ResponseBody, Stdout, and Stderr
// may themselves contain sensitive peer data and must not be persisted without
// applying the configured redactor.
type Result struct {
	ResponseBody []byte
	Stdout       []byte
	Stderr       []byte
	StatusCode   int
	ExitCode     int
	MessageID    string
	Truncated    bool
}

// ErrorKind classifies a delivery failure without including destination data.
type ErrorKind string

// Stable delivery error kinds.
const (
	ErrorInvalid   ErrorKind = "invalid"
	ErrorCanceled  ErrorKind = "canceled"
	ErrorTimeout   ErrorKind = "timeout"
	ErrorTransport ErrorKind = "transport"
	ErrorRejected  ErrorKind = "rejected"
	ErrorInternal  ErrorKind = "internal"
)

// DeliveryError is safe to include in diagnostics and durable attempt state.
// It deliberately does not retain a raw transport error, which could contain
// a URL, credential, recipient, request body, or server response.
type DeliveryError struct {
	Kind      ErrorKind
	Retryable bool
}

// Error returns a stable, secret-free description.
func (deliveryError *DeliveryError) Error() string {
	return "notification delivery " + string(deliveryError.Kind)
}

// Is preserves standard context cancellation classification without retaining
// a raw transport error.
func (deliveryError *DeliveryError) Is(target error) bool {
	return deliveryError.Kind == ErrorCanceled && target == context.Canceled ||
		deliveryError.Kind == ErrorTimeout && target == context.DeadlineExceeded
}

func deliveryError(kind ErrorKind, retryable bool) error {
	return &DeliveryError{Kind: kind, Retryable: retryable}
}

// IsRetryable reports whether retrying a delivery can reasonably succeed.
func IsRetryable(err error) bool {
	var deliveryError *DeliveryError

	return errors.As(err, &deliveryError) && deliveryError.Retryable
}

func classifyContext(ctx context.Context, fallback ErrorKind, retryable bool) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return deliveryError(ErrorCanceled, false)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return deliveryError(ErrorTimeout, true)
	}

	return deliveryError(fallback, retryable)
}

func withTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if timeout == 0 {
		timeout = defaultTimeout
	}
	if timeout < 0 {
		return nil, nil, errors.New("notification timeout must not be negative")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)

	return ctx, cancel, nil
}

func byteLimit(configured int64) (int64, error) {
	if configured == 0 {
		return defaultByteLimit, nil
	}
	if configured < 0 || configured > maximumByteLimit {
		return 0, fmt.Errorf("notification byte limit must be between 1 and %d", maximumByteLimit)
	}

	return configured, nil
}
