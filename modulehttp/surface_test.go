package modulehttp

import (
	"net/http"
	"strings"
	"testing"
)

type testSurface struct {
	contract string
	owner    string
	name     string
	routes   []Route
	handler  http.Handler
}

func (surface testSurface) ContractVersion() string { return surface.contract }
func (surface testSurface) Owner() string           { return surface.owner }
func (surface testSurface) Name() string            { return surface.name }
func (surface testSurface) Routes() []Route         { return surface.routes }
func (surface testSurface) Handler() http.Handler   { return surface.handler }

func TestValidateSurfaceAcceptsHostEnforcedModuleRoutes(t *testing.T) {
	surface := testSurface{
		contract: ContractVersion, owner: "party", name: "party_management", handler: http.NotFoundHandler(),
		routes: []Route{
			{Pattern: "GET /party/{partyID}", Exposures: []Exposure{ExposureTenantAdmin}, Authentication: AuthenticationAuthenticated, Permission: "party.read"},
			{Pattern: "GET /party/me", Exposures: []Exposure{ExposurePublic, ExposureTenantAdmin}, Authentication: AuthenticationAuthenticated, PrincipalOnly: true},
		},
	}
	if err := ValidateSurface(surface); err != nil {
		t.Fatalf("validate surface: %v", err)
	}
}

func TestValidateSurfaceRejectsIncompleteAuthorityDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		want  string
	}{
		{name: "no exposure", route: Route{Pattern: "GET /party", Authentication: AuthenticationAuthenticated, Permission: "party.read"}, want: "no exposure"},
		{name: "ambient authenticated", route: Route{Pattern: "GET /party", Exposures: []Exposure{ExposureTenantAdmin}, Authentication: AuthenticationAuthenticated}, want: "requires a permission"},
		{name: "anonymous permission", route: Route{Pattern: "GET /auth/callback", Exposures: []Exposure{ExposurePublic}, Authentication: AuthenticationAnonymous, Permission: "identity.login"}, want: "cannot declare"},
		{name: "unknown exposure", route: Route{Pattern: "GET /party", Exposures: []Exposure{"private"}, Authentication: AuthenticationAuthenticated, Permission: "party.read"}, want: "unsupported exposure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			surface := testSurface{contract: ContractVersion, owner: "party", name: "party", handler: http.NotFoundHandler(), routes: []Route{test.route}}
			err := ValidateSurface(surface)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want fragment %q", err, test.want)
			}
		})
	}
}

func TestValidateSurfaceRejectsDuplicateRoutes(t *testing.T) {
	route := Route{Pattern: "GET /party", Exposures: []Exposure{ExposureTenantAdmin}, Authentication: AuthenticationAuthenticated, Permission: "party.read"}
	surface := testSurface{contract: ContractVersion, owner: "party", name: "party", handler: http.NotFoundHandler(), routes: []Route{route, route}}
	if err := ValidateSurface(surface); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("unexpected error: %v", err)
	}
}

var _ Surface = testSurface{}
