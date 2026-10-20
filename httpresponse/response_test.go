package httpresponse

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
)

type observedWriter struct {
	*httptest.ResponseRecorder
	kind        string
	code        string
	internalErr error
}

func (writer *observedWriter) RecordError(kind, code string) {
	writer.kind, writer.code = kind, code
}

func (writer *observedWriter) RecordInternalError(err error) {
	writer.internalErr = err
}

func TestWriteErrorUsesCanonicalShapeAndObservabilityClassification(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &observedWriter{ResponseRecorder: recorder}
	request := httptest.NewRequest(http.MethodPost, "/customers", nil)
	request = request.WithContext(requestcontext.WithRequestID(request.Context(), "req-one"))

	WriteError(writer, request, Failure{
		Status:  http.StatusConflict,
		Code:    "crm.customer.conflict",
		Message: "Customer already exists",
		Params:  map[string]string{"customer_id": "customer-one"},
	})

	if recorder.Code != http.StatusConflict || writer.kind != "conflict" || writer.code != "crm.customer.conflict" {
		t.Fatalf("status=%d kind=%q code=%q", recorder.Code, writer.kind, writer.code)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("content-type=%q", contentType)
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != "crm.customer.conflict" || response.Message != "Customer already exists" || response.RequestID != "req-one" || response.Params["customer_id"] != "customer-one" {
		t.Fatalf("response=%+v", response)
	}
	if strings.Contains(recorder.Body.String(), `"error"`) || strings.Contains(recorder.Body.String(), "message_key") {
		t.Fatalf("legacy error fields leaked: %s", recorder.Body.String())
	}
}

func TestWriteErrorRedactsSensitiveParamsAndInternalDetails(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &observedWriter{ResponseRecorder: recorder}
	cause := errors.New("database password=private")

	WriteError(writer, nil, Failure{
		Status:  http.StatusInternalServerError,
		Code:    "backend.customer.persist_failed",
		Message: cause.Error(),
		Params:  map[string]string{"field": "client_secret", "actual": "private"},
		Cause:   cause,
	})

	if writer.internalErr == nil || writer.code != "backend.customer.persist_failed" {
		t.Fatalf("internal observability cause/code missing: err=%v code=%q", writer.internalErr, writer.code)
	}
	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Message != http.StatusText(http.StatusInternalServerError) || response.Params["actual"] != "[REDACTED]" {
		t.Fatalf("response=%+v", response)
	}
	if strings.Contains(recorder.Body.String(), "private") || strings.Contains(recorder.Body.String(), cause.Error()) {
		t.Fatalf("internal details leaked: %s", recorder.Body.String())
	}
}

func TestWriteErrorNormalizesInvalidStatusCodeAndMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := &observedWriter{ResponseRecorder: recorder}
	WriteError(writer, nil, Failure{Status: http.StatusOK, Code: "invalid code", Message: "unsafe\nmessage"})

	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError || response.Code != "backend.internal" || response.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("status=%d response=%+v", recorder.Code, response)
	}
}

func TestWriteJSONKeepsSuccessfulDTOUnwrapped(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteJSON(recorder, http.StatusCreated, struct {
		CustomerID string `json:"customer_id"`
	}{CustomerID: "customer-one"})

	if recorder.Code != http.StatusCreated || recorder.Body.String() != "{\"customer_id\":\"customer-one\"}\n" {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteJSONTurnsEncodingFailureIntoCanonicalError(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteJSON(recorder, http.StatusOK, map[string]any{"unsupported": make(chan struct{})})

	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusInternalServerError || response.Code != "backend.response_write_failed" || response.Message != http.StatusText(http.StatusInternalServerError) {
		t.Fatalf("status=%d response=%+v", recorder.Code, response)
	}
}
