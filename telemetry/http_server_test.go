package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestHTTPServerMiddlewareExtractsContextAndWritesOneTerminalEvent(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithIDGenerator(contextTraceIDGenerator{}))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()); otel.SetTracerProvider(previousProvider) })
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /widgets/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if requestcontext.TraceID(request.Context()) != "4bf92f3577b34da6a3ce929d0e0e4736" || requestcontext.RequestID(request.Context()) != requestcontext.TraceID(request.Context()) {
			t.Fatalf("request/trace id=%q/%q", requestcontext.RequestID(request.Context()), requestcontext.TraceID(request.Context()))
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := HTTPServerMiddleware(HTTPServerConfig{ListenerGroup: "public", TracerName: "test"}, mux)
	request := httptest.NewRequest(http.MethodGet, "/widgets/secret?token=hidden", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set(RequestIDHeader, "downstream-request")
	request.Header.Set("X-Correlation-ID", "untrusted-business-correlation")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Header().Get(RequestIDHeader) != "4bf92f3577b34da6a3ce929d0e0e4736" || response.Header().Get("X-Correlation-ID") != "" {
		t.Fatalf("response headers=%v", response.Header())
	}
	entries := observed.FilterMessage("http_request").All()
	if len(entries) != 1 {
		t.Fatalf("terminal events=%d entries=%#v", len(entries), observed.All())
	}
	fields := entries[0].ContextMap()
	if fields["route"] != "/widgets/{id}" || fields["method"] != http.MethodGet || fields["listener_group"] != "public" || fields[logging.LogTypeField] != logging.LogTypeAccess {
		t.Fatalf("fields=%#v", fields)
	}
	if _, leaked := fields["path"]; leaked {
		t.Fatalf("raw path field leaked: %#v", fields)
	}
}

func TestHTTPServerMiddlewareUsesCallerRequestIDAsTraceID(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithIDGenerator(contextTraceIDGenerator{}))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()); otel.SetTracerProvider(previousProvider) })

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	var observedRequestID, observedTraceID string
	handler := HTTPServerMiddleware(HTTPServerConfig{TracerName: "test"}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observedRequestID = requestcontext.RequestID(request.Context())
		observedTraceID = requestcontext.TraceID(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set(RequestIDHeader, traceID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if observedRequestID != traceID || observedTraceID != traceID || response.Header().Get(RequestIDHeader) != traceID {
		t.Fatalf("request=%q trace=%q response=%q", observedRequestID, observedTraceID, response.Header().Get(RequestIDHeader))
	}
}

func TestHTTPServerMiddlewareGeneratesTraceIDWhenRequestIDIsAbsent(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithIDGenerator(contextTraceIDGenerator{}))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()); otel.SetTracerProvider(previousProvider) })

	var observedRequestID, observedTraceID string
	handler := HTTPServerMiddleware(HTTPServerConfig{TracerName: "test"}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observedRequestID = requestcontext.RequestID(request.Context())
		observedTraceID = requestcontext.TraceID(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if requestcontext.NormalizeTraceID(observedRequestID) == "" || observedRequestID != observedTraceID || response.Header().Get(RequestIDHeader) != observedTraceID {
		t.Fatalf("generated request=%q trace=%q response=%q", observedRequestID, observedTraceID, response.Header().Get(RequestIDHeader))
	}
}

func TestHTTPServerMiddlewareKeepsCallerTraceAliasWhenTracingIsDisabled(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(nooptrace.NewTracerProvider())
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	var observedRequestID string
	handler := HTTPServerMiddleware(HTTPServerConfig{TracerName: "test"}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observedRequestID = requestcontext.RequestID(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set(RequestIDHeader, traceID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if observedRequestID != traceID || response.Header().Get(RequestIDHeader) != traceID {
		t.Fatalf("request=%q response=%q", observedRequestID, response.Header().Get(RequestIDHeader))
	}
}

func TestHTTPServerMiddlewareSuppressesSuccessfulProbeAndRecoversPanic(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /live", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) { panic("secret panic value") })
	handler := HTTPServerMiddleware(HTTPServerConfig{}, mux)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/live", nil))
	panicResponse := httptest.NewRecorder()
	handler.ServeHTTP(panicResponse, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if panicResponse.Code != http.StatusInternalServerError {
		t.Fatalf("panic status=%d", panicResponse.Code)
	}
	var panicFailure struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(panicResponse.Body).Decode(&panicFailure); err != nil {
		t.Fatal(err)
	}
	if panicFailure.Code != "backend.internal" || panicFailure.Message != http.StatusText(http.StatusInternalServerError) || panicFailure.RequestID == "" {
		t.Fatalf("panic failure=%+v", panicFailure)
	}
	if len(observed.FilterMessage("http_request").All()) != 1 || len(observed.FilterMessage("panic_recovered").All()) != 1 {
		t.Fatalf("entries=%#v", observed.All())
	}
	for _, entry := range observed.All() {
		if entry.Message == "secret panic value" {
			t.Fatalf("panic value leaked: %#v", entry)
		}
	}
}

func TestHTTPRoutePatternRejectsUnmatchedAndRawInput(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/private/path?secret=value", nil)
	if route := HTTPRoutePattern(request); route != "unmatched" {
		t.Fatalf("route=%q", route)
	}
	request.Pattern = "GET /items/{id}"
	if route := HTTPRoutePattern(request); route != "/items/{id}" {
		t.Fatalf("route=%q", route)
	}
}

func TestHTTPServerMiddlewareUsesRouteRecordedByManualRouter(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	handler := HTTPServerMiddleware(HTTPServerConfig{ListenerGroup: "public"}, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		RecordHTTPRoute(request, "GET /todo/v1/discovery")
		writer.WriteHeader(http.StatusNoContent)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/todo/v1/discovery?token=hidden", nil))
	entries := observed.FilterMessage("http_request").All()
	if len(entries) != 1 || entries[0].ContextMap()["route"] != "/todo/v1/discovery" {
		t.Fatalf("entries=%#v", entries)
	}
}

func TestRecordHTTPStatusErrorPublishesStableClassification(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	tests := []struct {
		status int
		kind   string
	}{
		{http.StatusBadRequest, "bad_request"},
		{http.StatusForbidden, "forbidden"},
		{http.StatusNotFound, "not_found"},
		{http.StatusConflict, "conflict"},
		{http.StatusTooManyRequests, "rate_limited"},
		{http.StatusServiceUnavailable, "unavailable"},
		{http.StatusInternalServerError, "internal"},
	}
	for _, test := range tests {
		handler := HTTPServerMiddleware(HTTPServerConfig{}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			RecordHTTPStatusError(writer, test.status, "module.failure")
			writer.WriteHeader(test.status)
		}))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/failure", nil))
		entries := observed.FilterMessage("http_request").All()
		entry := entries[len(entries)-1].ContextMap()
		if entry["error_kind"] != test.kind || entry["error_code"] != "module.failure" {
			t.Fatalf("status=%d fields=%#v", test.status, entry)
		}
	}
}

func TestHTTPServerSeparatesInternalSourceStackFromAccessLog(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })

	handler := HTTPServerMiddleware(HTTPServerConfig{ListenerGroup: "public"}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		RecordHTTPStatusError(writer, http.StatusInternalServerError, "backend.widget.load_failed", errors.New("password=secret"))
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/widgets", nil))

	diagnostics := observed.FilterMessage("http_internal_failure").All()
	access := observed.FilterMessage("http_request").All()
	if len(diagnostics) != 1 || len(access) != 1 {
		t.Fatalf("diagnostics=%#v access=%#v", diagnostics, access)
	}
	diagnosticFields := diagnostics[0].ContextMap()
	if diagnosticFields["error_code"] != "backend.widget.load_failed" || !strings.Contains(fmt.Sprint(diagnosticFields["stack"]), "TestHTTPServerSeparatesInternalSourceStackFromAccessLog") {
		t.Fatalf("diagnostic fields=%#v", diagnosticFields)
	}
	if strings.Contains(fmt.Sprint(diagnosticFields), "password=secret") {
		t.Fatalf("raw error leaked: %#v", diagnosticFields)
	}
	if _, exists := access[0].ContextMap()["stack"]; exists {
		t.Fatalf("access log contains source stack=%#v", access[0].ContextMap())
	}
}

func TestHTTPServerMiddlewareClassifiesUnannotatedModuleErrors(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })

	handler := HTTPServerMiddleware(HTTPServerConfig{}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "must not escape", http.StatusForbidden)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/module", nil))
	var failure struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "http.forbidden" || failure.Message != "http.forbidden" || failure.RequestID == "" || strings.Contains(response.Body.String(), "must not escape") {
		t.Fatalf("failure=%+v body=%q", failure, response.Body.String())
	}
	entries := observed.FilterMessage("http_request").All()
	if len(entries) != 1 {
		t.Fatalf("entries=%#v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["error_kind"] != "forbidden" || fields["error_code"] != "http.forbidden" {
		t.Fatalf("fields=%#v", fields)
	}
}

func TestHTTPServerMiddlewareCanonicalizesServeMuxRoutingErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /widgets", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := HTTPServerMiddleware(HTTPServerConfig{}, mux)
	for _, test := range []struct {
		method, path string
		status       int
		code         string
	}{
		{http.MethodPost, "/widgets", http.StatusMethodNotAllowed, "http.method_not_allowed"},
		{http.MethodGet, "/missing", http.StatusNotFound, "http.not_found"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		var failure struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		}
		if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
			t.Fatalf("%s %s: %v body=%q", test.method, test.path, err, response.Body.String())
		}
		if response.Code != test.status || failure.Code != test.code || failure.Message != test.code || failure.RequestID == "" {
			t.Fatalf("%s %s: status=%d failure=%+v", test.method, test.path, response.Code, failure)
		}
	}
}
