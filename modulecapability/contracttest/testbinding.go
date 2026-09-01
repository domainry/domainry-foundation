package contracttest

import (
	"encoding/json"

	"github.com/domainry/domainry-foundation/modulecapability"
	actioncontract "github.com/domainry/domainry-foundation/action"
)

// NewFixtureBinding returns the smallest valid capability bundle for transport
// tests whose subject is a module-specific Remote client rather than the
// module's real disclosure content.
func NewFixtureBinding(moduleKey string) (*modulecapability.StaticBinding, error) {
	operation, err := json.Marshal(map[string]any{
		"operationId": moduleKey + "Fixture", "responses": map[string]any{"200": map[string]any{"description": "fixture"}},
		modulecapability.OperationExtensionKey: modulecapability.OperationExtension{
			Owner: moduleKey, Authorization: modulecapability.Authorization{Strategy: actioncontract.AuthorizationAnonymousProtocol, PolicyKey: moduleKey + ".test_protocol"},
			Effect: modulecapability.EffectRead, Idempotency: modulecapability.Idempotency{Mode: "not_applicable"},
		},
	})
	if err != nil {
		return nil, err
	}
	category := modulecapability.CategoryDocument{Category: modulecapability.CategorySummary{
		Key: moduleKey + ".fixture", Name: "Fixture", Description: "Remote transport fixture.", OperationCount: 1, AssemblyChains: []string{"fixture_chain"}, ValidationScopes: []string{"fixture.validate"},
	}, OpenAPI: modulecapability.OpenAPIFragment{OpenAPI: "3.1.0", Paths: map[string]map[string]json.RawMessage{"/fixture": {"get": operation}}}, ValidationContracts: []modulecapability.ValidationScopeContract{{Kind: "fixture.validate", Description: "Validate one fixture authoring fragment.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"fixtures"}}}}
	summary := modulecapability.ModuleSummary{
		Identity: modulecapability.ModuleIdentity{Key: moduleKey, SourceOwner: moduleKey, ModuleVersion: "fixture-v1", ValidationRevision: "fixture-validation-v1", SupportedDeploymentModes: []modulecapability.DeploymentMode{modulecapability.DeploymentModeModule, modulecapability.DeploymentModeSaaS}},
		Name:     "Fixture", Description: "Remote transport fixture.",
		Scenarios: modulecapability.AdaptationScenarios{
			UseWhen: []string{"testing remote transport"}, DoNotUseWhen: []string{"production capability discovery"}, RequirementSignals: []string{"fixture"}, ProvidedCapabilities: []string{"fixture"},
			RequiredModules: []string{}, OptionalModules: []string{}, ConflictingModules: []string{}, AssemblyChains: []string{"fixture_chain"}, ValidationScopes: []string{"fixture.validate"},
			SelectionExamples: []modulecapability.ScenarioExample{{Requirement: "test transport", Reason: "fixture"}}, RejectionExamples: []modulecapability.ScenarioExample{{Requirement: "production", Reason: "fixture only"}},
		},
	}
	return modulecapability.NewStaticBinding(summary, []modulecapability.CategoryDocument{category}, nil)
}
