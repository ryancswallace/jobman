package notify

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type sequenceNotifier struct {
	errors []error
	calls  int
}

func (*sequenceNotifier) Name() string {
	return "sequence"
}

func (notifier *sequenceNotifier) Deliver(context.Context, Event) (Result, error) {
	index := notifier.calls
	notifier.calls++
	if index >= len(notifier.errors) {
		return Result{StatusCode: 204}, nil
	}

	return Result{StatusCode: 503}, notifier.errors[index]
}

type fakeClock struct {
	now      time.Time
	sleeps   []time.Duration
	sleepErr error
}

func (clock *fakeClock) Now() time.Time {
	result := clock.now
	clock.now = clock.now.Add(time.Second)

	return result
}

func (clock *fakeClock) Sleep(_ context.Context, duration time.Duration) error {
	clock.sleeps = append(clock.sleeps, duration)

	return clock.sleepErr
}

type attemptRecorder struct {
	attempts []Attempt
	err      error
}

func (recorder *attemptRecorder) RecordAttempt(_ context.Context, attempt Attempt) error {
	recorder.attempts = append(recorder.attempts, attempt)

	return recorder.err
}

func TestRetryerRecordsAttemptsAndUsesBoundedExponentialDelay(t *testing.T) {
	t.Parallel()

	notifier := &sequenceNotifier{errors: []error{
		&DeliveryError{Kind: ErrorTransport, Retryable: true},
		&DeliveryError{Kind: ErrorRejected, Retryable: true},
	}}
	clock := &fakeClock{now: time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)}
	recorder := &attemptRecorder{}
	attempts, err := (Retryer{Clock: clock, Recorder: recorder}).Deliver(
		t.Context(),
		notifier,
		testEvent(),
		RetryPolicy{MaxAttempts: 4, Delay: 3 * time.Second, MaxDelay: 5 * time.Second},
	)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if notifier.calls != 3 || len(attempts) != 3 || len(recorder.attempts) != 3 {
		t.Fatalf("calls/attempts/records = %d/%d/%d", notifier.calls, len(attempts), len(recorder.attempts))
	}
	if !reflect.DeepEqual(clock.sleeps, []time.Duration{3 * time.Second, 5 * time.Second}) {
		t.Fatalf("retry sleeps = %#v", clock.sleeps)
	}
	if attempts[0].ErrorKind != ErrorTransport || !attempts[0].Retryable || attempts[0].Succeeded {
		t.Fatalf("first attempt = %#v", attempts[0])
	}
	if attempts[2].ErrorKind != "" || attempts[2].Retryable || !attempts[2].Succeeded {
		t.Fatalf("successful attempt = %#v", attempts[2])
	}
	if attempts[0].FinishedAt.Sub(attempts[0].StartedAt) != time.Second {
		t.Fatalf("first attempt timing = %s to %s", attempts[0].StartedAt, attempts[0].FinishedAt)
	}
}

func TestRetryerStopsOnPermanentFailure(t *testing.T) {
	t.Parallel()

	notifier := &sequenceNotifier{errors: []error{&DeliveryError{Kind: ErrorRejected, Retryable: false}}}
	clock := &fakeClock{}
	attempts, err := (Retryer{Clock: clock}).Deliver(
		t.Context(),
		notifier,
		testEvent(),
		RetryPolicy{MaxAttempts: 5, Delay: time.Second},
	)
	if err == nil {
		t.Fatal("Deliver() error = nil")
	}
	if len(attempts) != 1 || notifier.calls != 1 || len(clock.sleeps) != 0 {
		t.Fatalf("attempts/calls/sleeps = %d/%d/%d", len(attempts), notifier.calls, len(clock.sleeps))
	}
}

func TestRetryerExhaustsBoundedAttempts(t *testing.T) {
	t.Parallel()

	notifier := &sequenceNotifier{errors: []error{
		&DeliveryError{Kind: ErrorTransport, Retryable: true},
		&DeliveryError{Kind: ErrorTransport, Retryable: true},
	}}
	attempts, err := (Retryer{Clock: &fakeClock{}}).Deliver(
		t.Context(),
		notifier,
		testEvent(),
		RetryPolicy{MaxAttempts: 2},
	)
	if err == nil || !IsRetryable(err) {
		t.Fatalf("Deliver() error = %v", err)
	}
	if len(attempts) != 2 || notifier.calls != 2 {
		t.Fatalf("attempts/calls = %d/%d", len(attempts), notifier.calls)
	}
}

func TestRetryerStopsWhenAttemptCannotBeRecorded(t *testing.T) {
	t.Parallel()

	notifier := &sequenceNotifier{}
	recorder := &attemptRecorder{err: errors.New("database detail")}
	attempts, err := (Retryer{Clock: &fakeClock{}, Recorder: recorder}).Deliver(
		t.Context(),
		notifier,
		testEvent(),
		RetryPolicy{MaxAttempts: 2},
	)
	if err == nil || len(attempts) != 1 || notifier.calls != 1 {
		t.Fatalf("Deliver() = (%d attempts, %v), calls = %d", len(attempts), err, notifier.calls)
	}
	if err.Error() != "notification delivery internal" {
		t.Fatalf("Deliver() error = %q", err)
	}
}

func TestRetryPolicyValidation(t *testing.T) {
	t.Parallel()

	tests := []RetryPolicy{
		{},
		{MaxAttempts: 101},
		{MaxAttempts: 1, Delay: -1},
		{MaxAttempts: 1, MaxDelay: -1},
		{MaxAttempts: 1, Delay: 2 * time.Second, MaxDelay: time.Second},
	}
	for _, policy := range tests {
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate(%#v) error = nil", policy)
		}
	}
	if err := (RetryPolicy{MaxAttempts: 1}).Validate(); err != nil {
		t.Fatalf("Validate(valid) error = %v", err)
	}
}

func TestRetryDelaySaturates(t *testing.T) {
	t.Parallel()

	delay := retryDelay(RetryPolicy{Delay: time.Duration(1 << 62)}, 3)
	if delay != time.Duration(1<<63-1) {
		t.Fatalf("retryDelay() = %s, want maximum duration", delay)
	}
	if capped := retryDelay(RetryPolicy{Delay: time.Hour, MaxDelay: 90 * time.Minute}, 2); capped != 90*time.Minute {
		t.Fatalf("capped retryDelay() = %s", capped)
	}
}

func TestRetryerValidationCancellationAndRealClock(t *testing.T) {
	t.Parallel()

	retryer := Retryer{}
	if _, err := retryer.Deliver(
		t.Context(), nil, testEvent(), RetryPolicy{MaxAttempts: 1},
	); err == nil {
		t.Fatal("Deliver(nil notifier) error = nil")
	}
	invalidEvent := testEvent()
	invalidEvent.ID = ""
	if _, err := retryer.Deliver(
		t.Context(), &sequenceNotifier{}, invalidEvent, RetryPolicy{MaxAttempts: 1},
	); err == nil {
		t.Fatal("Deliver(invalid event) error = nil")
	}
	if _, err := retryer.Deliver(
		t.Context(), &sequenceNotifier{}, testEvent(), RetryPolicy{},
	); err == nil {
		t.Fatal("Deliver(invalid policy) error = nil")
	}

	sleepFailure := errors.New("sleep failed")
	notifier := &sequenceNotifier{errors: []error{deliveryError(ErrorTransport, true)}}
	attempts, err := (Retryer{Clock: &fakeClock{sleepErr: sleepFailure}}).Deliver(
		t.Context(), notifier, testEvent(), RetryPolicy{MaxAttempts: 2, Delay: time.Second},
	)
	if len(attempts) != 1 || err == nil || IsRetryable(err) {
		t.Fatalf("Deliver(sleep failure) = %d attempts, %v", len(attempts), err)
	}

	clock := RealClock{}
	if clock.Now().IsZero() {
		t.Fatal("RealClock.Now() returned zero")
	}
	if err := clock.Sleep(t.Context(), 0); err != nil {
		t.Fatalf("RealClock.Sleep(0) error = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := clock.Sleep(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("RealClock.Sleep(canceled) error = %v", err)
	}
	if err := clock.Sleep(t.Context(), time.Nanosecond); err != nil {
		t.Fatalf("RealClock.Sleep(timer) error = %v", err)
	}
}

func TestNormalizeDeliveryError(t *testing.T) {
	t.Parallel()

	if err := normalizeDeliveryError(t.Context(), nil); err != nil {
		t.Fatalf("normalizeDeliveryError(nil) = %v", err)
	}
	classified := deliveryError(ErrorRejected, false)
	if got := normalizeDeliveryError(t.Context(), classified); !errors.Is(got, classified) {
		t.Fatalf("normalizeDeliveryError(classified) = %v", got)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if got := normalizeDeliveryError(canceled, errors.New("detail")); !errors.Is(got, context.Canceled) {
		t.Fatalf("normalizeDeliveryError(canceled) = %v", got)
	}
	if got := normalizeDeliveryError(t.Context(), errors.New("detail")); got == nil || IsRetryable(got) {
		t.Fatalf("normalizeDeliveryError(unknown) = %v", got)
	}
}
