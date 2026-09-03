package modulecapability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

func TestStaticAndRemoteBindingsHaveCanonicalParity(t *testing.T) {
	direct := testBinding(t)
	handler, err := NewHTTPHandler(direct, allowTestRequest)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	summary, err := direct.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	remote, err := OpenRemote(t.Context(), RemoteConfig{BaseURL: server.URL, Client: server.Client(), ExpectedModuleKey: "sample", ExpectedContractSHA256: summary.Identity.ContractSHA256})
	if err != nil {
		t.Fatal(err)
	}
	remoteSummary, err := remote.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	directCategory, err := direct.CapabilityCategory(t.Context(), "sample.execute")
	if err != nil {
		t.Fatal(err)
	}
	remoteCategory, err := remote.CapabilityCategory(t.Context(), "sample.execute")
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalEqual(t, summary, remoteSummary)
	assertCanonicalEqual(t, directCategory, remoteCategory)
	request := ValidationRequest{ContractVersion: ValidationContractVersion, ModuleKey: "sample", CategoryKey: "sample.execute", ContractSHA256: summary.Identity.ContractSHA256, Kind: "configuration", Candidate: testAuthoringFragment(`{"invalid":true}`)}
	directResult, err := direct.ValidateCapabilityCandidate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	remoteResult, err := remote.ValidateCapabilityCandidate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicalEqual(t, directResult, remoteResult)
}

func TestOpenRemoteRejectsContractMismatch(t *testing.T) {
	direct := testBinding(t)
	handler, err := NewHTTPHandler(direct, allowTestRequest)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	_, err = OpenRemote(t.Context(), RemoteConfig{BaseURL: server.URL, Client: server.Client(), ExpectedModuleKey: "sample", ExpectedContractSHA256: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "contract_mismatch") {
		t.Fatalf("OpenRemote() error = %v", err)
	}
}

func TestVerifyPinnedBindingValidatesCompleteBundle(t *testing.T) {
	direct := testBinding(t)
	summary, err := direct.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyPinnedBinding(t.Context(), direct, "sample", summary.Identity.ContractSHA256); err != nil {
		t.Fatalf("VerifyPinnedBinding() error = %v", err)
	}
	if err := VerifyPinnedBinding(t.Context(), direct, "sample", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "contract_mismatch") {
		t.Fatalf("VerifyPinnedBinding() mismatch error = %v", err)
	}
}

func TestVerifyPinnedBindingRejectsMissingExpectationAndBinding(t *testing.T) {
	if err := VerifyPinnedBinding(t.Context(), testBinding(t), "", ""); err == nil || !strings.Contains(err.Error(), "remote_expectation_required") {
		t.Fatalf("VerifyPinnedBinding() missing expectation error = %v", err)
	}
	if err := VerifyPinnedBinding(t.Context(), nil, "sample", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "remote_binding_required") {
		t.Fatalf("VerifyPinnedBinding() missing binding error = %v", err)
	}
}

func TestStaticBindingRejectsUnusedComponent(t *testing.T) {
	summary, categories := testContract(t)
	categories[0].OpenAPI.Components["schemas"]["Unused"] = json.RawMessage(`{"type":"string"}`)
	if _, err := NewStaticBinding(summary, categories, nil); err == nil || !strings.Contains(err.Error(), "outside the referenced closure") {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
}

func TestStaticBindingAcceptsReferencedSecurityScheme(t *testing.T) {
	summary, categories := testContract(t)
	categories[0].OpenAPI.Components["securitySchemes"] = map[string]json.RawMessage{
		"BearerAuth": json.RawMessage(`{"type":"http","scheme":"bearer"}`),
	}
	if _, err := NewStaticBinding(summary, categories, nil); err != nil {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
}

func TestStaticBindingAcceptsComponentInternalDefinitionReference(t *testing.T) {
	summary, categories := testContract(t)
	operation := categories[0].OpenAPI.Paths["/samples"]["post"]
	var value map[string]any
	if err := json.Unmarshal(operation, &value); err != nil {
		t.Fatal(err)
	}
	value["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"] = map[string]any{"$ref": "#/components/schemas/SampleRequest/$defs/Command"}
	operation, _ = json.Marshal(value)
	categories[0].OpenAPI.Paths["/samples"]["post"] = operation
	categories[0].OpenAPI.Components["schemas"]["SampleRequest"] = json.RawMessage(`{"type":"object","$defs":{"Command":{"type":"object"}}}`)
	if _, err := NewStaticBinding(summary, categories, nil); err != nil {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
}

func TestStaticBindingRejectsDuplicateOperationIDAcrossCategories(t *testing.T) {
	summary, categories := testContract(t)
	duplicate := categories[0]
	duplicate.Category = CategorySummary{Key: "sample.second", Name: "Second sample", Description: "Second sample category", OperationCount: 1, ValidationScopes: []string{"configuration"}}
	duplicate.OpenAPI.Paths = map[string]map[string]json.RawMessage{"/second-sample": {"post": categories[0].OpenAPI.Paths["/samples"]["post"]}}
	summary.Categories = append(summary.Categories, duplicate.Category)
	categories = append(categories, duplicate)
	if _, err := NewStaticBinding(summary, categories, nil); err == nil || !strings.Contains(err.Error(), "operationId") {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
}

func TestStaticBindingRejectsOversizedCategoryIndex(t *testing.T) {
	summary, categories := testContract(t)
	categories[0].Category.OperationCount = MaxCategoryOperations + 1
	if _, err := NewStaticBinding(summary, categories, nil); err == nil || !strings.Contains(err.Error(), "category is invalid") {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
}

func TestStaticBindingRejectsOversizedValidationIndex(t *testing.T) {
	summary, categories := testContract(t)
	categories[0].Category.ValidationScopes = make([]string, MaxCategoryValidations+1)
	for index := range categories[0].Category.ValidationScopes {
		categories[0].Category.ValidationScopes[index] = fmt.Sprintf("sample.validation_%02d", index)
	}
	if _, err := NewStaticBinding(summary, categories, nil); err == nil || !strings.Contains(err.Error(), "category is invalid") {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
}

func TestStaticBindingNormalizesRequiredEmptyCollectionsToJSONArrays(t *testing.T) {
	summary, categories := testContract(t)
	summary.Scenarios.RequiredModules = nil
	summary.Scenarios.OptionalModules = nil
	summary.Scenarios.ConflictingModules = nil
	summary.Scenarios.ValidationScopes = nil
	categories[0].Category.ValidationScopes = nil
	categories[0].ValidationContracts = nil
	binding, err := NewStaticBinding(summary, categories, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"required_modules":[]`, `"optional_modules":[]`, `"conflicting_modules":[]`, `"validation_scopes":[]`} {
		if !strings.Contains(string(payload), field) {
			t.Fatalf("normalized summary lacks %s: %s", field, payload)
		}
	}
}

func TestWireValidatorsRejectNullRequiredCollections(t *testing.T) {
	summary, _ := testContract(t)
	summary.Identity.ContractVersion = ContractVersion
	summary.Identity.ValidationContractVersion = ValidationContractVersion
	summary.Identity.ContractSHA256 = strings.Repeat("a", 64)
	summary.Scenarios.ValidationScopes = nil
	if err := ValidateModuleSummary(summary); err == nil || !strings.Contains(err.Error(), "JSON arrays") {
		t.Fatalf("ValidateModuleSummary() error = %v", err)
	}
	identity := summary.Identity
	result := ValidationResult{ContractVersion: ValidationContractVersion, ModuleKey: identity.Key, CategoryKey: "sample.execute", ContractSHA256: identity.ContractSHA256}
	if err := ValidateValidationResult(result, identity, "sample.execute"); err == nil || !strings.Contains(err.Error(), "JSON array") {
		t.Fatalf("ValidateValidationResult() error = %v", err)
	}
}

func TestStaticBindingRejectsStaleValidationRequest(t *testing.T) {
	binding := testBinding(t)
	_, err := binding.ValidateCapabilityCandidate(t.Context(), ValidationRequest{ContractVersion: ValidationContractVersion, ModuleKey: "sample", CategoryKey: "sample.execute", ContractSHA256: strings.Repeat("0", 64), Kind: "configuration", Candidate: testAuthoringFragment(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "contract_mismatch") {
		t.Fatalf("ValidateCapabilityCandidate() error = %v", err)
	}
}

func TestStaticBindingAllowsConfigurationOnlyCategory(t *testing.T) {
	summary, categories := testContract(t)
	category := CategorySummary{
		Key:              "sample.configuration",
		Name:             "Sample configuration",
		Description:      "Validate configuration without exposing a product endpoint",
		OperationCount:   0,
		AssemblyChains:   []string{"sample"},
		ValidationScopes: []string{"configuration"},
	}
	summary.Categories = append(summary.Categories, category)
	categories = append(categories, CategoryDocument{
		Category:            category,
		ValidationContracts: []ValidationScopeContract{{Kind: "configuration", Description: "Validate sample configuration.", Coverage: ValidationCoverageAllCandidates, CandidateCollections: []string{"samples"}}},
		OpenAPI: OpenAPIFragment{
			OpenAPI: "3.1.0",
			Paths:   map[string]map[string]json.RawMessage{},
		},
	})
	if _, err := NewStaticBinding(summary, categories, nil); err != nil {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
}

func TestStaticBindingAllowsModuleWithoutAuthorableCandidates(t *testing.T) {
	summary, categories := testContract(t)
	summary.Scenarios.ValidationScopes = []string{}
	categories[0].Category.ValidationScopes = []string{}
	categories[0].ValidationContracts = nil
	binding, err := NewStaticBinding(summary, categories, nil)
	if err != nil {
		t.Fatalf("NewStaticBinding() error = %v", err)
	}
	value, err := binding.CapabilitySummary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = binding.ValidateCapabilityCandidate(t.Context(), ValidationRequest{
		ContractVersion: ValidationContractVersion,
		ModuleKey:       value.Identity.Key,
		CategoryKey:     value.Categories[0].Key,
		ContractSHA256:  value.Identity.ContractSHA256,
		Kind:            "invented.configuration",
		Candidate:       testAuthoringFragment(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "validation_scope_invalid") {
		t.Fatalf("ValidateCapabilityCandidate() error = %v", err)
	}
}

func TestStaticBindingCarriesSortedSourceOwnedProjectionsInDigest(t *testing.T) {
	summary, categories := testContract(t)
	category := CategorySummary{Key: "sample.projections", Name: "Sample projections", Description: "Bounded source-owned projections", AssemblyChains: []string{"sample"}, ValidationScopes: []string{"configuration"}}
	summary.Categories = append(summary.Categories, category)
	categories = append(categories, CategoryDocument{Category: category, OpenAPI: OpenAPIFragment{OpenAPI: "3.1.0", Paths: map[string]map[string]json.RawMessage{}}, ValidationContracts: []ValidationScopeContract{{Kind: "configuration", Description: "Validate sample configuration.", Coverage: ValidationCoverageAllCandidates, CandidateCollections: []string{"samples"}}}, Projections: []SourceProjection{
		{Kind: "sample.definition", Key: "z", Payload: json.RawMessage(`{"key":"z"}`)},
		{Kind: "sample.definition", Key: "a", Payload: json.RawMessage(`{"key":"a"}`)},
	}})
	binding, err := NewStaticBinding(summary, categories, nil)
	if err != nil {
		t.Fatal(err)
	}
	document, err := binding.CapabilityCategory(t.Context(), "sample.projections")
	if err != nil {
		t.Fatal(err)
	}
	if document.Category.ProjectionCount != 2 || document.Projections[0].Key != "a" || document.Projections[1].Key != "z" {
		t.Fatalf("projections=%+v summary=%+v", document.Projections, document.Category)
	}
}

func testBinding(t *testing.T) *StaticBinding {
	t.Helper()
	summary, categories := testContract(t)
	binding, err := NewStaticBinding(summary, categories, func(_ context.Context, _ ValidationRequest) (ValidationResult, error) {
		return ValidationResult{Diagnostics: []Diagnostic{{Owner: "sample", RuleKey: "sample.invalid", Severity: SeverityError, FieldPath: "candidate.invalid", Message: "invalid sample candidate"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func testContract(t *testing.T) (ModuleSummary, []CategoryDocument) {
	t.Helper()
	extension, err := json.Marshal(OperationExtension{
		Owner:         "sample",
		Authorization: Authorization{Strategy: actioncontract.AuthorizationSigned, PolicyKey: "sample.execute", WorkspaceScope: "signed_request_workspace"},
		Effect:        EffectWrite,
		Idempotency:   Idempotency{Mode: "caller_key_required", KeySource: "Idempotency-Key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := json.RawMessage(`{"operationId":"executeSample","requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/SampleRequest"}}},"required":true},"responses":{"200":{"description":"ok"}},"security":[{"BearerAuth":[]},],"` + OperationExtensionKey + `":` + string(extension) + `}`)
	// Remove the intentionally visible trailing comma through normal JSON
	// construction so the fixture itself exercises RawMessage canonicalization.
	var operationValue map[string]any
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(operation), "},]", "}]")), &operationValue); err != nil {
		t.Fatal(err)
	}
	operation, err = json.Marshal(operationValue)
	if err != nil {
		t.Fatal(err)
	}
	category := CategorySummary{Key: "sample.execute", Name: "Sample execution", Description: "Execute one sample operation", OperationCount: 1, AssemblyChains: []string{"sample"}, ValidationScopes: []string{"configuration"}}
	return ModuleSummary{
			Identity: ModuleIdentity{Key: "sample", SourceOwner: "sample", ModuleVersion: "v1.0.0", ValidationRevision: "sample-validation-v1", SupportedDeploymentModes: []DeploymentMode{DeploymentModeModule, DeploymentModeSaaS}},
			Name:     "Sample", Description: "Sample module",
			Scenarios: AdaptationScenarios{
				UseWhen: []string{"A product must execute a sample operation"}, DoNotUseWhen: []string{"A product only stores unrelated records"},
				RequirementSignals: []string{"sample execution"}, ProvidedCapabilities: []string{"sample.execute"}, AssemblyChains: []string{"sample"}, ValidationScopes: []string{"configuration"},
				SelectionExamples: []ScenarioExample{{Requirement: "Execute the sample", Reason: "Sample owns execution"}}, RejectionExamples: []ScenarioExample{{Requirement: "List records", Reason: "Records owns listing"}},
			},
		}, []CategoryDocument{{
			Category:            category,
			ValidationContracts: []ValidationScopeContract{{Kind: "configuration", Description: "Validate sample configuration.", Coverage: ValidationCoverageAllCandidates, CandidateCollections: []string{"samples"}}},
			OpenAPI: OpenAPIFragment{OpenAPI: "3.1.0", Paths: map[string]map[string]json.RawMessage{"/samples": {"post": operation}}, Components: map[string]map[string]json.RawMessage{
				"schemas":         {"SampleRequest": json.RawMessage(`{"type":"object"}`)},
				"securitySchemes": {"BearerAuth": json.RawMessage(`{"type":"http","scheme":"bearer"}`)},
			}},
		}}
}

func testAuthoringFragment(value string) AuthoringFragment {
	return AuthoringFragment{Collection: "samples", Key: "sample", Value: json.RawMessage(value)}
}

func TestValidationContractAllowsSiblingReferencesInCandidateCollection(t *testing.T) {
	contract := ValidationScopeContract{
		Kind: "schema.object", Description: "Validate an object and its object relations.",
		Coverage:             ValidationCoverageAllCandidates,
		CandidateCollections: []string{"objects"}, ReferencedCollections: []string{"objects"},
	}
	if err := ValidateValidationScopeContract(contract); err != nil {
		t.Fatalf("same-collection sibling references must be supported: %v", err)
	}
}

func assertCanonicalEqual(t *testing.T, left, right any) {
	t.Helper()
	leftBytes, err := CanonicalJSON(left)
	if err != nil {
		t.Fatal(err)
	}
	rightBytes, err := CanonicalJSON(right)
	if err != nil {
		t.Fatal(err)
	}
	if string(leftBytes) != string(rightBytes) {
		t.Fatalf("canonical values differ\nleft:  %s\nright: %s", leftBytes, rightBytes)
	}
}

func allowTestRequest(*http.Request) error { return nil }
