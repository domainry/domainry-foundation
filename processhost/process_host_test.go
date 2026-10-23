package processhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/telemetry"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestHostOwnsLoggerTelemetryAndHTTPBoundary(t *testing.T) {
	t.Setenv("LOG_ENCODING", "json")
	host, err := Open(t.Context(), Config{ServiceName: "test-process", ServiceVersion: "test", Telemetry: telemetry.Config{Exporter: telemetry.ExporterDisabled}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ready", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) })
	response := httptest.NewRecorder()
	host.Handler("public", mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusNoContent || response.Header().Get(telemetry.RequestIDHeader) == "" || response.Header().Get("X-Correlation-ID") != "" {
		t.Fatalf("response=%d headers=%v", response.Code, response.Header())
	}
	if err := host.Finish(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestFinishPreservesTypedStartupFailureStage(t *testing.T) {
	core, observed := observer.New(zap.ErrorLevel)
	logger := zap.New(core)
	previous := zap.L()
	zap.ReplaceGlobals(logger)
	t.Cleanup(func() { zap.ReplaceGlobals(previous) })

	host := &Host{logger: logger}
	err := host.Finish(t.Context(), nil, FailStartup(context.Canceled, logging.Failure{Stage: "listener_bind", Dependency: "public_listener", Retryable: true}))
	if err == nil {
		t.Fatal("startup error was lost")
	}
	entries := observed.FilterMessage("process_start_failed").All()
	if len(entries) != 1 {
		t.Fatalf("entries=%v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["failure_stage"] != "listener_bind" || fields["dependency"] != "public_listener" || fields["retryable"] != true {
		t.Fatalf("fields=%v", fields)
	}
}
