package domain

import (
	"errors"
	"fmt"
)

var (
	ErrValidation        = errors.New("validation failed")
	ErrUnauthorized      = errors.New("authentication required")
	ErrForbidden         = errors.New("operation forbidden")
	ErrNotFound          = errors.New("resource not found")
	ErrConflict          = errors.New("resource conflict")
	ErrInvalidTransition = errors.New("invalid state transition")
	ErrCapacityExceeded  = errors.New("capacity exceeded")
	ErrLeaseLost         = errors.New("resource lease lost")
	ErrDependency        = errors.New("dependency unavailable")
	ErrExpired           = errors.New("resource expired")
)

type FieldViolation struct {
	Field string
	Rule  string
}

type ValidationError struct {
	Operation  string
	Violations []FieldViolation
}

func (e *ValidationError) Error() string {
	if len(e.Violations) == 0 {
		return fmt.Sprintf("%s: %v", e.Operation, ErrValidation)
	}
	first := e.Violations[0]
	return fmt.Sprintf("%s: %s %s", e.Operation, first.Field, first.Rule)
}

func (e *ValidationError) Unwrap() error { return ErrValidation }

func NewValidationError(operation string, violations ...FieldViolation) error {
	return &ValidationError{Operation: operation, Violations: violations}
}

type ConflictError struct {
	Resource string
	Key      string
	Cause    error
}

func (e *ConflictError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s: %v", e.Resource, e.Cause)
	}
	return fmt.Sprintf("%s %q: %v", e.Resource, e.Key, e.Cause)
}

func (e *ConflictError) Unwrap() error {
	if e.Cause != nil {
		return errors.Join(ErrConflict, e.Cause)
	}
	return ErrConflict
}

type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q: %v", e.Resource, e.ID, ErrNotFound)
}

func (e *NotFoundError) Unwrap() error { return ErrNotFound }

type TransitionError struct {
	Entity string
	From   string
	To     string
	Reason string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s cannot move from %s to %s: %s", e.Entity, e.From, e.To, e.Reason)
}

func (e *TransitionError) Unwrap() error { return ErrInvalidTransition }

type LeaseError struct {
	Resource   string
	Owner      string
	Generation int64
}

func (e *LeaseError) Error() string {
	return fmt.Sprintf("%s lease for %s at generation %d: %v", e.Resource, e.Owner, e.Generation, ErrLeaseLost)
}

func (e *LeaseError) Unwrap() error { return ErrLeaseLost }

func IsClientError(err error) bool {
	return errors.Is(err, ErrValidation) ||
		errors.Is(err, ErrUnauthorized) ||
		errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrConflict) ||
		errors.Is(err, ErrInvalidTransition) ||
		errors.Is(err, ErrCapacityExceeded) ||
		errors.Is(err, ErrExpired)
}
