package telemetry

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestAuthenticatedOTLPDoesNotFollowRedirect(t *testing.T) {
	var redirected atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	var authorized atomic.Int64
	collector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authorized.Add(1)
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(collector.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: collector.Certificate().Raw})
	file := filepath.Join(t.TempDir(), "collector.pem")
	if err := os.WriteFile(file, ca, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", file)
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer fixture-token")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_INSECURE", "false")

	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	shutdown, err := Initialize(ctx, Config{
		Exporter: ExporterOTLPHTTP, Endpoint: collector.URL + "/v1/traces", ExportTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("auth-check").Start(ctx, "fixture-span")
	span.End()
	_ = shutdown(ctx)
	if authorized.Load() != 1 || redirected.Load() != 0 {
		t.Fatalf("collector received %d authenticated requests, redirect target received %d", authorized.Load(), redirected.Load())
	}
}

func TestAuthenticatedOTLPRequiresVerifiedHTTPS(t *testing.T) {
	headers := map[string]string{"Authorization": "Bearer fixture-token"}
	for _, config := range []Config{
		{Exporter: ExporterOTLPHTTP, Endpoint: "http://localhost:4318/v1/traces", Headers: headers},
		{Exporter: ExporterOTLPHTTP, Endpoint: "http://localhost:4318/v1/traces", Authorization: "Bearer fixture-token"},
		{Exporter: ExporterOTLPHTTP, Endpoint: "https://localhost:4318/v1/traces", Headers: headers, Insecure: true},
	} {
		if _, err := Initialize(t.Context(), config); !errors.Is(err, errOTLPAuthorizationRequiresHTTPS) {
			t.Fatalf("expected verified HTTPS requirement, got %v", err)
		}
	}
}

func TestOTLPAuthorizationFromEnvironment(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", ExporterOTLPHTTP)
	t.Setenv("TELEMETRY_AUTHORIZATION", "Bearer fixture-token")
	config, err := ConfigFromEnvironment("test", "1")
	if err != nil || config.Authorization != "Bearer fixture-token" {
		t.Fatal("expected the site authorization to configure the trace exporter")
	}
	t.Setenv("TELEMETRY_AUTHORIZATION", "fixture-invalid-secret")
	if _, err := ConfigFromEnvironment("test", "1"); !errors.Is(err, errInvalidOTLPAuthorization) {
		t.Fatal("expected a sanitized error for malformed authorization")
	}
	t.Setenv("OTEL_TRACES_EXPORTER", ExporterDisabled)
	if _, err := ConfigFromEnvironment("test", "1"); err != nil {
		t.Fatal("disabled exporter must ignore the site authorization")
	}
}
