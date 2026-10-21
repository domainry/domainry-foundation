package definition

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestErrorClassificationContract(t *testing.T) {
	tests := []struct {
		input apperror.ErrorKind
		kind  apperror.ErrorKind
	}{
		{apperror.KindBadRequest, apperror.KindBadRequest},
		{apperror.KindForbidden, apperror.KindForbidden},
		{apperror.KindNotFound, apperror.KindNotFound},
		{apperror.KindConflict, apperror.KindConflict},
		{apperror.KindRateLimited, apperror.KindRateLimited},
		{apperror.KindUnavailable, apperror.KindUnavailable},
		{apperror.KindInternal, apperror.KindInternal},
	}
	for _, test := range tests {
		err := &Error{Kind: test.input, Code: "metadata.definition_failure"}
		if got := apperror.KindOf(err); got != test.kind {
			t.Fatalf("input=%q kind=%q want=%q", test.input, got, test.kind)
		}
		if got := apperror.CodeOf(err); got != "metadata.definition_failure" {
			t.Fatalf("input=%q code=%q", test.input, got)
		}
	}
}
