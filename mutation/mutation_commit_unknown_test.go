package mutation

import (
	"errors"
	"testing"
)

func TestTransactionCommitUnknownPreservesIdentityAndCause(t *testing.T) {
	cause := errors.New("connection lost after commit write")
	err := TransactionCommitUnknown(" action ", " execution-1 ", cause)
	var unknown *TransactionCommitUnknownError
	if !errors.As(err, &unknown) || unknown.Resource != "action" || unknown.Identifier != "execution-1" || !errors.Is(err, cause) || !IsTransactionCommitUnknown(err) {
		t.Fatalf("unknown commit=%+v error=%v", unknown, err)
	}
	if unknown.Error() == "" {
		t.Fatal("unknown commit error message is empty")
	}
	if IsTransactionCommitUnknown(errors.New("ordinary failure")) || TransactionCommitUnknownCode != "backend.transaction.commit_unknown" {
		t.Fatal("unknown commit classification is not closed")
	}
}
