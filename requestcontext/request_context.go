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
type correlationIDKey struct{}
type workspaceIDKey struct{}
type actorIDKey struct{}
type ownerExecutionIDKey struct{}

// NewRequestID returns an opaque correlation ID suitable for logs and HTTP
// propagation.
func NewRequestID() string {
	random := make([]byte, 16)
	_, _ = rand.Read(random)
	return "req_" + hex.EncodeToString(random)
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
	requestID, _ := ctx.Value(requestIDKey{}).(string)
	return strings.TrimSpace(requestID)
}

func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return withString(ctx, correlationIDKey{}, correlationID)
}

func CorrelationID(ctx context.Context) string {
	return stringValue(ctx, correlationIDKey{})
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
