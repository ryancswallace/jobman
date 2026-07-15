package logstore

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// RetentionLimit expresses either a finite nonnegative maximum or an explicit
// unlimited setting. Its zero value is a finite maximum of zero.
type RetentionLimit struct {
	Maximum   uint64
	Unlimited bool
}

// UnlimitedRetentionLimit constructs an explicit unlimited count/byte limit.
func UnlimitedRetentionLimit() RetentionLimit {
	return RetentionLimit{Unlimited: true}
}

// RetentionAgeLimit expresses either a finite nonnegative age or an explicit
// unlimited setting. Its zero value makes every completed candidate age-eligible.
type RetentionAgeLimit struct {
	Maximum   time.Duration
	Unlimited bool
}

// UnlimitedRetentionAge constructs an explicit unlimited age limit.
func UnlimitedRetentionAge() RetentionAgeLimit {
	return RetentionAgeLimit{Unlimited: true}
}

// RetentionPolicy applies all finite limits cumulatively. Planning always
// returns candidates in oldest-first order.
type RetentionPolicy struct {
	MaxAge         RetentionAgeLimit
	MaxJobs        RetentionLimit
	MaxRunsPerJob  RetentionLimit
	MaxBytesPerJob RetentionLimit
	MaxTotalBytes  RetentionLimit
}

// RetentionCandidate is the filesystem-log metadata required for pure cleanup
// planning. Active candidates count toward limits but are never selected.
type RetentionCandidate struct {
	CompletedAt time.Time
	JobID       string
	RunNumber   uint64
	Bytes       uint64
	Active      bool
}

// PlanRetention deterministically selects completed log sets that must be
// removed to satisfy age, job, run, per-job byte, and total-byte limits.
func PlanRetention(
	now time.Time,
	candidates []RetentionCandidate,
	policy RetentionPolicy,
) ([]RetentionCandidate, error) {
	if policyErr := validateRetentionPolicy(policy); policyErr != nil {
		return nil, policyErr
	}
	ordered := append([]RetentionCandidate(nil), candidates...)
	if candidateErr := validateRetentionCandidates(ordered); candidateErr != nil {
		return nil, candidateErr
	}
	sortRetentionOldestFirst(ordered)

	selected := make(map[retentionKey]struct{}, len(ordered))
	selectExpired(now, ordered, policy.MaxAge, selected)

	byJob := retentionByJob(ordered)
	for _, jobCandidates := range byJob {
		selectToRunLimit(jobCandidates, policy.MaxRunsPerJob, selected)
		if byteErr := selectToByteLimit(jobCandidates, policy.MaxBytesPerJob, selected); byteErr != nil {
			return nil, byteErr
		}
	}

	selectToJobLimit(byJob, policy.MaxJobs, selected)
	if byteErr := selectToByteLimit(ordered, policy.MaxTotalBytes, selected); byteErr != nil {
		return nil, byteErr
	}

	return selectedCandidates(ordered, selected), nil
}

func selectExpired(
	now time.Time,
	candidates []RetentionCandidate,
	limit RetentionAgeLimit,
	selected map[retentionKey]struct{},
) {
	if limit.Unlimited {
		return
	}
	cutoff := now.Add(-limit.Maximum)
	for _, candidate := range candidates {
		if !candidate.Active && !candidate.CompletedAt.After(cutoff) {
			selectRetentionCandidate(candidate, selected)
		}
	}
}

func selectToRunLimit(
	candidates []RetentionCandidate,
	limit RetentionLimit,
	selected map[retentionKey]struct{},
) {
	if limit.Unlimited {
		return
	}
	remaining := retainedCount(candidates, selected)
	for _, candidate := range candidates {
		if remaining <= limit.Maximum {
			return
		}
		if candidate.Active || retentionSelected(candidate, selected) {
			continue
		}
		selectRetentionCandidate(candidate, selected)
		remaining--
	}
}

func selectToByteLimit(
	candidates []RetentionCandidate,
	limit RetentionLimit,
	selected map[retentionKey]struct{},
) error {
	if limit.Unlimited {
		return nil
	}
	remaining, err := retainedBytes(candidates, selected)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if remaining <= limit.Maximum {
			return nil
		}
		if candidate.Active || retentionSelected(candidate, selected) {
			continue
		}
		selectRetentionCandidate(candidate, selected)
		remaining -= candidate.Bytes
	}

	return nil
}

func selectToJobLimit(
	byJob map[string][]RetentionCandidate,
	limit RetentionLimit,
	selected map[retentionKey]struct{},
) {
	if limit.Unlimited {
		return
	}
	jobs := retainedJobs(byJob, selected)
	remaining := uint64(0)
	for range jobs {
		remaining++
	}
	for remaining > limit.Maximum {
		jobID, removable := oldestRemovableJob(jobs)
		if !removable {
			return
		}
		for _, candidate := range jobs[jobID] {
			selectRetentionCandidate(candidate, selected)
		}
		delete(jobs, jobID)
		remaining--
	}
}

func selectRetentionCandidate(candidate RetentionCandidate, selected map[retentionKey]struct{}) {
	if !candidate.Active {
		selected[retentionCandidateKey(candidate)] = struct{}{}
	}
}

func retentionSelected(candidate RetentionCandidate, selected map[retentionKey]struct{}) bool {
	_, exists := selected[retentionCandidateKey(candidate)]

	return exists
}

func retainedCount(candidates []RetentionCandidate, selected map[retentionKey]struct{}) uint64 {
	remaining := uint64(0)
	for _, candidate := range candidates {
		if !retentionSelected(candidate, selected) {
			remaining++
		}
	}

	return remaining
}

func selectedCandidates(
	candidates []RetentionCandidate,
	selected map[retentionKey]struct{},
) []RetentionCandidate {
	result := make([]RetentionCandidate, 0, len(selected))
	for _, candidate := range candidates {
		if retentionSelected(candidate, selected) {
			result = append(result, candidate)
		}
	}

	return result
}

type retentionKey struct {
	jobID     string
	runNumber uint64
}

func retentionCandidateKey(candidate RetentionCandidate) retentionKey {
	return retentionKey{jobID: candidate.JobID, runNumber: candidate.RunNumber}
}

func validateRetentionPolicy(policy RetentionPolicy) error {
	if policy.MaxAge.Maximum < 0 {
		return errors.New("retention maximum age must not be negative")
	}
	if policy.MaxAge.Unlimited && policy.MaxAge.Maximum != 0 {
		return errors.New("unlimited retention age must not also set a maximum")
	}
	limits := []struct {
		name  string
		limit RetentionLimit
	}{
		{name: "jobs", limit: policy.MaxJobs},
		{name: "runs per job", limit: policy.MaxRunsPerJob},
		{name: "bytes per job", limit: policy.MaxBytesPerJob},
		{name: "total bytes", limit: policy.MaxTotalBytes},
	}
	for _, named := range limits {
		if named.limit.Unlimited && named.limit.Maximum != 0 {
			return fmt.Errorf("unlimited retention %s must not also set a maximum", named.name)
		}
	}

	return nil
}

func validateRetentionCandidates(candidates []RetentionCandidate) error {
	seen := make(map[retentionKey]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := validateJobID(candidate.JobID); err != nil {
			return err
		}
		if candidate.RunNumber == 0 {
			return errors.New("retention candidate run number must be positive")
		}
		if !candidate.Active && candidate.CompletedAt.IsZero() {
			return errors.New("completed retention candidate must have a completion time")
		}
		key := retentionCandidateKey(candidate)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate retention candidate %s run %d", candidate.JobID, candidate.RunNumber)
		}
		seen[key] = struct{}{}
	}

	return nil
}

func sortRetentionOldestFirst(candidates []RetentionCandidate) {
	sort.Slice(candidates, func(left, right int) bool {
		leftCandidate := candidates[left]
		rightCandidate := candidates[right]
		if leftCandidate.Active != rightCandidate.Active {
			return !leftCandidate.Active
		}
		if !leftCandidate.CompletedAt.Equal(rightCandidate.CompletedAt) {
			return leftCandidate.CompletedAt.Before(rightCandidate.CompletedAt)
		}
		if leftCandidate.JobID != rightCandidate.JobID {
			return leftCandidate.JobID < rightCandidate.JobID
		}

		return leftCandidate.RunNumber < rightCandidate.RunNumber
	})
}

func retentionByJob(candidates []RetentionCandidate) map[string][]RetentionCandidate {
	result := make(map[string][]RetentionCandidate)
	for _, candidate := range candidates {
		result[candidate.JobID] = append(result[candidate.JobID], candidate)
	}

	return result
}

func retainedBytes(candidates []RetentionCandidate, selected map[retentionKey]struct{}) (uint64, error) {
	var total uint64
	for _, candidate := range candidates {
		if _, removed := selected[retentionCandidateKey(candidate)]; removed {
			continue
		}
		if total > ^uint64(0)-candidate.Bytes {
			return 0, errors.New("retention candidate byte total overflows")
		}
		total += candidate.Bytes
	}

	return total, nil
}

func retainedJobs(
	byJob map[string][]RetentionCandidate,
	selected map[retentionKey]struct{},
) map[string][]RetentionCandidate {
	result := make(map[string][]RetentionCandidate, len(byJob))
	for jobID, candidates := range byJob {
		for _, candidate := range candidates {
			if _, removed := selected[retentionCandidateKey(candidate)]; !removed {
				result[jobID] = append(result[jobID], candidate)
			}
		}
	}

	return result
}

func oldestRemovableJob(jobs map[string][]RetentionCandidate) (string, bool) {
	var selectedID string
	var selectedNewest time.Time
	for jobID, candidates := range jobs {
		removable := true
		var newest time.Time
		for _, candidate := range candidates {
			if candidate.Active {
				removable = false
				break
			}
			if candidate.CompletedAt.After(newest) {
				newest = candidate.CompletedAt
			}
		}
		if !removable {
			continue
		}
		if selectedID == "" || newest.Before(selectedNewest) || newest.Equal(selectedNewest) && jobID < selectedID {
			selectedID = jobID
			selectedNewest = newest
		}
	}

	return selectedID, selectedID != ""
}
