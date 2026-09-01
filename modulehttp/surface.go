// Package modulehttp defines the deployment-neutral contract used by an
// in-process Domainry module to contribute module-owned HTTP routes to its
// embedding process. It deliberately contains no router, authentication or
// product-domain implementation; the host validates and mounts declarations.
package modulehttp

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

const ContractVersion = "domainry-module-http-surface-v2"

type Exposure = actioncontract.Exposure

const (
	ExposurePublic      = actioncontract.ExposurePublic
	ExposureTenantAdmin = actioncontract.ExposureTenantAdmin
	ExposureOps         = actioncontract.ExposureOps
)

// Route contains one canonical, source-owned Action declaration. The host
// derives the HTTP pattern, exposure, authorization and transport governance
// from Action; Route intentionally has no second permission policy shape.
type Route struct {
	Action actioncontract.ActionDefinition
}

// Pattern returns the exact net/http registration identity owned by Action.
func (route Route) Pattern() string {
	if route.Action.HTTP == nil {
		return ""
	}
	return strings.TrimSpace(route.Action.HTTP.Method) + " " + strings.TrimSpace(route.Action.HTTP.RouteTemplate)
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

// AuthorizationActions returns the detached Action manifest carried by an
// HTTP-only module's Surfaces. A module that also owns non-HTTP Actions must
// implement action.Provider and use ValidateAuthorizationProjection to prove
// that its HTTP routes are exact projections of that complete manifest.
func AuthorizationActions(provider Provider) ([]actioncontract.ActionDefinition, error) {
	if provider == nil {
		return nil, fmt.Errorf("module HTTP provider is required")
	}
	return AuthorizationActionsFromSurfaces(provider.HTTPSurfaces())
}

// AuthorizationActionsFromSurfaces is the slice form used after a host has
// already detached its module Surface inventory.
func AuthorizationActionsFromSurfaces(surfaces []Surface) ([]actioncontract.ActionDefinition, error) {
	definitions := []actioncontract.ActionDefinition{}
	seen := map[string]bool{}
	for _, surface := range surfaces {
		if err := ValidateSurface(surface); err != nil {
			return nil, err
		}
		for _, route := range surface.Routes() {
			definition, err := actioncontract.NormalizeDefinition(route.Action)
			if err != nil {
				return nil, fmt.Errorf("module HTTP surface %q action: %w", surface.Owner(), err)
			}
			if seen[definition.Key] {
				return nil, fmt.Errorf("module HTTP action %q is mounted more than once", definition.Key)
			}
			seen[definition.Key] = true
			definitions = append(definitions, definition)
		}
	}
	return definitions, nil
}

// ValidateSourceOwners enforces the canonical module owner convention without
// applying it to host-owned Surfaces that happen to use the same transport
// contract.
func ValidateSourceOwners(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("module HTTP provider is required")
	}
	for _, surface := range provider.HTTPSurfaces() {
		if err := ValidateSurface(surface); err != nil {
			return err
		}
		expectedOwner := "module:" + strings.TrimSpace(surface.Owner())
		for _, route := range surface.Routes() {
			definition, err := actioncontract.NormalizeDefinition(route.Action)
			if err != nil {
				return fmt.Errorf("module HTTP surface %q action: %w", surface.Owner(), err)
			}
			if definition.Owner != expectedOwner {
				return fmt.Errorf("module HTTP surface %q action %q owner=%q want=%q", surface.Owner(), definition.Key, definition.Owner, expectedOwner)
			}
		}
	}
	return nil
}

// ValidateAuthorizationProjection proves that every active HTTP Action in a
// module's complete source manifest has exactly one byte-for-byte-equivalent
// normalized Surface route, and that no Surface invents an extra Action. A nil
// HTTP provider is valid only for a manifest containing no active HTTP Action.
func ValidateAuthorizationProjection(definitions []actioncontract.ActionDefinition, provider Provider) error {
	manifest := make(map[string]actioncontract.ActionDefinition, len(definitions))
	for index := range definitions {
		definition, err := actioncontract.NormalizeDefinition(definitions[index])
		if err != nil {
			return fmt.Errorf("module authorization Action %d: %w", index, err)
		}
		if _, duplicate := manifest[definition.Key]; duplicate {
			return fmt.Errorf("module Action manifest repeats %q", definition.Key)
		}
		manifest[definition.Key] = definition
	}

	mounted := map[string]bool{}
	if provider != nil {
		routes, err := AuthorizationActions(provider)
		if err != nil {
			return err
		}
		for _, routeAction := range routes {
			canonical, found := manifest[routeAction.Key]
			if !found {
				return fmt.Errorf("module HTTP action %q is absent from its source manifest", routeAction.Key)
			}
			if !reflect.DeepEqual(routeAction, canonical) {
				return fmt.Errorf("module HTTP action %q differs from its source manifest", routeAction.Key)
			}
			mounted[routeAction.Key] = true
		}
	}
	for _, definition := range manifest {
		if definition.LifecycleStatus != actioncontract.LifecycleRetired && definition.HTTP != nil && !mounted[definition.Key] {
			return fmt.Errorf("module HTTP action %q has no mounted surface route", definition.Key)
		}
	}
	return nil
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
		pattern := route.Pattern()
		if seen[pattern] {
			return fmt.Errorf("module HTTP surface %q/%q declares route %q more than once", owner, name, pattern)
		}
		seen[pattern] = true
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
	definition, err := actioncontract.NormalizeDefinition(route.Action)
	if err != nil {
		return fmt.Errorf("module HTTP route action: %w", err)
	}
	if definition.HTTP == nil {
		return fmt.Errorf("module HTTP route action %q has no HTTP binding", definition.Key)
	}
	return nil
}

// RouteFromAction validates and detaches one canonical HTTP Action.
func RouteFromAction(definition actioncontract.ActionDefinition) (Route, error) {
	var err error
	definition, err = actioncontract.NormalizeDefinition(definition)
	if err != nil {
		return Route{}, err
	}
	if definition.HTTP == nil {
		return Route{}, fmt.Errorf("action %q has no HTTP binding", definition.Key)
	}
	return Route{Action: definition}, nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
