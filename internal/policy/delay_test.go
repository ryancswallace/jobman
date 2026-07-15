package policy

import (
	"math"
	"strings"
	"testing"
	"time"
)

type fixedJitterSource struct {
	value uint64
}

func (source fixedJitterSource) Uint64N(uint64) uint64 {
	return source.value
}

func TestDelayAlgorithms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		policy    DelayPolicy
		completed uint64
		want      time.Duration
	}{
		{
			name:      "constant",
			policy:    DelayPolicy{Base: 3 * time.Second, Backoff: BackoffConstant},
			completed: 100,
			want:      3 * time.Second,
		},
		{
			name:      "linear first",
			policy:    DelayPolicy{Base: 3 * time.Second, Backoff: BackoffLinear},
			completed: 1,
			want:      3 * time.Second,
		},
		{
			name:      "linear fourth",
			policy:    DelayPolicy{Base: 3 * time.Second, Backoff: BackoffLinear},
			completed: 4,
			want:      12 * time.Second,
		},
		{
			name: "exponential first",
			policy: DelayPolicy{
				Base: 3 * time.Second, Backoff: BackoffExponential, ExponentialBase: 2,
			},
			completed: 1,
			want:      3 * time.Second,
		},
		{
			name: "exponential fourth",
			policy: DelayPolicy{
				Base: 3 * time.Second, Backoff: BackoffExponential, ExponentialBase: 2,
			},
			completed: 4,
			want:      24 * time.Second,
		},
		{
			name: "exponential base one",
			policy: DelayPolicy{
				Base: 3 * time.Second, Backoff: BackoffExponential, ExponentialBase: 1,
			},
			completed: math.MaxUint64,
			want:      3 * time.Second,
		},
		{
			name: "explicit cap",
			policy: DelayPolicy{
				Base: 3 * time.Second, Backoff: BackoffLinear, MaxDelay: 10 * time.Second, HasMaxDelay: true,
			},
			completed: 4,
			want:      10 * time.Second,
		},
		{
			name: "zero cap",
			policy: DelayPolicy{
				Base: 3 * time.Second, Backoff: BackoffLinear, HasMaxDelay: true,
			},
			completed: math.MaxUint64,
			want:      0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.policy.Delay(test.completed, nil)
			if err != nil {
				t.Fatalf("Delay() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Delay() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDelaySaturatesOnOverflow(t *testing.T) {
	t.Parallel()

	linear := DelayPolicy{Base: time.Duration(math.MaxInt64 / 2), Backoff: BackoffLinear}
	delay, err := linear.Delay(math.MaxUint64, nil)
	if err != nil {
		t.Fatalf("linear Delay() error = %v", err)
	}
	if delay != time.Duration(math.MaxInt64) {
		t.Fatalf("linear Delay() = %v, want maximum duration", delay)
	}

	exponential := DelayPolicy{
		Base:            time.Nanosecond,
		Backoff:         BackoffExponential,
		ExponentialBase: math.MaxUint64,
		MaxDelay:        time.Hour,
		HasMaxDelay:     true,
	}
	delay, err = exponential.Delay(math.MaxUint64, nil)
	if err != nil {
		t.Fatalf("exponential Delay() error = %v", err)
	}
	if delay != time.Hour {
		t.Fatalf("exponential Delay() = %v, want one-hour cap", delay)
	}
}

func TestDelayJitterBoundsAndClamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy DelayPolicy
		sample uint64
		want   time.Duration
	}{
		{"lower endpoint", DelayPolicy{Base: 10, Backoff: BackoffConstant, Jitter: 4}, 0, 8},
		{"center", DelayPolicy{Base: 10, Backoff: BackoffConstant, Jitter: 4}, 2, 10},
		{"upper endpoint", DelayPolicy{Base: 10, Backoff: BackoffConstant, Jitter: 4}, 4, 12},
		{"odd width upper endpoint", DelayPolicy{Base: 10, Backoff: BackoffConstant, Jitter: 5}, 5, 13},
		{"clamp at zero", DelayPolicy{Base: 1, Backoff: BackoffConstant, Jitter: 4}, 0, 0},
		{
			"clamp at configured maximum",
			DelayPolicy{Base: 10, Backoff: BackoffConstant, MaxDelay: 11, HasMaxDelay: true, Jitter: 4},
			4,
			11,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.policy.Delay(1, fixedJitterSource{value: test.sample})
			if err != nil {
				t.Fatalf("Delay() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Delay() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDelayJitterProperty(t *testing.T) {
	t.Parallel()

	for completed := uint64(1); completed <= 64; completed++ {
		for sample := range uint64(21) {
			delayPolicy := DelayPolicy{
				Base:        7 * time.Millisecond,
				Backoff:     BackoffLinear,
				MaxDelay:    100 * time.Millisecond,
				HasMaxDelay: true,
				Jitter:      20,
			}
			delay, err := delayPolicy.Delay(completed, fixedJitterSource{value: sample})
			if err != nil {
				t.Fatalf("Delay(completed=%d, sample=%d) error = %v", completed, sample, err)
			}
			if delay < 0 || delay > delayPolicy.MaxDelay {
				t.Fatalf("Delay(completed=%d, sample=%d) = %v outside bounds", completed, sample, delay)
			}
		}
	}
}

func TestDelayValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		policy DelayPolicy
		part   string
	}{
		{DelayPolicy{Base: -1, Backoff: BackoffConstant}, "base"},
		{DelayPolicy{Backoff: BackoffConstant, Jitter: -1}, "jitter"},
		{DelayPolicy{Backoff: BackoffConstant, MaxDelay: -1, HasMaxDelay: true}, "maximum"},
		{DelayPolicy{Backoff: BackoffConstant, MaxDelay: 1}, "presence"},
		{DelayPolicy{Backoff: BackoffExponential}, "exponential base"},
		{DelayPolicy{Backoff: "quadratic"}, "unknown"},
	}

	for _, test := range tests {
		if err := test.policy.Validate(); err == nil || !strings.Contains(err.Error(), test.part) {
			t.Errorf("DelayPolicy%+v.Validate() error = %v, want containing %q", test.policy, err, test.part)
		}
	}

	valid := DelayPolicy{Backoff: BackoffConstant}
	if _, err := valid.Delay(0, nil); err == nil {
		t.Error("Delay(completedRuns=0) unexpectedly succeeded")
	}
	withJitter := DelayPolicy{Backoff: BackoffConstant, Jitter: 2}
	if _, err := withJitter.Delay(1, nil); err == nil {
		t.Error("Delay(nil jitter source) unexpectedly succeeded")
	}
	if _, err := withJitter.Delay(1, fixedJitterSource{value: 3}); err == nil {
		t.Error("Delay(out-of-range jitter source) unexpectedly succeeded")
	}
}
