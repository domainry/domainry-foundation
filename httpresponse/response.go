// Package httpresponse owns the JSON response contract shared by Domainry HTTP
// processes. Successful response DTOs remain source-owned; failures use the
// single ErrorResponse shape defined here.
package httpresponse

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-foundation/telemetry"
)

// ErrorResponse is the only ordinary JSON error response emitted by Domainry
// HTTP boundaries. It deliberately excludes raw errors, stacks and payloads.
type ErrorResponse struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Params    map[string]string `json:"params,omitempty"`
	RequestID string            `json:"request_id"`
}

// Failure contains the transport decision made by the boundary that owns the
// request outcome. Cause is used only by the outer observability recorder and
// is never serialized.
type Failure struct {
	Status  int
	Code    string
	Message string
	Params  map[string]string
	Cause   error
}

// WriteError emits the canonical JSON error response and publishes the same
// stable status/code/cause to the process-owned HTTP observability boundary.
func WriteError(writer http.ResponseWriter, request *http.Request, failure Failure) {
	if writer == nil {
		return
	}
	failure = normalizeFailure(failure)
	causes := []error(nil)
	if failure.Cause != nil {
		causes = append(causes, failure.Cause)
	}
	telemetry.RecordHTTPStatusError(writer, failure.Status, failure.Code, causes...)
	writeJSON(writer, failure.Status, ErrorResponse{
		Code:      failure.Code,
		Message:   failure.Message,
		Params:    apperror.SanitizeParams(failure.Params),
		RequestID: requestID(writer, request),
	})
}

// WriteJSON writes a source-owned successful JSON DTO without adding an
// envelope. A value that cannot be encoded is replaced by the canonical
// internal error while the response status is still mutable.
func WriteJSON(writer http.ResponseWriter, status int, value any) {
	if writer == nil {
		return
	}
	raw, err := json.Marshal(value)
	if err != nil {
		WriteError(writer, nil, Failure{Status: http.StatusInternalServerError, Code: "backend.response_write_failed", Cause: err})
		return
	}
	writeRawJSON(writer, status, raw)
}

func normalizeFailure(failure Failure) Failure {
	if failure.Status < http.StatusBadRequest || failure.Status > 599 {
		failure.Status = http.StatusInternalServerError
	}
	kind := apperror.NormalizeKind(telemetry.HTTPErrorKind(failure.Status))
	failure.Code = apperror.NormalizeCode(kind, failure.Code)
	if failure.Status >= http.StatusInternalServerError {
		failure.Message = http.StatusText(failure.Status)
	}
	failure.Message = strings.TrimSpace(failure.Message)
	if failure.Message == "" || len(failure.Message) > 512 || strings.ContainsAny(failure.Message, "\r\n") {
		failure.Message = failure.Code
	}
	return failure
}

func requestID(writer http.ResponseWriter, request *http.Request) string {
	if request != nil {
		if value := requestcontext.RequestID(request.Context()); value != "" {
			return value
		}
	}
	return strings.TrimSpace(writer.Header().Get(telemetry.RequestIDHeader))
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		raw = []byte(`{"code":"backend.response_write_failed","message":"Internal Server Error","request_id":""}`)
		status = http.StatusInternalServerError
		telemetry.RecordHTTPStatusError(writer, status, "backend.response_write_failed", err)
	}
	writeRawJSON(writer, status, raw)
}

func writeRawJSON(writer http.ResponseWriter, status int, raw []byte) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = writer.Write(append(raw, '\n'))
}
