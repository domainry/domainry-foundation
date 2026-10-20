package apperror

import (
	"errors"
	"strings"
	"testing"
)

func TestNewCapturesStackOnlyForUnclassifiedInternalCause(t *testing.T) {
	raw := errors.New("secret database detail")
	internal := New(KindInternal, "backend.database.query_failed", raw, nil)
	stack := StackOf(internal)
	if !strings.Contains(stack, "TestNewCapturesStackOnlyForUnclassifiedInternalCause") {
		t.Fatalf("internal source stack=%q", stack)
	}
	if strings.Contains(stack, raw.Error()) {
		t.Fatalf("raw error leaked into stack=%q", stack)
	}

	expected := New(KindBadRequest, "backend.request.invalid", raw, nil)
	if stack := StackOf(expected); stack != "" {
		t.Fatalf("expected error stack=%q", stack)
	}
}

func TestNewPreservesExistingInternalStack(t *testing.T) {
	first := New(KindInternal, "backend.storage.failed", errors.New("storage detail"), nil)
	wrapped := New(KindInternal, "backend.operation.failed", first, nil)
	if StackOf(wrapped) == "" || StackOf(wrapped) != StackOf(first) {
		t.Fatalf("first=%q wrapped=%q", StackOf(first), StackOf(wrapped))
	}
}
