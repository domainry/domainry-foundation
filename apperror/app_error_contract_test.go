package apperror

import (
	"errors"
	"testing"
)

type codedTestError struct{}

func (codedTestError) Error() string                  { return "coded" }
func (codedTestError) ErrorCode() string              { return "backend.test.failed" }
func (codedTestError) ErrorParams() map[string]string { return map[string]string{"field": "name"} }

type classifiedTestError struct{ kind, code string }

func (e classifiedTestError) Error() string                { return "classified" }
func (e classifiedTestError) ApplicationErrorKind() string { return e.kind }
func (e classifiedTestError) ErrorCode() string            { return e.code }
func (e classifiedTestError) ErrorParams() map[string]string {
	return map[string]string{"source": "sdk"}
}

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

func TestHelpersRecognizeDeploymentNeutralErrorContract(t *testing.T) {
	tests := []struct {
		kind string
		want ErrorKind
	}{
		{"bad_request", KindBadRequest},
		{"invalid", KindBadRequest},
		{"unauthorized", KindForbidden},
		{"forbidden", KindForbidden},
		{"not_found", KindNotFound},
		{"conflict", KindConflict},
		{"rate_limited", KindRateLimited},
		{"unavailable", KindUnavailable},
		{"unknown", KindInternal},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			err := classifiedTestError{kind: test.kind, code: "module.operation_failed"}
			if got := KindOf(err); got != test.want {
				t.Fatalf("kind=%q want=%q", got, test.want)
			}
			if got := CodeOf(err); got != "module.operation_failed" {
				t.Fatalf("code=%q", got)
			}
			if got := ParamsOf(err)["source"]; got != "sdk" {
				t.Fatalf("params=%#v", ParamsOf(err))
			}
		})
	}
	if got := CodeOf(classifiedTestError{kind: "forbidden", code: "not namespaced"}); got != "backend.internal" {
		t.Fatalf("invalid code=%q", got)
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
