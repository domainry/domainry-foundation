package modulecapability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxRemoteResponseBytes = 16 << 20

type RequestAuthorizer func(*http.Request) error

type RemoteConfig struct {
	BaseURL                string
	Client                 *http.Client
	Authorize              RequestAuthorizer
	ExpectedModuleKey      string
	ExpectedContractSHA256 string
}

// ValidateRemoteExpectation validates the topology-invariant identity a
// module-specific Remote factory must pin before returning an operational
// Binding. Custom transports use the same rule as OpenRemote.
func ValidateRemoteExpectation(moduleKey, contractSHA256 string) error {
	if !keyPattern.MatchString(strings.TrimSpace(moduleKey)) || !sha256Pattern(strings.TrimSpace(contractSHA256)) {
		return &Error{StatusCode: http.StatusInternalServerError, Code: "module_capability.remote_expectation_required", Message: "Remote module key and capability digest must be pinned before opening"}
	}
	return nil
}

// VerifyPinnedBinding applies the same topology-invariant contract check to a
// module-specific SaaS transport that already implements Binding. It is useful
// for SDKs whose business Remote transport is not constructed by OpenRemote.
// The complete bundle is loaded and validated before the transport may be
// returned as an operational module Binding.
func VerifyPinnedBinding(ctx context.Context, binding Binding, expectedModuleKey, expectedContractSHA256 string) error {
	expectedModuleKey = strings.TrimSpace(expectedModuleKey)
	expectedContractSHA256 = strings.TrimSpace(expectedContractSHA256)
	if err := ValidateRemoteExpectation(expectedModuleKey, expectedContractSHA256); err != nil {
		return err
	}
	if binding == nil {
		return &Error{StatusCode: http.StatusInternalServerError, Code: "module_capability.remote_binding_required", Message: "Remote capability binding is required"}
	}
	summary, err := binding.CapabilitySummary(ctx)
	if err != nil {
		return fmt.Errorf("load Remote module capability summary: %w", err)
	}
	if err := ValidateModuleSummary(summary); err != nil {
		return &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: err.Error()}
	}
	if summary.Identity.Key != expectedModuleKey {
		return &Error{StatusCode: http.StatusConflict, Code: "module_capability.module_mismatch", Message: "Remote module key differs from SDK expectation"}
	}
	if summary.Identity.ContractSHA256 != expectedContractSHA256 {
		return &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: "Remote capability digest differs from SDK expectation"}
	}
	categories := make([]CategoryDocument, 0, len(summary.Categories))
	for _, category := range summary.Categories {
		document, loadErr := binding.CapabilityCategory(ctx, category.Key)
		if loadErr != nil {
			return fmt.Errorf("load Remote module capability category %q: %w", category.Key, loadErr)
		}
		categories = append(categories, document)
	}
	if err := ValidateBundle(summary, categories); err != nil {
		return &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: err.Error()}
	}
	return nil
}

// RemoteBinding implements the same capability facet as a direct Module
// Binding. OpenRemote requires a caller-pinned module key and digest, then
// fails before project execution when the service contract identity differs.
type RemoteBinding struct {
	baseURL    string
	client     *http.Client
	authorize  RequestAuthorizer
	summary    ModuleSummary
	categories map[string]CategoryDocument
}

func OpenRemote(ctx context.Context, config RemoteConfig) (*RemoteBinding, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("module capability Remote base URL is invalid")
	}
	expectedModuleKey := strings.TrimSpace(config.ExpectedModuleKey)
	expectedContractSHA256 := strings.TrimSpace(config.ExpectedContractSHA256)
	if err := ValidateRemoteExpectation(expectedModuleKey, expectedContractSHA256); err != nil {
		return nil, err
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	remote := &RemoteBinding{baseURL: baseURL, client: client, authorize: config.Authorize}
	summary, err := remote.loadSummary(ctx)
	if err != nil {
		return nil, err
	}
	if summary.Identity.Key != expectedModuleKey {
		return nil, &Error{StatusCode: http.StatusConflict, Code: "module_capability.module_mismatch", Message: "Remote module key differs from SDK expectation"}
	}
	if summary.Identity.ContractSHA256 != expectedContractSHA256 {
		return nil, &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: "Remote capability digest differs from SDK expectation"}
	}
	remote.summary = summary
	remote.categories = make(map[string]CategoryDocument, len(summary.Categories))
	values := make([]CategoryDocument, 0, len(summary.Categories))
	for _, category := range summary.Categories {
		value, loadErr := remote.loadCategory(ctx, category.Key)
		if loadErr != nil {
			return nil, loadErr
		}
		remote.categories[category.Key] = value
		values = append(values, value)
	}
	if err := ValidateBundle(summary, values); err != nil {
		return nil, &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: err.Error()}
	}
	return remote, nil
}

func (r *RemoteBinding) CapabilitySummary(context.Context) (ModuleSummary, error) {
	if r == nil {
		return ModuleSummary{}, fmt.Errorf("module capability Remote binding is unavailable")
	}
	return cloneJSON(r.summary)
}

func (r *RemoteBinding) CapabilityCategory(ctx context.Context, key string) (CategoryDocument, error) {
	if r == nil {
		return CategoryDocument{}, fmt.Errorf("module capability Remote binding is unavailable")
	}
	key = strings.TrimSpace(key)
	if !keyPattern.MatchString(key) {
		return CategoryDocument{}, &Error{StatusCode: http.StatusBadRequest, Code: "module_capability.category_invalid", Message: "module capability category key is invalid"}
	}
	value, found := r.categories[key]
	if !found {
		return CategoryDocument{}, &Error{StatusCode: http.StatusNotFound, Code: "module_capability.category_not_found", Message: "unknown module capability category"}
	}
	return cloneJSON(value)
}

func (r *RemoteBinding) ValidateCapabilityCandidate(ctx context.Context, request ValidationRequest) (ValidationResult, error) {
	if r == nil {
		return ValidationResult{}, fmt.Errorf("module capability Remote binding is unavailable")
	}
	if err := ValidateValidationRequest(request); err != nil {
		return ValidationResult{}, &Error{StatusCode: http.StatusBadRequest, Code: "module_capability.validation_request_invalid", Message: err.Error()}
	}
	var value ValidationResult
	if err := r.doJSON(ctx, http.MethodPost, ValidationPath, request, &value); err != nil {
		return ValidationResult{}, err
	}
	if err := ValidateValidationResult(value, r.summary.Identity, request.CategoryKey); err != nil {
		return ValidationResult{}, &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: err.Error()}
	}
	return value, nil
}

func (r *RemoteBinding) loadCategory(ctx context.Context, key string) (CategoryDocument, error) {
	var value CategoryDocument
	if err := r.doJSON(ctx, http.MethodGet, CategoriesPath+url.PathEscape(key), nil, &value); err != nil {
		return CategoryDocument{}, err
	}
	if err := ValidateCategoryDocument(value, r.summary.Identity); err != nil {
		return CategoryDocument{}, &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: err.Error()}
	}
	return value, nil
}

func (r *RemoteBinding) loadSummary(ctx context.Context) (ModuleSummary, error) {
	var value ModuleSummary
	if err := r.doJSON(ctx, http.MethodGet, SummaryPath, nil, &value); err != nil {
		return ModuleSummary{}, err
	}
	if err := ValidateModuleSummary(value); err != nil {
		return ModuleSummary{}, &Error{StatusCode: http.StatusConflict, Code: "module_capability.contract_mismatch", Message: err.Error()}
	}
	return value, nil
}

func (r *RemoteBinding) doJSON(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		payload, err := CanonicalJSON(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, r.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build module capability Remote request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if r.authorize != nil {
		if err := r.authorize(request); err != nil {
			return fmt.Errorf("authorize module capability Remote request: %w", err)
		}
	}
	response, err := r.client.Do(request)
	if err != nil {
		return &Error{StatusCode: http.StatusServiceUnavailable, Code: "module_capability.remote_unavailable", Message: "module capability Remote request failed", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseBytes+1))
	if err != nil {
		return &Error{StatusCode: http.StatusBadGateway, Code: "module_capability.remote_response_invalid", Message: "read module capability Remote response", Retryable: true, Cause: err}
	}
	if len(payload) > maxRemoteResponseBytes {
		return &Error{StatusCode: http.StatusBadGateway, Code: "module_capability.remote_response_too_large", Message: "module capability Remote response exceeds limit"}
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		var remoteError Error
		if json.Unmarshal(payload, &remoteError) != nil || strings.TrimSpace(remoteError.Code) == "" {
			remoteError = Error{Code: "module_capability.remote_error", Message: "module capability Remote service rejected the request"}
		}
		remoteError.StatusCode = response.StatusCode
		return &remoteError
	}
	if err := decodeStrict(payload, output); err != nil {
		return &Error{StatusCode: http.StatusBadGateway, Code: "module_capability.remote_response_invalid", Message: "decode module capability Remote response", Cause: err}
	}
	return nil
}

var _ Binding = (*RemoteBinding)(nil)
