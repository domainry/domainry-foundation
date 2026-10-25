// Package processhost owns the observability lifecycle of a standalone Go
// process. Embedded modules must not import or call this package.
package processhost

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/telemetry"
	"go.uber.org/zap"
)

type Config struct {
	ServiceName            string
	ServiceVersion         string
	LoggerOutputPaths      []string
	LoggerErrorOutputPaths []string
	Telemetry              telemetry.Config
}

type Host struct {
	logger            *zap.Logger
	shutdownTelemetry telemetry.Shutdown
	stopDiagnosticLog func()
	closeOnce         sync.Once
	closeErr          error
	started           atomic.Bool
}

type startupFailure struct {
	err     error
	failure logging.Failure
}

func (failure *startupFailure) Error() string { return failure.err.Error() }
func (failure *startupFailure) Unwrap() error { return failure.err }

// FailStartup preserves the concrete startup stage until Finish emits the one
// terminal process_start_failed event.
func FailStartup(err error, failure logging.Failure) error {
	if err == nil {
		return nil
	}
	return &startupFailure{err: err, failure: failure}
}

func Open(ctx context.Context, config Config) (*Host, error) {
	serviceVersion := strings.TrimSpace(config.ServiceVersion)
	if serviceVersion == "" {
		serviceVersion = strings.TrimSpace(os.Getenv("DOMAINRY_SERVICE_VERSION"))
	}
	if serviceVersion == "" {
		serviceVersion = "dev"
	}
	loggingConfig := logging.ConfigFromEnvironment(config.ServiceName, serviceVersion)
	loggingConfig.OutputPaths = append([]string(nil), config.LoggerOutputPaths...)
	loggingConfig.ErrorOutputPaths = append([]string(nil), config.LoggerErrorOutputPaths...)
	logger, err := logging.Initialize(loggingConfig)
	if err != nil {
		logging.ReportBootstrapFailure(config.ServiceName, err, logging.Failure{Stage: "logging_initialize"})
		return nil, err
	}
	diagnosticPolicy, err := telemetry.DiagnosticLogPolicyFromEnvironment()
	if err != nil {
		logging.ProcessStartFailed(ctx, err, logging.Failure{Stage: "diagnostic_logging_configure"})
		_ = logging.Sync(logger)
		return nil, err
	}
	if err := telemetry.ConfigureDiagnosticLogging(diagnosticPolicy); err != nil {
		logging.ProcessStartFailed(ctx, err, logging.Failure{Stage: "diagnostic_logging_configure"})
		_ = logging.Sync(logger)
		return nil, err
	}
	telemetryConfig := config.Telemetry
	if strings.TrimSpace(telemetryConfig.ServiceName) == "" && strings.TrimSpace(telemetryConfig.Exporter) == "" && strings.TrimSpace(telemetryConfig.Endpoint) == "" {
		telemetryConfig, err = telemetry.ConfigFromEnvironment(config.ServiceName, config.ServiceVersion)
		if err != nil {
			logging.ProcessStartFailed(ctx, err, logging.Failure{Stage: "telemetry_configure"})
			_ = logging.Sync(logger)
			return nil, err
		}
	}
	if strings.TrimSpace(telemetryConfig.ServiceName) == "" {
		telemetryConfig.ServiceName = strings.TrimSpace(config.ServiceName)
	}
	if strings.TrimSpace(telemetryConfig.ServiceVersion) == "" {
		telemetryConfig.ServiceVersion = serviceVersion
	}
	shutdown, err := telemetry.Initialize(ctx, telemetryConfig)
	if err != nil {
		logging.ProcessStartFailed(ctx, err, logging.Failure{Stage: "telemetry_initialize", Dependency: "telemetry_exporter"})
		_ = logging.Sync(logger)
		return nil, err
	}
	var stopDiagnosticLog func()
	if policyPath := strings.TrimSpace(os.Getenv("LOG_DIAGNOSTIC_POLICY_FILE")); policyPath != "" {
		stopDiagnosticLog, err = telemetry.WatchDiagnosticLogPolicyFile(ctx, policyPath, 0)
		if err != nil {
			logging.ProcessStartFailed(ctx, err, logging.Failure{Stage: "diagnostic_logging_watch"})
			_ = shutdown(ctx)
			_ = logging.Sync(logger)
			return nil, err
		}
	}
	return &Host{logger: logger, shutdownTelemetry: shutdown, stopDiagnosticLog: stopDiagnosticLog}, nil
}

func (host *Host) Handler(listenerGroup string, next http.Handler) http.Handler {
	return telemetry.HTTPServerMiddleware(telemetry.HTTPServerConfig{ListenerGroup: listenerGroup, TracerName: "domainry.process.http"}, next)
}

func (host *Host) Started(ctx context.Context, fields map[string]any) {
	if host != nil {
		host.started.Store(true)
	}
	logging.ProcessStarted(ctx, fields)
}

func (host *Host) StartFailed(ctx context.Context, err error, failure logging.Failure) {
	logging.ProcessStartFailed(ctx, err, failure)
}

func (host *Host) Stopping(ctx context.Context, fields map[string]any) {
	logging.ProcessStopping(ctx, fields)
}

func (host *Host) Close(ctx context.Context, fields map[string]any) error {
	if host == nil {
		return nil
	}
	host.closeOnce.Do(func() {
		if host.stopDiagnosticLog != nil {
			host.stopDiagnosticLog()
		}
		if host.shutdownTelemetry != nil {
			host.closeErr = errors.Join(host.closeErr, host.shutdownTelemetry(ctx))
		}
		if host.started.Load() {
			logging.ProcessStopped(ctx, fields)
		}
		if host.logger != nil {
			host.closeErr = errors.Join(host.closeErr, logging.Sync(host.logger))
		}
	})
	return host.closeErr
}

// Finish records the terminal process result before flushing telemetry and the
// logger. Call it exactly once from the standalone command's named return.
func (host *Host) Finish(ctx context.Context, fields map[string]any, runErr error) error {
	if host == nil {
		return runErr
	}
	if runErr != nil {
		if host.started.Load() {
			logging.FromContext(ctx).Error("process_stopped", logging.ErrorFields(runErr, logging.Failure{Stage: "process_run"})...)
		} else {
			failure := logging.Failure{Stage: "process_start"}
			var startupErr *startupFailure
			if errors.As(runErr, &startupErr) {
				failure = startupErr.failure
			}
			logging.ProcessStartFailed(ctx, runErr, failure)
		}
	} else if host.started.Load() {
		logging.ProcessStopped(ctx, fields)
	}
	// Close owns only resource shutdown after Finish has emitted the one
	// terminal lifecycle event.
	host.started.Store(false)
	return errors.Join(runErr, host.Close(ctx, fields))
}
