package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestDisabledExporterStillInstallsW3CPropagation(t *testing.T) {
	shutdown, err := Initialize(t.Context(), Config{Exporter: ExporterDisabled})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shutdown(t.Context()) })
	carrier := propagation.MapCarrier{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "tracestate": "vendor=value"}
	ctx := otel.GetTextMapPropagator().Extract(t.Context(), carrier)
	if got := (propagation.TraceContext{}).Fields(); len(got) != 2 {
		t.Fatalf("trace context fields = %#v", got)
	}
	if ctx == nil {
		t.Fatal("trace context extraction returned nil")
	}
}

func TestAsyncBoundaryContinuesProducerTrace(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	parent := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	link := ParseAsyncLink(CaptureAsyncLink(parent))
	_, span := StartAsyncSpan(context.Background(), "test", "outbox.send", link)
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Parent().SpanID() != spanID || ended[0].SpanContext().TraceID() != traceID || len(ended[0].Links()) != 0 {
		t.Fatalf("async span=%#v link=%#v", ended, link)
	}
}

func TestUseCaseToPersistedAsyncWorkerTraceChain(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()); otel.SetTracerProvider(previous) })

	requestContext := requestcontext.WithOwnerExecutionID(t.Context(), "execution-42")
	requestContext, requestSpan := provider.Tracer("test.http").Start(requestContext, "POST /actions/{actionKey}")
	applicationContext, useCaseSpan := StartUseCase(requestContext, "action.invoke")
	payload := EnsureAsyncPayload(applicationContext, map[string]any{"record_id": "record-7"})
	EndUseCase(useCaseSpan, nil, "accepted")
	requestSpan.End()

	persisted, ok := payload[AsyncPayloadKey].(map[string]any)
	if !ok {
		t.Fatalf("persisted telemetry link missing: %#v", payload)
	}
	link := ParseAsyncLink(persisted)
	workerContext, workerSpan := StartAsyncSpan(context.Background(), "test.worker", "integration.outbox.send", link)
	workerSpan.End()

	ended := recorder.Ended()
	if len(ended) != 3 {
		t.Fatalf("ended spans=%d, want 3", len(ended))
	}
	var request, useCase, worker sdktrace.ReadOnlySpan
	for _, span := range ended {
		switch span.Name() {
		case "POST /actions/{actionKey}":
			request = span
		case "action.invoke":
			useCase = span
		case "integration.outbox.send":
			worker = span
		}
	}
	if request == nil || useCase == nil || worker == nil {
		t.Fatalf("trace chain spans missing: %#v", ended)
	}
	if useCase.Parent().SpanID() != request.SpanContext().SpanID() {
		t.Fatalf("use case parent=%s, want request span=%s", useCase.Parent().SpanID(), request.SpanContext().SpanID())
	}
	if worker.Parent().SpanID() != useCase.SpanContext().SpanID() || worker.SpanContext().TraceID() != useCase.SpanContext().TraceID() {
		t.Fatalf("worker parent=%s trace=%s, want use-case parent=%s trace=%s", worker.Parent().SpanID(), worker.SpanContext().TraceID(), useCase.SpanContext().SpanID(), useCase.SpanContext().TraceID())
	}
	if requestcontext.OwnerExecutionID(workerContext) != "execution-42" {
		t.Fatalf("worker context execution=%q", requestcontext.OwnerExecutionID(workerContext))
	}
}

func TestUseCaseSpanExportsStableNameAndResultWithoutErrorText(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()); otel.SetTracerProvider(previous) })
	_, span := StartUseCase(t.Context(), "record.create")
	EndUseCase(span, errors.New("dsn=secret raw sql"), "failed")
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Name() != "record.create" || ended[0].Status().Description != "failed" {
		t.Fatalf("span=%#v", ended)
	}
	for _, event := range ended[0].Events() {
		if strings.Contains(fmt.Sprint(event), "secret") {
			t.Fatalf("error text leaked into span: %#v", event)
		}
	}
}

func TestEnsureAsyncPayloadUsesTraceAndDoesNotOverwritePersistedLink(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	payload := EnsureAsyncPayload(ctx, map[string]any{"business": "value"})
	link, ok := payload[AsyncPayloadKey].(map[string]any)
	if !ok || link["trace_id"] != traceID.String() || link["span_id"] != spanID.String() {
		t.Fatalf("payload=%#v", payload)
	}
	persisted := map[string]any{"trace_id": traceID.String(), "span_id": spanID.String(), "trace_flags": byte(1)}
	payload[AsyncPayloadKey] = persisted
	if got := EnsureAsyncPayload(ctx, payload)[AsyncPayloadKey].(map[string]any); got["trace_id"] != traceID.String() || got["span_id"] != spanID.String() {
		t.Fatalf("persisted link was overwritten: %v", got)
	}
	application := ApplicationPayload(map[string]any{"request_id": "req", AsyncPayloadKey: link, "order_id": "one"})
	if len(application) != 1 || application["order_id"] != "one" {
		t.Fatalf("application payload=%#v", application)
	}
	invalid := EnsureAsyncPayload(t.Context(), map[string]any{AsyncPayloadKey: map[string]any{"trace_id": "invalid", "span_id": "invalid"}})
	if _, exists := invalid[AsyncPayloadKey]; exists {
		t.Fatalf("invalid persisted telemetry survived validation: %#v", invalid)
	}
}

func TestConsumeAsyncPayloadSeparatesFrameworkMetadata(t *testing.T) {
	payload := map[string]any{
		"customer": "safe",
		AsyncPayloadKey: map[string]any{
			"trace_id":           "4bf92f3577b34da6a3ce929d0e0e4736",
			"span_id":            "00f067aa0ba902b7",
			"trace_flags":        byte(1),
			"owner_execution_id": "execution-1",
		},
	}
	applicationPayload, link := ConsumeAsyncPayload(payload)
	if applicationPayload["customer"] != "safe" || applicationPayload[AsyncPayloadKey] != nil || link.OwnerExecutionID != "execution-1" || link.TraceID == "" || link.SpanID == "" {
		t.Fatalf("application payload=%#v link=%+v", applicationPayload, link)
	}
	if _, stillPersisted := payload[AsyncPayloadKey]; !stillPersisted {
		t.Fatal("consume mutated durable payload")
	}
}

func TestUnknownExporterIsRejected(t *testing.T) {
	if _, err := Initialize(t.Context(), Config{Exporter: "invented"}); err == nil {
		t.Fatal("unknown exporter was accepted")
	}
}

func TestUnavailableExporterDoesNotBlockBusinessSpanCreation(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(collector.Close)
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	shutdown, err := Initialize(t.Context(), Config{
		ServiceName: "runtime-test", Exporter: ExporterOTLPHTTP,
		Endpoint: collector.URL, Insecure: true, ExportTimeout: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("initialize exporter: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		_ = shutdown(shutdownContext)
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	started := time.Now()
	_, span := StartUseCase(t.Context(), "record.create")
	EndUseCase(span, nil, "success")
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("business span creation waited for failed exporter: %s", elapsed)
	}
}
