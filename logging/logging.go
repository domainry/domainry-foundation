package logging

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"syscall"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultLevel              = "info"
	defaultEncoding           = "json"
	defaultSamplingInitial    = 100
	defaultSamplingThereafter = 100
)

// Config is the complete process-owned logging contract. A hosted module must
// never construct this value: the process host owns the single root logger.
type Config struct {
	ServiceName        string
	ServiceVersion     string
	Environment        string
	DeploymentID       string
	InstanceID         string
	Level              string
	Encoding           string
	OutputPaths        []string
	ErrorOutputPaths   []string
	SamplingInitial    int
	SamplingThereafter int
}

// Failure describes stable operational failure context. Raw error text is
// deliberately excluded from ordinary logs.
type Failure struct {
	Stage      string
	Dependency string
	Retryable  bool
}

// ConfigFromEnvironment reads the one process-level logging configuration.
// Callers still provide source-owned service identity explicitly.
func ConfigFromEnvironment(serviceName, serviceVersion string) Config {
	instanceID := strings.TrimSpace(os.Getenv("DOMAINRY_INSTANCE_ID"))
	if instanceID == "" {
		instanceID = strings.TrimSpace(os.Getenv("RUNTIME_INSTANCE_ID"))
	}
	return Config{
		ServiceName:        strings.TrimSpace(serviceName),
		ServiceVersion:     strings.TrimSpace(serviceVersion),
		Environment:        strings.TrimSpace(os.Getenv("APP_ENV")),
		DeploymentID:       strings.TrimSpace(os.Getenv("DOMAINRY_DEPLOYMENT_ID")),
		InstanceID:         instanceID,
		Level:              strings.TrimSpace(os.Getenv("LOG_LEVEL")),
		Encoding:           strings.TrimSpace(os.Getenv("LOG_ENCODING")),
		SamplingInitial:    defaultSamplingInitial,
		SamplingThereafter: defaultSamplingThereafter,
	}
}

// New creates the process root logger. All hosted modules share its Core and
// derive request-scoped child loggers through FromContext.
func New(config Config) (*zap.Logger, error) {
	cfg, err := zapConfig(config)
	if err != nil {
		return nil, err
	}
	return cfg.Build()
}

// Validate checks the process logging contract without constructing or
// installing a logger. Process configuration owners use it to fail startup on
// unsupported levels or encodings before any long-lived resource is opened.
func Validate(config Config) error {
	_, err := zapConfig(config)
	return err
}

func zapConfig(config Config) (zap.Config, error) {
	if strings.TrimSpace(config.ServiceName) == "" {
		return zap.Config{}, fmt.Errorf("logging service name is required")
	}
	level, err := parseLevel(config.Level)
	if err != nil {
		return zap.Config{}, err
	}
	encoding, err := parseEncoding(config.Encoding)
	if err != nil {
		return zap.Config{}, err
	}
	if config.SamplingInitial <= 0 {
		config.SamplingInitial = defaultSamplingInitial
	}
	if config.SamplingThereafter <= 0 {
		config.SamplingThereafter = defaultSamplingThereafter
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(level)
	cfg.Encoding = encoding
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	if paths := normalizedOutputPaths(config.OutputPaths); len(paths) > 0 {
		cfg.OutputPaths = paths
	}
	if paths := normalizedOutputPaths(config.ErrorOutputPaths); len(paths) > 0 {
		cfg.ErrorOutputPaths = paths
	}
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.Sampling = &zap.SamplingConfig{Initial: config.SamplingInitial, Thereafter: config.SamplingThereafter}
	cfg.InitialFields = map[string]any{"service": strings.TrimSpace(config.ServiceName)}
	for key, value := range map[string]string{
		"version":       config.ServiceVersion,
		"environment":   config.Environment,
		"deployment_id": config.DeploymentID,
		"instance_id":   config.InstanceID,
	} {
		if value = strings.TrimSpace(value); value != "" {
			cfg.InitialFields[key] = value
		}
	}
	return cfg, nil
}

func normalizedOutputPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if path = strings.TrimSpace(path); path != "" {
			result = append(result, path)
		}
	}
	return result
}

// ErrorFields exports normalized error identity plus a bounded operational
// stage. Raw error text belongs in protected debugging workflows, never
// ordinary logs.
func ErrorFields(err error, failure Failure) []zap.Field {
	if err == nil {
		return nil
	}
	fields := []zap.Field{
		zap.String("error_kind", string(apperror.KindOf(err))),
		zap.String("error_code", apperror.CodeOf(err)),
	}
	if stage := strings.TrimSpace(failure.Stage); stage != "" {
		fields = append(fields, zap.String("failure_stage", stage))
	}
	if dependency := strings.TrimSpace(failure.Dependency); dependency != "" {
		fields = append(fields, zap.String("dependency", dependency))
	}
	fields = append(fields, zap.Bool("retryable", failure.Retryable))
	return fields
}

// Initialize installs the process logger as zap's global logger.
func Initialize(config Config) (*zap.Logger, error) {
	logger, err := New(config)
	if err != nil {
		return nil, err
	}
	zap.ReplaceGlobals(logger)
	return logger, nil
}

// Sync flushes the process logger once while treating unsupported fsync on
// stdout/stderr as a successful flush.
func Sync(logger *zap.Logger) error {
	if logger == nil {
		return nil
	}
	err := logger.Sync()
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EBADF) {
		return nil
	}
	return err
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
	if normalized == "error" || normalized == "error_message" || normalized == "raw_error" || normalized == "panic" {
		return true
	}
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

func ProcessStarted(ctx context.Context, fields map[string]any) {
	FromContext(ctx).Info("process_started", Fields(fields)...)
}

func ProcessStopping(ctx context.Context, fields map[string]any) {
	FromContext(ctx).Info("process_stopping", Fields(fields)...)
}

func ProcessStopped(ctx context.Context, fields map[string]any) {
	FromContext(ctx).Info("process_stopped", Fields(fields)...)
}

func ProcessStartFailed(ctx context.Context, err error, failure Failure) {
	FromContext(ctx).Error("process_start_failed", ErrorFields(err, failure)...)
}

func sanitizeFieldValue(value any) any {
	return sanitizeReflectValue(reflect.ValueOf(value))
}

func sanitizeReflectValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	for value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return "[complex_value_omitted]"
		}
		result := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			key := iterator.Key().String()
			if sensitiveFieldKey(key) {
				continue
			}
			result[key] = sanitizeReflectValue(iterator.Value())
		}
		return result
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return "[binary_value_omitted]"
		}
		result := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			result[index] = sanitizeReflectValue(value.Index(index))
		}
		return result
	case reflect.Struct, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return "[complex_value_omitted]"
	default:
		if value.CanInterface() {
			return value.Interface()
		}
		return "[complex_value_omitted]"
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
