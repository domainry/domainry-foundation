package action

import (
	"strings"
	"testing"
)

const testOwner = "orders:builtin"

func permissionAction(key string, bindings ...HTTPBinding) ActionDefinition {
	separator := strings.LastIndex(key, ".")
	resourceKey, operationKey := key[:separator], key[separator+1:]
	definition := ActionDefinition{
		Key: key, Owner: testOwner, SourceKind: "builtin_http",
		CapabilityKey: "orders.management", CapabilityLabel: "Order management",
		OperationKey: operationKey, OperationLabel: "Read", Label: "Read orders",
		Exposures:     []Exposure{ExposureManagement},
		Authorization: Authorization{Strategy: AuthorizationAuthenticated},
		Permission: &PermissionDefinition{
			Key: key, Owner: testOwner, ResourceKey: resourceKey, OperationKey: operationKey,
			Label: "Orders · Read", Category: "Order management", LifecycleStatus: LifecycleActive,
		},
		EffectClass: EffectRead, RiskLevel: RiskLow,
		IdempotencyDecision: "not_applicable", AuditClass: "management_read", LifecycleStatus: LifecycleActive,
	}
	if len(bindings) != 0 {
		definition.HTTP = &bindings[0]
	}
	return definition
}

func TestPermissionResourceAndOperationComposeActionKey(t *testing.T) {
	definition := permissionAction("orders.read")
	definition.Permission.ResourceKey = "customers"
	if err := ValidateDefinition(definition); err == nil {
		t.Fatal("permission display fragments must compose the exact action key")
	}
}

func TestRegistryFreezesOneActionWithOnePermission(t *testing.T) {
	registry := NewRegistry()
	action := permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders", DisplayRouteTemplate: "/orders"})
	if err := registry.Register(action); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Definition(action.Key); ok {
		t.Fatal("mutable registry exposed a definition before freeze")
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	if !registry.Frozen() {
		t.Fatal("registry did not freeze")
	}
	resolved, ok := registry.ResolveHTTP(" get ", "/orders")
	if !ok || resolved.Key != action.Key {
		t.Fatalf("resolved=%#v ok=%v", resolved, ok)
	}
	permissions := registry.PermissionDefinitions()
	if len(permissions) != 1 || permissions[0].Key != action.Key {
		t.Fatalf("permissions=%#v", permissions)
	}
	usages := registry.PermissionUsages(testOwner, action.Key)
	if len(usages) != 1 || usages[0].Action.Key != action.Key || usages[0].Permission.Key != action.Key {
		t.Fatalf("usages=%#v", usages)
	}
	resolved, _ = registry.Definition(action.Key)
	resolved.HTTP.RouteTemplate = "/mutated"
	resolved.Permission.Key = "mutated"
	again, _ := registry.Definition(action.Key)
	if again.HTTP.RouteTemplate == "/mutated" || again.Permission.Key != action.Key {
		t.Fatalf("caller mutated frozen registry: %#v", again)
	}
	if err := registry.Register(action); err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Fatalf("post-freeze registration error=%v", err)
	}
}

func TestRegistryRejectsConflictsWithoutPartiallyApplyingBatch(t *testing.T) {
	tests := []struct {
		name        string
		definitions []ActionDefinition
		want        string
	}{
		{name: "action key", definitions: []ActionDefinition{
			permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"}),
			permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/other"}),
		}, want: "action key"},
		{name: "HTTP binding", definitions: []ActionDefinition{
			permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"}),
			permissionAction("orders.search", HTTPBinding{Method: "GET", RouteTemplate: "/orders"}),
		}, want: "HTTP binding"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			err := registry.Register(test.definitions...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
			if err := registry.Freeze(); err != nil {
				t.Fatalf("empty registry could not freeze after rejected batch: %v", err)
			}
			if len(registry.Definitions()) != 0 {
				t.Fatalf("rejected batch partially applied: %#v", registry.Definitions())
			}
		})
	}
}

func TestRegistryRequiresActionPermissionIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ActionDefinition)
		want   string
	}{
		{name: "key", mutate: func(value *ActionDefinition) { value.Permission.Key = "orders.other" }, want: "must equal"},
		{name: "owner", mutate: func(value *ActionDefinition) { value.Permission.Owner = "other:owner" }, want: "cannot own"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			action := permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"})
			test.mutate(&action)
			if err := NewRegistry().Register(action); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

func TestRegistryResolvesNonHTTPAction(t *testing.T) {
	action := permissionAction("orders.rebuild")
	action.HTTP = nil
	action.NonHTTP = []NonHTTPBinding{{Kind: "job", InvocationKey: "orders.rebuild"}}
	action.EffectClass = EffectWrite
	action.IdempotencyDecision = "natural_key"
	registry := NewRegistry()
	if err := registry.Register(action); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	resolved, ok := registry.ResolveNonHTTP("job", "orders.rebuild")
	if !ok || resolved.Key != action.Key {
		t.Fatalf("resolved=%#v ok=%v", resolved, ok)
	}
}

func TestRegistryRevisionIsFrozenAndDeterministic(t *testing.T) {
	first := permissionAction("orders.read", HTTPBinding{Method: "GET", RouteTemplate: "/orders"})
	second := permissionAction("orders.create", HTTPBinding{Method: "POST", RouteTemplate: "/orders"})
	build := func(definitions ...ActionDefinition) *Registry {
		registry := NewRegistry()
		if registry.Revision() != "" {
			t.Fatal("mutable registry exposed a revision")
		}
		if err := registry.Register(definitions...); err != nil {
			t.Fatal(err)
		}
		if err := registry.Freeze(); err != nil {
			t.Fatal(err)
		}
		return registry
	}
	one := build(first, second)
	two := build(second, first)
	if one.Revision() == "" || one.Revision() != two.Revision() {
		t.Fatalf("revisions differ: %q %q", one.Revision(), two.Revision())
	}
	changed := first
	changed.Label = "Changed"
	if build(changed, second).Revision() == one.Revision() {
		t.Fatal("changed Action did not change registry revision")
	}
}

func TestRegistryRetiredActionDoesNotResolve(t *testing.T) {
	retired := permissionAction("orders.retired", HTTPBinding{Method: "GET", RouteTemplate: "/retired"})
	retired.LifecycleStatus = LifecycleRetired
	retired.Permission.LifecycleStatus = LifecycleRetired
	registry := NewRegistry()
	if err := registry.Register(retired); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Definition(retired.Key); ok {
		t.Fatal("retired action resolved by key")
	}
	if _, ok := registry.ResolveHTTP("GET", "/retired"); ok {
		t.Fatal("retired action resolved by HTTP binding")
	}
}

func TestRegistryRejectsDynamicObjectWildcardDisplayRoute(t *testing.T) {
	definition := permissionAction("objects.read", HTTPBinding{Method: "GET", RouteTemplate: "/objects/{objectKey}/records", DisplayRouteTemplate: "/objects/{objectKey}/records"})
	registry := NewRegistry()
	if err := registry.Register(definition); err == nil || !strings.Contains(err.Error(), "dynamic object wildcard") {
		t.Fatalf("wildcard display error=%v", err)
	}
}

type testActionProvider struct {
	definitions []ActionDefinition
}

func (provider testActionProvider) AuthorizationActions() ([]ActionDefinition, error) {
	result := make([]ActionDefinition, len(provider.definitions))
	for index := range provider.definitions {
		result[index] = CloneDefinition(provider.definitions[index])
	}
	return result, nil
}

var _ Provider = testActionProvider{}
