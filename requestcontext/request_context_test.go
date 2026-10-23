package requestcontext

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestRequestIDContextContract(t *testing.T) {
	ctx := WithRequestID(t.Context(), " req-123 ")
	if got := RequestID(ctx); got != "req-123" {
		t.Fatalf("request id=%q", got)
	}
	if RequestID(context.WithoutCancel(ctx)) != "req-123" {
		t.Fatal("request id must survive lifecycle detachment")
	}
}

func TestRequestContextNilAndBlankInputs(t *testing.T) {
	if WithRequestID(nil, "request") != nil || WithRequestID(t.Context(), " ") == nil || RequestID(nil) != "" {
		t.Fatal("request ID nil/blank contract changed")
	}
	if withString(nil, workspaceIDKey{}, "workspace") != nil || withString(t.Context(), workspaceIDKey{}, " ") == nil {
		t.Fatal("string context nil/blank contract changed")
	}
	if stringValue(nil, workspaceIDKey{}) != "" {
		t.Fatal("nil string context returned a value")
	}
}

func TestRequestScopeAndTraceContextContract(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}))
	ctx = WithRequestID(ctx, "must-not-diverge-from-trace")
	ctx = WithWorkspaceID(ctx, "workspace-a")
	ctx = WithActorID(ctx, "user-a")
	ctx = WithOwnerExecutionID(ctx, "outbox-1")
	if WorkspaceID(ctx) != "workspace-a" || ActorID(ctx) != "user-a" || OwnerExecutionID(ctx) != "outbox-1" {
		t.Fatalf("unexpected request scope")
	}
	if TraceID(ctx) != traceID.String() || RequestID(ctx) != traceID.String() || SpanID(ctx) != spanID.String() {
		t.Fatalf("request/trace identity = %s/%s/%s", RequestID(ctx), TraceID(ctx), SpanID(ctx))
	}
}

func TestNewRequestIDIsAValidUniqueTraceIdentity(t *testing.T) {
	first, second := NewRequestID(), NewRequestID()
	firstTrace, firstErr := trace.TraceIDFromHex(first)
	secondTrace, secondErr := trace.TraceIDFromHex(second)
	if first == second || firstErr != nil || secondErr != nil || !firstTrace.IsValid() || !secondTrace.IsValid() {
		t.Fatalf("request ids = %q, %q", first, second)
	}
}

func TestNormalizeTraceID(t *testing.T) {
	if got := NormalizeTraceID(" 4bf92f3577b34da6a3ce929d0e0e4736 "); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("normalized trace id=%q", got)
	}
	for _, invalid := range []string{"", "request-1", "4BF92F3577B34DA6A3CE929D0E0E4736", "00000000000000000000000000000000"} {
		if got := NormalizeTraceID(invalid); got != "" {
			t.Fatalf("invalid trace ID %q normalized as %q", invalid, got)
		}
	}
}

func TestRequestIDOrNewUsesOnlyValidTraceIdentity(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	if got := RequestIDOrNew(WithRequestID(t.Context(), traceID)); got != traceID {
		t.Fatalf("request id=%q", got)
	}
	generated := RequestIDOrNew(WithRequestID(t.Context(), "business-operation-1"))
	if generated == "business-operation-1" || NormalizeTraceID(generated) == "" {
		t.Fatalf("generated request id=%q", generated)
	}
}
