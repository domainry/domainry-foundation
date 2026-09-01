package modulecapability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
)

// HTTPRouteCategory projects one source-owned module HTTP manifest into a
// capability category. It is a construction helper, not another provider
// lifecycle: the resulting CategoryDocument is still owned and served by the
// module's one capability Binding.
type HTTPRouteCategory struct {
	Category            CategorySummary
	Owner               string
	Routes              []modulehttp.Route
	Operations          map[string]map[string]any
	Components          map[string]map[string]json.RawMessage
	ValidationContracts []ValidationScopeContract
	WorkspaceScope      string
	ExtensionOverrides  map[string]OperationExtension
}

// CategoryFromHTTPRoutes requires a full source-owned OpenAPI Operation for
// every exact Route.Pattern and derives only the common non-OpenAPI extension
// from that route's already-declared authorization and governance policy.
func CategoryFromHTTPRoutes(input HTTPRouteCategory) (CategoryDocument, error) {
	owner := strings.TrimSpace(input.Owner)
	if !keyPattern.MatchString(owner) {
		return CategoryDocument{}, fmt.Errorf("module HTTP capability owner is invalid")
	}
	routes := append([]modulehttp.Route(nil), input.Routes...)
	sort.Slice(routes, func(i, j int) bool { return routes[i].Pattern() < routes[j].Pattern() })
	paths := map[string]map[string]json.RawMessage{}
	for _, route := range routes {
		if err := modulehttp.ValidateRoute(route); err != nil {
			return CategoryDocument{}, err
		}
		pattern := route.Pattern()
		method, path, found := strings.Cut(pattern, " ")
		method, path = strings.ToLower(strings.TrimSpace(method)), strings.TrimSpace(path)
		if !found || method == "" || path == "" {
			return CategoryDocument{}, fmt.Errorf("module HTTP capability route %q is invalid", pattern)
		}
		operation := input.Operations[pattern]
		if len(operation) == 0 {
			return CategoryDocument{}, fmt.Errorf("module HTTP capability route %q has no source-owned OpenAPI Operation", pattern)
		}
		value, err := cloneOperationMap(operation)
		if err != nil {
			return CategoryDocument{}, fmt.Errorf("module HTTP capability route %q: %w", pattern, err)
		}
		extension, overridden := input.ExtensionOverrides[pattern]
		if !overridden {
			extension, err = operationExtensionFromHTTPRoute(owner, input.WorkspaceScope, route)
			if err != nil {
				return CategoryDocument{}, err
			}
		}
		value[OperationExtensionKey] = extension
		payload, err := json.Marshal(value)
		if err != nil {
			return CategoryDocument{}, err
		}
		if paths[path] == nil {
			paths[path] = map[string]json.RawMessage{}
		}
		if _, duplicate := paths[path][method]; duplicate {
			return CategoryDocument{}, fmt.Errorf("module HTTP capability repeats %s %s", strings.ToUpper(method), path)
		}
		paths[path][method] = json.RawMessage(payload)
	}
	input.Category.OperationCount = len(routes)
	components, err := cloneComponents(input.Components)
	if err != nil {
		return CategoryDocument{}, err
	}
	return CategoryDocument{Category: input.Category, OpenAPI: OpenAPIFragment{OpenAPI: "3.1.0", Paths: paths, Components: components}, ValidationContracts: append([]ValidationScopeContract(nil), input.ValidationContracts...)}, nil
}

func operationExtensionFromHTTPRoute(owner, workspaceScope string, route modulehttp.Route) (OperationExtension, error) {
	authorization := Authorization{
		Strategy: route.Action.Authorization.Strategy, PolicyKey: route.Action.Authorization.PolicyKey,
		Audiences: append([]string(nil), route.Action.Authorization.Audiences...),
	}
	if route.Action.Permission != nil {
		authorization.Permission = route.Action.Permission.Key
	}
	if authorization.Strategy != actioncontract.AuthorizationAnonymousProtocol {
		authorization.WorkspaceScope = strings.TrimSpace(workspaceScope)
		if authorization.WorkspaceScope == "" {
			if authorization.Strategy == actioncontract.AuthorizationDelegatedCredential {
				authorization.WorkspaceScope = "credential_workspace"
			} else if authorization.Strategy == actioncontract.AuthorizationServiceIdentity {
				authorization.WorkspaceScope = "application_workspace"
			} else {
				authorization.WorkspaceScope = "authenticated_workspace_principal"
			}
		}
	}
	return OperationExtension{
		Owner: owner, Authorization: authorization,
		Effect:      EffectClass(route.Action.EffectClass),
		Idempotency: Idempotency{Mode: strings.TrimSpace(route.Action.IdempotencyDecision)},
	}, nil
}

func cloneOperationMap(source map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := decodeStrict(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneComponents(source map[string]map[string]json.RawMessage) (map[string]map[string]json.RawMessage, error) {
	if len(source) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var result map[string]map[string]json.RawMessage
	if err := decodeStrict(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}
