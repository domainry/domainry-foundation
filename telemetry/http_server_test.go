package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestHTTPServerMiddlewareExtractsContextAndWritesOneTerminalEvent(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()); otel.SetTracerProvider(previousProvider) })
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /widgets/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if requestcontext.TraceID(request.Context()) != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("trace id=%q", requestcontext.TraceID(request.Context()))
		}
		if requestcontext.RequestID(request.Context()) == "" || requestcontext.RequestID(request.Context()) == "downstream-request" || requestcontext.CorrelationID(request.Context()) != "corr-123" {
			t.Fatalf("request context request=%q correlation=%q", requestcontext.RequestID(request.Context()), requestcontext.CorrelationID(request.Context()))
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := HTTPServerMiddleware(HTTPServerConfig{ListenerGroup: "public", TracerName: "test"}, mux)
	request := httptest.NewRequest(http.MethodGet, "/widgets/secret?token=hidden", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set(RequestIDHeader, "downstream-request")
	request.Header.Set(CorrelationIDHeader, "corr-123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Header().Get(RequestIDHeader) == "" || response.Header().Get(RequestIDHeader) == "downstream-request" || response.Header().Get(CorrelationIDHeader) != "corr-123" {
		t.Fatalf("response headers=%v", response.Header())
	}
	entries := observed.FilterMessage("http_request").All()
	if len(entries) != 1 {
		t.Fatalf("terminal events=%d entries=%#v", len(entries), observed.All())
	}
	fields := entries[0].ContextMap()
	if fields["route"] != "/widgets/{id}" || fields["method"] != http.MethodGet || fields["listener_group"] != "public" {
		t.Fatalf("fields=%#v", fields)
	}
	if _, leaked := fields["path"]; leaked {
		t.Fatalf("raw path field leaked: %#v", fields)
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
