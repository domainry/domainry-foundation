package modulecapability

import (
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
)

func TestCategoryFromHTTPRoutesRequiresCompleteOwnerManifest(t *testing.T) {
	route, err := modulehttp.RouteFromAction(actioncontract.ActionDefinition{
		Key: "example.read", Owner: "module:example", SourceKind: "module_surface", CapabilityKey: "example", CapabilityLabel: "Example",
		OperationKey: "read", OperationLabel: "Read", Label: "Read example", Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:          &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/examples/{exampleID}"}, Permission: &actioncontract.PermissionDefinition{
			Key: "example.read", Owner: "module:example", ResourceKey: "example", OperationKey: "read", Label: "Example · Read", Category: "Example", LifecycleStatus: actioncontract.LifecycleActive,
		},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "owner_read", LifecycleStatus: actioncontract.LifecycleActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	document, err := CategoryFromHTTPRoutes(HTTPRouteCategory{
		Owner: "example", Category: CategorySummary{Key: "example.read", Name: "Example", Description: "Read examples.", AssemblyChains: []string{"example_chain"}, ValidationScopes: []string{"example.query"}},
		ValidationContracts: []ValidationScopeContract{{Kind: "example.query", Description: "Validate example authoring queries.", Coverage: ValidationCoverageAllCandidates, CandidateCollections: []string{"examples"}}},
		Routes:              []modulehttp.Route{route}, Operations: map[string]map[string]any{route.Pattern(): {"operationId": "getExample", "parameters": []any{map[string]any{"name": "exampleID", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}}, "responses": map[string]any{"200": map[string]any{"description": "example"}}}},
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
