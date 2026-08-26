package telemetry

import (
	"context"
	"fmt"
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

func Initialize(ctx context.Context, cfg Config) (Shutdown, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	exporterName := strings.ToLower(strings.TrimSpace(cfg.Exporter))
	if exporterName == "" || exporterName == "off" || exporterName == ExporterDisabled {
		return func(context.Context) error { return nil }, nil
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
	res := resource.NewWithAttributes("",
		semconv.ServiceName(strings.TrimSpace(cfg.ServiceName)),
		semconv.ServiceVersion(strings.TrimSpace(cfg.ServiceVersion)),
	)
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
	TraceID       string
	SpanID        string
	TraceFlags    byte
	CorrelationID string
}

func CaptureAsyncLink(ctx context.Context, correlationID string) map[string]any {
	spanContext := trace.SpanContextFromContext(ctx)
	result := map[string]any{"correlation_id": strings.TrimSpace(correlationID)}
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
	if _, exists := payload[AsyncPayloadKey]; exists {
		return payload
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

func ParseAsyncLink(values map[string]any) AsyncLink {
	return AsyncLink{
		TraceID: asyncString(values["trace_id"]), SpanID: asyncString(values["span_id"]),
		TraceFlags: asyncTraceFlags(values["trace_flags"]), CorrelationID: asyncString(values["correlation_id"]),
	}
}

func asyncString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func StartLinkedSpan(ctx context.Context, tracerName, spanName string, link AsyncLink, options ...trace.SpanStartOption) (context.Context, trace.Span) {
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
