package mutation

import (
	"errors"
	"strings"
)

// ConstraintError converts portable unique-constraint signatures into a
// stable mutation conflict. Unknown database failures pass through unchanged.
func ConstraintError(err error, resource, identifier string, kind MutationConflictKind) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if containsAny(message,
		"unique constraint", "duplicate entry", "duplicate key", "primary key constraint",
		"sqlstate 23505", "error 1062", "constraint failed (1555)", "constraint failed (2067)",
	) {
		return MutationConflict(resource, identifier, kind, err)
	}
	return err
}

// TransactionError converts common PostgreSQL, MySQL and SQLite transient
// transaction signatures into stable retry classifications.
func TransactionError(err error, resource, identifier string) error {
	if err == nil {
		return nil
	}
	var conflict *MutationConflictError
	if errors.As(err, &conflict) {
		return err
	}
	var transient *TransactionTransientError
	if errors.As(err, &transient) {
		return err
	}
	message := strings.ToLower(err.Error())
	switch {
	case containsAny(message, "error 1213", "deadlock found", "sqlstate 40p01", "deadlock detected"):
		return TransactionTransient(resource, identifier, TransactionTransientDeadlock, err)
	case containsAny(message, "sqlstate 40001", "serialization failure", "could not serialize access"):
		return TransactionTransient(resource, identifier, TransactionTransientSerializationFailure, err)
	case containsAny(message, "database is locked", "database table is locked", "database is busy", "sqlite_busy", "error 1205", "lock wait timeout exceeded", "sqlstate 55p03", "lock timeout"):
		return TransactionTransient(resource, identifier, TransactionTransientLockTimeout, err)
	default:
		return err
	}
}

func containsAny(message string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
