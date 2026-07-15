package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

// MarkRunLogsPruned durably records successful filesystem removal of a
// completed run's captured logs. Repeating the operation is idempotent.
func (s *Store) MarkRunLogsPruned(
	ctx context.Context,
	runID model.RunID,
	prunedAt time.Time,
	removedFiles uint64,
	removedBytes uint64,
) error {
	if !runID.Valid() {
		return errors.New("mark run logs pruned: invalid run ID")
	}
	if prunedAt.IsZero() || prunedAt.UnixNano() < 0 {
		return errors.New("mark run logs pruned: invalid prune time")
	}
	if removedFiles > math.MaxInt64 || removedBytes > math.MaxInt64 {
		return errors.New("mark run logs pruned: removed counts exceed SQLite INTEGER range")
	}
	removedFilesDB := int64(removedFiles)
	removedBytesDB := int64(removedBytes)

	return s.writeTransaction(ctx, "mark run logs pruned", func(tx *sql.Tx) error {
		run, err := getRunWithQueryer(ctx, tx, runID)
		if err != nil {
			return err
		}
		if !run.Logs.Available() {
			return nil
		}
		if run.Phase != model.RunPhaseCompleted || run.CompletedAt == nil {
			return fmt.Errorf("mark logs for active run %s pruned: %w", runID, ErrConflict)
		}
		prunedAt = prunedAt.UTC()
		if prunedAt.Before(*run.CompletedAt) {
			return fmt.Errorf("mark run %s logs pruned before completion: %w", runID, ErrConflict)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO run_log_pruning(run_id, pruned_at_ns, removed_files, removed_bytes)
			VALUES (?, ?, ?, ?)`,
			runID.String(), prunedAt.UnixNano(), removedFilesDB, removedBytesDB,
		); err != nil {
			return fmt.Errorf("mark run %s logs pruned: %w", runID, classifySQLite("mark run logs pruned", err))
		}

		return nil
	})
}
