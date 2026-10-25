package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	"go.uber.org/zap"
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
	Enabled         bool     `json:"enabled"`
	Features        []string `json:"features"`
	HTTPContent     bool     `json:"http_content"`
	SQLStatement    bool     `json:"sql_statement"`
	MaxContentBytes int      `json:"max_content_bytes"`
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

// DefaultDiagnosticLogPolicy records every supported boundary and SQL
// statement template. HTTP content remains opt-in until a process or generated
// product explicitly enables its bounded, redacted capture.
func DefaultDiagnosticLogPolicy() DiagnosticLogPolicy {
	return DiagnosticLogPolicy{
		Enabled:         true,
		Features:        append([]string(nil), diagnosticFeatures...),
		HTTPContent:     false,
		SQLStatement:    true,
		MaxContentBytes: defaultDiagnosticContentBytes,
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
					"policy_digest": digest,
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
	fields := logging.Fields(map[string]any{
		"dependency_kind": boundedDependencyValue(call.Feature),
		"dependency":      boundedDependencyValue(call.Dependency),
		"operation":       boundedDependencyValue(call.Operation),
		"outcome":         outcome,
		"duration_ms":     duration.Milliseconds(),
		"input":           call.Input,
		"output":          result.Output,
	})
	logger := logging.FromContext(ctx)
	if result.Err != nil {
		fields = append(fields, zap.String("error_kind", dependencyErrorKind(result.Err)), zap.String("error_code", "dependency.call_failed"))
		logger.Error("dependency_call", fields...)
		return
	}
	logger.Info("dependency_call", fields...)
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
