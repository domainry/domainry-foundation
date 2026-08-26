package telemetry

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestSQLMetricsNilBoundsOperationsAndOutcomes(t *testing.T) {
	var nilMetrics *SQLMetrics
	nilMetrics.ObserveQuery("runtime", "select", 0, nil)
	nilMetrics.ObserveTransaction("runtime", "commit", 0, nil)
	if nilMetrics.OpenMetrics(t.Context()) != "" {
		t.Fatal("nil SQL metrics emitted output")
	}

	metrics := NewSQLMetrics()
	metrics.ObserveTransaction(" unknown ", "unknown", -time.Second, nil)
	metrics.ObserveTransaction("migration", "commit", time.Millisecond, errors.New("failure"))
	for _, test := range []struct {
		err     error
		outcome string
	}{
		{err: nil, outcome: "success"},
		{err: context.Canceled, outcome: "timeout"},
		{err: context.DeadlineExceeded, outcome: "timeout"},
		{err: mutation.TransactionTransient("table", "id", mutation.TransactionTransientDeadlock, nil), outcome: "deadlock"},
		{err: mutation.TransactionTransient("table", "id", mutation.TransactionTransientSerializationFailure, nil), outcome: "serialization_failure"},
		{err: mutation.TransactionTransient("table", "id", mutation.TransactionTransientLockTimeout, nil), outcome: "lock_timeout"},
		{err: mutation.MutationConflict("table", "id", mutation.MutationConflictUnique, nil), outcome: "conflict"},
		{err: errors.New("SQLSTATE 40P01"), outcome: "deadlock"},
		{err: errors.New("error 1213"), outcome: "deadlock"},
		{err: errors.New("serialization failure"), outcome: "serialization_failure"},
		{err: errors.New("SQLSTATE 40001"), outcome: "serialization_failure"},
		{err: errors.New("database is locked"), outcome: "lock_timeout"},
		{err: errors.New("lock timeout"), outcome: "lock_timeout"},
		{err: errors.New("SQLITE_BUSY"), outcome: "lock_timeout"},
		{err: errors.New("duplicate constraint"), outcome: "conflict"},
		{err: errors.New("duplicate key"), outcome: "conflict"},
		{err: errors.New("SQLSTATE 23505"), outcome: "conflict"},
		{err: errors.New("connection reset"), outcome: "error"},
	} {
		if got := sqlOutcome(test.err); got != test.outcome {
			t.Fatalf("sqlOutcome(%v)=%q want %q", test.err, got, test.outcome)
		}
		metrics.ObserveQuery("migration", "other", time.Millisecond, test.err)
	}
	output := metrics.OpenMetrics(t.Context())
	if !strings.Contains(output, `role="runtime",operation="transaction",outcome="error"`) || !strings.Contains(output, `role="migration",operation="other",outcome="conflict"`) {
		t.Fatalf("bounded SQL metrics missing:\n%s", output)
	}
	nilMetrics.observe(nil, "runtime", "select", "success", time.Millisecond)
}

func TestSQLOperationAndMetricCloneEdges(t *testing.T) {
	for query, want := range map[string]string{
		"": "other", " SELECT 1": "select", "WITH rows AS (SELECT 1) SELECT * FROM rows": "select", "SHOW tables": "select", "EXPLAIN SELECT 1": "select",
		"INSERT INTO x VALUES (1)": "insert", "UPDATE x SET y=1": "update", "UPSERT x": "update", "DELETE FROM x": "delete",
		"CREATE TABLE x (id INT)": "ddl", "ALTER TABLE x": "ddl", "DROP TABLE x": "ddl", "TRUNCATE x": "ddl", "PRAGMA busy_timeout": "pragma", "VACUUM": "other",
	} {
		if got := sqlOperation(query); got != want {
			t.Fatalf("sqlOperation(%q)=%q want %q", query, got, want)
		}
	}
	if boundedSQLRole(" migration ") != "migration" || boundedSQLRole("unknown") != "runtime" {
		t.Fatal("SQL role bounding mismatch")
	}
	for _, operation := range []string{"select", "insert", "update", "delete", "ddl", "pragma", "transaction"} {
		if boundedSQLOperation(" "+operation+" ") != operation {
			t.Fatalf("operation %q was not retained", operation)
		}
	}

	source := map[string]sqlMetricSeries{
		"b": {Role: "runtime", Operation: "select", Outcome: "success", Buckets: []uint64{1}},
		"a": {Role: "migration", Operation: "update", Outcome: "error", Buckets: []uint64{2}},
	}
	clone := cloneSQLSeries(source)
	clone[0].Buckets[0] = 99
	if len(clone) != 2 || clone[0].Role != "migration" || source["a"].Buckets[0] != 2 {
		t.Fatalf("metric clone=%+v source=%+v", clone, source)
	}
}

func TestTelemetryHeaderAsyncAndUseCaseFallbackEdges(t *testing.T) {
	headers := cloneHeaders(map[string]string{" Authorization ": " token ", " ": "ignored"})
	if len(headers) != 1 || headers["Authorization"] != "token" {
		t.Fatalf("headers=%v", headers)
	}
	if asyncString(nil) != "" || asyncString(" value ") != "value" {
		t.Fatal("async string normalization mismatch")
	}
	for value, want := range map[any]byte{byte(1): 1, float64(2): 2, int(3): 3, "4": 0} {
		if got := asyncTraceFlags(value); got != want {
			t.Fatalf("trace flags(%#v)=%d want %d", value, got, want)
		}
	}

	if got := EnsureAsyncPayload(t.Context(), nil); len(got) != 0 {
		t.Fatalf("empty async payload=%v", got)
	}
	requestContext := requestcontext.WithRequestID(t.Context(), "request-1")
	payload := EnsureAsyncPayload(requestContext, nil)
	link := payload[AsyncPayloadKey].(map[string]any)
	if link["correlation_id"] != "request-1" {
		t.Fatalf("request fallback link=%v", link)
	}
	EndUseCase(nil, errors.New("ignored"), "")
	_, success := StartUseCase(t.Context(), " success ")
	EndUseCase(success, nil, "")
	_, failed := StartUseCase(t.Context(), "failed")
	EndUseCase(failed, errors.New("failure"), "")
}

func TestInitializeExporterAliasesAndOptionalSettings(t *testing.T) {
	for _, exporter := range []string{"", "off"} {
		shutdown, err := Initialize(t.Context(), Config{Exporter: exporter})
		if err != nil {
			t.Fatalf("disabled exporter %q: %v", exporter, err)
		}
		if err := shutdown(t.Context()); err != nil {
			t.Fatalf("shutdown disabled exporter %q: %v", exporter, err)
		}
	}

	previousProvider := otel.GetTracerProvider()
	shutdown, err := Initialize(t.Context(), Config{
		Exporter:    "otlp",
		Headers:     map[string]string{" Authorization ": " token "},
		SampleRatio: 2,
	})
	if err != nil {
		t.Fatalf("initialize OTLP alias: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = shutdown(shutdownContext)
		otel.SetTracerProvider(previousProvider)
	})
}

func TestInitializeReportsExporterStartFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	certificate := server.Certificate()
	certificatePath := filepath.Join(t.TempDir(), "collector.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", certificatePath)
	if _, err := Initialize(t.Context(), Config{Exporter: ExporterOTLPHTTP, Insecure: true}); err == nil {
		t.Fatal("insecure exporter with TLS certificate was accepted")
	}
}

func TestEnsureAsyncPayloadWithTraceOnlyAndInvalidLink(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID})
	payload := EnsureAsyncPayload(trace.ContextWithSpanContext(t.Context(), spanContext), nil)
	if _, ok := payload[AsyncPayloadKey]; !ok {
		t.Fatalf("trace-only payload=%v", payload)
	}

	_, span := StartLinkedSpan(t.Context(), "test", "invalid-link", AsyncLink{
		TraceID: traceID.String(),
		SpanID:  "invalid",
	})
	span.End()
	_, span = StartLinkedSpan(t.Context(), "test", "invalid-trace", AsyncLink{
		TraceID: "invalid",
		SpanID:  spanID.String(),
	})
	span.End()
}
