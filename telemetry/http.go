package telemetry

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type httpClientOperationKey struct{}
type requestedTraceIDKey struct{}

// WithHTTPClientOperation attaches a bounded logical operation name to an
// outbound request. It must be a source-owned constant, never a raw URL.
func WithHTTPClientOperation(ctx context.Context, operation string) context.Context {
	operation = strings.TrimSpace(operation)
	if ctx == nil || operation == "" {
		return ctx
	}
	return context.WithValue(ctx, httpClientOperationKey{}, operation)
}

// ExtractHTTPContext restores standard W3C trace and baggage context at an
// inbound HTTP boundary. A valid X-Request-ID selects the same root trace ID;
// business and durable-execution identities are not accepted from headers.
func ExtractHTTPContext(ctx context.Context, header http.Header) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
	requestID := requestcontext.NormalizeTraceID(header.Get(RequestIDHeader))
	if requestID == "" {
		return ctx
	}
	requestTraceID, _ := trace.TraceIDFromHex(requestID)
	remote := trace.SpanContextFromContext(ctx)
	if remote.IsValid() && remote.TraceID() == requestTraceID {
		return ctx
	}
	// X-Request-ID is the caller-selected root trace ID. When it is present it
	// wins over a conflicting traceparent so the process never exposes two
	// different identities for the same request.
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	return context.WithValue(ctx, requestedTraceIDKey{}, requestTraceID)
}

// InjectHTTPContext propagates standard W3C trace and baggage context plus the
// same trace ID under the public X-Request-ID alias. Business execution
// identities remain local to their owners.
func InjectHTTPContext(ctx context.Context, header http.Header) {
	if ctx == nil || header == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
	if requestID := requestcontext.NormalizeTraceID(requestcontext.RequestID(ctx)); requestID != "" {
		header.Set(RequestIDHeader, requestID)
	}
}

// StartHTTPServerSpan extracts the remote parent before starting the local
// server span. Callers may rename the span to a bounded route template after
// routing completes.
func StartHTTPServerSpan(ctx context.Context, header http.Header, tracerName, operation, method string) (context.Context, trace.Span) {
	ctx = ExtractHTTPContext(ctx, header)
	tracerName = strings.TrimSpace(tracerName)
	if tracerName == "" {
		tracerName = "github.com/domainry/domainry-foundation/telemetry/http"
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "HTTP " + strings.TrimSpace(method)
	}
	return otel.Tracer(tracerName).Start(ctx, operation,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", strings.TrimSpace(method))),
	)
}

// HTTPTransport creates one client span for every real network attempt. A
// retry therefore receives a fresh span and fresh injected parent identity.
type HTTPTransport struct {
	Base                     http.RoundTripper
	TracerName               string
	DisableDiagnosticContent bool
}

func (transport HTTPTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := transport.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if request == nil {
		return base.RoundTrip(request)
	}
	operation, _ := request.Context().Value(httpClientOperationKey{}).(string)
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "http.client"
	}
	tracerName := strings.TrimSpace(transport.TracerName)
	if tracerName == "" {
		tracerName = "github.com/domainry/domainry-foundation/telemetry/http"
	}
	attributes := []attribute.KeyValue{attribute.String("http.request.method", request.Method)}
	attributes = append(attributes, attribute.String("http.route", operation))
	if request.URL != nil {
		if host := strings.TrimSpace(request.URL.Hostname()); host != "" {
			attributes = append(attributes, attribute.String("server.address", host))
		}
		if port, portErr := strconv.Atoi(request.URL.Port()); portErr == nil && port > 0 {
			attributes = append(attributes, attribute.Int("server.port", port))
		}
	}
	started := time.Now()
	ctx, span := otel.Tracer(tracerName).Start(request.Context(), operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attributes...),
	)
	defer span.End()

	clone := request.Clone(ctx)
	clone.Header = request.Header.Clone()
	InjectHTTPContext(ctx, clone.Header)
	policy := currentDiagnosticPolicy()
	captureContent := policy.Enabled && policy.features[DiagnosticFeatureHTTPClient] && policy.HTTPContent && !transport.DisableDiagnosticContent
	var requestCapture *boundedByteCapture
	if captureContent && clone.Body != nil {
		requestCapture = newBoundedByteCapture(policy.MaxContentBytes)
		clone.Body = &capturingReadCloser{ReadCloser: clone.Body, capture: requestCapture}
	}
	dependency := "http"
	if clone.URL != nil && strings.TrimSpace(clone.URL.Hostname()) != "" {
		dependency = strings.TrimSpace(clone.URL.Hostname())
	}
	var completion sync.Once
	complete := func(response *http.Response, responseCapture *boundedByteCapture, callErr error) {
		completion.Do(func() {
			input := map[string]any{"method": strings.TrimSpace(clone.Method)}
			if captureContent {
				for key, value := range diagnosticHTTPInput(clone, requestCapture) {
					input[key] = value
				}
			}
			output := map[string]any{}
			outcome := "success"
			if response != nil {
				output["status"] = response.StatusCode
				if captureContent {
					for key, value := range diagnosticHTTPOutput(response.Header, responseCapture) {
						output[key] = value
					}
				}
				if response.StatusCode >= http.StatusBadRequest {
					outcome = "http_error"
				}
			}
			if callErr != nil {
				outcome = "transport_error"
			}
			RecordDependencyCall(ctx, DependencyCall{
				Feature: DiagnosticFeatureHTTPClient, Dependency: dependency, Operation: operation, Input: input,
			}, DependencyResult{Outcome: outcome, Output: output, Err: callErr}, time.Since(started))
		})
	}
	response, err := base.RoundTrip(clone)
	if err != nil {
		span.SetStatus(codes.Error, "transport_error")
		complete(response, nil, err)
		return response, err
	}
	if response != nil {
		span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode))
		if response.StatusCode >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, "server_error")
		}
	}
	if response == nil || response.Body == nil {
		complete(response, nil, nil)
		return response, nil
	}
	var responseCapture *boundedByteCapture
	if captureContent {
		responseCapture = newBoundedByteCapture(policy.MaxContentBytes)
	}
	response.Body = &diagnosticHTTPResponseBody{
		ReadCloser: response.Body,
		capture:    responseCapture,
		complete: func(readErr error) {
			complete(response, responseCapture, readErr)
		},
	}
	return response, nil
}

type diagnosticHTTPResponseBody struct {
	io.ReadCloser
	capture  *boundedByteCapture
	complete func(error)
	once     sync.Once
}

func (body *diagnosticHTTPResponseBody) Read(payload []byte) (int, error) {
	count, err := body.ReadCloser.Read(payload)
	body.capture.Write(payload[:count])
	if err != nil {
		if err == io.EOF {
			body.finish(nil)
		} else {
			body.finish(err)
		}
	}
	return count, err
}

func (body *diagnosticHTTPResponseBody) Close() error {
	err := body.ReadCloser.Close()
	body.finish(err)
	return err
}

func (body *diagnosticHTTPResponseBody) finish(err error) {
	body.once.Do(func() {
		if body.complete != nil {
			body.complete(err)
		}
	})
}

// InstrumentHTTPClient returns a shallow client copy whose transport emits
// and propagates one span per physical attempt. The caller's client is never
// mutated.
func InstrumentHTTPClient(source *http.Client, tracerName string) *http.Client {
	return InstrumentHTTPClientWithOptions(source, tracerName, HTTPClientInstrumentationOptions{})
}

// HTTPClientInstrumentationOptions can only tighten the process diagnostic
// policy. It is used by credential and identity adapters whose repository
// contract forbids response-content logging even when the host enables it.
type HTTPClientInstrumentationOptions struct {
	DisableDiagnosticContent bool
}

// InstrumentHTTPClientWithOptions returns an instrumented shallow copy with a
// source-owned safety ceiling.
func InstrumentHTTPClientWithOptions(source *http.Client, tracerName string, options HTTPClientInstrumentationOptions) *http.Client {
	if source == nil {
		source = http.DefaultClient
	}
	clone := *source
	if existing, ok := clone.Transport.(HTTPTransport); ok {
		if strings.TrimSpace(existing.TracerName) == "" && strings.TrimSpace(tracerName) != "" {
			existing.TracerName = tracerName
		}
		existing.DisableDiagnosticContent = existing.DisableDiagnosticContent || options.DisableDiagnosticContent
		clone.Transport = existing
		return &clone
	}
	clone.Transport = HTTPTransport{Base: source.Transport, TracerName: tracerName, DisableDiagnosticContent: options.DisableDiagnosticContent}
	return &clone
}
