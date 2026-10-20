package processhost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/telemetry"
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
	if response.Code != http.StatusNoContent || response.Header().Get(telemetry.RequestIDHeader) == "" || response.Header().Get(telemetry.CorrelationIDHeader) == "" {
		t.Fatalf("response=%d headers=%v", response.Code, response.Header())
	}
	if err := host.Finish(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
