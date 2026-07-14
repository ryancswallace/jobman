package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) writeTransaction(
	ctx context.Context,
	operation string,
	work func(*sql.Tx) error,
) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", operation, classifySQLite("begin "+operation, err))
	}
	defer func() {
		if tx == nil {
			return
		}
		rollbackErr := tx.Rollback()
		if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback %s: %w", operation, rollbackErr))
		}
	}()

	if err := work(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", operation, classifySQLite("commit "+operation, err))
	}
	tx = nil

	return nil
}
