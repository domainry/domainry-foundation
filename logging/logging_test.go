package logging

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewReadsEnvironmentConfiguration(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_ENCODING", "console")
	logger, err := New("runtime-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Sync() }()
	if !logger.Core().Enabled(zapcore.DebugLevel) {
		t.Fatal("debug logging was not enabled")
	}
}

func TestLoggingDefaultsInitializationAndNilErrorFields(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_ENCODING", "")
	logger, err := New(" ")
	if err != nil || !logger.Core().Enabled(zapcore.InfoLevel) {
		t.Fatalf("default logger=%v error=%v", logger, err)
	}
	_ = logger.Sync()
	if fields := StableErrorFields(nil); fields != nil {
		t.Fatalf("nil error fields = %#v", fields)
	}

	previous := zap.L()
	initialized, err := Initialize(" runtime ")
	if err != nil || zap.L() != initialized {
		t.Fatalf("initialized logger=%v error=%v", initialized, err)
	}
	zap.ReplaceGlobals(previous)

	t.Setenv("LOG_LEVEL", "invalid")
	if _, err := Initialize("runtime"); err == nil {
		t.Fatal("invalid global logger configuration accepted")
	}
}

func TestNewRejectsInvalidEnvironmentConfiguration(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	if _, err := New("runtime-test"); err == nil {
		t.Fatal("invalid log level was accepted")
	}
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_ENCODING", "xml")
	if _, err := New("runtime-test"); err == nil {
		t.Fatal("invalid log encoding was accepted")
	}
}

func TestFromContextAddsRequestID(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	ctx := requestcontext.WithRequestID(context.Background(), "req-123")
	ctx = requestcontext.WithCorrelationID(ctx, "corr-123")
	ctx = requestcontext.WithWorkspaceID(ctx, "workspace-a")
	ctx = requestcontext.WithActorID(ctx, "user-a")
	FromContext(ctx).Info("handled")
	entries := observed.All()
	fields := entries[0].ContextMap()
	if len(entries) != 1 || fields["request_id"] != "req-123" || fields["correlation_id"] != "corr-123" || fields["workspace_id"] != "workspace-a" || fields["actor_id"] != "user-a" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestFieldsAreFilteredAndStable(t *testing.T) {
	fields := Fields(map[string]any{" ": "ignored", "z": 1, "empty": "", "a": true})
	if len(fields) != 2 || fields[0].Key != "a" || fields[1].Key != "z" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestSensitiveTokenFieldVariants(t *testing.T) {
	for _, key := range []string{"access_token_value", "refresh_token_value", "api_token_value", "bearer_token_value"} {
		if !sensitiveFieldKey(key) {
			t.Fatalf("sensitive token key accepted: %q", key)
		}
	}
	if sensitiveFieldKey("token_count") {
		t.Fatal("non-sensitive token metadata was rejected")
	}
}

func TestFieldsRedactSensitiveKeysRecursively(t *testing.T) {
	fields := Fields(map[string]any{
		"authorization": "Bearer secret", "payload": map[string]any{"name": "hidden"},
		"details": map[string]any{"status": "failed", "password": "hidden", "nested": []any{map[string]any{"token": "hidden", "code": "stable"}}},
	})
	if len(fields) != 1 || fields[0].Key != "details" {
		t.Fatalf("fields=%#v", fields)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	zap.New(core).Info("redaction", fields...)
	encoded := observed.All()[0].ContextMap()
	text := fmt.Sprint(encoded)
	if strings.Contains(text, "hidden") || strings.Contains(strings.ToLower(text), "password") || !strings.Contains(text, "stable") {
		t.Fatalf("redacted fields=%s", text)
	}
}

func TestLogIdempotencyEmitsCanonicalCorrelationFieldsWithoutRawKey(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	LogIdempotency(context.Background(), idempotency.AuditFacts{WorkspaceID: "workspace-a", Scope: "record.create", Key: "raw-secret-key", Status: "replayed", FencingToken: 9}, "request-a")
	entries := observed.All()
	if len(entries) != 1 || entries[0].Message != "idempotency_receipt" {
		t.Fatalf("entries=%#v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["workspace_id"] != "workspace-a" || fields["idempotency_scope"] != "record.create" || fields["idempotency_status"] != "replayed" || fields["request_id"] != "request-a" || fields["fencing_token"] != int64(9) || fields["idempotency_key_hash"] == "raw-secret-key" {
		t.Fatalf("fields=%#v", fields)
	}
	ctx := requestcontext.WithRequestID(context.Background(), "request-from-context")
	LogIdempotency(ctx, idempotency.AuditFacts{WorkspaceID: "workspace-a", Scope: "record.create", Key: "other-key", Status: "acquired"}, " ")
	entries = observed.All()
	if len(entries) != 2 || entries[1].ContextMap()["request_id"] != "request-from-context" {
		t.Fatalf("context request fields=%#v", entries)
	}
}

func TestLogIdempotencyDefaultsRequestIDFromContext(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	ctx := requestcontext.WithRequestID(context.Background(), "request-from-context")
	LogIdempotency(ctx, idempotency.AuditFacts{WorkspaceID: "workspace", Scope: "record.create", Key: "key", Status: "acquired"}, " ")
	fields := observed.All()[0].ContextMap()
	if fields["request_id"] != "request-from-context" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestStableErrorFieldsExcludeRawErrorText(t *testing.T) {
	fields := StableErrorFields(fmt.Errorf("password=secret sql=SELECT * FROM users"))
	core, observed := observer.New(zapcore.ErrorLevel)
	zap.New(core).Error("failed", fields...)
	text := fmt.Sprint(observed.All()[0].ContextMap())
	if strings.Contains(text, "secret") || strings.Contains(text, "SELECT") || !strings.Contains(text, "backend.internal") {
		t.Fatalf("unstable error fields=%s", text)
	}
}
