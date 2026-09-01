package action

import "testing"

func TestAuthorizationPoliciesHaveExplicitValidShapes(t *testing.T) {
	base := permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"})
	tests := []struct {
		name       string
		strategy   AuthorizationStrategy
		policy     string
		audiences  []string
		permission bool
	}{
		{name: "role permission", strategy: AuthorizationExactRolePermission, permission: true},
		{name: "anonymous protocol", strategy: AuthorizationAnonymousProtocol, policy: "runtime.api_info"},
		{name: "delegated credential", strategy: AuthorizationDelegatedCredential, policy: "agent.task_tool_credential"},
		{name: "authenticated principal", strategy: AuthorizationAuthenticatedPrincipal},
		{name: "self or permission", strategy: AuthorizationSelfOrPermission, policy: "identity.self", permission: true},
		{name: "service identity", strategy: AuthorizationServiceIdentity, policy: "scheduler.trigger", audiences: []string{"scheduler_service"}},
		{name: "operations identity", strategy: AuthorizationOperationsIdentity, policy: "runtime.operations"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := base
			definition.Authorization = Authorization{Strategy: test.strategy, PolicyKey: test.policy, Audiences: test.audiences}
			if !test.permission {
				definition.Permission = nil
			}
			if err := validateDefinition(normalizeDefinition(definition)); err != nil {
				t.Fatalf("policy rejected: %v", err)
			}
		})
	}
}

func TestAuthorizationPoliciesRejectMixedOrAmbientAuthority(t *testing.T) {
	base := permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"})
	tests := []struct {
		name   string
		mutate func(*ActionDefinition)
	}{
		{name: "missing role permission", mutate: func(value *ActionDefinition) { value.Permission = nil }},
		{name: "anonymous without policy", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationAnonymousProtocol}
			value.Permission = nil
		}},
		{name: "delegated credential without policy", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationDelegatedCredential}
			value.Permission = nil
		}},
		{name: "principal with permission", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationAuthenticatedPrincipal}
		}},
		{name: "self without permission", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationSelfOrPermission, PolicyKey: "identity.self"}
			value.Permission = nil
		}},
		{name: "service with permission", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationServiceIdentity, PolicyKey: "scheduler.trigger", Audiences: []string{"scheduler_service"}}
		}},
		{name: "service without audience", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationServiceIdentity, PolicyKey: "scheduler.trigger"}
			value.Permission = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := base
			test.mutate(&definition)
			if err := validateDefinition(normalizeDefinition(definition)); err == nil {
				t.Fatal("invalid authorization declaration accepted")
			}
		})
	}
}
