package requestcontext

import (
	"context"
	"strings"
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

func TestCorrelationScopeAndTraceContextContract(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}))
	ctx = WithCorrelationID(ctx, "corr-1")
	ctx = WithWorkspaceID(ctx, "workspace-a")
	ctx = WithActorID(ctx, "user-a")
	ctx = WithOwnerExecutionID(ctx, "outbox-1")
	if CorrelationID(ctx) != "corr-1" || WorkspaceID(ctx) != "workspace-a" || ActorID(ctx) != "user-a" || OwnerExecutionID(ctx) != "outbox-1" {
		t.Fatalf("unexpected correlation scope")
	}
	if TraceID(ctx) != traceID.String() || SpanID(ctx) != spanID.String() {
		t.Fatalf("trace identity = %s/%s", TraceID(ctx), SpanID(ctx))
	}
}

func TestNewRequestIDIsOpaqueAndUnique(t *testing.T) {
	first, second := NewRequestID(), NewRequestID()
	if !strings.HasPrefix(first, "req_") || first == second {
		t.Fatalf("request ids = %q, %q", first, second)
	}
}
