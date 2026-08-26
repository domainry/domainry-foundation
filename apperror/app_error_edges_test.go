package apperror

import (
	"errors"
	"testing"
)

type invalidCodedTestError struct{}

func (invalidCodedTestError) Error() string                  { return "invalid coded" }
func (invalidCodedTestError) ErrorCode() string              { return "not a code" }
func (invalidCodedTestError) ErrorParams() map[string]string { return nil }

func TestAppErrorFallbacksAndCodedDetailEdges(t *testing.T) {
	cause := errors.New("cause")
	if message := (&AppError{Err: cause}).Error(); message != "cause" {
		t.Fatalf("cause message = %q", message)
	}
	if message := (&AppError{Code: "backend.test"}).Error(); message != "backend.test" {
		t.Fatalf("coded message = %q", message)
	}
	if params := (&AppError{}).ErrorParams(); params != nil {
		t.Fatalf("empty app params = %#v", params)
	}
	if params := (&CodedError{}).ErrorParams(); params != nil {
		t.Fatalf("empty coded params = %#v", params)
	}
	coded := &CodedError{Code: " backend.test ", Params: map[string]string{"field": "name"}}
	if coded.Error() != " backend.test " || coded.ErrorCode() != "backend.test" || coded.ErrorParams()["field"] != "name" {
		t.Fatalf("coded error = %#v", coded)
	}
	if wrapped := FromError(KindBadRequest, nil); KindOf(wrapped) != KindBadRequest || CodeOf(wrapped) != DefaultCode(KindBadRequest) {
		t.Fatalf("nil wrapped error = %v", wrapped)
	}
	if message := (&AppError{Kind: KindUnavailable}).Error(); message != string(KindUnavailable) {
		t.Fatalf("kind message = %q", message)
	}
	if params := ParamsOf(errors.New("plain")); params != nil {
		t.Fatalf("plain params = %#v", params)
	}
	if code, params, ok := codedDetails(nil); ok || code != "" || params != nil {
		t.Fatalf("nil coded details = %q %#v %v", code, params, ok)
	}
	if code, params, ok := codedDetails(invalidCodedTestError{}); ok || code != "" || params != nil {
		t.Fatalf("invalid coded details = %q %#v %v", code, params, ok)
	}
	if code, params, ok := codedDetails(errors.New("plain")); ok || code != "" || params != nil {
		t.Fatalf("plain coded details = %q %#v %v", code, params, ok)
	}
	if wrapped := FromError(KindUnavailable, invalidCodedTestError{}); CodeOf(wrapped) != DefaultCode(KindUnavailable) {
		t.Fatalf("invalid coded error = %v", wrapped)
	}
}

func TestI18nCodeCharacterAndDefaultKindMatrix(t *testing.T) {
	for _, code := range []string{"backend.lower", "backend.UPPER", "backend.123", "backend-name.code", "backend_name.code", "backend.-", "backend._"} {
		if !IsI18nCode(code) {
			t.Fatalf("valid code rejected: %q", code)
		}
	}
	for _, code := range []string{"backend.bad code", "backend.{"} {
		if IsI18nCode(code) {
			t.Fatalf("invalid code accepted: %q", code)
		}
	}
	for kind, want := range map[ErrorKind]string{
		KindBadRequest: "backend.bad_request",
		KindForbidden:  "backend.forbidden",
		KindNotFound:   "backend.not_found",
		KindConflict:   "backend.conflict",
		KindInternal:   "backend.internal",
	} {
		if got := DefaultCode(kind); got != want {
			t.Fatalf("kind %q code=%q want=%q", kind, got, want)
		}
	}
	if ValueOrDefault(" value ", "fallback") != "value" || ValueOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("value fallback normalization changed")
	}
	if cloned := cloneParams(nil); cloned != nil {
		t.Fatalf("nil params clone = %#v", cloned)
	}
}
