package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
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

type httpRouteStateKey struct{}

type httpRouteState struct {
	mu    sync.RWMutex
	route string
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

// RecordHTTPStatusError maps the already-selected HTTP response status to the
// closed application error vocabulary and attaches its stable code to the
// outer access event. Module-owned response writers call this immediately
// before WriteHeader; it never logs and never accepts raw error text.
func RecordHTTPStatusError(writer http.ResponseWriter, status int, code string) {
	RecordHTTPError(writer, HTTPErrorKind(status), code)
}

// HTTPErrorKind is the one status-to-observability mapping shared by Runtime,
// hosted modules and independent SaaS processes.
func HTTPErrorKind(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return "bad_request"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict, http.StatusPreconditionFailed:
		return "conflict"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "unavailable"
	default:
		return "internal"
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

		routeState := &httpRouteState{}
		ctx := context.WithValue(ExtractHTTPContext(request.Context(), request.Header), httpRouteStateKey{}, routeState)
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
	pattern := ""
	if state, ok := request.Context().Value(httpRouteStateKey{}).(*httpRouteState); ok && state != nil {
		state.mu.RLock()
		pattern = state.route
		state.mu.RUnlock()
	}
	if pattern == "" {
		pattern = strings.TrimSpace(request.Pattern)
	}
	return normalizedHTTPRoutePattern(request.Method, pattern)
}

// RecordHTTPRoute publishes the source-owned route template selected by a
// manual or nested router to the outer process observability boundary. Raw URL
// paths must never be passed here.
func RecordHTTPRoute(request *http.Request, pattern string) {
	if request == nil {
		return
	}
	pattern = normalizedHTTPRoutePattern(request.Method, pattern)
	if pattern == "unmatched" {
		return
	}
	state, ok := request.Context().Value(httpRouteStateKey{}).(*httpRouteState)
	if !ok || state == nil {
		return
	}
	state.mu.Lock()
	state.route = pattern
	state.mu.Unlock()
}

// HTTPRoute wraps one already-selected route handler. It only records the
// bounded template; span completion and access logging remain outer-owned.
func HTTPRoute(pattern string, next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		RecordHTTPRoute(request, pattern)
		next.ServeHTTP(writer, request)
	})
}

func normalizedHTTPRoutePattern(method, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if prefix := strings.TrimSpace(method) + " "; strings.HasPrefix(pattern, prefix) {
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
