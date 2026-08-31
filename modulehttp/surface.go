// Package modulehttp defines the deployment-neutral contract used by an
// in-process Domainry module to contribute module-owned HTTP routes to its
// embedding process. It deliberately contains no router, authentication or
// product-domain implementation; the host validates and mounts declarations.
package modulehttp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const ContractVersion = "domainry-module-http-surface-v1"

type Exposure string

const (
	ExposurePublic      Exposure = "public"
	ExposureTenantAdmin Exposure = "tenant_admin"
	ExposureOps         Exposure = "ops"
)

type Authentication string

const (
	AuthenticationAnonymous     Authentication = "anonymous"
	AuthenticationAuthenticated Authentication = "authenticated"
	AuthenticationService       Authentication = "service"
)

// EffectClass classifies whether a route observes state or can change it. The
// host uses this declaration to apply transport governance without importing a
// capability's product-domain implementation.
type EffectClass string

const (
	EffectRead  EffectClass = "read"
	EffectWrite EffectClass = "write"
)

// HighRiskPolicy declares the common evidence a host must verify before a
// request reaches the module handler. Domain-specific authorization remains
// owned by the module application service.
type HighRiskPolicy string

const (
	HighRiskNone                 HighRiskPolicy = "none"
	HighRiskReasonRequired       HighRiskPolicy = "reason_required"
	HighRiskConfirmationRequired HighRiskPolicy = "confirmation_required"
	HighRiskBreakGlassRequired   HighRiskPolicy = "break_glass_required"
)

// Governance is optional for compatibility with existing v1 module surfaces.
// New product routes should declare it so the host can enforce the same
// transport policy regardless of whether the capability runs in Runtime or in
// an embedded module.
type Governance struct {
	EffectClass         EffectClass
	HighRiskPolicy      HighRiskPolicy
	IdempotencyDecision string
	AuditClass          string
}

// Route is a host-enforced declaration. Permission is required for an
// authenticated or service route unless the route is explicitly classified as
// principal_only. Anonymous routes are intended for authentication bootstrap,
// callbacks, health or other reviewed public protocols.
type Route struct {
	Pattern        string
	Exposures      []Exposure
	Authentication Authentication
	Permission     string
	AnyPermissions []string
	PrincipalOnly  bool
	Governance     *Governance
}

type AuditEvent struct {
	Event, ObjectKey, RecordID, WorkspaceID, ActorID, RoleKey, Summary string
	Metadata                                                           map[string]any
}

type AuditRecorder interface {
	Record(context.Context, AuditEvent) error
}

// Surface is implemented by a module Binding when that module owns inbound
// HTTP semantics. SaaS Bindings must not return in-process Surfaces.
type Surface interface {
	ContractVersion() string
	Owner() string
	Name() string
	Routes() []Route
	Handler() http.Handler
}

type Provider interface {
	HTTPSurfaces() []Surface
}

// OpenAPIProvider is implemented by a Surface that owns full OpenAPI
// operations for its declared routes. Keys are exact Route.Pattern values and
// values are OpenAPI operation objects. The host validates route ownership and
// merges copies into its process document.
type OpenAPIProvider interface {
	OpenAPIOperations() map[string]map[string]any
}

// ValidateSurface rejects incomplete and ambient-authority declarations before
// the embedding host adds any route to a listener.
func ValidateSurface(surface Surface) error {
	if surface == nil || surface.Handler() == nil {
		return fmt.Errorf("module HTTP surface is incomplete")
	}
	if surface.ContractVersion() != ContractVersion {
		return fmt.Errorf("module HTTP surface contract is invalid")
	}
	owner, name := strings.TrimSpace(surface.Owner()), strings.TrimSpace(surface.Name())
	if owner == "" || name == "" {
		return fmt.Errorf("module HTTP surface owner and name are required")
	}
	if len(surface.Routes()) == 0 {
		return fmt.Errorf("module HTTP surface %q/%q has no routes", owner, name)
	}
	seen := map[string]bool{}
	for _, route := range surface.Routes() {
		if err := ValidateRoute(route); err != nil {
			return fmt.Errorf("module HTTP surface %q/%q: %w", owner, name, err)
		}
		if seen[route.Pattern] {
			return fmt.Errorf("module HTTP surface %q/%q declares route %q more than once", owner, name, route.Pattern)
		}
		seen[route.Pattern] = true
	}
	if provider, ok := surface.(OpenAPIProvider); ok {
		for pattern, operation := range provider.OpenAPIOperations() {
			if !seen[strings.TrimSpace(pattern)] {
				return fmt.Errorf("module HTTP surface %q/%q publishes OpenAPI for undeclared route %q", owner, name, pattern)
			}
			if len(operation) == 0 || strings.TrimSpace(stringValue(operation["operationId"])) == "" {
				return fmt.Errorf("module HTTP surface %q/%q route %q has incomplete OpenAPI", owner, name, pattern)
			}
		}
	}
	return nil
}

func ValidateRoute(route Route) error {
	if strings.TrimSpace(route.Pattern) == "" {
		return fmt.Errorf("route pattern is required")
	}
	if len(route.Exposures) == 0 {
		return fmt.Errorf("route %q has no exposure", route.Pattern)
	}
	exposures := map[Exposure]bool{}
	for _, exposure := range route.Exposures {
		switch exposure {
		case ExposurePublic, ExposureTenantAdmin, ExposureOps:
		default:
			return fmt.Errorf("route %q has unsupported exposure %q", route.Pattern, exposure)
		}
		if exposures[exposure] {
			return fmt.Errorf("route %q repeats exposure %q", route.Pattern, exposure)
		}
		exposures[exposure] = true
	}
	switch route.Authentication {
	case AuthenticationAnonymous:
		if strings.TrimSpace(route.Permission) != "" || len(route.AnyPermissions) != 0 || route.PrincipalOnly {
			return fmt.Errorf("anonymous route %q cannot declare principal authorization", route.Pattern)
		}
	case AuthenticationAuthenticated, AuthenticationService:
		if strings.TrimSpace(route.Permission) == "" && len(route.AnyPermissions) == 0 && !route.PrincipalOnly {
			return fmt.Errorf("authorized route %q requires a permission or principal_only", route.Pattern)
		}
		if strings.TrimSpace(route.Permission) != "" && len(route.AnyPermissions) != 0 {
			return fmt.Errorf("authorized route %q cannot combine permission and any_permissions", route.Pattern)
		}
	default:
		return fmt.Errorf("route %q has unsupported authentication %q", route.Pattern, route.Authentication)
	}
	if route.Governance != nil {
		if err := validateGovernance(route.Pattern, *route.Governance); err != nil {
			return err
		}
	}
	return nil
}

func validateGovernance(pattern string, governance Governance) error {
	switch governance.EffectClass {
	case EffectRead, EffectWrite:
	default:
		return fmt.Errorf("route %q has unsupported effect class %q", pattern, governance.EffectClass)
	}
	switch governance.HighRiskPolicy {
	case HighRiskNone, HighRiskReasonRequired, HighRiskConfirmationRequired, HighRiskBreakGlassRequired:
	default:
		return fmt.Errorf("route %q has unsupported high-risk policy %q", pattern, governance.HighRiskPolicy)
	}
	if strings.TrimSpace(governance.IdempotencyDecision) == "" {
		return fmt.Errorf("route %q requires an idempotency decision", pattern)
	}
	if strings.TrimSpace(governance.AuditClass) == "" {
		return fmt.Errorf("route %q requires an audit class", pattern)
	}
	return nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
