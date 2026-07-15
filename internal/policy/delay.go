package policy

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Backoff identifies a retry/repetition delay algorithm.
type Backoff string

// Backoff algorithms defined by the v1 specification.
const (
	BackoffConstant    Backoff = "constant"
	BackoffLinear      Backoff = "linear"
	BackoffExponential Backoff = "exponential"
)

// JitterSource supplies a uniform integer in [0, upperBound). Implementations
// must return promptly and must not use global mutable random state.
type JitterSource interface {
	Uint64N(upperBound uint64) uint64
}

// DelayPolicy configures retry and successful-repetition delays. Jitter is the
// full width of the symmetric interval. HasMaxDelay distinguishes an explicit
// zero cap from no cap.
type DelayPolicy struct {
	Base            time.Duration
	Backoff         Backoff
	ExponentialBase uint64
	MaxDelay        time.Duration
	HasMaxDelay     bool
	Jitter          time.Duration
}

// Validate checks delay bounds and algorithm parameters.
func (policy DelayPolicy) Validate() error {
	if policy.Base < 0 {
		return errors.New("validate delay policy: base delay must not be negative")
	}
	if policy.Jitter < 0 {
		return errors.New("validate delay policy: jitter must not be negative")
	}
	if policy.HasMaxDelay && policy.MaxDelay < 0 {
		return errors.New("validate delay policy: maximum delay must not be negative")
	}
	if !policy.HasMaxDelay && policy.MaxDelay != 0 {
		return errors.New("validate delay policy: maximum delay requires presence marker")
	}

	switch policy.Backoff {
	case BackoffConstant, BackoffLinear:
	case BackoffExponential:
		if policy.ExponentialBase < 1 {
			return errors.New("validate delay policy: exponential base must be at least one")
		}
	default:
		return fmt.Errorf("validate delay policy: unknown backoff %q", policy.Backoff)
	}

	return nil
}

// Delay computes the delay after completedRuns runs, applies a cap, then adds
// bounded jitter. Arithmetic saturates instead of wrapping on overflow.
func (policy DelayPolicy) Delay(completedRuns uint64, source JitterSource) (time.Duration, error) {
	if err := policy.Validate(); err != nil {
		return 0, err
	}
	if completedRuns == 0 {
		return 0, errors.New("compute delay: completed runs must be positive")
	}

	ceiling := time.Duration(math.MaxInt64)
	if policy.HasMaxDelay {
		ceiling = policy.MaxDelay
	}

	nominal := policy.nominalDelay(completedRuns, ceiling)
	if policy.Jitter == 0 {
		return nominal, nil
	}
	if source == nil {
		return 0, errors.New("compute delay: jitter source must not be nil")
	}

	width := uint64(policy.Jitter) + 1 //nolint:gosec // Validate established that jitter is nonnegative.
	sample := source.Uint64N(width)
	if sample >= width {
		return 0, fmt.Errorf("compute delay: jitter source returned %d outside [0,%d)", sample, width)
	}

	lowerHalf := int64(policy.Jitter / 2)
	delta := int64(sample) - lowerHalf //nolint:gosec // sample is bounded by a duration-sized interval.
	jittered := addDurationSaturating(nominal, delta)
	if jittered > ceiling {
		return ceiling, nil
	}

	return jittered, nil
}

func (policy DelayPolicy) nominalDelay(completedRuns uint64, ceiling time.Duration) time.Duration {
	if policy.Base == 0 || ceiling == 0 {
		return 0
	}

	switch policy.Backoff {
	case BackoffConstant:
		return min(policy.Base, ceiling)
	case BackoffLinear:
		return multiplyDurationSaturating(policy.Base, completedRuns, ceiling)
	case BackoffExponential:
		return exponentialDurationSaturating(
			policy.Base,
			policy.ExponentialBase,
			completedRuns-1,
			ceiling,
		)
	default:
		return 0
	}
}

func exponentialDurationSaturating(
	value time.Duration,
	base uint64,
	exponent uint64,
	ceiling time.Duration,
) time.Duration {
	factor := base
	for exponent > 0 {
		if exponent&1 == 1 {
			value = multiplyDurationSaturating(value, factor, ceiling)
			if value == ceiling {
				return ceiling
			}
		}

		exponent >>= 1
		if exponent != 0 {
			factor = multiplyUint64Saturating(factor, factor)
		}
	}

	return value
}

func multiplyDurationSaturating(value time.Duration, factor uint64, ceiling time.Duration) time.Duration {
	if value == 0 || factor == 0 || ceiling == 0 {
		return 0
	}
	if value >= ceiling || factor > uint64(ceiling/value) { //nolint:gosec // All durations are validated nonnegative.
		return ceiling
	}

	return value * time.Duration(factor) //nolint:gosec // The preceding quotient check proves the conversion fits.
}

func multiplyUint64Saturating(left, right uint64) uint64 {
	if left != 0 && right > math.MaxUint64/left {
		return math.MaxUint64
	}

	return left * right
}

func addDurationSaturating(value time.Duration, delta int64) time.Duration {
	if delta < 0 {
		magnitude := time.Duration(-delta)
		if magnitude >= value {
			return 0
		}

		return value - magnitude
	}
	if delta > math.MaxInt64-int64(value) {
		return time.Duration(math.MaxInt64)
	}

	return value + time.Duration(delta)
}
