package store

import (
	"context"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

// ResetInputEOF starts a fresh per-run EOF scope before a live-input target is
// attached. Clients only observe running targets, so the reset cannot erase an
// EOF accepted for that same run.
func (s *Store) ResetInputEOF(ctx context.Context, jobID model.JobID, at time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE job_runtime SET input_eof_requested = 0,
		revision = revision + 1, updated_at_ns = ? WHERE job_id = ?`,
		at.UTC().UnixNano(), jobID.String())
	if err != nil {
		return classifySQLite("reset live-input EOF", err)
	}

	return requireOneUpdate(result, "job runtime", jobID.String(), 0, "")
}
