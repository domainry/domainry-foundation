package modulehttp

import (
	"net/http"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

type testAdapter struct {
	contract string
	owner    string
	name     string
	routes   []Route
	handler  http.Handler
}

type testProvider struct{ adapters []Adapter }

func (provider testProvider) HTTPAdapters() []Adapter {
	return append([]Adapter(nil), provider.adapters...)
}

func (adapter testAdapter) ContractVersion() string { return adapter.contract }
func (adapter testAdapter) Owner() string           { return adapter.owner }
func (adapter testAdapter) Name() string            { return adapter.name }
func (adapter testAdapter) Routes() []Route         { return adapter.routes }
func (adapter testAdapter) Handler() http.Handler   { return adapter.handler }

func TestValidateAdapterAcceptsHostEnforcedModuleRoutes(t *testing.T) {
	adapter := testAdapter{
		contract: ContractVersion, owner: "party", name: "party_management", handler: http.NotFoundHandler(),
		routes: []Route{
			mustTestRoute(t, actionTestDefinition()),
			mustTestRoute(t, principalActionTestDefinition()),
		},
	}
	if err := ValidateAdapter(adapter); err != nil {
		t.Fatalf("validate adapter: %v", err)
	}
}

func TestValidateAdapterRejectsIncompleteAuthorityDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		want  string
	}{
		{name: "no exposure", route: mutateTestRoute(actionTestDefinition(), func(action *actioncontract.ActionDefinition) { action.Exposures = nil }), want: "requires an exposure"},
		{name: "authenticated policy without permission", route: mutateTestRoute(actionTestDefinition(), func(action *actioncontract.ActionDefinition) {
			action.Permission = nil
			action.Authorization.PolicyKey = "party.self"
		}), want: "invalid authorization"},
		{name: "anonymous permission", route: mutateTestRoute(actionTestDefinition(), func(action *actioncontract.ActionDefinition) {
			action.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAnonymous}
		}), want: "invalid authorization"},
		{name: "unknown exposure", route: mutateTestRoute(actionTestDefinition(), func(action *actioncontract.ActionDefinition) { action.Exposures = []actioncontract.Exposure{"private"} }), want: "unsupported exposure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := testAdapter{contract: ContractVersion, owner: "party", name: "party", handler: http.NotFoundHandler(), routes: []Route{test.route}}
			err := ValidateAdapter(adapter)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want fragment %q", err, test.want)
			}
		})
	}
}

func TestValidateAdapterRejectsDuplicateRoutes(t *testing.T) {
	route := mustTestRoute(t, actionTestDefinition())
	adapter := testAdapter{contract: ContractVersion, owner: "party", name: "party", handler: http.NotFoundHandler(), routes: []Route{route, route}}
	if err := ValidateAdapter(adapter); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRouteGovernanceAndOwnedOpenAPI(t *testing.T) {
	action := principalActionTestDefinition()
	action.Key, action.OperationKey, action.OperationLabel, action.Label = "reports.snapshots.refresh", "refresh", "Refresh", "Refresh report snapshot"
	action.HTTP = &actioncontract.HTTPBinding{Method: "POST", RouteTemplate: "/report/{reportKey}/snapshots/refresh"}
	action.EffectClass, action.RiskLevel = actioncontract.EffectWrite, actioncontract.RiskMedium
	action.IdempotencyDecision, action.AuditClass = "caller_key_required", "mutation_audit_required"
	route := mustTestRoute(t, action)
	adapter := governedTestAdapter{testAdapter: testAdapter{contract: ContractVersion, owner: "report", name: "reports", handler: http.NotFoundHandler(), routes: []Route{route}}, operations: map[string]map[string]any{
		route.Pattern(): {"operationId": "refreshReportSnapshot"},
	}}
	if err := ValidateAdapter(adapter); err != nil {
		t.Fatalf("validate governed adapter: %v", err)
	}

	invalid := route
	invalid.Action.IdempotencyDecision = ""
	if err := ValidateRoute(invalid); err == nil || !strings.Contains(err.Error(), "idempotency decision") {
		t.Fatalf("unexpected governance error: %v", err)
	}

	adapter.operations = map[string]map[string]any{"GET /unknown": {"operationId": "unknown"}}
	if err := ValidateAdapter(adapter); err == nil || !strings.Contains(err.Error(), "undeclared route") {
		t.Fatalf("unexpected OpenAPI ownership error: %v", err)
	}
}

func TestRouteFromCanonicalActionIsLosslessForSupportedStrategies(t *testing.T) {
	definition := actionTestDefinition()
	route, err := RouteFromAction(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoute(route); err != nil {
		t.Fatal(err)
	}
	if route.Pattern() != "GET /party/{partyID}" || route.Action.Permission == nil || route.Action.Permission.Key != "party.read" || route.Action.Key != definition.Key {
		t.Fatalf("route=%#v", route)
	}
}

func TestHTTPBindingChangePreservesActionAndPermissionIdentityWithoutAlias(t *testing.T) {
	before := actionTestDefinition()
	after := actioncontract.CloneDefinition(before)
	after.HTTP.RouteTemplate = "/parties/{partyID}"
	after.HTTP.DisplayRouteTemplate = "/parties/{partyID}"

	registry := actioncontract.NewRegistry()
	if err := registry.Register(after); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	resolved, found := registry.ResolveHTTP(http.MethodGet, "/parties/{partyID}")
	if !found || resolved.Key != before.Key || resolved.Permission == nil || resolved.Permission.Key != before.Permission.Key {
		t.Fatalf("updated binding changed authorization identity: action=%#v found=%t", resolved, found)
	}
	if _, found := registry.ResolveHTTP(http.MethodGet, before.HTTP.RouteTemplate); found {
		t.Fatal("old HTTP binding survived as an implicit authorization alias")
	}
}

func TestRouteFromCanonicalActionSupportsExceptionalStrategy(t *testing.T) {
	definition := actionTestDefinition()
	for name, authorization := range map[string]actioncontract.Authorization{
		"signed policy": {Strategy: actioncontract.AuthorizationSigned, PolicyKey: "party.service.verify", Audiences: []string{"party_service"}},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := definition
			candidate.Authorization = authorization
			candidate.Permission = nil
			route, err := RouteFromAction(candidate)
			if err != nil || route.Action.Authorization.Strategy != actioncontract.AuthorizationSigned {
				t.Fatalf("route=%#v error=%v", route, err)
			}
		})
	}
}

func TestRouteHasNoParallelPermissionPolicy(t *testing.T) {
	route := mustTestRoute(t, actionTestDefinition())
	if route.Action.Permission == nil || route.Action.Permission.Key != route.Action.Key {
		t.Fatalf("route action=%#v", route.Action)
	}
}

func TestAuthorizationActionsUsesHTTPRoutesAsTheHTTPOnlyManifest(t *testing.T) {
	definition := actionTestDefinition()
	definition.Owner = "module:party"
	definition.Permission.Owner = "module:party"
	provider := testProvider{adapters: []Adapter{testAdapter{
		contract: ContractVersion, owner: "party", name: "party", handler: http.NotFoundHandler(),
		routes: []Route{mustTestRoute(t, definition)},
	}}}
	actions, err := AuthorizationActions(provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Key != definition.Key {
		t.Fatalf("actions=%#v", actions)
	}
	actions[0].Label = "mutated"
	again, err := AuthorizationActions(provider)
	if err != nil || again[0].Label == "mutated" {
		t.Fatalf("provider manifest was not detached: actions=%#v error=%v", again, err)
	}
}

func TestValidateSourceOwnersRejectsHostOrModuleOwnerDrift(t *testing.T) {
	definition := actionTestDefinition()
	provider := testProvider{adapters: []Adapter{testAdapter{
		contract: ContractVersion, owner: "party", name: "party", handler: http.NotFoundHandler(),
		routes: []Route{mustTestRoute(t, definition)},
	}}}
	if err := ValidateSourceOwners(provider); err == nil || !strings.Contains(err.Error(), `owner="party:module" want="module:party"`) {
		t.Fatalf("owner drift error=%v", err)
	}
}

func TestValidateAuthorizationProjectionAcceptsNonHTTPAndRejectsAdapterDrift(t *testing.T) {
	httpAction := actionTestDefinition()
	httpAction.Owner = "module:party"
	httpAction.Permission.Owner = "module:party"
	nonHTTP := principalActionTestDefinition()
	nonHTTP.Key = "party.jobs.run"
	nonHTTP.Owner = "module:party"
	nonHTTP.OperationKey, nonHTTP.OperationLabel, nonHTTP.Label = "run", "Run", "Run party job"
	nonHTTP.HTTP = nil
	nonHTTP.NonHTTP = []actioncontract.NonHTTPBinding{{Kind: "job", InvocationKey: "party.jobs.run"}}
	provider := testProvider{adapters: []Adapter{testAdapter{
		contract: ContractVersion, owner: "party", name: "party", handler: http.NotFoundHandler(),
		routes: []Route{mustTestRoute(t, httpAction)},
	}}}
	if err := ValidateAuthorizationProjection([]actioncontract.ActionDefinition{httpAction, nonHTTP}, provider); err != nil {
		t.Fatal(err)
	}

	drifted := httpAction
	drifted.Label = "Drifted"
	if err := ValidateAuthorizationProjection([]actioncontract.ActionDefinition{drifted, nonHTTP}, provider); err == nil || !strings.Contains(err.Error(), "differs from its source manifest") {
		t.Fatalf("drift error=%v", err)
	}
	if err := ValidateAuthorizationProjection([]actioncontract.ActionDefinition{httpAction}, nil); err == nil || !strings.Contains(err.Error(), "no mounted adapter route") {
		t.Fatalf("missing adapter error=%v", err)
	}
}

func actionTestDefinition() actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "party.read", Owner: "party:module", SourceKind: "module_adapter",
		CapabilityKey: "party.management", CapabilityLabel: "Party management",
		OperationKey: "read", OperationLabel: "Read", Label: "Get party",
		Exposures:     []actioncontract.Exposure{actioncontract.ExposureManagement},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:          &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/party/{partyID}", DisplayRouteTemplate: "/party/{partyID}"},
		Permission: &actioncontract.PermissionDefinition{
			Key: "party.read", Owner: "party:module", ResourceKey: "party", OperationKey: "read", Label: "Party · Read",
			Category: "Party management", LifecycleStatus: actioncontract.LifecycleActive,
		},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow,
		IdempotencyDecision: "not_applicable", AuditClass: "party_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func principalActionTestDefinition() actioncontract.ActionDefinition {
	definition := actionTestDefinition()
	definition.Key, definition.OperationKey, definition.OperationLabel, definition.Label = "party.me.read", "me.read", "Read self", "Get current party"
	definition.HTTP = &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/party/me"}
	definition.Exposures = []actioncontract.Exposure{actioncontract.ExposurePublic, actioncontract.ExposureManagement}
	definition.Authorization = actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated}
	definition.Permission = nil
	return definition
}

func mustTestRoute(t *testing.T, definition actioncontract.ActionDefinition) Route {
	t.Helper()
	route, err := RouteFromAction(definition)
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func mutateTestRoute(definition actioncontract.ActionDefinition, mutate func(*actioncontract.ActionDefinition)) Route {
	mutate(&definition)
	return Route{Action: definition}
}

type governedTestAdapter struct {
	testAdapter
	operations map[string]map[string]any
}

func (adapter governedTestAdapter) OpenAPIOperations() map[string]map[string]any {
	return adapter.operations
}

var _ Adapter = testAdapter{}
var _ OpenAPIProvider = governedTestAdapter{}
