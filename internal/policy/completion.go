package policy

import (
	"errors"
	"fmt"
	"time"
)

// Limit is either a positive finite count or an explicit unlimited value.
type Limit struct {
	Value     uint64
	Unlimited bool
}

// FiniteLimit constructs a positive finite limit.
func FiniteLimit(value uint64) (Limit, error) {
	limit := Limit{Value: value}
	if err := limit.Validate(); err != nil {
		return Limit{}, err
	}

	return limit, nil
}

// UnlimitedLimit constructs an explicit unlimited value.
func UnlimitedLimit() Limit {
	return Limit{Unlimited: true}
}

// Validate checks that the limit has exactly one valid representation.
func (limit Limit) Validate() error {
	if limit.Unlimited {
		if limit.Value != 0 {
			return errors.New("validate limit: unlimited limit must not have a finite value")
		}

		return nil
	}
	if limit.Value == 0 {
		return errors.New("validate limit: finite value must be positive")
	}

	return nil
}

// Reached reports whether count has met or exceeded a finite limit.
func (limit Limit) Reached(count uint64) bool {
	return !limit.Unlimited && limit.Value != 0 && count >= limit.Value
}

// DefaultCompletionPolicy returns the ordinary one-run policy.
func DefaultCompletionPolicy() CompletionPolicy {
	one := Limit{Value: 1}

	return CompletionPolicy{
		MaxRuns:       one,
		SuccessTarget: one,
		FailureLimit:  one,
	}
}

// CompletionPolicy configures terminal limits and an optional absolute retry
// deadline. The deadline is not extended by a user-requested pause.
type CompletionPolicy struct {
	MaxRuns         Limit
	SuccessTarget   Limit
	FailureLimit    Limit
	RetryAbortAt    time.Time
	HasRetryAbortAt bool
}

// Validate checks every completion-policy limit and deadline.
func (policy CompletionPolicy) Validate() error {
	limits := []struct {
		name  string
		limit Limit
	}{
		{name: "max runs", limit: policy.MaxRuns},
		{name: "success target", limit: policy.SuccessTarget},
		{name: "failure limit", limit: policy.FailureLimit},
	}
	for _, configured := range limits {
		if err := configured.limit.Validate(); err != nil {
			return fmt.Errorf("validate completion policy %s: %w", configured.name, err)
		}
	}

	if policy.HasRetryAbortAt && policy.RetryAbortAt.IsZero() {
		return errors.New("validate completion policy: retry abort time must not be zero")
	}
	if !policy.HasRetryAbortAt && !policy.RetryAbortAt.IsZero() {
		return errors.New("validate completion policy: retry abort time requires presence marker")
	}

	return nil
}

// RunCounts summarizes all completed runs after the latest result.
type RunCounts struct {
	Completed uint64
	Successes uint64
	Failures  uint64
}

// Validate checks that the counters partition completed runs.
func (counts RunCounts) Validate() error {
	if counts.Completed == 0 {
		return errors.New("validate run counts: at least one completed run is required")
	}
	if counts.Successes > counts.Completed {
		return errors.New("validate run counts: successes exceed completed runs")
	}
	if counts.Failures != counts.Completed-counts.Successes {
		return errors.New("validate run counts: successes and failures must partition completed runs")
	}

	return nil
}

// CompletionEvaluation contains the durable facts used after one run.
type CompletionEvaluation struct {
	Counts         RunCounts
	Classification RunClassification
	Canceled       bool
	JobTimedOut    bool
	Now            time.Time
	NextDelay      time.Duration
}

// CompletionAction tells the supervisor whether to finish or schedule a run.
type CompletionAction string

// Completion actions produced by policy evaluation.
const (
	CompletionActionSchedule CompletionAction = "schedule"
	CompletionActionComplete CompletionAction = "complete"
)

// CompletionOutcome is the terminal job outcome selected by the policy.
type CompletionOutcome string

// Completion outcomes selected by completion-policy evaluation.
const (
	CompletionOutcomeSuccess  CompletionOutcome = "success"
	CompletionOutcomeFailure  CompletionOutcome = "failure"
	CompletionOutcomeTimedOut CompletionOutcome = "timed_out"
	CompletionOutcomeCanceled CompletionOutcome = "cancelled" //nolint:misspell // The specification defines this persisted spelling.
	CompletionOutcomeAborted  CompletionOutcome = "aborted"
)

// CompletionReason is a stable explanation for a completion decision.
type CompletionReason string

// Completion reasons produced in deterministic precedence order.
const (
	CompletionReasonNextRun             CompletionReason = "next_run"
	CompletionReasonCancellation        CompletionReason = "cancellation"
	CompletionReasonJobTimeout          CompletionReason = "job_timeout"
	CompletionReasonSuccessTarget       CompletionReason = "success_target"
	CompletionReasonNonRetryableFailure CompletionReason = "non_retryable_failure"
	CompletionReasonFailureLimit        CompletionReason = "failure_limit"
	CompletionReasonRunLimit            CompletionReason = "run_limit"
	CompletionReasonRetryDeadline       CompletionReason = "retry_deadline"
)

// CompletionDecision is the complete deterministic result of evaluation.
type CompletionDecision struct {
	Action  CompletionAction
	Outcome CompletionOutcome
	Reason  CompletionReason
}

// Evaluate applies the specification's terminal-condition precedence.
func (policy CompletionPolicy) Evaluate(evaluation CompletionEvaluation) (CompletionDecision, error) {
	if err := policy.Validate(); err != nil {
		return CompletionDecision{}, err
	}
	if err := evaluation.Validate(); err != nil {
		return CompletionDecision{}, err
	}

	if evaluation.Canceled {
		return complete(CompletionOutcomeCanceled, CompletionReasonCancellation), nil
	}
	if evaluation.JobTimedOut {
		return complete(CompletionOutcomeTimedOut, CompletionReasonJobTimeout), nil
	}
	if policy.SuccessTarget.Reached(evaluation.Counts.Successes) {
		return complete(CompletionOutcomeSuccess, CompletionReasonSuccessTarget), nil
	}
	if evaluation.Classification == RunClassificationNonRetryableFailure {
		return complete(CompletionOutcomeFailure, CompletionReasonNonRetryableFailure), nil
	}
	if policy.FailureLimit.Reached(evaluation.Counts.Failures) {
		return complete(CompletionOutcomeFailure, CompletionReasonFailureLimit), nil
	}
	if policy.MaxRuns.Reached(evaluation.Counts.Completed) {
		return complete(CompletionOutcomeFailure, CompletionReasonRunLimit), nil
	}

	if policy.HasRetryAbortAt {
		tooLate, err := WouldStartAfter(evaluation.Now, evaluation.NextDelay, policy.RetryAbortAt)
		if err != nil {
			return CompletionDecision{}, fmt.Errorf("evaluate retry deadline: %w", err)
		}
		if tooLate {
			return complete(CompletionOutcomeAborted, CompletionReasonRetryDeadline), nil
		}
	}

	return CompletionDecision{
		Action: CompletionActionSchedule,
		Reason: CompletionReasonNextRun,
	}, nil
}

// Validate checks one post-run evaluation for internal consistency.
func (evaluation CompletionEvaluation) Validate() error {
	if err := evaluation.Counts.Validate(); err != nil {
		return err
	}
	if evaluation.Now.IsZero() {
		return errors.New("validate completion evaluation: current time must not be zero")
	}
	if evaluation.NextDelay < 0 {
		return errors.New("validate completion evaluation: next delay must not be negative")
	}

	switch evaluation.Classification {
	case RunClassificationSuccess:
		if evaluation.Counts.Successes == 0 {
			return errors.New("validate completion evaluation: successful latest run requires a success count")
		}
	case RunClassificationRetryableFailure, RunClassificationNonRetryableFailure:
		if evaluation.Counts.Failures == 0 {
			return errors.New("validate completion evaluation: failed latest run requires a failure count")
		}
	default:
		return fmt.Errorf("validate completion evaluation: unknown classification %q", evaluation.Classification)
	}

	return nil
}

func complete(outcome CompletionOutcome, reason CompletionReason) CompletionDecision {
	return CompletionDecision{
		Action:  CompletionActionComplete,
		Outcome: outcome,
		Reason:  reason,
	}
}
