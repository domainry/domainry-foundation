package telemetry

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

const ExporterDisabled = "none"
const ExporterOTLPHTTP = "otlp-http"
const AsyncPayloadKey = "_telemetry"

var w3cPropagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

func init() {
	otel.SetTextMapPropagator(w3cPropagator)
}

type Config struct {
	ServiceName    string
	ServiceVersion string
	Exporter       string
	Endpoint       string
	Headers        map[string]string
	Insecure       bool
	SampleRatio    float64
	ExportTimeout  time.Duration
}

type Shutdown func(context.Context) error

// ConfigFromEnvironment reads the process-level OpenTelemetry contract. Trace
// propagation remains active when the exporter is disabled.
func ConfigFromEnvironment(serviceName, serviceVersion string) (Config, error) {
	exporter := strings.TrimSpace(os.Getenv("OTEL_TRACES_EXPORTER"))
	if exporter == "" {
		exporter = strings.TrimSpace(os.Getenv("DOMAINRY_TELEMETRY_EXPORTER"))
	}
	if exporter == "" {
		exporter = strings.TrimSpace(os.Getenv("TELEMETRY_EXPORTER"))
	}
	if exporter == "" {
		exporter = ExporterDisabled
	}
	config := Config{
		ServiceName: strings.TrimSpace(serviceName), ServiceVersion: strings.TrimSpace(serviceVersion), Exporter: exporter,
		Endpoint: firstEnvironmentValue("TELEMETRY_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"), Headers: parseEnvironmentHeaders(firstEnvironmentValue("TELEMETRY_HEADERS", "OTEL_EXPORTER_OTLP_HEADERS")),
		Insecure: environmentBool("TELEMETRY_INSECURE", "OTEL_EXPORTER_OTLP_INSECURE"), SampleRatio: 1, ExportTimeout: 10 * time.Second,
	}
	if raw := firstEnvironmentValue("TELEMETRY_SAMPLE_RATIO", "OTEL_TRACES_SAMPLER_ARG"); raw != "" {
		ratio, err := strconv.ParseFloat(raw, 64)
		if err != nil || ratio <= 0 || ratio > 1 {
			return Config{}, fmt.Errorf("OTEL_TRACES_SAMPLER_ARG must be greater than 0 and at most 1")
		}
		config.SampleRatio = ratio
	}
	if raw := firstEnvironmentValue("TELEMETRY_EXPORT_TIMEOUT"); raw != "" {
		duration, durationErr := time.ParseDuration(raw)
		if durationErr != nil || duration <= 0 {
			return Config{}, fmt.Errorf("TELEMETRY_EXPORT_TIMEOUT must be a positive duration")
		}
		config.ExportTimeout = duration
	}
	return config, nil
}

func firstEnvironmentValue(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func environmentBool(names ...string) bool {
	return strings.EqualFold(firstEnvironmentValue(names...), "true")
}

func parseEnvironmentHeaders(raw string) map[string]string {
	result := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		key, value, found := strings.Cut(entry, "=")
		if key = strings.TrimSpace(key); found && key != "" {
			result[key] = strings.TrimSpace(value)
		}
	}
	return result
}

func Initialize(ctx context.Context, cfg Config) (Shutdown, error) {
	otel.SetTextMapPropagator(w3cPropagator)
	exporterName := strings.ToLower(strings.TrimSpace(cfg.Exporter))
	res := resource.NewWithAttributes("",
		semconv.ServiceName(strings.TrimSpace(cfg.ServiceName)),
		semconv.ServiceVersion(strings.TrimSpace(cfg.ServiceVersion)),
	)
	if exporterName == "" || exporterName == "off" || exporterName == ExporterDisabled {
		// A real SDK provider is still required when export is disabled. The
		// OpenTelemetry no-op provider preserves the remote parent SpanID instead
		// of creating a local Client/Server SpanID, which makes process logs point
		// at the wrong hop. NeverSample keeps this propagation-only mode cheap
		// while still producing correct local span identities.
		provider := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.NeverSample()),
		)
		otel.SetTracerProvider(provider)
		return provider.Shutdown, nil
	}
	if exporterName == "otlp" {
		exporterName = ExporterOTLPHTTP
	}
	if exporterName != ExporterOTLPHTTP {
		return nil, fmt.Errorf("unsupported telemetry exporter %q", cfg.Exporter)
	}
	opts := []otlptracehttp.Option{}
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpointURL(endpoint))
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(cloneHeaders(cfg.Headers)))
	}
	if cfg.ExportTimeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(cfg.ExportTimeout))
	}
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("initialize OTLP HTTP trace exporter: %w", err)
	}
	ratio := cfg.SampleRatio
	if ratio <= 0 {
		ratio = 1
	}
	if ratio > 1 {
		ratio = 1
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func cloneHeaders(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		if key = strings.TrimSpace(key); key != "" {
			result[key] = strings.TrimSpace(value)
		}
	}
	return result
}

type AsyncLink struct {
	TraceID          string
	SpanID           string
	TraceFlags       byte
	CorrelationID    string
	OwnerExecutionID string
}

func CaptureAsyncLink(ctx context.Context, correlationID string) map[string]any {
	spanContext := trace.SpanContextFromContext(ctx)
	result := map[string]any{
		"correlation_id":     normalizedCorrelationValue(correlationID),
		"owner_execution_id": strings.TrimSpace(requestcontext.OwnerExecutionID(ctx)),
	}
	if spanContext.IsValid() {
		result["trace_id"] = spanContext.TraceID().String()
		result["span_id"] = spanContext.SpanID().String()
		result["trace_flags"] = byte(spanContext.TraceFlags())
	}
	return result
}

func EnsureAsyncPayload(ctx context.Context, payload map[string]any) map[string]any {
	if payload == nil {
		payload = map[string]any{}
	}
	if persisted, exists := payload[AsyncPayloadKey]; exists {
		values, validShape := persisted.(map[string]any)
		if validShape {
			link := ParseAsyncLink(values)
			if link.CorrelationID != "" || (link.TraceID != "" && link.SpanID != "") {
				return payload
			}
		}
		delete(payload, AsyncPayloadKey)
	}
	correlationID := requestcontext.CorrelationID(ctx)
	if correlationID == "" {
		correlationID = requestcontext.RequestID(ctx)
	}
	if correlationID == "" && !trace.SpanContextFromContext(ctx).IsValid() {
		return payload
	}
	payload[AsyncPayloadKey] = CaptureAsyncLink(ctx, correlationID)
	return payload
}

func ApplicationPayload(payload map[string]any) map[string]any {
	result := make(map[string]any, len(payload))
	for key, value := range payload {
		if key == AsyncPayloadKey || key == "request_id" {
			continue
		}
		result[key] = value
	}
	return result
}

// ConsumeAsyncPayload separates framework-owned trace linkage from the
// application payload. Workers call it after a durable claim and must pass only
// the returned application payload to business code, audit or providers.
func ConsumeAsyncPayload(payload map[string]any) (map[string]any, AsyncLink) {
	var link AsyncLink
	if values, ok := payload[AsyncPayloadKey].(map[string]any); ok {
		link = ParseAsyncLink(values)
	}
	return ApplicationPayload(payload), link
}

func ParseAsyncLink(values map[string]any) AsyncLink {
	link := AsyncLink{
		TraceID: asyncString(values["trace_id"]), SpanID: asyncString(values["span_id"]),
		TraceFlags: asyncTraceFlags(values["trace_flags"]), CorrelationID: normalizedCorrelationValue(asyncString(values["correlation_id"])),
		OwnerExecutionID: asyncString(values["owner_execution_id"]),
	}
	traceID, traceErr := trace.TraceIDFromHex(link.TraceID)
	spanID, spanErr := trace.SpanIDFromHex(link.SpanID)
	if traceErr != nil || spanErr != nil || !traceID.IsValid() || !spanID.IsValid() {
		link.TraceID = ""
		link.SpanID = ""
		link.TraceFlags = 0
	}
	return link
}

func asyncString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func StartLinkedSpan(ctx context.Context, tracerName, spanName string, link AsyncLink, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = requestcontext.WithCorrelationID(ctx, link.CorrelationID)
	ctx = requestcontext.WithOwnerExecutionID(ctx, link.OwnerExecutionID)
	if traceID, err := trace.TraceIDFromHex(link.TraceID); err == nil {
		if spanID, spanErr := trace.SpanIDFromHex(link.SpanID); spanErr == nil {
			spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.TraceFlags(link.TraceFlags), Remote: true})
			options = append(options, trace.WithLinks(trace.Link{SpanContext: spanContext}))
		}
	}
	options = append(options, trace.WithNewRoot())
	return otel.Tracer(strings.TrimSpace(tracerName)).Start(ctx, strings.TrimSpace(spanName), options...)
}

func StartUseCase(ctx context.Context, name string, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	name = strings.TrimSpace(name)
	return otel.Tracer("github.com/domainry/domainry-foundation/telemetry").Start(ctx, name, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(append([]attribute.KeyValue{attribute.String("application.use_case", name)}, attributes...)...))
}

func EndUseCase(span trace.Span, err error, result string) {
	if span == nil {
		return
	}
	result = strings.TrimSpace(result)
	if result == "" {
		if err != nil {
			result = "error"
		} else {
			result = "success"
		}
	}
	span.SetAttributes(attribute.String("application.result", result), attribute.Bool("error", err != nil))
	if err != nil {
		span.SetStatus(codes.Error, result)
	}
	span.End()
}

func asyncTraceFlags(value any) byte {
	switch typed := value.(type) {
	case byte:
		return typed
	case float64:
		return byte(typed)
	case int:
		return byte(typed)
	default:
		return 0
	}
}
