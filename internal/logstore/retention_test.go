package logstore

import (
	"reflect"
	"testing"
	"time"
)

const secondTestJobID = "019c5f8b-7c8a-7000-8000-000000000002"

func TestPlanRetentionAppliesLimitsOldestFirst(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	candidates := []RetentionCandidate{
		{JobID: testJobID, RunNumber: 3, CompletedAt: now.Add(-time.Hour), Bytes: 3},
		{JobID: secondTestJobID, RunNumber: 1, CompletedAt: now.Add(-4 * time.Hour), Bytes: 4},
		{JobID: testJobID, RunNumber: 1, CompletedAt: now.Add(-3 * time.Hour), Bytes: 5},
		{JobID: testJobID, RunNumber: 2, CompletedAt: now.Add(-2 * time.Hour), Bytes: 4},
	}

	tests := []struct {
		name   string
		policy RetentionPolicy
		want   []retentionKey
	}{
		{
			name:   "unlimited",
			policy: unlimitedRetentionPolicy(),
			want:   []retentionKey{},
		},
		{
			name:   "age",
			policy: withRetentionAge(unlimitedRetentionPolicy(), 2*time.Hour),
			want: []retentionKey{
				{jobID: secondTestJobID, runNumber: 1},
				{jobID: testJobID, runNumber: 1},
				{jobID: testJobID, runNumber: 2},
			},
		},
		{
			name:   "runs per job",
			policy: withRunLimit(unlimitedRetentionPolicy(), 2),
			want:   []retentionKey{{jobID: testJobID, runNumber: 1}},
		},
		{
			name:   "bytes per job",
			policy: withJobByteLimit(unlimitedRetentionPolicy(), 7),
			want:   []retentionKey{{jobID: testJobID, runNumber: 1}},
		},
		{
			name:   "total bytes",
			policy: withTotalByteLimit(unlimitedRetentionPolicy(), 7),
			want: []retentionKey{
				{jobID: secondTestJobID, runNumber: 1},
				{jobID: testJobID, runNumber: 1},
			},
		},
		{
			name:   "jobs",
			policy: withJobLimit(unlimitedRetentionPolicy(), 1),
			want:   []retentionKey{{jobID: secondTestJobID, runNumber: 1}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := PlanRetention(now, candidates, test.policy)
			if err != nil {
				t.Fatalf("PlanRetention() error = %v", err)
			}
			keys := make([]retentionKey, 0, len(got))
			for _, candidate := range got {
				keys = append(keys, retentionCandidateKey(candidate))
			}
			if !reflect.DeepEqual(keys, test.want) {
				t.Errorf("PlanRetention() = %#v, want %#v", keys, test.want)
			}
		})
	}
}

func TestPlanRetentionNeverSelectsActiveRuns(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	candidates := []RetentionCandidate{
		{JobID: testJobID, RunNumber: 1, CompletedAt: now.Add(-time.Hour), Bytes: 4},
		{JobID: testJobID, RunNumber: 2, Bytes: 10, Active: true},
		{JobID: secondTestJobID, RunNumber: 1, CompletedAt: now.Add(-2 * time.Hour), Bytes: 4},
	}
	policy := RetentionPolicy{}
	selected, err := PlanRetention(now, candidates, policy)
	if err != nil {
		t.Fatalf("PlanRetention() error = %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("PlanRetention() selected %d candidates, want 2 completed candidates", len(selected))
	}
	for _, candidate := range selected {
		if candidate.Active {
			t.Fatalf("PlanRetention() selected active candidate %+v", candidate)
		}
	}
}

func TestPlanRetentionRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name       string
		candidates []RetentionCandidate
		policy     RetentionPolicy
	}{
		{
			name:   "negative age",
			policy: RetentionPolicy{MaxAge: RetentionAgeLimit{Maximum: -time.Second}},
		},
		{
			name:   "ambiguous unlimited",
			policy: RetentionPolicy{MaxJobs: RetentionLimit{Maximum: 1, Unlimited: true}},
		},
		{
			name: "invalid job ID",
			candidates: []RetentionCandidate{{
				JobID: "../bad", RunNumber: 1, CompletedAt: now,
			}},
			policy: unlimitedRetentionPolicy(),
		},
		{
			name: "duplicate",
			candidates: []RetentionCandidate{
				{JobID: testJobID, RunNumber: 1, CompletedAt: now},
				{JobID: testJobID, RunNumber: 1, CompletedAt: now},
			},
			policy: unlimitedRetentionPolicy(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := PlanRetention(now, test.candidates, test.policy); err == nil {
				t.Fatal("PlanRetention() error = nil, want invalid input")
			}
		})
	}
}

func unlimitedRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		MaxAge:         UnlimitedRetentionAge(),
		MaxJobs:        UnlimitedRetentionLimit(),
		MaxRunsPerJob:  UnlimitedRetentionLimit(),
		MaxBytesPerJob: UnlimitedRetentionLimit(),
		MaxTotalBytes:  UnlimitedRetentionLimit(),
	}
}

func withRetentionAge(policy RetentionPolicy, maximum time.Duration) RetentionPolicy {
	policy.MaxAge = RetentionAgeLimit{Maximum: maximum}

	return policy
}

func withRunLimit(policy RetentionPolicy, maximum uint64) RetentionPolicy {
	policy.MaxRunsPerJob = RetentionLimit{Maximum: maximum}

	return policy
}

func withJobByteLimit(policy RetentionPolicy, maximum uint64) RetentionPolicy {
	policy.MaxBytesPerJob = RetentionLimit{Maximum: maximum}

	return policy
}

func withTotalByteLimit(policy RetentionPolicy, maximum uint64) RetentionPolicy {
	policy.MaxTotalBytes = RetentionLimit{Maximum: maximum}

	return policy
}

func withJobLimit(policy RetentionPolicy, maximum uint64) RetentionPolicy {
	policy.MaxJobs = RetentionLimit{Maximum: maximum}

	return policy
}
