package mutation

import (
	"errors"
	"fmt"
	"strings"
)

type TransactionTransientKind string

const (
	TransactionTransientDeadlock             TransactionTransientKind = "deadlock"
	TransactionTransientLockTimeout          TransactionTransientKind = "lock_timeout"
	TransactionTransientSerializationFailure TransactionTransientKind = "serialization_failure"
)

type TransactionTransientError struct {
	Kind       TransactionTransientKind
	Resource   string
	Identifier string
	Cause      error
}

func (e *TransactionTransientError) Error() string {
	return fmt.Sprintf("transaction transient failure: kind=%s resource=%s identifier=%s", e.Kind, e.Resource, e.Identifier)
}

func (e *TransactionTransientError) Unwrap() error { return e.Cause }

func IsTransactionTransient(err error, kind TransactionTransientKind) bool {
	var transient *TransactionTransientError
	return errors.As(err, &transient) && (kind == "" || transient.Kind == kind)
}

func TransactionTransient(resource, identifier string, kind TransactionTransientKind, cause error) error {
	return &TransactionTransientError{Kind: kind, Resource: strings.TrimSpace(resource), Identifier: strings.TrimSpace(identifier), Cause: cause}
}

func StableTransientCode(kind TransactionTransientKind) string {
	switch kind {
	case TransactionTransientDeadlock:
		return "backend.transaction.deadlock"
	case TransactionTransientLockTimeout:
		return "backend.transaction.lock_timeout"
	case TransactionTransientSerializationFailure:
		return "backend.transaction.serialization_failure"
	default:
		return "backend.transaction.transient_failure"
	}
}
