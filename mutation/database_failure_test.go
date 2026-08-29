package mutation

import (
	"errors"
	"testing"
)

func TestConstraintErrorClassifiesPortableUniqueFailures(t *testing.T) {
	for _, message := range []string{"UNIQUE constraint failed", "duplicate entry", "duplicate key", "SQLSTATE 23505", "Error 1062", "constraint failed (2067)"} {
		cause := errors.New(message)
		err := ConstraintError(cause, "record", "one", MutationConflictUnique)
		if !IsMutationConflict(err, MutationConflictUnique) || !errors.Is(err, cause) {
			t.Fatalf("message=%q error=%v", message, err)
		}
	}
	plain := errors.New("network failure")
	if ConstraintError(plain, "record", "one", MutationConflictUnique) != plain || ConstraintError(nil, "", "", "") != nil {
		t.Fatal("unknown constraint failure must pass through")
	}
}

func TestTransactionErrorClassifiesPortableTransientFailures(t *testing.T) {
	tests := []struct {
		message string
		kind    TransactionTransientKind
	}{
		{"deadlock detected SQLSTATE 40P01", TransactionTransientDeadlock},
		{"could not serialize access SQLSTATE 40001", TransactionTransientSerializationFailure},
		{"database is locked SQLITE_BUSY", TransactionTransientLockTimeout},
		{"lock wait timeout exceeded Error 1205", TransactionTransientLockTimeout},
	}
	for _, test := range tests {
		cause := errors.New(test.message)
		err := TransactionError(cause, "record", "one")
		if !IsTransactionTransient(err, test.kind) || !errors.Is(err, cause) {
			t.Fatalf("message=%q error=%v", test.message, err)
		}
	}
	existing := TransactionTransient("record", "one", TransactionTransientDeadlock, errors.New("cause"))
	if TransactionError(existing, "other", "two") != existing {
		t.Fatal("existing typed failure must be preserved")
	}
}
