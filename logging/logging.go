package logging

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultLevel    = "info"
	defaultEncoding = "json"
)

// New creates the process logger. LOG_LEVEL controls the minimum level and
// LOG_ENCODING may be set to "json" or "console".
func New(serviceName string) (*zap.Logger, error) {
	level, err := parseLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		return nil, err
	}
	encoding, err := parseEncoding(os.Getenv("LOG_ENCODING"))
	if err != nil {
		return nil, err
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(level)
	cfg.Encoding = encoding
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	if serviceName = strings.TrimSpace(serviceName); serviceName != "" {
		cfg.InitialFields = map[string]any{"service": serviceName}
	}
	return cfg.Build()
}

// StableErrorFields exports only the normalized kind/code pair. Raw error
// text belongs in protected debugging workflows, never ordinary logs.
func StableErrorFields(err error) []zap.Field {
	if err == nil {
		return nil
	}
	return []zap.Field{
		zap.String("error_kind", string(apperror.KindOf(err))),
		zap.String("error_code", apperror.CodeOf(err)),
	}
}

// Initialize installs the process logger as zap's global logger.
func Initialize(serviceName string) (*zap.Logger, error) {
	logger, err := New(serviceName)
	if err != nil {
		return nil, err
	}
	zap.ReplaceGlobals(logger)
	return logger, nil
}

// FromContext enriches the global logger with request-scoped correlation data.
func FromContext(ctx context.Context) *zap.Logger {
	logger := zap.L()
	fields := make([]zap.Field, 0, 7)
	for _, entry := range []struct{ key, value string }{
		{"request_id", requestcontext.RequestID(ctx)},
		{"trace_id", requestcontext.TraceID(ctx)},
		{"span_id", requestcontext.SpanID(ctx)},
		{"correlation_id", requestcontext.CorrelationID(ctx)},
		{"workspace_id", requestcontext.WorkspaceID(ctx)},
		{"actor_id", requestcontext.ActorID(ctx)},
		{"owner_execution_id", requestcontext.OwnerExecutionID(ctx)},
	} {
		if entry.value != "" {
			fields = append(fields, zap.String(entry.key, entry.value))
		}
	}
	if len(fields) > 0 {
		logger = logger.With(fields...)
	}
	return logger
}

// Fields converts a dynamic field map into deterministic zap fields.
func Fields(values map[string]any) []zap.Field {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" || sensitiveFieldKey(key) || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]zap.Field, 0, len(keys))
	for _, key := range keys {
		fields = append(fields, zap.Any(key, sanitizeFieldValue(values[key])))
	}
	return fields
}

func sensitiveFieldKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, fragment := range []string{"secret", "password", "authorization", "cookie", "credential", "dsn", "payload", "request_body", "response_body"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	if normalized == "token" || strings.Contains(normalized, "access_token") || strings.Contains(normalized, "refresh_token") || strings.Contains(normalized, "api_token") || strings.Contains(normalized, "bearer_token") {
		return true
	}
	return false
}

func sanitizeFieldValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveFieldKey(key) {
				continue
			}
			result[key] = sanitizeFieldValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = sanitizeFieldValue(item)
		}
		return result
	default:
		return value
	}
}

// LogIdempotency emits the one canonical receipt-decision event shape.
func LogIdempotency(ctx context.Context, facts idempotency.AuditFacts, requestID string) {
	if strings.TrimSpace(requestID) == "" {
		requestID = requestcontext.RequestID(ctx)
	}
	FromContext(ctx).Info("idempotency_receipt", Fields(idempotency.LogMetadata(facts, requestID))...)
}

func parseLevel(raw string) (zapcore.Level, error) {
	if strings.TrimSpace(raw) == "" {
		raw = defaultLevel
	}
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(raw)))); err != nil {
		return zapcore.InfoLevel, fmt.Errorf("invalid LOG_LEVEL %q: %w", raw, err)
	}
	return level, nil
}

func parseEncoding(raw string) (string, error) {
	encoding := strings.ToLower(strings.TrimSpace(raw))
	if encoding == "" {
		encoding = defaultEncoding
	}
	if encoding != "json" && encoding != "console" {
		return "", fmt.Errorf("invalid LOG_ENCODING %q: must be json or console", raw)
	}
	return encoding, nil
}
