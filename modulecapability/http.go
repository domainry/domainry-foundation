package modulecapability

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type RequestAuthenticator func(*http.Request) error

// NewHTTPHandler exposes the one canonical wire mapping used by SaaS Remote
// Bindings. Operational health, service discovery and module product routes
// are intentionally outside this handler.
func NewHTTPHandler(binding Binding, authenticate RequestAuthenticator) (http.Handler, error) {
	if binding == nil || authenticate == nil {
		return nil, fmt.Errorf("module capability HTTP binding and service authenticator are required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+SummaryPath, func(response http.ResponseWriter, request *http.Request) {
		if err := authenticate(request); err != nil {
			writeHTTPError(response, err)
			return
		}
		value, err := binding.CapabilitySummary(request.Context())
		if err != nil {
			writeHTTPError(response, err)
			return
		}
		response.Header().Set("ETag", `"`+value.Identity.ContractSHA256+`"`)
		writeJSON(response, http.StatusOK, value)
	})
	mux.HandleFunc("GET "+CategoriesPath+"{key}", func(response http.ResponseWriter, request *http.Request) {
		if err := authenticate(request); err != nil {
			writeHTTPError(response, err)
			return
		}
		value, err := binding.CapabilityCategory(request.Context(), request.PathValue("key"))
		if err != nil {
			writeHTTPError(response, err)
			return
		}
		response.Header().Set("ETag", `"`+value.ContractSHA256+`"`)
		writeJSON(response, http.StatusOK, value)
	})
	mux.HandleFunc("POST "+ValidationPath, func(response http.ResponseWriter, request *http.Request) {
		if err := authenticate(request); err != nil {
			writeHTTPError(response, err)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, MaxValidationRequestBytes)
		var value ValidationRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			writeHTTPError(response, &Error{StatusCode: http.StatusBadRequest, Code: "module_capability.validation_request_invalid", Message: "decode candidate validation request"})
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeHTTPError(response, &Error{StatusCode: http.StatusBadRequest, Code: "module_capability.validation_request_invalid", Message: "candidate validation request must contain one JSON object"})
			return
		}
		result, err := binding.ValidateCapabilityCandidate(request.Context(), value)
		if err != nil {
			writeHTTPError(response, err)
			return
		}
		response.Header().Set("ETag", `"`+result.ContractSHA256+`"`)
		writeJSON(response, http.StatusOK, result)
	})
	return mux, nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	payload, err := CanonicalJSON(value)
	if err != nil {
		writeHTTPError(response, err)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = response.Write(append(payload, '\n'))
}

func writeHTTPError(response http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	value := &Error{StatusCode: status, Code: "module_capability.internal", Message: "module capability request failed"}
	var typed *Error
	if errors.As(err, &typed) {
		value = &Error{StatusCode: typed.StatusCode, Code: strings.TrimSpace(typed.Code), Message: strings.TrimSpace(typed.Message), Retryable: typed.Retryable}
		if value.StatusCode >= 400 && value.StatusCode <= 599 {
			status = value.StatusCode
		}
	}
	if value.Code == "" {
		value.Code = "module_capability.internal"
	}
	payload, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		payload = []byte(`{"code":"module_capability.internal"}`)
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_, _ = response.Write(append(payload, '\n'))
}
