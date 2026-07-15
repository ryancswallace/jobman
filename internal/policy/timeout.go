package policy

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// TimeoutScope distinguishes per-run and whole-job timeout accounting.
type TimeoutScope string

// Timeout scopes defined by the v1 specification.
const (
	TimeoutScopeRun TimeoutScope = "run"
	TimeoutScopeJob TimeoutScope = "job"
)

// TimeoutBudget immutably tracks active elapsed time across pause/resume. A
// zero limit disables expiry while retaining the same accounting API.
type TimeoutBudget struct {
	scope       TimeoutScope
	limit       time.Duration
	startedAt   time.Time
	pausedAt    time.Time
	pausedTotal time.Duration
}

// NewTimeoutBudget starts a run or job budget at the supplied clock reading.
func NewTimeoutBudget(
	scope TimeoutScope,
	limit time.Duration,
	startedAt time.Time,
) (TimeoutBudget, error) {
	if scope != TimeoutScopeRun && scope != TimeoutScopeJob {
		return TimeoutBudget{}, fmt.Errorf("create timeout budget: unknown scope %q", scope)
	}
	if limit < 0 {
		return TimeoutBudget{}, errors.New("create timeout budget: limit must not be negative")
	}
	if startedAt.IsZero() {
		return TimeoutBudget{}, errors.New("create timeout budget: start time must not be zero")
	}

	return TimeoutBudget{
		scope:     scope,
		limit:     limit,
		startedAt: startedAt,
	}, nil
}

// Scope returns whether this is a run or job timeout budget.
func (budget TimeoutBudget) Scope() TimeoutScope {
	return budget.scope
}

// Limit returns the configured duration; zero means disabled.
func (budget TimeoutBudget) Limit() time.Duration {
	return budget.limit
}

// StartedAt returns the clock reading at which accounting began.
func (budget TimeoutBudget) StartedAt() time.Time {
	return budget.startedAt
}

// Enabled reports whether this budget can expire.
func (budget TimeoutBudget) Enabled() bool {
	return budget.limit > 0
}

// Paused reports whether active-time accounting is currently suspended.
func (budget TimeoutBudget) Paused() bool {
	return !budget.pausedAt.IsZero()
}

// Pause returns a copy whose accounting is suspended at the supplied time.
// Repeated pause calls are idempotent. An already-expired budget rejects pause
// so a previously observed timeout retains precedence.
func (budget TimeoutBudget) Pause(at time.Time) (TimeoutBudget, error) {
	if err := budget.validateTime(at); err != nil {
		return TimeoutBudget{}, fmt.Errorf("pause timeout budget: %w", err)
	}
	if budget.Paused() {
		return budget, nil
	}

	expired, err := budget.Expired(at)
	if err != nil {
		return TimeoutBudget{}, fmt.Errorf("pause timeout budget: %w", err)
	}
	if expired {
		return TimeoutBudget{}, errors.New("pause timeout budget: budget is already expired")
	}

	budget.pausedAt = at

	return budget, nil
}

// Resume returns a copy whose accounting continues at the supplied time.
// Repeated resume calls are idempotent.
func (budget TimeoutBudget) Resume(at time.Time) (TimeoutBudget, error) {
	if err := budget.validateTime(at); err != nil {
		return TimeoutBudget{}, fmt.Errorf("resume timeout budget: %w", err)
	}
	if !budget.Paused() {
		return budget, nil
	}
	if at.Before(budget.pausedAt) {
		return TimeoutBudget{}, errors.New("resume timeout budget: resume time precedes pause time")
	}

	budget.pausedTotal = addNonnegativeDurationSaturating(
		budget.pausedTotal,
		at.Sub(budget.pausedAt),
	)
	budget.pausedAt = time.Time{}

	return budget, nil
}

// Elapsed returns active, non-paused time consumed as of at.
func (budget TimeoutBudget) Elapsed(at time.Time) (time.Duration, error) {
	if err := budget.validateTime(at); err != nil {
		return 0, fmt.Errorf("compute timeout elapsed time: %w", err)
	}

	end := at
	if budget.Paused() {
		if at.Before(budget.pausedAt) {
			return 0, errors.New("compute timeout elapsed time: current time precedes pause time")
		}
		end = budget.pausedAt
	}

	total := end.Sub(budget.startedAt)
	if budget.pausedTotal >= total {
		return 0, nil
	}

	return total - budget.pausedTotal, nil
}

// Remaining returns active time left. A disabled budget reports no deadline
// through the second return value.
func (budget TimeoutBudget) Remaining(at time.Time) (time.Duration, bool, error) {
	if !budget.Enabled() {
		if err := budget.validateTime(at); err != nil {
			return 0, false, fmt.Errorf("compute timeout remaining time: %w", err)
		}

		return 0, false, nil
	}

	elapsed, err := budget.Elapsed(at)
	if err != nil {
		return 0, false, err
	}
	if elapsed >= budget.limit {
		return 0, true, nil
	}

	return budget.limit - elapsed, true, nil
}

// Expired reports whether active time has consumed the configured limit.
func (budget TimeoutBudget) Expired(at time.Time) (bool, error) {
	remaining, enabled, err := budget.Remaining(at)
	if err != nil {
		return false, err
	}

	return enabled && remaining == 0, nil
}

// Deadline returns the effective monotonic deadline as viewed at. While
// paused, it advances with at so the remaining budget stays fixed.
func (budget TimeoutBudget) Deadline(at time.Time) (time.Time, bool, error) {
	remaining, enabled, err := budget.Remaining(at)
	if err != nil {
		return time.Time{}, false, err
	}
	if !enabled {
		return time.Time{}, false, nil
	}

	return at.Add(remaining), true, nil
}

func (budget TimeoutBudget) validateTime(at time.Time) error {
	if budget.scope != TimeoutScopeRun && budget.scope != TimeoutScopeJob {
		return fmt.Errorf("invalid timeout scope %q", budget.scope)
	}
	if budget.limit < 0 {
		return errors.New("timeout limit must not be negative")
	}
	if budget.startedAt.IsZero() {
		return errors.New("timeout start time must not be zero")
	}
	if at.IsZero() {
		return errors.New("current time must not be zero")
	}
	if at.Before(budget.startedAt) {
		return errors.New("current time precedes timeout start")
	}

	return nil
}

func addNonnegativeDurationSaturating(left, right time.Duration) time.Duration {
	if right < 0 {
		return left
	}
	if right > time.Duration(math.MaxInt64)-left {
		return time.Duration(math.MaxInt64)
	}

	return left + right
}
