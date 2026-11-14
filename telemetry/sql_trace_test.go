package telemetry

import (
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestSQLExecutionTraceRepresentsOneCallWithoutLiteralData(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()); otel.SetTracerProvider(previous) })

	base := &telemetryConn{valid: true}
	connection := &observedConn{Conn: base, role: "runtime", metrics: NewSQLMetrics()}
	query := "/* tenant=secret */ SELECT id FROM accounts WHERE email = 'alice@example.com' AND id = $1 LIMIT 25"
	rows, err := connection.QueryContext(t.Context(), query, []driver.NamedValue{{Ordinal: 1, Value: "customer-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := rows.Next(nil); err != io.EOF {
		t.Fatalf("Next() error = %v, want EOF", err)
	}

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended SQL spans = %d, want one per real call", len(ended))
	}
	span := ended[0]
	if span.Name() != "SQL select" || span.SpanKind() != trace.SpanKindClient {
		t.Fatalf("span name/kind = %q/%s", span.Name(), span.SpanKind())
	}
	attributes := traceAttributeMap(span)
	template := fmt.Sprint(attributes["db.query.summary"])
	if template != "SELECT id FROM accounts WHERE email = ? AND id = ? LIMIT ?" {
		t.Fatalf("safe SQL template = %q", template)
	}
	if fmt.Sprint(attributes["db.query.fingerprint"]) == "" || fmt.Sprint(attributes["db.operation.name"]) != "select" {
		t.Fatalf("SQL identity attributes = %#v", attributes)
	}
	encoded := fmt.Sprint(attributes)
	for _, forbidden := range []string{"tenant=secret", "alice@example.com", "customer-secret"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("SQL span leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestSQLExecutionTraceMarksFailedCallWithoutErrorText(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()); otel.SetTracerProvider(previous) })

	base := &telemetryConn{valid: true, err: fakeSQLError("password=do-not-export")}
	connection := &observedConn{Conn: base, role: "runtime", metrics: NewSQLMetrics()}
	_, _ = connection.ExecContext(t.Context(), "UPDATE accounts SET active = false WHERE id = 9", nil)
	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Status().Code != codes.Error || ended[0].Status().Description != "sql_error" {
		t.Fatalf("failed SQL spans = %#v", ended)
	}
	encoded := fmt.Sprint(traceAttributeMap(ended[0]))
	if strings.Contains(encoded, "do-not-export") {
		t.Fatalf("SQL error text leaked into span: %s", encoded)
	}
}

func TestSanitizeSQLTraceTemplateBoundsAndRemovesLiterals(t *testing.T) {
	input := "-- secret comment\nSELECT * FROM customers WHERE token = $$top-secret$$ AND age = 42 AND name = 'private' " + strings.Repeat("AND safe_identifier = safe_identifier ", 40)
	got := sanitizeSQLTraceTemplate(input)
	for _, forbidden := range []string{"secret comment", "top-secret", "42", "private"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("safe SQL template leaked %q: %q", forbidden, got)
		}
	}
	if len([]rune(got)) > maxSQLTraceTemplateRunes || !strings.HasPrefix(got, "SELECT * FROM customers") {
		t.Fatalf("unexpected safe SQL template: %q", got)
	}
}

func traceAttributeMap(span sdktrace.ReadOnlySpan) map[string]any {
	result := map[string]any{}
	for _, pair := range span.Attributes() {
		result[string(pair.Key)] = pair.Value.AsInterface()
	}
	return result
}
