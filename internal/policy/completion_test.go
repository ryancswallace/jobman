package policy

import (
	"strings"
	"testing"
	"time"
)

func TestLimit(t *testing.T) {
	t.Parallel()

	finite, err := FiniteLimit(3)
	if err != nil {
		t.Fatalf("FiniteLimit() error = %v", err)
	}
	if finite.Reached(2) || !finite.Reached(3) || !finite.Reached(4) {
		t.Fatal("finite Reached() does not implement an inclusive upper bound")
	}

	unlimited := UnlimitedLimit()
	if validateErr := unlimited.Validate(); validateErr != nil {
		t.Fatalf("UnlimitedLimit().Validate() error = %v", validateErr)
	}
	if unlimited.Reached(^uint64(0)) {
		t.Fatal("unlimited limit was reached")
	}

	for _, limit := range []Limit{{}, {Value: 1, Unlimited: true}} {
		if validateErr := limit.Validate(); validateErr == nil {
			t.Errorf("Limit%+v.Validate() unexpectedly succeeded", limit)
		}
	}
	if _, finiteErr := FiniteLimit(0); finiteErr == nil {
		t.Fatal("FiniteLimit(0) unexpectedly succeeded")
	}
}

func TestCompletionEvaluationPrecedence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	basePolicy := CompletionPolicy{
		MaxRuns:         Limit{Value: 4},
		SuccessTarget:   Limit{Value: 2},
		FailureLimit:    Limit{Value: 2},
		RetryAbortAt:    now.Add(time.Minute),
		HasRetryAbortAt: true,
	}
	runLimitPolicy := basePolicy
	runLimitPolicy.FailureLimit = UnlimitedLimit()

	tests := []struct {
		name       string
		policy     CompletionPolicy
		evaluation CompletionEvaluation
		want       CompletionDecision
	}{
		{
			name:   "cancellation precedes everything",
			policy: basePolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 4, Successes: 2, Failures: 2},
				Classification: RunClassificationNonRetryableFailure,
				Canceled:       true,
				JobTimedOut:    true,
				Now:            now,
			},
			want: complete(CompletionOutcomeCanceled, CompletionReasonCancellation),
		},
		{
			name:   "timeout precedes success target",
			policy: basePolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 2, Successes: 2},
				Classification: RunClassificationSuccess,
				JobTimedOut:    true,
				Now:            now,
			},
			want: complete(CompletionOutcomeTimedOut, CompletionReasonJobTimeout),
		},
		{
			name:   "success target precedes latest non-retryable failure",
			policy: basePolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 3, Successes: 2, Failures: 1},
				Classification: RunClassificationNonRetryableFailure,
				Now:            now,
			},
			want: complete(CompletionOutcomeSuccess, CompletionReasonSuccessTarget),
		},
		{
			name:   "non-retryable failure precedes limits",
			policy: basePolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 2, Successes: 1, Failures: 1},
				Classification: RunClassificationNonRetryableFailure,
				Now:            now,
			},
			want: complete(CompletionOutcomeFailure, CompletionReasonNonRetryableFailure),
		},
		{
			name:   "failure limit precedes run limit",
			policy: basePolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 3, Successes: 1, Failures: 2},
				Classification: RunClassificationRetryableFailure,
				Now:            now,
			},
			want: complete(CompletionOutcomeFailure, CompletionReasonFailureLimit),
		},
		{
			name:   "run limit",
			policy: runLimitPolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 4, Successes: 1, Failures: 3},
				Classification: RunClassificationRetryableFailure,
				Now:            now,
			},
			want: complete(CompletionOutcomeFailure, CompletionReasonRunLimit),
		},
		{
			name:   "retry deadline",
			policy: basePolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 1, Failures: 1},
				Classification: RunClassificationRetryableFailure,
				Now:            now,
				NextDelay:      61 * time.Second,
			},
			want: complete(CompletionOutcomeAborted, CompletionReasonRetryDeadline),
		},
		{
			name:   "schedule exactly at retry deadline",
			policy: basePolicy,
			evaluation: CompletionEvaluation{
				Counts:         RunCounts{Completed: 1, Failures: 1},
				Classification: RunClassificationRetryableFailure,
				Now:            now,
				NextDelay:      time.Minute,
			},
			want: CompletionDecision{Action: CompletionActionSchedule, Reason: CompletionReasonNextRun},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.policy.Evaluate(test.evaluation)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Evaluate() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestSuccessfulRunsRepeatUntilTarget(t *testing.T) {
	t.Parallel()

	completionPolicy := CompletionPolicy{
		MaxRuns:       Limit{Value: 10},
		SuccessTarget: Limit{Value: 3},
		FailureLimit:  UnlimitedLimit(),
	}
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)

	for successes := uint64(1); successes <= 3; successes++ {
		decision, err := completionPolicy.Evaluate(CompletionEvaluation{
			Counts: RunCounts{
				Completed: successes,
				Successes: successes,
			},
			Classification: RunClassificationSuccess,
			Now:            now,
		})
		if err != nil {
			t.Fatalf("Evaluate(successes=%d) error = %v", successes, err)
		}

		wantAction := CompletionActionSchedule
		if successes == 3 {
			wantAction = CompletionActionComplete
		}
		if decision.Action != wantAction {
			t.Errorf("Evaluate(successes=%d) action = %q, want %q", successes, decision.Action, wantAction)
		}
	}
}

func TestUnlimitedCompletionPolicySchedulesBoundedSample(t *testing.T) {
	t.Parallel()

	completionPolicy := CompletionPolicy{
		MaxRuns:       UnlimitedLimit(),
		SuccessTarget: UnlimitedLimit(),
		FailureLimit:  UnlimitedLimit(),
	}
	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)

	for completed := uint64(1); completed <= 1_000; completed++ {
		decision, err := completionPolicy.Evaluate(CompletionEvaluation{
			Counts:         RunCounts{Completed: completed, Failures: completed},
			Classification: RunClassificationRetryableFailure,
			Now:            now,
		})
		if err != nil {
			t.Fatalf("Evaluate(completed=%d) error = %v", completed, err)
		}
		if decision.Action != CompletionActionSchedule {
			t.Fatalf("Evaluate(completed=%d) = %+v, want schedule", completed, decision)
		}
	}
}

func TestCompletionValidation(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	valid := DefaultCompletionPolicy()
	if err := valid.Validate(); err != nil {
		t.Fatalf("DefaultCompletionPolicy().Validate() error = %v", err)
	}

	invalidPolicies := []CompletionPolicy{
		{},
		{
			MaxRuns: UnlimitedLimit(), SuccessTarget: UnlimitedLimit(), FailureLimit: UnlimitedLimit(),
			HasRetryAbortAt: true,
		},
		{
			MaxRuns: UnlimitedLimit(), SuccessTarget: UnlimitedLimit(), FailureLimit: UnlimitedLimit(),
			RetryAbortAt: fixedTime,
		},
	}
	for _, completionPolicy := range invalidPolicies {
		if err := completionPolicy.Validate(); err == nil {
			t.Errorf("CompletionPolicy%+v.Validate() unexpectedly succeeded", completionPolicy)
		}
	}

	for _, counts := range []RunCounts{
		{},
		{Completed: 1, Successes: 2},
		{Completed: 2, Successes: 1},
	} {
		if err := counts.Validate(); err == nil {
			t.Errorf("RunCounts%+v.Validate() unexpectedly succeeded", counts)
		}
	}

	_, err := valid.Evaluate(CompletionEvaluation{
		Counts:         RunCounts{Completed: 1, Failures: 1},
		Classification: "unknown",
		Now:            fixedTime,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown classification") {
		t.Fatalf("Evaluate(unknown classification) error = %v", err)
	}
}
