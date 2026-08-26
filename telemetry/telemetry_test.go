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

func TestAsyncBoundaryCreatesNewRootWithLink(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	parent := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	link := ParseAsyncLink(CaptureAsyncLink(parent, "corr-1"))
	_, span := StartLinkedSpan(context.Background(), "test", "outbox.send", link)
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Parent().IsValid() || len(ended[0].Links()) != 1 || ended[0].Links()[0].SpanContext.TraceID() != traceID || link.CorrelationID != "corr-1" {
		t.Fatalf("async span=%#v link=%#v", ended, link)
	}
}

func TestUseCaseToPersistedAsyncWorkerTraceChain(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()); otel.SetTracerProvider(previous) })

	requestContext := requestcontext.WithCorrelationID(t.Context(), "correlation-42")
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
	_, workerSpan := StartLinkedSpan(context.Background(), "test.worker", "integration.outbox.send", link)
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
	if worker.Parent().IsValid() {
		t.Fatalf("worker reused a stale parent: %s", worker.Parent().SpanID())
	}
	if len(worker.Links()) != 1 || worker.Links()[0].SpanContext.SpanID() != useCase.SpanContext().SpanID() {
		t.Fatalf("worker links=%#v, want persisted use-case link", worker.Links())
	}
	if link.CorrelationID != "correlation-42" {
		t.Fatalf("correlation id=%q", link.CorrelationID)
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

func TestEnsureAsyncPayloadUsesCorrelationAndDoesNotOverwritePersistedLink(t *testing.T) {
	ctx := requestcontext.WithCorrelationID(t.Context(), "corr-1")
	payload := EnsureAsyncPayload(ctx, map[string]any{"business": "value"})
	link, ok := payload[AsyncPayloadKey].(map[string]any)
	if !ok || link["correlation_id"] != "corr-1" {
		t.Fatalf("payload=%#v", payload)
	}
	payload[AsyncPayloadKey] = map[string]any{"correlation_id": "persisted"}
	if got := EnsureAsyncPayload(requestcontext.WithCorrelationID(t.Context(), "corr-2"), payload)[AsyncPayloadKey].(map[string]any)["correlation_id"]; got != "persisted" {
		t.Fatalf("persisted link was overwritten: %v", got)
	}
	application := ApplicationPayload(map[string]any{"request_id": "req", AsyncPayloadKey: link, "order_id": "one"})
	if len(application) != 1 || application["order_id"] != "one" {
		t.Fatalf("application payload=%#v", application)
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
