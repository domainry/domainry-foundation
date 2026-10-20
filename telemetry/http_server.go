package telemetry

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

const RequestIDHeader = "X-Request-ID"

// HTTPServerObservation is the bounded, payload-free terminal observation of
// one HTTP request. Process-owned metric adapters may consume it.
type HTTPServerObservation struct {
	ListenerGroup string
	Method        string
	Route         string
	Status        int
	Duration      time.Duration
}

// HTTPServerConfig describes one process-owned outer HTTP boundary.
type HTTPServerConfig struct {
	ListenerGroup string
	TracerName    string
	Observe       func(HTTPServerObservation)
	// WritePanic may preserve a host's public error-envelope contract. Recovery,
	// classification and logging remain owned by this outer middleware.
	WritePanic func(http.ResponseWriter, *http.Request)
}

type httpStatusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	errorKind   string
	errorCode   string
}

func (recorder *httpStatusRecorder) WriteHeader(status int) {
	if recorder.wroteHeader {
		return
	}
	recorder.status = status
	recorder.wroteHeader = true
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *httpStatusRecorder) Write(payload []byte) (int, error) {
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(payload)
}

func (recorder *httpStatusRecorder) Unwrap() http.ResponseWriter { return recorder.ResponseWriter }

func (recorder *httpStatusRecorder) RecordError(kind, code string) {
	recorder.errorKind = boundedDiagnostic(kind)
	recorder.errorCode = boundedDiagnostic(code)
}

// RecordHTTPError attaches stable classification to the outer access event.
// It does not log and never accepts an original error or user-controlled text.
func RecordHTTPError(writer http.ResponseWriter, kind, code string) {
	for writer != nil {
		if recorder, ok := writer.(interface{ RecordError(string, string) }); ok {
			recorder.RecordError(kind, code)
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		next := unwrapper.Unwrap()
		if next == writer {
			return
		}
		writer = next
	}
}

// HTTPServerMiddleware owns request identity, W3C extraction, the Server span,
// panic recovery and the single terminal access event for a standalone Go
// process. It must wrap the final listener handler, not an individual router.
func HTTPServerMiddleware(config HTTPServerConfig, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	listenerGroup := boundedDiagnostic(config.ListenerGroup)
	if listenerGroup == "" {
		listenerGroup = "public"
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		// Request identity is local to this process boundary. An upstream request
		// ID must never be reused across a service hop; correlation_id and W3C
		// trace context are the cross-process identities.
		requestID := requestcontext.NewRequestID()
		correlationID := normalizedCorrelationValue(request.Header.Get(CorrelationIDHeader))
		if correlationID == "" {
			correlationID = requestID
		}

		ctx := ExtractHTTPContext(request.Context(), request.Header)
		ctx = requestcontext.WithRequestID(ctx, requestID)
		ctx = requestcontext.WithCorrelationID(ctx, correlationID)
		ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(request.Header.Get("X-Workspace-ID")))
		ctx = requestcontext.WithActorID(ctx, strings.TrimSpace(request.Header.Get("X-User-ID")))
		ctx, span := StartHTTPServerSpan(ctx, nil, config.TracerName, "HTTP "+request.Method, request.Method)
		defer span.End()
		request = request.WithContext(ctx)
		writer.Header().Set(RequestIDHeader, requestID)
		writer.Header().Set(CorrelationIDHeader, correlationID)
		recorder := &httpStatusRecorder{ResponseWriter: writer, status: http.StatusOK}

		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					if recovered == http.ErrAbortHandler {
						panic(recovered)
					}
					logging.FromContext(request.Context()).Error("panic_recovered",
						zap.String("component", "http"),
						zap.String("panic_type", fmt.Sprintf("%T", recovered)),
						zap.ByteString("stack", debug.Stack()),
					)
					recorder.RecordError("internal", "backend.internal")
					if !recorder.wroteHeader {
						if config.WritePanic != nil {
							config.WritePanic(recorder, request)
						} else {
							http.Error(recorder, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
						}
					}
				}
			}()
			next.ServeHTTP(recorder, request)
		}()

		route := HTTPRoutePattern(request)
		span.SetName(request.Method + " " + route)
		span.SetAttributes(attribute.String("http.route", route), attribute.Int("http.response.status_code", recorder.status))
		if recorder.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, "server_error")
			if recorder.errorKind == "" {
				recorder.RecordError("internal", "http.server_error")
			}
		}
		observation := HTTPServerObservation{ListenerGroup: listenerGroup, Method: request.Method, Route: route, Status: recorder.status, Duration: time.Since(started)}
		if config.Observe != nil {
			config.Observe(observation)
		}
		if recorder.status < http.StatusBadRequest && isSuccessfulProbeRoute(route) {
			return
		}
		fields := logging.Fields(map[string]any{
			"listener_group": listenerGroup,
			"method":         request.Method,
			"route":          route,
			"status":         recorder.status,
			"duration_ms":    observation.Duration.Milliseconds(),
			"error_kind":     recorder.errorKind,
			"error_code":     recorder.errorCode,
		})
		logger := logging.FromContext(request.Context())
		if recorder.status >= http.StatusInternalServerError {
			logger.Error("http_request", fields...)
		} else {
			logger.Info("http_request", fields...)
		}
	})
}

// HTTPRoutePattern returns a bounded source-owned route template. Raw paths,
// queries and unmatched user input are never returned.
func HTTPRoutePattern(request *http.Request) string {
	if request == nil {
		return "unmatched"
	}
	pattern := strings.TrimSpace(request.Pattern)
	if prefix := request.Method + " "; strings.HasPrefix(pattern, prefix) {
		pattern = strings.TrimSpace(strings.TrimPrefix(pattern, prefix))
	}
	if pattern == "" || len(pattern) > 256 || strings.ContainsAny(pattern, "?\r\n") || !strings.HasPrefix(pattern, "/") {
		return "unmatched"
	}
	return pattern
}

func boundedDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func isSuccessfulProbeRoute(route string) bool {
	switch route {
	case "/live", "/ready", "/metrics":
		return true
	default:
		return false
	}
}
