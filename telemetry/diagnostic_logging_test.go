package telemetry

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestHTTPServerDiagnosticContentIsBoundedRedactedAndTransparent(t *testing.T) {
	restoreDiagnosticPolicy(t)
	if err := ConfigureDiagnosticLogging(DiagnosticLogPolicy{
		Enabled: true, Features: []string{DiagnosticFeatureHTTPServer}, HTTPContent: true, SQLStatement: true, MaxContentBytes: 1024,
	}); err != nil {
		t.Fatal(err)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /widgets", func(writer http.ResponseWriter, request *http.Request) {
		raw, err := io.ReadAll(request.Body)
		if err != nil || string(raw) != `{"name":"visible","password":"do-not-log"}` {
			t.Fatalf("handler body=%q err=%v", raw, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"widget-1","access_token":"do-not-log"}`))
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/widgets?page=2&token=query-secret", strings.NewReader(`{"name":"visible","password":"do-not-log"}`))
	request.Header.Set("Content-Type", "application/json")
	HTTPServerMiddleware(HTTPServerConfig{}, mux).ServeHTTP(recorder, request)
	if recorder.Body.String() != `{"id":"widget-1","access_token":"do-not-log"}` {
		t.Fatalf("response changed: %q", recorder.Body.String())
	}
	entries := observed.FilterMessage("http_request").All()
	if len(entries) != 1 {
		t.Fatalf("events=%#v", observed.All())
	}
	encoded := entries[0].ContextMap()
	diagnostic := strings.ToLower(strings.TrimSpace(toJSON(encoded)))
	for _, forbidden := range []string{"do-not-log", "query-secret", "password", "access_token", "token"} {
		if strings.Contains(diagnostic, forbidden) {
			t.Fatalf("sensitive value %q leaked: %s", forbidden, diagnostic)
		}
	}
	for _, expected := range []string{"visible", "widget-1", "page"} {
		if !strings.Contains(diagnostic, expected) {
			t.Fatalf("safe value %q missing: %s", expected, diagnostic)
		}
	}
}

func TestHTTPClientDiagnosticContentCompletesWhenBodyCloses(t *testing.T) {
	restoreDiagnosticPolicy(t)
	if err := ConfigureDiagnosticLogging(DiagnosticLogPolicy{
		Enabled: true, Features: []string{DiagnosticFeatureHTTPClient}, HTTPContent: true, MaxContentBytes: 1024,
	}); err != nil {
		t.Fatal(err)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	client := InstrumentHTTPClient(&http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		_, _ = io.ReadAll(request.Body)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"secret":"hidden"}`)),
			Request:    request,
		}, nil
	})}, "diagnostic.test")
	request, err := http.NewRequestWithContext(WithHTTPClientOperation(t.Context(), "widgets.create"), http.MethodPost, "https://api.example.test/widgets?token=hidden", bytes.NewBufferString(`{"name":"visible","secret":"hidden"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	entries := observed.FilterMessage("dependency_call").All()
	if len(entries) != 1 {
		t.Fatalf("events=%#v", observed.All())
	}
	if entries[0].ContextMap()[logging.LogTypeField] != logging.LogTypeInfrastructure {
		t.Fatalf("dependency log type=%v", entries[0].ContextMap()[logging.LogTypeField])
	}
	diagnostic := toJSON(entries[0].ContextMap())
	if strings.Contains(diagnostic, "hidden") || !strings.Contains(diagnostic, "visible") || !strings.Contains(diagnostic, "201") {
		t.Fatalf("unexpected diagnostic event: %s", diagnostic)
	}
}

func TestHTTPClientAdapterCanPermanentlyDisableDiagnosticContent(t *testing.T) {
	restoreDiagnosticPolicy(t)
	if err := ConfigureDiagnosticLogging(DiagnosticLogPolicy{
		Enabled: true, Features: []string{DiagnosticFeatureHTTPClient}, HTTPContent: true, MaxContentBytes: 1024,
	}); err != nil {
		t.Fatal(err)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	client := InstrumentHTTPClientWithOptions(&http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		_, _ = io.ReadAll(request.Body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"profile":"must-not-appear"}`)),
			Request:    request,
		}, nil
	})}, "diagnostic.sensitive", HTTPClientInstrumentationOptions{DisableDiagnosticContent: true})
	request, err := http.NewRequestWithContext(WithHTTPClientOperation(t.Context(), "identity.lookup"), http.MethodPost, "https://identity.example.test/token?code=must-not-appear", strings.NewReader(`{"credential":"must-not-appear"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	entries := observed.FilterMessage("dependency_call").All()
	if len(entries) != 1 {
		t.Fatalf("events=%#v", observed.All())
	}
	if entries[0].ContextMap()[logging.LogTypeField] != logging.LogTypeInfrastructure {
		t.Fatalf("dependency log type=%v", entries[0].ContextMap()[logging.LogTypeField])
	}
	diagnostic := toJSON(entries[0].ContextMap())
	if strings.Contains(diagnostic, "must-not-appear") || !strings.Contains(diagnostic, "identity.lookup") || !strings.Contains(diagnostic, "200") {
		t.Fatalf("unexpected metadata-only diagnostic event: %s", diagnostic)
	}
}

func TestDependencySamplingDropsOnlyFastSuccessfulDiagnostics(t *testing.T) {
	restoreDiagnosticPolicy(t)
	if err := ConfigureDiagnosticLogging(DiagnosticLogPolicy{
		Enabled: true, Features: []string{DiagnosticFeatureSQL}, MaxContentBytes: 1024,
		DependencySuccessSampleRate: 0.01, DependencySlowMilliseconds: 250,
	}); err != nil {
		t.Fatal(err)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	ctx := requestcontext.WithRequestID(t.Context(), "00000000000000000000000000000001")
	call := DependencyCall{Feature: DiagnosticFeatureSQL, Dependency: "database.runtime", Operation: "select"}

	RecordDependencyCall(ctx, call, DependencyResult{}, 5*time.Millisecond)
	RecordDependencyCall(ctx, call, DependencyResult{}, 250*time.Millisecond)
	RecordDependencyCall(ctx, call, DependencyResult{Err: errors.New("database unavailable")}, 5*time.Millisecond)

	entries := observed.FilterMessage("dependency_call").All()
	if len(entries) != 2 {
		t.Fatalf("dependency events=%#v, want slow success and error only", entries)
	}
	if entries[0].ContextMap()["outcome"] != "success" || entries[0].ContextMap()["duration_ms"] != int64(250) {
		t.Fatalf("slow dependency event=%#v", entries[0].ContextMap())
	}
	if entries[1].Level != zapcore.ErrorLevel || entries[1].ContextMap()["outcome"] != "error" {
		t.Fatalf("failed dependency event=%#v", entries[1])
	}
}

func TestDependencySamplingPolicyRejectsUnsafeBounds(t *testing.T) {
	base := DefaultDiagnosticLogPolicy()
	base.DependencySuccessSampleRate = 1.01
	if err := ConfigureDiagnosticLogging(base); err == nil || !strings.Contains(err.Error(), "LOG_DEPENDENCY_SUCCESS_SAMPLE_RATE") {
		t.Fatalf("invalid sample rate error=%v", err)
	}
	base = DefaultDiagnosticLogPolicy()
	base.DependencySlowMilliseconds = -1
	if err := ConfigureDiagnosticLogging(base); err == nil || !strings.Contains(err.Error(), "LOG_DEPENDENCY_SLOW_MS") {
		t.Fatalf("invalid slow threshold error=%v", err)
	}
}

func TestDiagnosticPolicyFileCanBeReloadedWithoutReinstallingObservers(t *testing.T) {
	restoreDiagnosticPolicy(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "diagnostic-policy.json")
	writePolicy := func(enabled bool) {
		raw := `{"enabled":` + strconvBool(enabled) + `,"features":["sql"],"http_content":false,"sql_statement":true,"max_content_bytes":2048}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy(false)
	stop, err := WatchDiagnosticLogPolicyFile(t.Context(), path, 5*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	if CurrentDiagnosticLogPolicy().Enabled {
		t.Fatal("initial file policy was not installed")
	}
	writePolicy(true)
	deadline := time.Now().Add(time.Second)
	for !CurrentDiagnosticLogPolicy().Enabled && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !CurrentDiagnosticLogPolicy().Enabled {
		t.Fatal("updated file policy was not installed")
	}
}

func TestObservedSQLLogsExecutableStatementAndResult(t *testing.T) {
	restoreDiagnosticPolicy(t)
	if err := ConfigureDiagnosticLogging(DiagnosticLogPolicy{
		Enabled: true, Features: []string{DiagnosticFeatureSQL}, SQLStatement: true, MaxContentBytes: 2048,
	}); err != nil {
		t.Fatal(err)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	base := &telemetryConn{valid: true}
	connection := &observedConn{Conn: base, role: "runtime", metrics: NewSQLMetrics()}
	query := `UPDATE accounts SET display_name = ? WHERE id = ?`
	if _, err := connection.ExecContext(t.Context(), query, []driver.NamedValue{{Ordinal: 1, Value: "visible-name"}, {Ordinal: 2, Value: "secret-id"}}); err != nil {
		t.Fatal(err)
	}
	if base.lastQuery != query || len(base.lastArgs) != 2 || base.lastArgs[0].Value != "visible-name" || base.lastArgs[1].Value != "secret-id" {
		t.Fatalf("underlying execution changed query=%q args=%#v", base.lastQuery, base.lastArgs)
	}
	entries := observed.FilterMessage("dependency_call").All()
	if len(entries) != 1 {
		t.Fatalf("events=%#v", observed.All())
	}
	if entries[0].ContextMap()[logging.LogTypeField] != logging.LogTypeInfrastructure {
		t.Fatalf("dependency log type=%v", entries[0].ContextMap()[logging.LogTypeField])
	}
	diagnostic := toJSON(entries[0].ContextMap())
	executable := `UPDATE accounts SET display_name = 'visible-name' WHERE id = 'secret-id'`
	if !strings.Contains(diagnostic, executable) || !strings.Contains(diagnostic, "rows_affected") {
		t.Fatalf("SQL statement/result missing: %s", diagnostic)
	}
	if !strings.Contains(diagnostic, `"statement_template":"UPDATE accounts SET display_name = ? WHERE id = ?"`) || !strings.Contains(diagnostic, `"statement_fingerprint":"`) {
		t.Fatalf("safe SQL identity missing: %s", diagnostic)
	}
}

func restoreDiagnosticPolicy(t *testing.T) {
	t.Helper()
	previous := CurrentDiagnosticLogPolicy()
	t.Cleanup(func() {
		if err := ConfigureDiagnosticLogging(previous); err != nil {
			t.Errorf("restore diagnostic policy: %v", err)
		}
	})
}

func strconvBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func toJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
