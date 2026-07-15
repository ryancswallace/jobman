package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

// TransitionEvent identifies the immutable state event that notification work
// uses as its stable envelope identity.
type TransitionEvent struct {
	ID         model.EventID
	RunID      model.RunID
	OccurredAt time.Time
}

// TransitionEventID returns the durable event ID assigned to one entity
// revision. Notification delivery reuses this identifier as its stable
// idempotency key.
func (s *Store) TransitionEventID(
	ctx context.Context,
	entity model.EntityKind,
	entityID string,
	revision uint64,
) (model.EventID, error) {
	event, err := s.TransitionEvent(ctx, entity, entityID, revision)

	return event.ID, err
}

// TransitionEvent returns stable event envelope fields for one entity
// revision. Reading the occurrence and run from the durable event prevents an
// idempotent notification replay from changing its payload.
func (s *Store) TransitionEvent(
	ctx context.Context,
	entity model.EntityKind,
	entityID string,
	revision uint64,
) (TransitionEvent, error) {
	if !entity.Valid() || entityID == "" || revision == 0 {
		return TransitionEvent{}, errors.New("load transition event: invalid identity")
	}
	var encoded string
	var runIDText sql.NullString
	var occurredAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, run_id, occurred_at_ns FROM state_events
		WHERE entity_kind = ? AND entity_id = ? AND entity_revision = ?`,
		string(entity), entityID, revision).Scan(&encoded, &runIDText, &occurredAt)
	if err != nil {
		return TransitionEvent{}, fmt.Errorf("load transition event: %w", classifySQLite("load transition event", err))
	}
	id, err := model.ParseEventID(encoded)
	if err != nil {
		return TransitionEvent{}, fmt.Errorf("load transition event: parse ID: %w", err)
	}
	var runID model.RunID
	if runIDText.Valid {
		runID, err = model.ParseRunID(runIDText.String)
		if err != nil {
			return TransitionEvent{}, fmt.Errorf("load transition event: parse run ID: %w", err)
		}
	}

	return TransitionEvent{ID: id, RunID: runID, OccurredAt: timeFromDatabase(occurredAt)}, nil
}
