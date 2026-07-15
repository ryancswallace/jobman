package notify

import (
	"context"
	"errors"
	"time"
)

const maximumDeliveryAttempts = 100

// RetryPolicy configures bounded exponential retry after the first attempt.
type RetryPolicy struct {
	Delay       time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
}

// Validate checks that retry work is finite and durations are usable.
func (policy RetryPolicy) Validate() error {
	if policy.MaxAttempts < 1 || policy.MaxAttempts > maximumDeliveryAttempts {
		return errors.New("notification retry attempts must be between 1 and 100")
	}
	if policy.Delay < 0 || policy.MaxDelay < 0 {
		return errors.New("notification retry delays must not be negative")
	}
	if policy.MaxDelay != 0 && policy.MaxDelay < policy.Delay {
		return errors.New("notification maximum retry delay must not be less than the initial delay")
	}

	return nil
}

// Attempt is a durable-record-ready summary of one delivery. ErrorKind is
// deliberately structured rather than a raw error string.
type Attempt struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Result     Result
	EventID    string
	Notifier   string
	ErrorKind  ErrorKind
	Number     int
	Retryable  bool
	Succeeded  bool
}

// AttemptRecorder persists an attempt before a retry begins or delivery is
// considered complete.
type AttemptRecorder interface {
	RecordAttempt(context.Context, Attempt) error
}

// Clock makes retry timing deterministic in tests.
type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

// RealClock uses the process monotonic clock and cancellable timers.
type RealClock struct{}

// Now returns the current time.
func (RealClock) Now() time.Time {
	return time.Now()
}

// Sleep waits for duration or context cancellation.
func (RealClock) Sleep(ctx context.Context, duration time.Duration) error {
	if duration == 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Retryer orchestrates bounded, at-least-once delivery attempts.
type Retryer struct {
	Clock    Clock
	Recorder AttemptRecorder
}

// Deliver sends an event until it succeeds, fails permanently, exhausts the
// policy, or is canceled. Every completed attempt is recorded before another
// attempt starts.
func (retryer Retryer) Deliver(
	ctx context.Context,
	notifier Notifier,
	event Event,
	policy RetryPolicy,
) ([]Attempt, error) {
	if notifier == nil {
		return nil, errors.New("notification notifier is required")
	}
	if err := event.Validate(); err != nil {
		return nil, err
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	clock := retryer.Clock
	if clock == nil {
		clock = RealClock{}
	}

	attempts := make([]Attempt, 0, policy.MaxAttempts)
	for number := 1; number <= policy.MaxAttempts; number++ {
		startedAt := clock.Now()
		result, deliveryErr := notifier.Deliver(ctx, event)
		finishedAt := clock.Now()
		deliveryErr = normalizeDeliveryError(ctx, deliveryErr)
		attempt := Attempt{
			Number:     number,
			Notifier:   notifier.Name(),
			EventID:    event.ID,
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			Result:     result,
			Succeeded:  deliveryErr == nil,
			Retryable:  IsRetryable(deliveryErr),
		}
		var classified *DeliveryError
		if errors.As(deliveryErr, &classified) {
			attempt.ErrorKind = classified.Kind
		}
		attempts = append(attempts, attempt)
		if retryer.Recorder != nil {
			if err := retryer.Recorder.RecordAttempt(ctx, attempt); err != nil {
				return attempts, deliveryError(ErrorInternal, true)
			}
		}
		if deliveryErr == nil {
			return attempts, nil
		}
		if !attempt.Retryable || number == policy.MaxAttempts {
			return attempts, deliveryErr
		}
		if err := clock.Sleep(ctx, retryDelay(policy, number)); err != nil {
			return attempts, classifyContext(ctx, ErrorCanceled, false)
		}
	}

	return attempts, deliveryError(ErrorInternal, false)
}

func normalizeDeliveryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var classified *DeliveryError
	if errors.As(err, &classified) {
		return classified
	}
	if ctx.Err() != nil {
		return classifyContext(ctx, ErrorTransport, true)
	}

	return deliveryError(ErrorInternal, false)
}

func retryDelay(policy RetryPolicy, completedAttempt int) time.Duration {
	delay := policy.Delay
	for range completedAttempt - 1 {
		if delay > time.Duration(1<<63-1)/2 {
			delay = time.Duration(1<<63 - 1)
			break
		}
		delay *= 2
	}
	if policy.MaxDelay != 0 && delay > policy.MaxDelay {
		return policy.MaxDelay
	}

	return delay
}
