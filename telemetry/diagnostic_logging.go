package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	DiagnosticFeatureHTTPServer  = "http_server"
	DiagnosticFeatureHTTPClient  = "http_client"
	DiagnosticFeatureSQL         = "sql"
	DiagnosticFeatureRedis       = "redis"
	DiagnosticFeatureObjectStore = "object_store"
	DiagnosticFeatureSMTP        = "smtp"
	DiagnosticFeatureMQTT        = "mqtt"
	DiagnosticFeatureFilesystem  = "filesystem"
	DiagnosticFeatureProcess     = "process"
	DiagnosticFeatureScanner     = "scanner"

	defaultDiagnosticContentBytes = 16 << 10
	maxDiagnosticContentBytes     = 64 << 10
	defaultDependencySlowMS       = 250
	defaultDependencySuccessRate  = 1.0
)

var diagnosticFeatures = []string{
	DiagnosticFeatureHTTPServer,
	DiagnosticFeatureHTTPClient,
	DiagnosticFeatureSQL,
	DiagnosticFeatureRedis,
	DiagnosticFeatureObjectStore,
	DiagnosticFeatureSMTP,
	DiagnosticFeatureMQTT,
	DiagnosticFeatureFilesystem,
	DiagnosticFeatureProcess,
	DiagnosticFeatureScanner,
}

// DiagnosticLogPolicy is the process-wide switchboard for diagnostic boundary
// events. Content switches never override the hard redaction rules enforced by
// the individual observers.
type DiagnosticLogPolicy struct {
	Enabled                     bool     `json:"enabled"`
	Features                    []string `json:"features"`
	HTTPContent                 bool     `json:"http_content"`
	SQLStatement                bool     `json:"sql_statement"`
	MaxContentBytes             int      `json:"max_content_bytes"`
	DependencySuccessSampleRate float64  `json:"dependency_success_sample_rate"`
	DependencySlowMilliseconds  int64    `json:"dependency_slow_ms"`
}

type diagnosticPolicySnapshot struct {
	DiagnosticLogPolicy
	features map[string]bool
}

var activeDiagnosticPolicy atomic.Pointer[diagnosticPolicySnapshot]

func init() {
	policy := DefaultDiagnosticLogPolicy()
	snapshot, _ := diagnosticPolicySnapshotFor(policy)
	activeDiagnosticPolicy.Store(snapshot)
}

// DefaultDiagnosticLogPolicy records every supported boundary and executable
// SQL statement. HTTP content remains opt-in until a process or generated
// product explicitly enables its bounded, redacted capture.
func DefaultDiagnosticLogPolicy() DiagnosticLogPolicy {
	return DiagnosticLogPolicy{
		Enabled:                     true,
		Features:                    append([]string(nil), diagnosticFeatures...),
		HTTPContent:                 false,
		SQLStatement:                true,
		MaxContentBytes:             defaultDiagnosticContentBytes,
		DependencySuccessSampleRate: defaultDependencySuccessRate,
		DependencySlowMilliseconds:  defaultDependencySlowMS,
	}
}

// DiagnosticLogPolicyFromEnvironment returns the process bootstrap policy.
// Runtime changes use ConfigureDiagnosticLogging or WatchDiagnosticLogPolicyFile
// rather than reconstructing clients, routers, or database pools.
func DiagnosticLogPolicyFromEnvironment() (DiagnosticLogPolicy, error) {
	policy := DefaultDiagnosticLogPolicy()
	var joined error
	if raw := strings.TrimSpace(os.Getenv("LOG_DIAGNOSTIC_ENABLED")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("invalid LOG_DIAGNOSTIC_ENABLED %q: must be true or false", raw))
		} else {
			policy.Enabled = value
		}
	}
	if raw, found := os.LookupEnv("LOG_DIAGNOSTIC_FEATURES"); found {
		policy.Features = splitDiagnosticFeatures(raw)
	}
	for _, item := range []struct {
		name   string
		target *bool
	}{
		{name: "LOG_HTTP_CONTENT", target: &policy.HTTPContent},
		{name: "LOG_SQL_STATEMENT", target: &policy.SQLStatement},
	} {
		if raw := strings.TrimSpace(os.Getenv(item.name)); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				joined = errors.Join(joined, fmt.Errorf("invalid %s %q: must be true or false", item.name, raw))
			} else {
				*item.target = value
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LOG_CONTENT_MAX_BYTES")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			joined = errors.Join(joined, fmt.Errorf("invalid LOG_CONTENT_MAX_BYTES %q: must be an integer", raw))
		} else {
			policy.MaxContentBytes = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LOG_DEPENDENCY_SUCCESS_SAMPLE_RATE")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value <= 0 || value > 1 {
			joined = errors.Join(joined, fmt.Errorf("invalid LOG_DEPENDENCY_SUCCESS_SAMPLE_RATE %q: must be greater than 0 and at most 1", raw))
		} else {
			policy.DependencySuccessSampleRate = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LOG_DEPENDENCY_SLOW_MS")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			joined = errors.Join(joined, fmt.Errorf("invalid LOG_DEPENDENCY_SLOW_MS %q: must be a positive integer", raw))
		} else {
			policy.DependencySlowMilliseconds = value
		}
	}
	if joined != nil {
		return DiagnosticLogPolicy{}, joined
	}
	if _, err := diagnosticPolicySnapshotFor(policy); err != nil {
		return DiagnosticLogPolicy{}, err
	}
	return policy, nil
}

func splitDiagnosticFeatures(raw string) []string {
	values := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// ConfigureDiagnosticLogging atomically changes the policy used by all
// already-installed Foundation observers in this process.
func ConfigureDiagnosticLogging(policy DiagnosticLogPolicy) error {
	snapshot, err := diagnosticPolicySnapshotFor(policy)
	if err != nil {
		return err
	}
	activeDiagnosticPolicy.Store(snapshot)
	return nil
}

// CurrentDiagnosticLogPolicy returns a detached, deterministic copy.
func CurrentDiagnosticLogPolicy() DiagnosticLogPolicy {
	snapshot := currentDiagnosticPolicy()
	policy := snapshot.DiagnosticLogPolicy
	policy.Features = append([]string(nil), policy.Features...)
	return policy
}

func currentDiagnosticPolicy() *diagnosticPolicySnapshot {
	if snapshot := activeDiagnosticPolicy.Load(); snapshot != nil {
		return snapshot
	}
	policy := DefaultDiagnosticLogPolicy()
	snapshot, _ := diagnosticPolicySnapshotFor(policy)
	return snapshot
}

func diagnosticPolicySnapshotFor(policy DiagnosticLogPolicy) (*diagnosticPolicySnapshot, error) {
	if policy.MaxContentBytes == 0 {
		policy.MaxContentBytes = defaultDiagnosticContentBytes
	}
	if policy.MaxContentBytes < 1 || policy.MaxContentBytes > maxDiagnosticContentBytes {
		return nil, fmt.Errorf("LOG_CONTENT_MAX_BYTES must be between 1 and %d", maxDiagnosticContentBytes)
	}
	if policy.DependencySuccessSampleRate == 0 {
		policy.DependencySuccessSampleRate = defaultDependencySuccessRate
	}
	if policy.DependencySuccessSampleRate <= 0 || policy.DependencySuccessSampleRate > 1 {
		return nil, errors.New("LOG_DEPENDENCY_SUCCESS_SAMPLE_RATE must be greater than 0 and at most 1")
	}
	if policy.DependencySlowMilliseconds == 0 {
		policy.DependencySlowMilliseconds = defaultDependencySlowMS
	}
	if policy.DependencySlowMilliseconds < 1 {
		return nil, errors.New("LOG_DEPENDENCY_SLOW_MS must be a positive integer")
	}
	allowed := make(map[string]bool, len(diagnosticFeatures))
	for _, feature := range diagnosticFeatures {
		allowed[feature] = true
	}
	features := make(map[string]bool, len(policy.Features))
	for _, raw := range policy.Features {
		feature := strings.ToLower(strings.TrimSpace(raw))
		if !allowed[feature] {
			return nil, fmt.Errorf("unsupported diagnostic log feature %q", raw)
		}
		features[feature] = true
	}
	policy.Features = make([]string, 0, len(features))
	for feature := range features {
		policy.Features = append(policy.Features, feature)
	}
	sort.Strings(policy.Features)
	return &diagnosticPolicySnapshot{DiagnosticLogPolicy: policy, features: features}, nil
}

func diagnosticFeatureEnabled(feature string) bool {
	policy := currentDiagnosticPolicy()
	return policy.Enabled && policy.features[strings.ToLower(strings.TrimSpace(feature))]
}

// LoadDiagnosticLogPolicyFile reads and validates a complete policy document.
func LoadDiagnosticLogPolicyFile(path string) (DiagnosticLogPolicy, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return DiagnosticLogPolicy{}, errors.New("diagnostic log policy path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return DiagnosticLogPolicy{}, fmt.Errorf("read diagnostic log policy: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var policy DiagnosticLogPolicy
	if err := decoder.Decode(&policy); err != nil {
		return DiagnosticLogPolicy{}, fmt.Errorf("decode diagnostic log policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return DiagnosticLogPolicy{}, errors.New("decode diagnostic log policy: multiple JSON values")
	}
	if _, err := diagnosticPolicySnapshotFor(policy); err != nil {
		return DiagnosticLogPolicy{}, err
	}
	return policy, nil
}

// WatchDiagnosticLogPolicyFile reloads a policy without reconstructing any
// instrumented resource. Invalid revisions are reported once and the previous
// valid snapshot remains active.
func WatchDiagnosticLogPolicyFile(parent context.Context, path string, interval time.Duration) (func(), error) {
	policy, err := LoadDiagnosticLogPolicyFile(path)
	if err != nil {
		return nil, err
	}
	if err := ConfigureDiagnosticLogging(policy); err != nil {
		return nil, err
	}
	if parent == nil {
		parent = context.Background()
	}
	if interval <= 0 {
		interval = time.Second
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	var stopOnce sync.Once
	lastDigest, err := diagnosticPolicyFileDigest(path)
	if err != nil {
		cancel()
		return nil, err
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				digest, digestErr := diagnosticPolicyFileDigest(path)
				if digestErr != nil || digest == lastDigest {
					continue
				}
				lastDigest = digest
				next, loadErr := LoadDiagnosticLogPolicyFile(path)
				if loadErr != nil {
					logging.FromContext(ctx).Warn("diagnostic_log_policy_rejected",
						zap.String("error_code", "diagnostic_policy.invalid"),
						zap.String("policy_digest", digest),
					)
					continue
				}
				if configureErr := ConfigureDiagnosticLogging(next); configureErr != nil {
					continue
				}
				logging.FromContext(ctx).Info("diagnostic_log_policy_changed", logging.Fields(map[string]any{
					"enabled": next.Enabled, "features": next.Features, "http_content": next.HTTPContent,
					"sql_statement": next.SQLStatement, "max_content_bytes": next.MaxContentBytes,
					"dependency_success_sample_rate": next.DependencySuccessSampleRate,
					"dependency_slow_ms":             next.DependencySlowMilliseconds,
					"policy_digest":                  digest,
				})...)
			}
		}
	}()
	return func() {
		stopOnce.Do(func() {
			cancel()
			<-done
		})
	}, nil
}

func diagnosticPolicyFileDigest(path string) (string, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// DependencyCall and DependencyResult form the shared completion event for
// non-HTTP infrastructure adapters. Input and Output must contain only safe,
// bounded facts; logging.Fields applies a second recursive secret filter.
type DependencyCall struct {
	Feature    string
	Dependency string
	Operation  string
	Input      map[string]any
}

type DependencyResult struct {
	Outcome string
	Output  map[string]any
	Err     error
}

// RecordDependencyCall emits one terminal infrastructure event.
func RecordDependencyCall(ctx context.Context, call DependencyCall, result DependencyResult, duration time.Duration) {
	if !diagnosticFeatureEnabled(call.Feature) {
		return
	}
	outcome := boundedDependencyValue(result.Outcome)
	if outcome == "" {
		if result.Err != nil {
			outcome = "error"
		} else {
			outcome = "success"
		}
	}
	if result.Err == nil && outcome == "success" && !recordSuccessfulDependency(ctx, call, duration) {
		return
	}
	caller := dependencyBusinessCaller()
	fields := logging.Fields(map[string]any{
		"dependency_kind": boundedDependencyValue(call.Feature),
		"dependency":      boundedDependencyValue(call.Dependency),
		"operation":       boundedDependencyValue(call.Operation),
		"outcome":         outcome,
		"duration_ms":     duration.Milliseconds(),
		"input":           call.Input,
		"output":          result.Output,
	})
	logger := logging.InfrastructureLogger(ctx)
	if result.Err != nil {
		fields = append(fields, zap.String("error_kind", dependencyErrorKind(result.Err)), zap.String("error_code", "dependency.call_failed"))
		writeDependencyLog(logger, zapcore.ErrorLevel, caller, fields)
		return
	}
	writeDependencyLog(logger, zapcore.InfoLevel, caller, fields)
}

var dependencySuccessCounter atomic.Uint64

// recordSuccessfulDependency keeps every slow call and deterministically
// samples fast successes by trace. Metrics remain unsampled; this function
// only bounds verbose per-execution diagnostic logs. A trace-level decision
// keeps the dependency evidence internally coherent instead of retaining a
// random subset of SQL spans from the same request.
func recordSuccessfulDependency(ctx context.Context, call DependencyCall, duration time.Duration) bool {
	policy := currentDiagnosticPolicy()
	if duration >= time.Duration(policy.DependencySlowMilliseconds)*time.Millisecond {
		return true
	}
	if policy.DependencySuccessSampleRate >= 1 {
		return true
	}
	identity := requestcontext.RequestID(ctx)
	if identity == "" {
		identity = strconv.FormatUint(dependencySuccessCounter.Add(1), 10)
	}
	digest := sha256.Sum256([]byte(identity + "\x00" + boundedDependencyValue(call.Dependency) + "\x00" + boundedDependencyValue(call.Operation)))
	// Use the high 53 bits so conversion to float64 is exact. The decision is
	// stable for a trace/dependency/operation tuple and contains no identifier
	// in emitted telemetry.
	sample := float64(binary.BigEndian.Uint64(digest[:8])>>11) / float64(uint64(1)<<53)
	return sample < policy.DependencySuccessSampleRate
}

// dependencyBusinessCaller resolves the first source-owned frame above the
// generic diagnostic boundary. Zap's default caller is RecordDependencyCall,
// which identifies the logging adapter rather than the repository, use case,
// or handler that initiated the dependency operation.
func dependencyBusinessCaller() zapcore.EntryCaller {
	pcs := make([]uintptr, 64)
	count := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:count])
	for {
		frame, more := frames.Next()
		if !dependencyInfrastructureFrame(frame) {
			caller := zapcore.NewEntryCaller(frame.PC, frame.File, frame.Line, frame.PC != 0 && frame.File != "" && frame.Line > 0)
			caller.Function = frame.Function
			return caller
		}
		if !more {
			return zapcore.EntryCaller{}
		}
	}
}

func dependencyInfrastructureFrame(frame runtime.Frame) bool {
	file := filepath.Clean(frame.File)
	if root := strings.TrimSpace(runtime.GOROOT()); root != "" && strings.HasPrefix(file, filepath.Join(root, "src")+string(filepath.Separator)) {
		return true
	}
	for _, prefix := range []string{
		"database/sql.",
		"database/sql/driver.",
		"github.com/domainry/domainry-foundation/telemetry.",
		"github.com/domainry/domainry-orm/",
		"go.opentelemetry.io/",
	} {
		if strings.HasPrefix(frame.Function, prefix) {
			return true
		}
	}
	return dependencyStandardLibraryFunction(frame.Function)
}

var dependencyBuildModules struct {
	once  sync.Once
	paths []string
}

func dependencyStandardLibraryFunction(function string) bool {
	function = strings.TrimSpace(function)
	if function == "" || strings.HasPrefix(function, "main.") {
		return false
	}
	for _, modulePath := range dependencyLinkedModulePaths() {
		if function == modulePath || strings.HasPrefix(function, modulePath+"/") || strings.HasPrefix(function, modulePath+".") {
			return false
		}
	}
	head := function
	if slash := strings.IndexByte(head, '/'); slash >= 0 {
		head = head[:slash]
	} else if dot := strings.IndexByte(head, '.'); dot >= 0 {
		head = head[:dot]
	}
	return head != "" && !strings.Contains(head, ".")
}

func dependencyLinkedModulePaths() []string {
	dependencyBuildModules.once.Do(func() {
		buildInfo, ok := debug.ReadBuildInfo()
		if !ok {
			return
		}
		if path := strings.TrimSpace(buildInfo.Main.Path); path != "" {
			dependencyBuildModules.paths = append(dependencyBuildModules.paths, path)
		}
		for _, dependency := range buildInfo.Deps {
			if dependency == nil {
				continue
			}
			if path := strings.TrimSpace(dependency.Path); path != "" {
				dependencyBuildModules.paths = append(dependencyBuildModules.paths, path)
			}
		}
	})
	return dependencyBuildModules.paths
}

func writeDependencyLog(logger *zap.Logger, level zapcore.Level, caller zapcore.EntryCaller, fields []zap.Field) {
	checked := logger.Check(level, "dependency_call")
	if checked == nil {
		return
	}
	if caller.Defined {
		checked.Entry.Caller = caller
	}
	checked.Write(fields...)
}

func boundedDependencyValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func dependencyErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "unavailable"
	}
}
