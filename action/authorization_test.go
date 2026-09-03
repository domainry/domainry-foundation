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
		{name: "anonymous", strategy: AuthorizationAnonymous},
		{name: "authenticated principal", strategy: AuthorizationAuthenticated},
		{name: "authenticated exact permission", strategy: AuthorizationAuthenticated, permission: true},
		{name: "authenticated self policy", strategy: AuthorizationAuthenticated, policy: "identity.self", permission: true},
		{name: "signed request", strategy: AuthorizationSigned, policy: "scheduler.trigger"},
		{name: "signed audience", strategy: AuthorizationSigned, policy: "scheduler.trigger", audiences: []string{"scheduler_service"}},
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
		{name: "anonymous with permission", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationAnonymous}
		}},
		{name: "anonymous with policy", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationAnonymous, PolicyKey: "runtime.public"}
			value.Permission = nil
		}},
		{name: "authenticated policy without permission", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationAuthenticated, PolicyKey: "identity.self"}
			value.Permission = nil
		}},
		{name: "signed without policy", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationSigned}
			value.Permission = nil
		}},
		{name: "signed with permission", mutate: func(value *ActionDefinition) {
			value.Authorization = Authorization{Strategy: AuthorizationSigned, PolicyKey: "scheduler.trigger"}
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

func TestAuthorizationPoliciesRejectRemovedStrategies(t *testing.T) {
	base := permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"})
	base.Permission = nil
	for _, strategy := range []AuthorizationStrategy{
		"exact_role_permission",
		"anonymous_protocol",
		"delegated_credential",
		"authenticated_principal",
		"self_or_permission",
		"service_identity",
		"operations_identity",
	} {
		t.Run(string(strategy), func(t *testing.T) {
			definition := base
			definition.Authorization = Authorization{Strategy: strategy, PolicyKey: "legacy.policy"}
			if err := validateDefinition(normalizeDefinition(definition)); err == nil {
				t.Fatalf("removed authorization strategy %q was accepted", strategy)
			}
		})
	}
}
