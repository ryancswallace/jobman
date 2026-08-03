package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	ToOutcome  string
	Details    json.RawMessage
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
	event, err := scanTransitionEvent(s.db.QueryRowContext(ctx, `
		SELECT id, run_id, occurred_at_ns, to_outcome, details_json FROM state_events
		WHERE entity_kind = ? AND entity_id = ? AND entity_revision = ?`,
		string(entity), entityID, revision))
	if err != nil {
		return TransitionEvent{}, fmt.Errorf("load transition event: %w", classifySQLite("load transition event", err))
	}

	return event, nil
}

// TransitionEventByID loads the immutable fields needed to reproduce one
// notification payload across delivery retries and recovery.
func (s *Store) TransitionEventByID(ctx context.Context, id model.EventID) (TransitionEvent, error) {
	if !id.Valid() {
		return TransitionEvent{}, errors.New("load transition event: invalid event ID")
	}
	event, err := scanTransitionEvent(s.db.QueryRowContext(ctx, `
		SELECT id, run_id, occurred_at_ns, to_outcome, details_json FROM state_events
		WHERE id = ?`, id.String()))
	if err != nil {
		return TransitionEvent{}, fmt.Errorf("load transition event: %w", classifySQLite("load transition event", err))
	}

	return event, nil
}

func scanTransitionEvent(row *sql.Row) (TransitionEvent, error) {
	var encoded, details string
	var runIDText, outcome sql.NullString
	var occurredAt int64
	if err := row.Scan(&encoded, &runIDText, &occurredAt, &outcome, &details); err != nil {
		return TransitionEvent{}, err
	}
	id, err := model.ParseEventID(encoded)
	if err != nil {
		return TransitionEvent{}, fmt.Errorf("parse ID: %w", err)
	}
	var runID model.RunID
	if runIDText.Valid {
		runID, err = model.ParseRunID(runIDText.String)
		if err != nil {
			return TransitionEvent{}, fmt.Errorf("parse run ID: %w", err)
		}
	}
	if !json.Valid([]byte(details)) {
		return TransitionEvent{}, errors.New("details are invalid JSON")
	}

	return TransitionEvent{
		ID: id, RunID: runID, OccurredAt: timeFromDatabase(occurredAt),
		ToOutcome: outcome.String, Details: json.RawMessage(details),
	}, nil
}
