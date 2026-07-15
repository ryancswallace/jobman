package policy

import (
	"testing"
	"time"
)

func TestTimeoutBudgetPauseResumeAccounting(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	budget, err := NewTimeoutBudget(TimeoutScopeJob, 10*time.Second, startedAt)
	if err != nil {
		t.Fatalf("NewTimeoutBudget() error = %v", err)
	}

	assertBudgetReading(t, budget, startedAt.Add(3*time.Second), 3*time.Second, 7*time.Second, false)
	deadline, enabled, err := budget.Deadline(startedAt.Add(3 * time.Second))
	if err != nil {
		t.Fatalf("Deadline() error = %v", err)
	}
	if !enabled || !deadline.Equal(startedAt.Add(10*time.Second)) {
		t.Fatalf("Deadline() = (%v, %t), want (%v, true)", deadline, enabled, startedAt.Add(10*time.Second))
	}

	paused, err := budget.Pause(startedAt.Add(3 * time.Second))
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if !paused.Paused() {
		t.Fatal("Pause() did not mark budget paused")
	}
	assertBudgetReading(t, paused, startedAt.Add(8*time.Second), 3*time.Second, 7*time.Second, false)
	deadline, enabled, err = paused.Deadline(startedAt.Add(8 * time.Second))
	if err != nil {
		t.Fatalf("paused Deadline() error = %v", err)
	}
	if !enabled || !deadline.Equal(startedAt.Add(15*time.Second)) {
		t.Fatalf("paused Deadline() = (%v, %t), want (%v, true)", deadline, enabled, startedAt.Add(15*time.Second))
	}

	resumed, err := paused.Resume(startedAt.Add(8 * time.Second))
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if resumed.Paused() {
		t.Fatal("Resume() left budget paused")
	}
	assertBudgetReading(t, resumed, startedAt.Add(9*time.Second), 4*time.Second, 6*time.Second, false)
	assertBudgetReading(t, resumed, startedAt.Add(15*time.Second), 10*time.Second, 0, true)
}

func TestTimeoutBudgetIdempotencyAndPrecedence(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	budget, err := NewTimeoutBudget(TimeoutScopeRun, 10*time.Second, startedAt)
	if err != nil {
		t.Fatalf("NewTimeoutBudget() error = %v", err)
	}

	resumed, err := budget.Resume(startedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("idempotent Resume() error = %v", err)
	}
	if resumed.Paused() {
		t.Fatal("idempotent Resume() paused budget")
	}

	paused, err := budget.Pause(startedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	repeated, err := paused.Pause(startedAt.Add(5 * time.Second))
	if err != nil {
		t.Fatalf("idempotent Pause() error = %v", err)
	}
	assertBudgetReading(t, repeated, startedAt.Add(5*time.Second), time.Second, 9*time.Second, false)

	if _, pauseErr := budget.Pause(startedAt.Add(10 * time.Second)); pauseErr == nil {
		t.Fatal("Pause() at expired deadline unexpectedly succeeded")
	}
}

func TestDisabledTimeoutBudget(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	budget, err := NewTimeoutBudget(TimeoutScopeJob, 0, startedAt)
	if err != nil {
		t.Fatalf("NewTimeoutBudget() error = %v", err)
	}
	if budget.Enabled() {
		t.Fatal("zero timeout budget is enabled")
	}

	remaining, enabled, err := budget.Remaining(startedAt.Add(100 * 365 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("Remaining() error = %v", err)
	}
	if remaining != 0 || enabled {
		t.Fatalf("Remaining() = (%v, %t), want (0, false)", remaining, enabled)
	}
	expired, err := budget.Expired(startedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Expired() error = %v", err)
	}
	if expired {
		t.Fatal("disabled timeout expired")
	}
	_, hasDeadline, err := budget.Deadline(startedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("Deadline() error = %v", err)
	}
	if hasDeadline {
		t.Fatal("disabled timeout has deadline")
	}

	paused, err := budget.Pause(startedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("disabled Pause() error = %v", err)
	}
	if _, err = paused.Resume(startedAt.Add(time.Hour)); err != nil {
		t.Fatalf("disabled Resume() error = %v", err)
	}
}

func TestTimeoutBudgetRejectsInvalidReadings(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	invalidInputs := []struct {
		scope TimeoutScope
		limit time.Duration
		start time.Time
	}{
		{scope: "process", limit: time.Second, start: startedAt},
		{scope: TimeoutScopeRun, limit: -1, start: startedAt},
		{scope: TimeoutScopeJob, limit: time.Second},
	}
	for _, input := range invalidInputs {
		if _, err := NewTimeoutBudget(input.scope, input.limit, input.start); err == nil {
			t.Errorf("NewTimeoutBudget(%q, %v, %v) unexpectedly succeeded", input.scope, input.limit, input.start)
		}
	}

	budget, err := NewTimeoutBudget(TimeoutScopeRun, time.Second, startedAt)
	if err != nil {
		t.Fatalf("NewTimeoutBudget() error = %v", err)
	}
	if _, err = budget.Elapsed(startedAt.Add(-time.Nanosecond)); err == nil {
		t.Error("Elapsed(before start) unexpectedly succeeded")
	}
	paused, err := budget.Pause(startedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if _, err = paused.Resume(startedAt); err == nil {
		t.Error("Resume(before pause) unexpectedly succeeded")
	}
}

func TestTimeoutBudgetPauseProperty(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	for activeBeforePause := range 50 {
		for pauseSeconds := range 50 {
			budget, err := NewTimeoutBudget(TimeoutScopeJob, time.Minute, startedAt)
			if err != nil {
				t.Fatalf("NewTimeoutBudget() error = %v", err)
			}
			paused, err := budget.Pause(startedAt.Add(time.Duration(activeBeforePause) * time.Second))
			if err != nil {
				t.Fatalf("Pause(active=%d) error = %v", activeBeforePause, err)
			}
			resumed, err := paused.Resume(
				startedAt.Add(time.Duration(activeBeforePause+pauseSeconds) * time.Second),
			)
			if err != nil {
				t.Fatalf("Resume(active=%d,pause=%d) error = %v", activeBeforePause, pauseSeconds, err)
			}

			deadline, enabled, err := resumed.Deadline(
				startedAt.Add(time.Duration(activeBeforePause+pauseSeconds) * time.Second),
			)
			if err != nil {
				t.Fatalf("Deadline(active=%d,pause=%d) error = %v", activeBeforePause, pauseSeconds, err)
			}
			want := startedAt.Add(time.Minute + time.Duration(pauseSeconds)*time.Second)
			if !enabled || !deadline.Equal(want) {
				t.Fatalf(
					"Deadline(active=%d,pause=%d) = (%v,%t), want (%v,true)",
					activeBeforePause,
					pauseSeconds,
					deadline,
					enabled,
					want,
				)
			}
		}
	}
}

func assertBudgetReading(
	t *testing.T,
	budget TimeoutBudget,
	at time.Time,
	wantElapsed time.Duration,
	wantRemaining time.Duration,
	wantExpired bool,
) {
	t.Helper()

	elapsed, err := budget.Elapsed(at)
	if err != nil {
		t.Fatalf("Elapsed() error = %v", err)
	}
	if elapsed != wantElapsed {
		t.Fatalf("Elapsed() = %v, want %v", elapsed, wantElapsed)
	}
	remaining, enabled, err := budget.Remaining(at)
	if err != nil {
		t.Fatalf("Remaining() error = %v", err)
	}
	if !enabled || remaining != wantRemaining {
		t.Fatalf("Remaining() = (%v, %t), want (%v, true)", remaining, enabled, wantRemaining)
	}
	expired, err := budget.Expired(at)
	if err != nil {
		t.Fatalf("Expired() error = %v", err)
	}
	if expired != wantExpired {
		t.Fatalf("Expired() = %t, want %t", expired, wantExpired)
	}
}
