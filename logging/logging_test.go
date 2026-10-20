package logging

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewReadsEnvironmentConfiguration(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_ENCODING", "console")
	t.Setenv("APP_ENV", "test")
	t.Setenv("DOMAINRY_DEPLOYMENT_ID", "deployment-test")
	t.Setenv("DOMAINRY_INSTANCE_ID", "instance-test")
	logger, err := New(ConfigFromEnvironment("runtime-test", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logger.Sync() }()
	if !logger.Core().Enabled(zapcore.DebugLevel) {
		t.Fatal("debug logging was not enabled")
	}
	config := ConfigFromEnvironment("runtime-test", "v1")
	if config.Environment != "test" || config.DeploymentID != "deployment-test" || config.InstanceID != "instance-test" {
		t.Fatalf("environment config=%+v", config)
	}
	built, err := zapConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if built.InitialFields["service"] != "runtime-test" || built.InitialFields["version"] != "v1" || built.InitialFields["environment"] != "test" || built.InitialFields["deployment_id"] != "deployment-test" || built.InitialFields["instance_id"] != "instance-test" {
		t.Fatalf("process fields=%v", built.InitialFields)
	}
	if built.Sampling == nil || built.Sampling.Initial != defaultSamplingInitial || built.Sampling.Thereafter != defaultSamplingThereafter {
		t.Fatalf("sampling=%+v", built.Sampling)
	}
	if !built.DisableStacktrace {
		t.Fatal("automatic Zap stacktraces must remain disabled")
	}
}

func TestErrorLogDoesNotIncludeAutomaticBoundaryStacktrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.log")
	logger, err := New(Config{ServiceName: "runtime-test", OutputPaths: []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	logger.Error("classified_failure", zap.String("error_code", "backend.test.failed"))
	if err := Sync(logger); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), `"stacktrace"`) || strings.Contains(string(contents), `"stack"`) {
		t.Fatalf("automatic boundary stack leaked into error log: %s", contents)
	}
}

func TestLoggingDefaultsInitializationAndNilErrorFields(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_ENCODING", "")
	logger, err := New(Config{ServiceName: "runtime-test"})
	if err != nil || !logger.Core().Enabled(zapcore.InfoLevel) {
		t.Fatalf("default logger=%v error=%v", logger, err)
	}
	_ = logger.Sync()
	if fields := ErrorFields(nil, Failure{}); fields != nil {
		t.Fatalf("nil error fields = %#v", fields)
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("blank service name was accepted")
	}

	previous := zap.L()
	initialized, err := Initialize(Config{ServiceName: " runtime "})
	if err != nil || zap.L() != initialized {
		t.Fatalf("initialized logger=%v error=%v", initialized, err)
	}
	zap.ReplaceGlobals(previous)

	t.Setenv("LOG_LEVEL", "invalid")
	if _, err := Initialize(ConfigFromEnvironment("runtime", "development")); err == nil {
		t.Fatal("invalid global logger configuration accepted")
	}
}

func TestNewRejectsInvalidEnvironmentConfiguration(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")
	if _, err := New(ConfigFromEnvironment("runtime-test", "development")); err == nil {
		t.Fatal("invalid log level was accepted")
	}
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_ENCODING", "xml")
	if _, err := New(ConfigFromEnvironment("runtime-test", "development")); err == nil {
		t.Fatal("invalid log encoding was accepted")
	}
}

func TestValidateChecksContractWithoutBuildingLogger(t *testing.T) {
	if err := Validate(Config{ServiceName: "runtime-test", Level: "debug", Encoding: "console"}); err != nil {
		t.Fatalf("valid contract: %v", err)
	}
	for _, config := range []Config{
		{ServiceName: "", Level: "info", Encoding: "json"},
		{ServiceName: "runtime-test", Level: "verbose", Encoding: "json"},
		{ServiceName: "runtime-test", Level: "info", Encoding: "xml"},
	} {
		if err := Validate(config); err == nil {
			t.Fatalf("invalid contract accepted: %+v", config)
		}
	}
}

func TestNewSupportsProcessOwnedOutputSelection(t *testing.T) {
	config, err := zapConfig(Config{ServiceName: "evaluation-cli", OutputPaths: []string{" stderr ", ""}, ErrorOutputPaths: []string{" stderr "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(config.OutputPaths) != 1 || config.OutputPaths[0] != "stderr" || len(config.ErrorOutputPaths) != 1 || config.ErrorOutputPaths[0] != "stderr" {
		t.Fatalf("output paths=%v error paths=%v", config.OutputPaths, config.ErrorOutputPaths)
	}
}

func TestFromContextAddsRequestID(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	ctx := requestcontext.WithRequestID(context.Background(), "req-123")
	ctx = requestcontext.WithCorrelationID(ctx, "corr-123")
	ctx = requestcontext.WithWorkspaceID(ctx, "workspace-a")
	ctx = requestcontext.WithActorID(ctx, "user-a")
	FromContext(ctx).Info("handled")
	entries := observed.All()
	fields := entries[0].ContextMap()
	if len(entries) != 1 || fields["request_id"] != "req-123" || fields["correlation_id"] != "corr-123" || fields["workspace_id"] != "workspace-a" || fields["actor_id"] != "user-a" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestHostedModuleChildrenShareProcessSink(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previous) })

	for _, module := range []string{"identity", "notification"} {
		ctx := requestcontext.WithRequestID(context.Background(), "req-"+module)
		FromContext(ctx).Info("module_event", zap.String("owner", module))
	}
	entries := observed.FilterMessage("module_event").All()
	if len(entries) != 2 {
		t.Fatalf("shared sink entries=%#v", entries)
	}
	for index, owner := range []string{"identity", "notification"} {
		fields := entries[index].ContextMap()
		if fields["owner"] != owner || fields["request_id"] != "req-"+owner {
			t.Fatalf("entry %d fields=%#v", index, fields)
		}
	}
}

func TestFieldsAreFilteredAndStable(t *testing.T) {
	fields := Fields(map[string]any{" ": "ignored", "z": 1, "empty": "", "a": true})
	if len(fields) != 2 || fields[0].Key != "a" || fields[1].Key != "z" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestSensitiveTokenFieldVariants(t *testing.T) {
	for _, key := range []string{"access_token_value", "refresh_token_value", "api_token_value", "bearer_token_value"} {
		if !sensitiveFieldKey(key) {
			t.Fatalf("sensitive token key accepted: %q", key)
		}
	}
	if sensitiveFieldKey("token_count") {
		t.Fatal("non-sensitive token metadata was rejected")
	}
}

func TestFieldsRedactSensitiveKeysRecursively(t *testing.T) {
	type unsafeDetails struct {
		Password string
	}
	fields := Fields(map[string]any{
		"authorization": "Bearer secret", "payload": map[string]any{"name": "hidden"},
		"error": "sql=SELECT secret", "raw_error": "hidden", "panic": "credential",
		"details":    map[string]any{"status": "failed", "password": "hidden", "nested": []any{map[string]any{"token": "hidden", "code": "stable"}}},
		"string_map": map[string]string{"credential": "hidden", "result": "bounded"},
		"struct":     unsafeDetails{Password: "hidden"}, "binary": []byte("hidden"),
	})
	if len(fields) != 4 || fields[0].Key != "binary" || fields[1].Key != "details" || fields[2].Key != "string_map" || fields[3].Key != "struct" {
		t.Fatalf("fields=%#v", fields)
	}
	core, observed := observer.New(zapcore.InfoLevel)
	zap.New(core).Info("redaction", fields...)
	encoded := observed.All()[0].ContextMap()
	text := fmt.Sprint(encoded)
	if strings.Contains(text, "hidden") || strings.Contains(strings.ToLower(text), "password") || !strings.Contains(text, "stable") || !strings.Contains(text, "bounded") {
		t.Fatalf("redacted fields=%s", text)
	}
}

func TestLogIdempotencyEmitsCanonicalCorrelationFieldsWithoutRawKey(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	LogIdempotency(context.Background(), idempotency.AuditFacts{WorkspaceID: "workspace-a", Scope: "record.create", Key: "raw-secret-key", Status: "replayed", FencingToken: 9}, "request-a")
	entries := observed.All()
	if len(entries) != 1 || entries[0].Message != "idempotency_receipt" {
		t.Fatalf("entries=%#v", entries)
	}
	fields := entries[0].ContextMap()
	if fields["workspace_id"] != "workspace-a" || fields["idempotency_scope"] != "record.create" || fields["idempotency_status"] != "replayed" || fields["request_id"] != "request-a" || fields["fencing_token"] != int64(9) || fields["idempotency_key_hash"] == "raw-secret-key" {
		t.Fatalf("fields=%#v", fields)
	}
	ctx := requestcontext.WithRequestID(context.Background(), "request-from-context")
	LogIdempotency(ctx, idempotency.AuditFacts{WorkspaceID: "workspace-a", Scope: "record.create", Key: "other-key", Status: "acquired"}, " ")
	entries = observed.All()
	if len(entries) != 2 || entries[1].ContextMap()["request_id"] != "request-from-context" {
		t.Fatalf("context request fields=%#v", entries)
	}
}

func TestLogIdempotencyDefaultsRequestIDFromContext(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	ctx := requestcontext.WithRequestID(context.Background(), "request-from-context")
	LogIdempotency(ctx, idempotency.AuditFacts{WorkspaceID: "workspace", Scope: "record.create", Key: "key", Status: "acquired"}, " ")
	fields := observed.All()[0].ContextMap()
	if fields["request_id"] != "request-from-context" {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestErrorFieldsExcludeRawErrorTextAndIncludeStableFailureContext(t *testing.T) {
	fields := ErrorFields(fmt.Errorf("password=secret sql=SELECT * FROM users"), Failure{Stage: "database_query", Dependency: "primary_database", Retryable: true})
	core, observed := observer.New(zapcore.ErrorLevel)
	zap.New(core).Error("failed", fields...)
	values := observed.All()[0].ContextMap()
	text := fmt.Sprint(values)
	if strings.Contains(text, "secret") || strings.Contains(text, "SELECT") || !strings.Contains(text, "backend.internal") {
		t.Fatalf("unstable error fields=%s", text)
	}
	if values["failure_stage"] != "database_query" || values["dependency"] != "primary_database" || values["retryable"] != true {
		t.Fatalf("failure fields=%v", values)
	}
	if !strings.Contains(fmt.Sprint(values["stack"]), "TestErrorFieldsExcludeRawErrorTextAndIncludeStableFailureContext") {
		t.Fatalf("internal source stack=%v", values["stack"])
	}
}

func TestErrorFieldsDoNotAttachStackToClassifiedExpectedFailure(t *testing.T) {
	err := apperror.New(apperror.KindBadRequest, "backend.request.invalid", fmt.Errorf("invalid input"), nil)
	fields := ErrorFields(err, Failure{Stage: "request_validation"})
	core, observed := observer.New(zapcore.ErrorLevel)
	zap.New(core).Error("failed", fields...)
	values := observed.All()[0].ContextMap()
	if _, exists := values["stack"]; exists {
		t.Fatalf("expected failure contains stack=%v", values["stack"])
	}
}

func TestProcessLifecycleUsesCanonicalEvents(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previous := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(previous)

	ProcessStarted(t.Context(), map[string]any{"module_count": 3})
	ProcessStopping(t.Context(), nil)
	ProcessStopped(t.Context(), nil)
	ProcessStartFailed(t.Context(), fmt.Errorf("secret startup detail"), Failure{Stage: "module_assembly"})

	entries := observed.All()
	if len(entries) != 4 || entries[0].Message != "process_started" || entries[1].Message != "process_stopping" || entries[2].Message != "process_stopped" || entries[3].Message != "process_start_failed" {
		t.Fatalf("process events=%#v", entries)
	}
	if entries[0].ContextMap()["module_count"] != int64(3) || entries[3].ContextMap()["failure_stage"] != "module_assembly" {
		t.Fatalf("process fields=%#v", entries)
	}
}
