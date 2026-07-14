// Package model defines Jobman's persisted domain model and lifecycle rules.
package model

import (
	"errors"
	"fmt"
	"strings"
)

// ValidationError reports one invalid model field.
type ValidationError struct {
	Field  string
	Reason string
}

// Error implements error.
func (err *ValidationError) Error() string {
	if err.Field == "" {
		return "invalid model value: " + err.Reason
	}

	return fmt.Sprintf("invalid %s: %s", err.Field, err.Reason)
}

// ConflictError reports that an operation is invalid for the current state.
type ConflictError struct {
	Entity    EntityKind
	ID        string
	Operation string
	Actual    string
	Allowed   []string
}

// Error implements error.
func (err *ConflictError) Error() string {
	entity := string(err.Entity)
	if entity == "" {
		entity = "entity"
	}

	identity := ""
	if err.ID != "" {
		identity = " " + err.ID
	}

	expected := ""
	if len(err.Allowed) != 0 {
		expected = "; expected " + strings.Join(err.Allowed, ", ")
	}

	return fmt.Sprintf(
		"cannot %s %s%s in state %s%s",
		err.Operation,
		entity,
		identity,
		err.Actual,
		expected,
	)
}

// IsConflict reports whether err contains a lifecycle conflict.
func IsConflict(err error) bool {
	var conflict *ConflictError

	return errors.As(err, &conflict)
}

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}
