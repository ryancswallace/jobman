package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

const (
	// DefaultListLimit bounds an unconfigured list query.
	DefaultListLimit = 100
	// MaximumListLimit bounds memory and query work for one list operation.
	MaximumListLimit = 1000
	minimumIDPrefix  = 8
)

// ListJobsOptions filters and bounds a job listing.
type ListJobsOptions struct {
	Phase           model.JobPhase
	Outcome         model.JobOutcome
	Name            string
	Group           string
	SubmittedAfter  time.Time
	SubmittedBefore time.Time
	Active          bool
	Completed       bool
	Cursor          *JobListCursor
	Limit           int
}

// JobListCursor identifies the last row of a previous newest-first listing.
// It permits stable keyset pagination without an increasingly expensive SQL
// offset or gaps when lifecycle fields change between pages.
type JobListCursor struct {
	SubmittedAt time.Time
	ID          model.JobID
}

// GetJob returns one validated job snapshot by canonical ID.
func (s *Store) GetJob(ctx context.Context, id model.JobID) (model.JobState, error) {
	if !id.Valid() {
		return model.JobState{}, errors.New("get job: invalid job ID")
	}

	return getJobWithQueryer(ctx, s.db, id)
}

// GetRun returns one validated run snapshot by canonical ID.
func (s *Store) GetRun(ctx context.Context, id model.RunID) (model.RunState, error) {
	if !id.Valid() {
		return model.RunState{}, errors.New("get run: invalid run ID")
	}

	return getRunWithQueryer(ctx, s.db, id)
}

// GetSupervisor returns one validated ownership snapshot by canonical ID.
func (s *Store) GetSupervisor(ctx context.Context, id model.SupervisorID) (model.SupervisorState, error) {
	if !id.Valid() {
		return model.SupervisorState{}, errors.New("get supervisor: invalid supervisor ID")
	}

	return getSupervisorWithQueryer(ctx, s.db, id)
}

// GetSupervisorForJob returns the supervisor snapshot currently linked to a
// job, including a released supervisor retained for history.
func (s *Store) GetSupervisorForJob(ctx context.Context, jobID model.JobID) (model.SupervisorState, error) {
	if !jobID.Valid() {
		return model.SupervisorState{}, errors.New("get supervisor for job: invalid job ID")
	}

	state, err := scanSupervisor(s.db.QueryRowContext(
		ctx,
		"SELECT "+supervisorColumns+" FROM supervisors WHERE job_id = ?",
		jobID.String(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return model.SupervisorState{}, fmt.Errorf("get supervisor for job %s: %w", jobID, ErrNotFound)
	}
	if err != nil {
		return model.SupervisorState{}, fmt.Errorf("get supervisor for job %s: %w", jobID, err)
	}

	return state, nil
}

// ListJobs returns validated job snapshots ordered newest first.
//
//nolint:cyclop // Independent bounded filters and cursor validation remain explicit at the query boundary.
func (s *Store) ListJobs(ctx context.Context, options ListJobsOptions) ([]model.JobState, error) {
	limit := options.Limit
	if limit == 0 {
		limit = DefaultListLimit
	}
	if limit < 1 || limit > MaximumListLimit {
		return nil, fmt.Errorf("list jobs: limit must be between 1 and %d", MaximumListLimit)
	}
	if options.Phase != "" && !options.Phase.Valid() {
		return nil, errors.New("list jobs: invalid phase")
	}
	if options.Outcome != "" && !options.Outcome.Valid() {
		return nil, errors.New("list jobs: invalid outcome")
	}
	if options.Active && options.Completed {
		return nil, errors.New("list jobs: active and completed filters are mutually exclusive")
	}
	cursorSet := options.Cursor != nil
	cursorTime := int64(0)
	cursorID := ""
	if cursorSet {
		if options.Cursor.SubmittedAt.IsZero() || !options.Cursor.ID.Valid() {
			return nil, errors.New("list jobs: invalid cursor")
		}
		cursorTime = boundedUnixNano(options.Cursor.SubmittedAt)
		cursorID = options.Cursor.ID.String()
	}
	afterSet := !options.SubmittedAfter.IsZero()
	beforeSet := !options.SubmittedBefore.IsZero()
	after := boundedUnixNano(options.SubmittedAfter)
	before := boundedUnixNano(options.SubmittedBefore)

	rows, err := s.db.QueryContext(
		ctx,
		"SELECT "+jobColumns+` FROM jobs
		 WHERE (? = '' OR phase = ?)
		   AND (? = '' OR outcome = ?)
		   AND (? = '' OR name = ?)
		   AND (NOT ? OR phase != 'completed')
		   AND (NOT ? OR phase = 'completed')
		   AND (NOT ? OR submitted_at_ns > ?)
		   AND (NOT ? OR submitted_at_ns < ?)
		   AND (? = '' OR EXISTS (
		       SELECT 1
		       FROM json_each(jobs.spec_json, '$.execution_policy.groups')
		       WHERE json_each.value = ?
		   ))
		   AND (NOT ? OR submitted_at_ns < ? OR (submitted_at_ns = ? AND id < ?))
		 ORDER BY submitted_at_ns DESC, id DESC
		 LIMIT ?`,
		string(options.Phase),
		string(options.Phase),
		string(options.Outcome),
		string(options.Outcome),
		options.Name,
		options.Name,
		options.Active,
		options.Completed,
		afterSet,
		after,
		beforeSet,
		before,
		options.Group,
		options.Group,
		cursorSet,
		cursorTime,
		cursorTime,
		cursorID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", classifySQLite("list jobs", err))
	}
	defer rows.Close()

	jobs := make([]model.JobState, 0)
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list jobs: decode snapshot: %w", scanErr)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list jobs: iterate snapshots: %w", err)
	}

	return jobs, nil
}

func boundedUnixNano(value time.Time) int64 {
	if value.IsZero() || value.Before(time.Unix(0, 0)) {
		return -1
	}
	maximum := time.Unix(0, math.MaxInt64)
	if value.After(maximum) {
		return math.MaxInt64
	}

	return value.UnixNano()
}

// ListRuns returns every retained run for a job in ascending run-number order.
func (s *Store) ListRuns(ctx context.Context, jobID model.JobID) ([]model.RunState, error) {
	if !jobID.Valid() {
		return nil, errors.New("list runs: invalid job ID")
	}

	return listRunsWithQueryer(ctx, s.db, jobID)
}

func listRunsWithQueryer(
	ctx context.Context,
	queryer schemaQueryer,
	jobID model.JobID,
) ([]model.RunState, error) {
	rows, err := queryer.QueryContext(
		ctx,
		"SELECT "+runColumns+" FROM runs WHERE job_id = ? ORDER BY run_number",
		jobID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("list runs for job %s: %w", jobID, classifySQLite("list runs", err))
	}
	defer rows.Close()

	runs := make([]model.RunState, 0)
	for rows.Next() {
		run, scanErr := scanRun(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list runs for job %s: decode snapshot: %w", jobID, scanErr)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list runs for job %s: iterate snapshots: %w", jobID, err)
	}

	return runs, nil
}

// ResolveJob resolves exact ID, unique ID prefix, then exact display name.
func (s *Store) ResolveJob(ctx context.Context, selector string) (model.JobState, error) {
	return resolveJobWithQueryer(ctx, s.db, selector)
}

// GetJobWithRuns resolves a selector and returns its job and complete run
// history from one consistent SQLite read transaction.
func (s *Store) GetJobWithRuns(
	ctx context.Context,
	selector string,
) (job model.JobState, runs []model.RunState, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.JobState{}, nil, fmt.Errorf("begin job inspection: %w", classifySQLite("begin job inspection", err))
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback job inspection: %w", rollbackErr))
		}
	}()

	job, err = resolveJobWithQueryer(ctx, tx, selector)
	if err != nil {
		return model.JobState{}, nil, err
	}
	runs, err = listRunsWithQueryer(ctx, tx, job.ID)
	if err != nil {
		return model.JobState{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return model.JobState{}, nil, fmt.Errorf("commit job inspection: %w", err)
	}

	return job, runs, nil
}

func resolveJobWithQueryer(
	ctx context.Context,
	queryer schemaQueryer,
	selector string,
) (model.JobState, error) {
	if selector == "" {
		return model.JobState{}, errors.New("resolve job: selector is empty")
	}

	if id, err := model.ParseJobID(selector); err == nil {
		job, getErr := getJobWithQueryer(ctx, queryer, id)
		if getErr == nil {
			return job, nil
		}
		if !errors.Is(getErr, ErrNotFound) {
			return model.JobState{}, getErr
		}
	}

	if len(selector) >= minimumIDPrefix && validIDPrefix(selector) {
		jobs, err := jobsMatching(ctx, queryer, "substr(id, 1, length(?)) = ?", selector, selector)
		if err != nil {
			return model.JobState{}, err
		}
		switch len(jobs) {
		case 1:
			return jobs[0], nil
		case 2:
			return model.JobState{}, fmt.Errorf("resolve job %q: %w", selector, ErrAmbiguous)
		}
	}

	jobs, err := jobsMatching(ctx, queryer, "name = ?", selector)
	if err != nil {
		return model.JobState{}, err
	}
	switch len(jobs) {
	case 0:
		return model.JobState{}, fmt.Errorf("resolve job %q: %w", selector, ErrNotFound)
	case 1:
		return jobs[0], nil
	default:
		return model.JobState{}, fmt.Errorf("resolve job %q: %w", selector, ErrAmbiguous)
	}
}

func jobsMatching(
	ctx context.Context,
	queryer schemaQueryer,
	predicate string,
	arguments ...any,
) ([]model.JobState, error) {
	// predicate is selected exclusively from fixed literals in ResolveJob.
	query := "SELECT " + jobColumns + " FROM jobs WHERE " + predicate + " ORDER BY submitted_at_ns DESC, id DESC LIMIT 2" // #nosec G202
	rows, err := queryer.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("resolve job: %w", classifySQLite("resolve job", err))
	}
	defer rows.Close()

	jobs := make([]model.JobState, 0, 2)
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("resolve job: decode snapshot: %w", scanErr)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("resolve job: iterate snapshots: %w", err)
	}

	return jobs, nil
}

func validIDPrefix(value string) bool {
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}

			continue
		}
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}

	return len(value) <= 36
}
