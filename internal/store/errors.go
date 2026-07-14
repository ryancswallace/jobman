// Package store persists Jobman metadata in SQLite.
package store

import (
	"errors"
	"fmt"
)

var (
	// ErrAmbiguous is returned when a selector matches more than one job.
	ErrAmbiguous = errors.New("ambiguous job selector")
	// ErrBusy is returned when SQLite remains locked past the configured limit.
	ErrBusy = errors.New("store busy")
	// ErrConflict is returned when an optimistic state update loses a race.
	ErrConflict = errors.New("store revision conflict")
	// ErrNotFound is returned when an entity does not exist.
	ErrNotFound = errors.New("store entity not found")
)

// SchemaError reports an incompatible or internally inconsistent database
// schema.
type SchemaError struct {
	Reason string
}

// Error implements error.
func (e *SchemaError) Error() string {
	return "incompatible Jobman database: " + e.Reason
}

// BusyError records the operation that exceeded SQLite's bounded lock wait.
type BusyError struct {
	Operation string
	Err       error
}

// RevisionConflictError reports a failed compare-and-swap state update.
type RevisionConflictError struct {
	Entity           string
	ID               string
	ExpectedRevision uint64
	ExpectedPhase    string
}

// Error implements error.
func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf(
		"update %s %s at revision %d in phase %s: %v",
		e.Entity,
		e.ID,
		e.ExpectedRevision,
		e.ExpectedPhase,
		ErrConflict,
	)
}

// Unwrap makes RevisionConflictError classifiable as ErrConflict.
func (*RevisionConflictError) Unwrap() error {
	return ErrConflict
}

// Error implements error.
func (e *BusyError) Error() string {
	return fmt.Sprintf("%s: %v", e.Operation, ErrBusy)
}

// Unwrap makes BusyError classifiable as both ErrBusy and the driver error.
func (e *BusyError) Unwrap() []error {
	return []error{ErrBusy, e.Err}
}
