package definition

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestErrorClassificationContract(t *testing.T) {
	tests := []struct {
		status int
		kind   apperror.ErrorKind
	}{
		{400, apperror.KindBadRequest},
		{403, apperror.KindForbidden},
		{404, apperror.KindNotFound},
		{409, apperror.KindConflict},
		{429, apperror.KindRateLimited},
		{503, apperror.KindUnavailable},
		{500, apperror.KindInternal},
	}
	for _, test := range tests {
		err := &Error{StatusCode: test.status, Code: "metadata.definition_failure"}
		if got := apperror.KindOf(err); got != test.kind {
			t.Fatalf("status=%d kind=%q want=%q", test.status, got, test.kind)
		}
		if got := apperror.CodeOf(err); got != "metadata.definition_failure" {
			t.Fatalf("status=%d code=%q", test.status, got)
		}
	}
}
