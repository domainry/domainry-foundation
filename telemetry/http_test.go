package telemetry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestInstrumentHTTPClientPropagatesTraceAndCorrelation(t *testing.T) {
	logCore, observedLogs := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(logCore))
	defer zap.ReplaceGlobals(previousLogger)

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		_ = provider.Shutdown(t.Context())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	var serverContext trace.SpanContext
	var serverCorrelation, inboundRequestID, baggageValue string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, span := StartHTTPServerSpan(request.Context(), request.Header, "test.server", "POST /saas/items", request.Method)
		defer span.End()
		serverContext = trace.SpanContextFromContext(ctx)
		serverCorrelation = requestcontext.CorrelationID(ctx)
		baggageValue = baggage.FromContext(ctx).Member("domainry.test").Value()
		logging.FromContext(ctx).Info("saas_request")
		inboundRequestID = request.Header.Get("X-Request-ID")
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	ctx := requestcontext.WithCorrelationID(t.Context(), "corr-123")
	ctx = requestcontext.WithRequestID(ctx, "runtime-request-must-not-cross-hop")
	member, err := baggage.NewMember("domainry.test", "propagated")
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	ctx = baggage.ContextWithBaggage(ctx, bag)
	ctx, parent := provider.Tracer("test.runtime").Start(ctx, "runtime.use_case")
	request, err := http.NewRequestWithContext(WithHTTPClientOperation(ctx, "notification.accept"), http.MethodPost, server.URL+"/saas/items?token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := InstrumentHTTPClient(nil, "test.client").Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	parent.End()

	if !serverContext.IsValid() || serverCorrelation != "corr-123" || inboundRequestID != "" || baggageValue != "propagated" {
		t.Fatalf("server context=%v correlation=%q request_id=%q baggage=%q", serverContext, serverCorrelation, inboundRequestID, baggageValue)
	}
	ended := recorder.Ended()
	var clientSpan, serverSpan sdktrace.ReadOnlySpan
	for _, span := range ended {
		switch span.Name() {
		case "notification.accept":
			clientSpan = span
		case "POST /saas/items":
			serverSpan = span
		}
		if strings.Contains(fmt.Sprint(span.Attributes()), "secret") || strings.Contains(span.Name(), "token") {
			t.Fatalf("raw URL/query leaked into span: %#v", span)
		}
	}
	if clientSpan == nil || serverSpan == nil {
		t.Fatalf("client/server spans missing: %#v", ended)
	}
	if clientSpan.SpanContext().TraceID() != serverSpan.SpanContext().TraceID() || serverSpan.Parent().SpanID() != clientSpan.SpanContext().SpanID() {
		t.Fatalf("trace parent mismatch client=%v server=%v server_parent=%v", clientSpan.SpanContext(), serverSpan.SpanContext(), serverSpan.Parent())
	}
	logs := observedLogs.All()
	if len(logs) != 1 || logs[0].ContextMap()["trace_id"] != serverSpan.SpanContext().TraceID().String() || logs[0].ContextMap()["span_id"] != serverSpan.SpanContext().SpanID().String() || logs[0].ContextMap()["correlation_id"] != "corr-123" {
		t.Fatalf("server logs=%#v", logs)
	}
}

func TestHTTPPropagationRejectsInvalidCorrelationAndCreatesAttemptSpans(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(t.Context())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get(CorrelationIDHeader) != "" {
			t.Fatalf("invalid correlation propagated: %q", request.Header.Get(CorrelationIDHeader))
		}
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: request}, nil
	})
	client := InstrumentHTTPClient(&http.Client{Transport: transport}, "test.attempt")
	ctx := requestcontext.WithCorrelationID(context.Background(), "contains space")
	for range 2 {
		request, err := http.NewRequestWithContext(WithHTTPClientOperation(ctx, "identity.resolve"), http.MethodGet, "https://identity.example.test/users", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	ended := recorder.Ended()
	if len(ended) != 2 || ended[0].SpanContext().SpanID() == ended[1].SpanContext().SpanID() {
		t.Fatalf("attempt spans=%#v", ended)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}
