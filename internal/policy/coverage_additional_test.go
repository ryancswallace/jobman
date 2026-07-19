package policy

import (
	"math"
	"testing"
	"time"
)

type outOfRangeJitter struct{}

func (outOfRangeJitter) Uint64N(limit uint64) uint64 { return limit }

func TestAdditionalPolicyValidationBranches(t *testing.T) {
	t.Parallel()

	if err := (RunResult{
		Termination: RunTerminationPlatform, PlatformReason: "reason", Signal: "TERM",
	}).Validate(); err == nil {
		t.Fatal("RunResult.Validate() accepted ambiguous platform termination")
	}

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	validCounts := RunCounts{Completed: 1, Successes: 1}
	validEvaluation := CompletionEvaluation{
		Counts: validCounts, Classification: RunClassificationSuccess, Now: now,
	}
	if _, err := (CompletionPolicy{}).Evaluate(validEvaluation); err == nil {
		t.Fatal("CompletionPolicy.Evaluate() accepted invalid policy")
	}
	for name, evaluation := range map[string]CompletionEvaluation{
		"zero time":       {Counts: validCounts, Classification: RunClassificationSuccess},
		"negative delay":  {Counts: validCounts, Classification: RunClassificationSuccess, Now: now, NextDelay: -1},
		"missing success": {Counts: RunCounts{Completed: 1, Failures: 1}, Classification: RunClassificationSuccess, Now: now},
		"missing failure": {Counts: RunCounts{Completed: 1, Successes: 1}, Classification: RunClassificationRetryableFailure, Now: now},
		"classification":  {Counts: validCounts, Classification: "unknown", Now: now},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := evaluation.Validate(); err == nil {
				t.Fatal("CompletionEvaluation.Validate() error = nil")
			}
		})
	}
}

func TestAdditionalDelayArithmeticBranches(t *testing.T) {
	t.Parallel()

	if _, err := (DelayPolicy{Backoff: "unknown"}).Delay(1, nil); err == nil {
		t.Fatal("Delay() accepted invalid policy")
	}
	policy := DelayPolicy{Base: time.Second, Backoff: BackoffConstant, Jitter: time.Second}
	if _, err := policy.Delay(1, outOfRangeJitter{}); err == nil {
		t.Fatal("Delay() accepted out-of-range jitter")
	}
	if got := (DelayPolicy{Base: time.Second, Backoff: "unknown"}).nominalDelay(1, time.Hour); got != 0 {
		t.Fatalf("nominalDelay(unknown) = %v", got)
	}
	if got := multiplyDurationSaturating(0, 10, time.Hour); got != 0 {
		t.Fatalf("multiplyDurationSaturating(zero) = %v", got)
	}
	if got := addDurationSaturating(time.Duration(math.MaxInt64), 1); got != time.Duration(math.MaxInt64) {
		t.Fatalf("addDurationSaturating(overflow) = %v", got)
	}
}

func TestTimeoutBudgetAccessorsAndFailurePropagation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	budget, err := NewTimeoutBudget(TimeoutScopeRun, time.Second, now)
	if err != nil {
		t.Fatal(err)
	}
	if budget.Scope() != TimeoutScopeRun || budget.Limit() != time.Second || !budget.StartedAt().Equal(now) {
		t.Fatalf("budget accessors = %q, %v, %v", budget.Scope(), budget.Limit(), budget.StartedAt())
	}
	corrupt := budget
	corrupt.startedAt = time.Time{}
	if _, err := corrupt.Pause(now); err == nil {
		t.Fatal("Pause() accepted corrupt budget")
	}
	if _, _, err := corrupt.Remaining(now); err == nil {
		t.Fatal("Remaining() accepted corrupt budget")
	}
}

func TestWaitDefensiveBranches(t *testing.T) {
	t.Parallel()

	if _, err := EvaluateDelay(time.Time{}, time.Now(), 0); err == nil {
		t.Fatal("EvaluateDelay() accepted a zero clock")
	}
	if matchesFileKind(0, FileKind("unknown")) {
		t.Fatal("matchesFileKind() accepted unknown kind")
	}
	//nolint:staticcheck // A nil context is deliberately supplied to verify the defensive API boundary.
	if result := EvaluateProbe(nil, nil, ProbeSpec{}); !result.Fatal {
		t.Fatal("EvaluateProbe(nil context) was not fatal")
	}
}
