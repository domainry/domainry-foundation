package mutation

import (
	"errors"
	"fmt"
	"strings"
)

type MutationConflictKind string

const (
	MutationConflictOptimistic  MutationConflictKind = "optimistic"
	MutationConflictUnique      MutationConflictKind = "unique"
	MutationConflictIdempotency MutationConflictKind = "idempotency"
	MutationConflictLeaseLost   MutationConflictKind = "lease_lost"
)

type MutationConflictError struct {
	Kind       MutationConflictKind
	Resource   string
	Identifier string
	Cause      error
}

type PolicyConflictError struct {
	Code       string
	Resource   string
	Identifier string
	Field      string
}

func (e *PolicyConflictError) Error() string {
	return fmt.Sprintf("policy conflict: code=%s resource=%s identifier=%s field=%s", e.Code, e.Resource, e.Identifier, e.Field)
}

func PolicyConflict(code, resource, identifier, field string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "backend.mutation.predicate_failed"
	}
	return &PolicyConflictError{Code: code, Resource: strings.TrimSpace(resource), Identifier: strings.TrimSpace(identifier), Field: strings.TrimSpace(field)}
}

func (e *MutationConflictError) Error() string {
	return fmt.Sprintf("mutation conflict: kind=%s resource=%s identifier=%s", e.Kind, e.Resource, e.Identifier)
}

func (e *MutationConflictError) Unwrap() error { return e.Cause }

func IsMutationConflict(err error, kind MutationConflictKind) bool {
	var conflict *MutationConflictError
	return errors.As(err, &conflict) && (kind == "" || conflict.Kind == kind)
}

func MutationConflict(resource, identifier string, kind MutationConflictKind, cause error) error {
	return &MutationConflictError{Kind: kind, Resource: strings.TrimSpace(resource), Identifier: strings.TrimSpace(identifier), Cause: cause}
}

func StableConflictCode(kind MutationConflictKind) string {
	switch kind {
	case MutationConflictUnique:
		return "backend.mutation.unique_conflict"
	case MutationConflictIdempotency:
		return "backend.mutation.idempotency_conflict"
	case MutationConflictOptimistic:
		return "backend.mutation.optimistic_conflict"
	case MutationConflictLeaseLost:
		return "backend.mutation.lease_lost"
	default:
		return "backend.mutation.conflict"
	}
}
