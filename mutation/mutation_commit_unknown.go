package mutation

import (
	"errors"
	"fmt"
	"strings"
)

const TransactionCommitUnknownCode = "backend.transaction.commit_unknown"

// TransactionCommitUnknownError means the database client lost proof of the
// final commit outcome. Callers must reconcile through the same idempotency
// identity instead of assuming either commit or rollback.
type TransactionCommitUnknownError struct {
	Resource   string
	Identifier string
	Cause      error
}

func (e *TransactionCommitUnknownError) Error() string {
	return fmt.Sprintf("transaction commit outcome unknown: resource=%s identifier=%s", e.Resource, e.Identifier)
}

func (e *TransactionCommitUnknownError) Unwrap() error { return e.Cause }

func TransactionCommitUnknown(resource, identifier string, cause error) error {
	return &TransactionCommitUnknownError{Resource: strings.TrimSpace(resource), Identifier: strings.TrimSpace(identifier), Cause: cause}
}

func IsTransactionCommitUnknown(err error) bool {
	var unknown *TransactionCommitUnknownError
	return errors.As(err, &unknown)
}
