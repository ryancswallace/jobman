package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

// PruneCompletedJobMetadata deletes a completed job and its retained history
// once all logs have pruning tombstones and no unresolved dependency or
// notification work still needs the record. Dry-run performs the same locked
// eligibility checks without deleting anything.
func (s *Store) PruneCompletedJobMetadata(
	ctx context.Context,
	jobID model.JobID,
	completedBefore time.Time,
	dryRun bool,
) (eligible bool, returnedErr error) {
	if !jobID.Valid() {
		return false, errors.New("prune completed job metadata: invalid job ID")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("prune completed job metadata: begin: %w", classifySQLite("begin pruning", err))
	}
	defer func() {
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("rollback metadata pruning: %w", rollbackErr))
		}
	}()

	eligible, err = completedJobEligibleForPruning(ctx, tx, jobID, completedBefore)
	if err != nil || !eligible {
		return eligible, err
	}
	blocked, err := metadataPruningBlocked(ctx, tx, jobID, dryRun)
	if err != nil {
		return false, err
	}
	if blocked {
		return false, nil
	}
	if dryRun {
		if err := tx.Rollback(); err != nil {
			return false, fmt.Errorf("finish metadata pruning dry run: %w", err)
		}

		return true, nil
	}

	if err := deleteCompletedJobMetadata(ctx, tx, jobID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit completed job metadata pruning: %w", classifySQLite("commit pruning", err))
	}

	return true, nil
}

func completedJobEligibleForPruning(
	ctx context.Context,
	tx *sql.Tx,
	jobID model.JobID,
	completedBefore time.Time,
) (bool, error) {
	var phase string
	var completedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT phase, completed_at_ns FROM jobs WHERE id = ?`, jobID.String()).
		Scan(&phase, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("prune completed job metadata %s: %w", jobID, ErrNotFound)
	}
	if err != nil {
		return false, fmt.Errorf("prune completed job metadata %s: %w", jobID, err)
	}

	return model.JobPhase(phase) == model.JobPhaseCompleted && completedAt.Valid &&
		completedAt.Int64 <= completedBefore.UTC().UnixNano(), nil
}

type metadataPruningCheck struct {
	query string
	label string
}

func metadataPruningBlocked(ctx context.Context, tx *sql.Tx, jobID model.JobID, dryRun bool) (bool, error) {
	checks := []metadataPruningCheck{
		{`SELECT EXISTS(
			SELECT 1 FROM job_dependencies
			WHERE dependency_job_id = ? AND observed_revision IS NULL)`, "unresolved dependents"},
		{`SELECT EXISTS(
			SELECT 1 FROM notification_deliveries
			WHERE job_id = ? AND status IN ('pending', 'delivering'))`, "pending notifications"},
		{`SELECT EXISTS(
			SELECT 1 FROM admissions WHERE job_id = ? AND released_at_ns IS NULL)`, "active admission"},
	}
	if !dryRun {
		checks = append([]metadataPruningCheck{{`SELECT EXISTS(
			SELECT 1 FROM runs r LEFT JOIN run_log_pruning p ON p.run_id = r.id
			WHERE r.job_id = ? AND p.run_id IS NULL)`, "unpruned logs"}}, checks...)
	}
	for _, check := range checks {
		var blocked int
		if err := tx.QueryRowContext(ctx, check.query, jobID.String()).Scan(&blocked); err != nil {
			return false, fmt.Errorf("inspect %s: %w", check.label, err)
		}
		if blocked != 0 {
			return true, nil
		}
	}

	return false, nil
}

func deleteCompletedJobMetadata(ctx context.Context, tx *sql.Tx, jobID model.JobID) error {
	statements := []string{
		`DELETE FROM notification_deliveries WHERE job_id = ?`,
		`DELETE FROM notification_attempts WHERE job_id = ?`,
		`DELETE FROM job_dependencies WHERE job_id = ? OR dependency_job_id = ?`,
		`DELETE FROM wait_evaluations WHERE job_id = ?`,
		`DELETE FROM job_tags WHERE job_id = ?`,
		`DELETE FROM admission_requests WHERE job_id = ?`,
		`DELETE FROM admissions WHERE job_id = ?`,
		`DELETE FROM job_runtime WHERE job_id = ?`,
		`DELETE FROM state_events WHERE job_id = ?`,
		`DELETE FROM run_log_pruning WHERE run_id IN (SELECT id FROM runs WHERE job_id = ?)`,
		`DELETE FROM runs WHERE job_id = ?`,
		`DELETE FROM supervisors WHERE job_id = ?`,
		`DELETE FROM jobs WHERE id = ?`,
	}
	for _, statement := range statements {
		arguments := []any{jobID.String()}
		if statement == `DELETE FROM job_dependencies WHERE job_id = ? OR dependency_job_id = ?` {
			arguments = append(arguments, jobID.String())
		}
		if _, err := tx.ExecContext(ctx, statement, arguments...); err != nil {
			return fmt.Errorf("delete completed job metadata: %w", classifySQLite("delete metadata", err))
		}
	}

	return nil
}
