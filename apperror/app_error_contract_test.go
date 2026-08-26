package apperror

import (
	"errors"
	"testing"
)

type codedTestError struct{}

func (codedTestError) Error() string                  { return "coded" }
func (codedTestError) ErrorCode() string              { return "backend.test.failed" }
func (codedTestError) ErrorParams() map[string]string { return map[string]string{"field": "name"} }

func TestHelpersPreserveStructuredErrors(t *testing.T) {
	wrapped := FromError(KindBadRequest, codedTestError{})
	if KindOf(wrapped) != KindBadRequest || CodeOf(wrapped) != "backend.test.failed" || ParamsOf(wrapped)["field"] != "name" {
		t.Fatalf("structured error = kind=%s code=%q params=%#v", KindOf(wrapped), CodeOf(wrapped), ParamsOf(wrapped))
	}
	existing := New(KindConflict, "backend.test.conflict", errors.New("cause"), map[string]string{"id": "1"})
	if FromError(KindBadRequest, existing) != existing || CodeOf(existing) != "backend.test.conflict" {
		t.Fatal("existing application error was replaced")
	}
}

func TestHelpersNormalizeInvalidCodesAndCloneParams(t *testing.T) {
	params := map[string]string{"field": "name"}
	err := New(KindForbidden, "not a code", nil, params)
	params["field"] = "changed"
	if CodeOf(err) != "backend.forbidden" || ParamsOf(err)["field"] != "name" {
		t.Fatalf("normalized error code=%q params=%#v", CodeOf(err), ParamsOf(err))
	}
	if KindOf(errors.New("plain")) != KindInternal || CodeOf(errors.New("plain")) != "backend.internal" {
		t.Fatal("plain error fallback changed")
	}
}
