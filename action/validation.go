package action

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var stableKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._:/-][a-z0-9]+)*$`)
var resolverKeyPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]*(?:[._:/-][A-Za-z0-9]+)*$`)

var supportedHTTPMethods = map[string]bool{
	"DELETE": true, "GET": true, "HEAD": true, "OPTIONS": true,
	"PATCH": true, "POST": true, "PUT": true,
}

// ValidateDefinition verifies one normalized Action shape. Cross-definition
// ownership, binding conflicts and resolver completeness are validated by a
// Registry when it freezes.
func ValidateDefinition(definition ActionDefinition) error {
	_, err := NormalizeDefinition(definition)
	return err
}

// NormalizeDefinition returns the deterministic detached representation used
// by Registry registration.
func NormalizeDefinition(definition ActionDefinition) (ActionDefinition, error) {
	normalized := normalizeDefinition(definition)
	if err := validateDefinition(normalized); err != nil {
		return ActionDefinition{}, err
	}
	return normalized, nil
}

// CloneDefinition returns a detached copy suitable for adapters and immutable
// projections.
func CloneDefinition(definition ActionDefinition) ActionDefinition {
	return cloneDefinition(definition)
}

func normalizeDefinition(source ActionDefinition) ActionDefinition {
	definition := cloneDefinition(source)
	definition.Key = strings.TrimSpace(definition.Key)
	definition.Owner = strings.TrimSpace(definition.Owner)
	definition.SourceKind = strings.TrimSpace(definition.SourceKind)
	definition.CapabilityKey = strings.TrimSpace(definition.CapabilityKey)
	definition.CapabilityLabel = strings.TrimSpace(definition.CapabilityLabel)
	definition.OperationKey = strings.TrimSpace(definition.OperationKey)
	definition.OperationLabel = strings.TrimSpace(definition.OperationLabel)
	definition.Label = strings.TrimSpace(definition.Label)
	definition.Authorization.PolicyKey = strings.TrimSpace(definition.Authorization.PolicyKey)
	for index := range definition.Authorization.Audiences {
		definition.Authorization.Audiences[index] = strings.TrimSpace(definition.Authorization.Audiences[index])
	}
	sort.Strings(definition.Authorization.Audiences)
	definition.IdempotencyDecision = strings.TrimSpace(definition.IdempotencyDecision)
	definition.AuditClass = strings.TrimSpace(definition.AuditClass)
	definition.AuditEvent = strings.TrimSpace(definition.AuditEvent)
	for index := range definition.Exposures {
		definition.Exposures[index] = Exposure(strings.TrimSpace(string(definition.Exposures[index])))
	}
	sort.Slice(definition.Exposures, func(i, j int) bool { return definition.Exposures[i] < definition.Exposures[j] })
	if definition.HTTP != nil {
		definition.HTTP.Method = strings.ToUpper(strings.TrimSpace(definition.HTTP.Method))
		definition.HTTP.RouteTemplate = strings.TrimSpace(definition.HTTP.RouteTemplate)
		definition.HTTP.DisplayRouteTemplate = strings.TrimSpace(definition.HTTP.DisplayRouteTemplate)
	}
	for index := range definition.Pages {
		definition.Pages[index].Route = strings.TrimSpace(definition.Pages[index].Route)
		definition.Pages[index].Label = strings.TrimSpace(definition.Pages[index].Label)
	}
	sort.Slice(definition.Pages, func(i, j int) bool {
		if definition.Pages[i].Route != definition.Pages[j].Route {
			return definition.Pages[i].Route < definition.Pages[j].Route
		}
		return definition.Pages[i].Label < definition.Pages[j].Label
	})
	for index := range definition.NonHTTP {
		definition.NonHTTP[index].Kind = strings.TrimSpace(definition.NonHTTP[index].Kind)
		definition.NonHTTP[index].InvocationKey = strings.TrimSpace(definition.NonHTTP[index].InvocationKey)
	}
	sort.Slice(definition.NonHTTP, func(i, j int) bool {
		if definition.NonHTTP[i].Kind != definition.NonHTTP[j].Kind {
			return definition.NonHTTP[i].Kind < definition.NonHTTP[j].Kind
		}
		return definition.NonHTTP[i].InvocationKey < definition.NonHTTP[j].InvocationKey
	})
	if definition.Permission != nil {
		permission := definition.Permission
		permission.Key = strings.TrimSpace(permission.Key)
		permission.Owner = strings.TrimSpace(permission.Owner)
		permission.ResourceKey = strings.TrimSpace(permission.ResourceKey)
		permission.OperationKey = strings.TrimSpace(permission.OperationKey)
		permission.Label = strings.TrimSpace(permission.Label)
		permission.Description = strings.TrimSpace(permission.Description)
		permission.Category = strings.TrimSpace(permission.Category)
	}
	for index := range definition.AssuranceRequired {
		definition.AssuranceRequired[index] = strings.TrimSpace(definition.AssuranceRequired[index])
	}
	sort.Strings(definition.AssuranceRequired)
	sort.Slice(definition.ApprovalPolicies, func(i, j int) bool { return definition.ApprovalPolicies[i] < definition.ApprovalPolicies[j] })
	return definition
}

func validateDefinition(definition ActionDefinition) error {
	for _, field := range []struct{ name, value string }{
		{name: "key", value: definition.Key}, {name: "owner", value: definition.Owner}, {name: "source kind", value: definition.SourceKind},
		{name: "capability key", value: definition.CapabilityKey}, {name: "capability label", value: definition.CapabilityLabel},
		{name: "operation key", value: definition.OperationKey}, {name: "operation label", value: definition.OperationLabel},
		{name: "label", value: definition.Label}, {name: "idempotency decision", value: definition.IdempotencyDecision},
		{name: "audit class", value: definition.AuditClass},
	} {
		name, value := field.name, field.value
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("action %s is required", name)
		}
	}
	for _, field := range []struct{ name, value string }{
		{name: "key", value: definition.Key}, {name: "owner", value: definition.Owner}, {name: "source kind", value: definition.SourceKind},
		{name: "capability key", value: definition.CapabilityKey}, {name: "operation key", value: definition.OperationKey},
	} {
		name, value := field.name, field.value
		if !stableKeyPattern.MatchString(value) {
			return fmt.Errorf("action %s %q is invalid", name, value)
		}
	}
	if len(definition.Exposures) == 0 {
		return fmt.Errorf("action %q requires an exposure", definition.Key)
	}
	seenExposures := map[Exposure]bool{}
	for _, exposure := range definition.Exposures {
		switch exposure {
		case ExposurePublic, ExposureManagement, ExposureOps:
		default:
			return fmt.Errorf("action %q has unsupported exposure %q", definition.Key, exposure)
		}
		if seenExposures[exposure] {
			return fmt.Errorf("action %q repeats exposure %q", definition.Key, exposure)
		}
		seenExposures[exposure] = true
	}
	if definition.HTTP == nil && len(definition.NonHTTP) == 0 {
		return fmt.Errorf("action %q requires an executable binding", definition.Key)
	}
	if definition.HTTP != nil {
		binding := *definition.HTTP
		if !supportedHTTPMethods[binding.Method] || !validRouteTemplate(binding.RouteTemplate) {
			return fmt.Errorf("action %q has an invalid HTTP binding", definition.Key)
		}
		if display := binding.DisplayRouteTemplate; display != "" {
			if !validRouteTemplate(display) {
				return fmt.Errorf("action %q has an invalid display route template", definition.Key)
			}
			if strings.Contains(binding.RouteTemplate, "{objectKey}") && strings.Contains(display, "{objectKey}") {
				return fmt.Errorf("action %q exposes a dynamic object wildcard as its display route", definition.Key)
			}
			if strings.Contains(binding.RouteTemplate, "{actionKey}") && strings.Contains(display, "{actionKey}") {
				return fmt.Errorf("action %q exposes a dynamic action wildcard as its display route", definition.Key)
			}
		}
	}
	seenPages := map[string]bool{}
	for _, page := range definition.Pages {
		if !validRouteTemplate(page.Route) || page.Label == "" {
			return fmt.Errorf("action %q has an invalid page binding", definition.Key)
		}
		identity := page.Route + "\x00" + page.Label
		if seenPages[identity] {
			return fmt.Errorf("action %q repeats a page binding", definition.Key)
		}
		seenPages[identity] = true
	}
	seenNonHTTP := map[string]bool{}
	for _, binding := range definition.NonHTTP {
		if !stableKeyPattern.MatchString(binding.Kind) || !stableKeyPattern.MatchString(binding.InvocationKey) {
			return fmt.Errorf("action %q has an invalid non-HTTP binding", definition.Key)
		}
		identity := nonHTTPIdentity(binding.Kind, binding.InvocationKey)
		if seenNonHTTP[identity] {
			return fmt.Errorf("action %q repeats non-HTTP binding %q", definition.Key, identity)
		}
		seenNonHTTP[identity] = true
	}
	if err := validateAuthorization(definition); err != nil {
		return err
	}
	if definition.Permission != nil {
		if err := validatePermissionDefinition(definition, *definition.Permission); err != nil {
			return err
		}
	}
	switch definition.EffectClass {
	case EffectRead, EffectWrite:
	default:
		return fmt.Errorf("action %q has unsupported effect class %q", definition.Key, definition.EffectClass)
	}
	switch definition.RiskLevel {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
	default:
		return fmt.Errorf("action %q has unsupported risk level %q", definition.Key, definition.RiskLevel)
	}
	if duplicateOrBlank(definition.AssuranceRequired) {
		return fmt.Errorf("action %q has an invalid assurance requirement", definition.Key)
	}
	seenApproval := map[ApprovalPolicy]bool{}
	for _, policy := range definition.ApprovalPolicies {
		switch policy {
		case ApprovalReason, ApprovalConfirmation, ApprovalBreakGlass, ApprovalMakerChecker, ApprovalWorkflow:
		default:
			return fmt.Errorf("action %q has unsupported approval policy %q", definition.Key, policy)
		}
		if seenApproval[policy] {
			return fmt.Errorf("action %q repeats approval policy %q", definition.Key, policy)
		}
		seenApproval[policy] = true
	}
	switch definition.LifecycleStatus {
	case LifecycleActive, LifecycleDeprecated, LifecycleRetired:
	default:
		return fmt.Errorf("action %q has unsupported lifecycle status %q", definition.Key, definition.LifecycleStatus)
	}
	return nil
}

func validateAuthorization(definition ActionDefinition) error {
	authorization := definition.Authorization
	hasPermission := definition.Permission != nil
	switch authorization.Strategy {
	case AuthorizationAnonymous:
		if hasPermission || authorization.PolicyKey != "" || len(authorization.Audiences) != 0 {
			return fmt.Errorf("anonymous action %q has an invalid authorization declaration", definition.Key)
		}
	case AuthorizationAuthenticated:
		if len(authorization.Audiences) != 0 || authorization.PolicyKey != "" && !hasPermission {
			return fmt.Errorf("authenticated action %q has an invalid authorization declaration", definition.Key)
		}
	case AuthorizationSigned:
		if hasPermission || !resolverKeyPattern.MatchString(authorization.PolicyKey) || duplicateOrBlank(authorization.Audiences) {
			return fmt.Errorf("signed action %q has an invalid authorization declaration", definition.Key)
		}
	default:
		return fmt.Errorf("action %q has unsupported authorization strategy %q", definition.Key, authorization.Strategy)
	}
	return nil
}

func validatePermissionDefinition(action ActionDefinition, permission PermissionDefinition) error {
	if !stableKeyPattern.MatchString(permission.Key) || !stableKeyPattern.MatchString(permission.Owner) ||
		!stableKeyPattern.MatchString(permission.ResourceKey) || !stableKeyPattern.MatchString(permission.OperationKey) ||
		permission.Label == "" || permission.Category == "" {
		return fmt.Errorf("action %q has an incomplete owned permission", action.Key)
	}
	if permission.Owner != action.Owner {
		return fmt.Errorf("action %q cannot own permission %q for owner %q", action.Key, permission.Key, permission.Owner)
	}
	if permission.Key != action.Key {
		return fmt.Errorf("action %q permission key must equal its action key", action.Key)
	}
	if permission.Key != permission.ResourceKey+"."+permission.OperationKey {
		return fmt.Errorf("action %q permission resource and operation must compose its key", action.Key)
	}
	if permission.LifecycleStatus != LifecycleActive && permission.LifecycleStatus != LifecycleRetired {
		return fmt.Errorf("action %q permission %q has unsupported lifecycle status %q", action.Key, permission.Key, permission.LifecycleStatus)
	}
	return nil
}

func validRouteTemplate(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, " \t\r\n?#")
}

func duplicateOrBlank(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func httpIdentity(method, routeTemplate string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(routeTemplate)
}

// definitionHTTPIdentity indexes a path-resolved Action by its concrete
// display route. Generic router templates remain owned by their compiled
// endpoint Action and dispatch to the concrete Action through NonHTTP.
func definitionHTTPIdentity(definition ActionDefinition) string {
	if definition.HTTP == nil {
		return ""
	}
	route := definition.HTTP.RouteTemplate
	if len(definition.NonHTTP) != 0 && definition.HTTP.DisplayRouteTemplate != "" {
		route = definition.HTTP.DisplayRouteTemplate
	}
	return httpIdentity(definition.HTTP.Method, route)
}

func nonHTTPIdentity(kind, invocationKey string) string {
	return strings.TrimSpace(kind) + "\x00" + strings.TrimSpace(invocationKey)
}

func permissionIdentity(owner, key string) string {
	return strings.TrimSpace(owner) + "\x00" + strings.TrimSpace(key)
}
