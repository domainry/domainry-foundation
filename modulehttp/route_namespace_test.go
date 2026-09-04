package modulehttp

import (
	"net/http"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

func TestValidateRouteNamespaceRequiresModuleRoot(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		owner, path string
		wantErr     bool
	}{
		{owner: "identity", path: "/identity/users"},
		{owner: "identity", path: "/auth/login"},
		{owner: "identity", path: "/.well-known/openid-configuration"},
		{owner: "data_exchange", path: "/data-exchange/jobs"},
		{owner: "notification", path: "/notification/inbox"},
		{owner: "notification", path: "/business/notifications", wantErr: true},
		{owner: "scheduler", path: "/management/scheduler/definitions", wantErr: true},
		{owner: "lifecycle", path: "/operations/lifecycle/policies", wantErr: true},
	} {
		t.Run(test.owner+strings.ReplaceAll(test.path, "/", "_"), func(t *testing.T) {
			route := Route{Action: actioncontract.ActionDefinition{
				Key: "test.route", Owner: "module:" + test.owner, SourceKind: "module_adapter",
				CapabilityKey: "test", CapabilityLabel: "Test", OperationKey: "route", OperationLabel: "Route", Label: "Route",
				Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic}, Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
				HTTP: &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: test.path}, EffectClass: actioncontract.EffectRead,
				RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "owner_read_audit_policy", LifecycleStatus: actioncontract.LifecycleActive,
			}}
			err := ValidateRouteNamespace(test.owner, route)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateRouteNamespace() error=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
}
