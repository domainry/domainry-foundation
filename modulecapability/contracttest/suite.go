// Package contracttest provides the shared conformance suite consumed by every
// Domainry module SDK and implementation.
package contracttest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/modulecapability"
)

type ValidationCase struct {
	Name    string
	Request modulecapability.ValidationRequest
}

func VerifyBinding(t *testing.T, binding modulecapability.Binding) {
	t.Helper()
	summary, categories := readBundle(t, binding)
	if err := modulecapability.ValidateBundle(summary, categories); err != nil {
		t.Fatalf("module capability bundle is invalid: %v", err)
	}
}

// VerifyModuleRemoteParity proves that the canonical HTTP mapping changes only
// transport, not model-facing capability truth or validation behavior.
func VerifyModuleRemoteParity(t *testing.T, direct modulecapability.Binding, validationCases ...ValidationCase) {
	t.Helper()
	directSummary, directCategories := readBundle(t, direct)
	handler, err := modulecapability.NewHTTPHandler(direct, func(request *http.Request) error {
		if request.Header.Get("X-Domainry-Capability-Test") != directSummary.Identity.Key {
			return &modulecapability.Error{StatusCode: http.StatusUnauthorized, Code: "module_capability.service_authentication_required"}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	remote, err := modulecapability.OpenRemote(t.Context(), modulecapability.RemoteConfig{
		BaseURL: server.URL, Client: server.Client(), ExpectedModuleKey: directSummary.Identity.Key, ExpectedContractSHA256: directSummary.Identity.ContractSHA256,
		Authorize: func(request *http.Request) error {
			request.Header.Set("X-Domainry-Capability-Test", directSummary.Identity.Key)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, mismatchErr := modulecapability.OpenRemote(t.Context(), modulecapability.RemoteConfig{
		BaseURL: server.URL, Client: server.Client(), ExpectedModuleKey: directSummary.Identity.Key, ExpectedContractSHA256: strings.Repeat("0", 64),
		Authorize: func(request *http.Request) error {
			request.Header.Set("X-Domainry-Capability-Test", directSummary.Identity.Key)
			return nil
		},
	}); mismatchErr == nil || !strings.Contains(mismatchErr.Error(), "module_capability.contract_mismatch") {
		t.Fatalf("Remote binding accepted a stale capability digest: %v", mismatchErr)
	}
	remoteSummary, remoteCategories := readBundle(t, remote)
	assertCanonicalEqual(t, directSummary, remoteSummary)
	assertCanonicalEqual(t, directCategories, remoteCategories)
	for _, test := range validationCases {
		t.Run(test.Name, func(t *testing.T) {
			directResult, directErr := direct.ValidateCapabilityCandidate(context.Background(), test.Request)
			remoteResult, remoteErr := remote.ValidateCapabilityCandidate(context.Background(), test.Request)
			if errorString(directErr) != errorString(remoteErr) {
				t.Fatalf("validation errors differ: direct=%v remote=%v", directErr, remoteErr)
			}
			assertCanonicalEqual(t, directResult, remoteResult)
		})
	}
}

func readBundle(t *testing.T, binding modulecapability.Binding) (modulecapability.ModuleSummary, []modulecapability.CategoryDocument) {
	t.Helper()
	if binding == nil {
		t.Fatal("module capability binding is nil")
	}
	summary, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	categories := make([]modulecapability.CategoryDocument, 0, len(summary.Categories))
	for _, category := range summary.Categories {
		document, err := binding.CapabilityCategory(t.Context(), category.Key)
		if err != nil {
			t.Fatalf("load category %q: %v", category.Key, err)
		}
		categories = append(categories, document)
	}
	return summary, categories
}

func assertCanonicalEqual(t *testing.T, left, right any) {
	t.Helper()
	leftBytes, err := modulecapability.CanonicalJSON(left)
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := modulecapability.CanonicalJSON(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftBytes) != string(rightBytes) {
		t.Fatalf("canonical values differ\nleft:  %s\nright: %s", leftBytes, rightBytes)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
