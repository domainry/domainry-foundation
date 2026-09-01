package modulecapability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (value roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return value(request)
}

func TestRemoteTransportHonorsCallerCancellationAndRedactsTransportFailure(t *testing.T) {
	secret := "service-token-must-not-leak"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("transport failed while using " + secret)
	})}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := OpenRemote(ctx, pinnedRemoteTestConfig("https://module.invalid", client))
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "module_capability.remote_unavailable" || !typed.Retryable {
		t.Fatalf("Remote cancellation mapping=%+v", err)
	}
	if strings.Contains(typed.Message, secret) || strings.Contains(typed.Error(), secret) {
		t.Fatalf("Remote error leaked transport detail: %v", typed)
	}
}

func TestRemoteTransportDoesNotRetryCapabilityCallsInternally(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"code":"module_capability.busy","message":"retry outside the shared client","retryable":true}`)),
			Request:    request,
		}, nil
	})}
	_, err := OpenRemote(t.Context(), pinnedRemoteTestConfig("https://module.invalid", client))
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "module_capability.busy" || !typed.Retryable || calls.Load() != 1 {
		t.Fatalf("Remote retry classification=%+v calls=%d", err, calls.Load())
	}
}

func TestRemoteTransportNeverReturnsUnstructuredResponseBody(t *testing.T) {
	secret := "upstream-stack-and-secret"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, secret, http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	_, err := OpenRemote(t.Context(), pinnedRemoteTestConfig(server.URL, server.Client()))
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "module_capability.remote_error") {
		t.Fatalf("Remote unstructured error mapping=%v", err)
	}
}

func TestOpenRemoteRejectsMissingPinnedExpectationBeforeNetwork(t *testing.T) {
	var calls atomic.Int64
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not be called")
	})}
	_, err := OpenRemote(t.Context(), RemoteConfig{BaseURL: "https://module.invalid", Client: client})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "module_capability.remote_expectation_required" || calls.Load() != 0 {
		t.Fatalf("missing expectation error=%+v calls=%d", err, calls.Load())
	}
}

func pinnedRemoteTestConfig(baseURL string, client *http.Client) RemoteConfig {
	return RemoteConfig{
		BaseURL: baseURL, Client: client, ExpectedModuleKey: "sample",
		ExpectedContractSHA256: strings.Repeat("a", 64),
	}
}
