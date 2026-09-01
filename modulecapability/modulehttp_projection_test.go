package modulecapability

import (
	"testing"

	"github.com/domainry/domainry-foundation/modulehttp"
)

func TestCategoryFromHTTPRoutesRequiresCompleteOwnerManifest(t *testing.T) {
	route := modulehttp.Route{Pattern: "GET /examples/{exampleID}", Exposures: []modulehttp.Exposure{modulehttp.ExposurePublic}, Authentication: modulehttp.AuthenticationAuthenticated, Permission: "example.read", Governance: &modulehttp.Governance{EffectClass: modulehttp.EffectRead, HighRiskPolicy: modulehttp.HighRiskNone, IdempotencyDecision: "not_applicable", AuditClass: "owner_read"}}
	document, err := CategoryFromHTTPRoutes(HTTPRouteCategory{
		Owner: "example", Category: CategorySummary{Key: "example.read", Name: "Example", Description: "Read examples.", AssemblyChains: []string{"example_chain"}, ValidationScopes: []string{"example.query"}},
		ValidationContracts: []ValidationScopeContract{{Kind: "example.query", Description: "Validate example authoring queries.", Coverage: ValidationCoverageAllCandidates, CandidateCollections: []string{"examples"}}},
		Routes:              []modulehttp.Route{route}, Operations: map[string]map[string]any{route.Pattern: {"operationId": "getExample", "parameters": []any{map[string]any{"name": "exampleID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}}, "responses": map[string]any{"200": map[string]any{"description": "example"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if document.Category.OperationCount != 1 {
		t.Fatalf("operation count=%d", document.Category.OperationCount)
	}
	summary := ModuleSummary{
		Identity: ModuleIdentity{Key: "example", SourceOwner: "example", ModuleVersion: "v1", ValidationRevision: "example-validation-v1", SupportedDeploymentModes: []DeploymentMode{DeploymentModeModule}},
		Name:     "Example", Description: "Example module.",
		Scenarios: AdaptationScenarios{
			UseWhen: []string{"examples are required"}, DoNotUseWhen: []string{"examples are not required"}, RequirementSignals: []string{"example"}, ProvidedCapabilities: []string{"example.read"},
			AssemblyChains: []string{"example_chain"}, ValidationScopes: []string{"example.query"}, SelectionExamples: []ScenarioExample{{Requirement: "read examples", Reason: "example owns reads"}}, RejectionExamples: []ScenarioExample{{Requirement: "write records", Reason: "example does not own records"}},
		},
	}
	if _, err := NewStaticBinding(summary, []CategoryDocument{document}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := CategoryFromHTTPRoutes(HTTPRouteCategory{Owner: "example", Category: document.Category, Routes: []modulehttp.Route{route}}); err == nil {
		t.Fatal("accepted a route without a source-owned OpenAPI Operation")
	}
}
