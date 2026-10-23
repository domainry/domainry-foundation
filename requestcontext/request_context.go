package requestcontext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type requestIDKey struct{}
type workspaceIDKey struct{}
type actorIDKey struct{}
type ownerExecutionIDKey struct{}

// NewRequestID returns a valid 128-bit trace identity for code paths that do
// not yet have an OpenTelemetry span. At an instrumented boundary RequestID
// always resolves to the active trace ID instead.
func NewRequestID() string {
	random := make([]byte, 16)
	_, _ = rand.Read(random)
	return hex.EncodeToString(random)
}

// NormalizeTraceID returns the canonical lowercase 128-bit trace identity or
// an empty string when the caller supplied an invalid value.
func NormalizeTraceID(value string) string {
	traceID, err := trace.TraceIDFromHex(strings.TrimSpace(value))
	if err != nil || !traceID.IsValid() {
		return ""
	}
	return traceID.String()
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if ctx == nil || requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if traceID := TraceID(ctx); traceID != "" {
		return traceID
	}
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return strings.TrimSpace(requestID)
}

// RequestIDOrNew returns the active trace identity, or creates one when the
// current execution has no valid trace identity yet. Business, execution, and
// idempotency identifiers are deliberately not accepted as fallbacks.
func RequestIDOrNew(ctx context.Context) string {
	if requestID := NormalizeTraceID(RequestID(ctx)); requestID != "" {
		return requestID
	}
	return NewRequestID()
}

func WithWorkspaceID(ctx context.Context, workspaceID string) context.Context {
	return withString(ctx, workspaceIDKey{}, workspaceID)
}

func WorkspaceID(ctx context.Context) string {
	return stringValue(ctx, workspaceIDKey{})
}

func WithActorID(ctx context.Context, actorID string) context.Context {
	return withString(ctx, actorIDKey{}, actorID)
}

func ActorID(ctx context.Context) string {
	return stringValue(ctx, actorIDKey{})
}

func WithOwnerExecutionID(ctx context.Context, executionID string) context.Context {
	return withString(ctx, ownerExecutionIDKey{}, executionID)
}

func OwnerExecutionID(ctx context.Context) string {
	return stringValue(ctx, ownerExecutionIDKey{})
}

func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func SpanID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.SpanID().String()
}

func withString(ctx context.Context, key, value any) context.Context {
	valueText := strings.TrimSpace(fmt.Sprint(value))
	if ctx == nil || valueText == "" {
		return ctx
	}
	return context.WithValue(ctx, key, valueText)
}

func stringValue(ctx context.Context, key any) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(key).(string)
	return strings.TrimSpace(value)
}
