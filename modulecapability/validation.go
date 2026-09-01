package modulecapability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

var openAPIMethods = map[string]bool{
	"delete": true, "get": true, "head": true, "options": true,
	"patch": true, "post": true, "put": true, "trace": true,
}

func ValidateBundle(summary ModuleSummary, categories []CategoryDocument) error {
	if err := ValidateModuleSummary(summary); err != nil {
		return err
	}
	if len(categories) != len(summary.Categories) {
		return fmt.Errorf("module %q category summary/detail count differs", summary.Identity.Key)
	}
	seen := map[string]bool{}
	operationIDs := map[string]string{}
	endpoints := map[string]string{}
	projections := map[string]string{}
	for index, document := range categories {
		if err := validateCategoryDocument(document, summary.Identity, operationIDs, endpoints, projections); err != nil {
			return err
		}
		if seen[document.Category.Key] {
			return fmt.Errorf("module %q repeats category %q", summary.Identity.Key, document.Category.Key)
		}
		seen[document.Category.Key] = true
		left, _ := CanonicalJSON(summary.Categories[index])
		right, _ := CanonicalJSON(document.Category)
		if !bytes.Equal(left, right) {
			return fmt.Errorf("module %q category %q summary differs from detail", summary.Identity.Key, document.Category.Key)
		}
	}
	digest, err := contractSHA256(summary, categories)
	if err != nil {
		return err
	}
	if digest != summary.Identity.ContractSHA256 {
		return fmt.Errorf("module %q capability digest is stale", summary.Identity.Key)
	}
	return nil
}

func ValidateModuleSummary(summary ModuleSummary) error {
	identity := summary.Identity
	if !keyPattern.MatchString(identity.Key) || !keyPattern.MatchString(identity.SourceOwner) {
		return fmt.Errorf("module capability identity key and source owner are required")
	}
	if strings.TrimSpace(identity.ModuleVersion) == "" || identity.ContractVersion != ContractVersion || identity.ValidationContractVersion != ValidationContractVersion || strings.TrimSpace(identity.ValidationRevision) == "" || !sha256Pattern(identity.ContractSHA256) {
		return fmt.Errorf("module %q capability contract identity is invalid", identity.Key)
	}
	if err := validateDeploymentModes(identity.SupportedDeploymentModes); err != nil {
		return fmt.Errorf("module %q: %w", identity.Key, err)
	}
	if strings.TrimSpace(summary.Name) == "" || strings.TrimSpace(summary.Description) == "" {
		return fmt.Errorf("module %q capability name and description are required", identity.Key)
	}
	if summary.Scenarios.UseWhen == nil || summary.Scenarios.DoNotUseWhen == nil || summary.Scenarios.RequirementSignals == nil ||
		summary.Scenarios.ProvidedCapabilities == nil || summary.Scenarios.RequiredModules == nil || summary.Scenarios.OptionalModules == nil ||
		summary.Scenarios.ConflictingModules == nil || summary.Scenarios.AssemblyChains == nil || summary.Scenarios.ValidationScopes == nil ||
		summary.Scenarios.SelectionExamples == nil || summary.Scenarios.RejectionExamples == nil || summary.Categories == nil {
		return fmt.Errorf("module %q capability required collections must be JSON arrays", identity.Key)
	}
	if err := validateScenarios(identity.Key, summary.Scenarios); err != nil {
		return err
	}
	if len(summary.Categories) == 0 {
		return fmt.Errorf("module %q must disclose at least one category", identity.Key)
	}
	previous := ""
	for _, category := range summary.Categories {
		if err := ValidateCategorySummary(category); err != nil {
			return fmt.Errorf("module %q: %w", identity.Key, err)
		}
		if previous != "" && category.Key <= previous {
			return fmt.Errorf("module %q categories must be uniquely sorted", identity.Key)
		}
		previous = category.Key
	}
	return nil
}

func ValidateCategorySummary(category CategorySummary) error {
	if !keyPattern.MatchString(category.Key) || strings.TrimSpace(category.Name) == "" || strings.TrimSpace(category.Description) == "" || category.OperationCount < 0 || category.OperationCount > MaxCategoryOperations || category.ProjectionCount < 0 || category.ProjectionCount > MaxCategoryProjections || len(category.ValidationScopes) > MaxCategoryValidations {
		return fmt.Errorf("module capability category is invalid")
	}
	if category.AssemblyChains == nil || category.ValidationScopes == nil {
		return fmt.Errorf("category %q required collections must be JSON arrays", category.Key)
	}
	if err := validateUniqueNonBlank("assembly chains", category.AssemblyChains, false); err != nil {
		return fmt.Errorf("category %q: %w", category.Key, err)
	}
	if err := validateUniqueNonBlank("validation scopes", category.ValidationScopes, false); err != nil {
		return fmt.Errorf("category %q: %w", category.Key, err)
	}
	return nil
}

func ValidateCategoryDocument(document CategoryDocument, identity ModuleIdentity) error {
	return validateCategoryDocument(document, identity, map[string]string{}, map[string]string{}, map[string]string{})
}

func validateCategoryDocument(document CategoryDocument, identity ModuleIdentity, operationIDs, endpoints, projectionKeys map[string]string) error {
	if document.ContractVersion != ContractVersion || document.ModuleKey != identity.Key || document.ContractSHA256 != identity.ContractSHA256 {
		return fmt.Errorf("module %q category %q contract identity differs", identity.Key, document.Category.Key)
	}
	if err := ValidateCategorySummary(document.Category); err != nil {
		return err
	}
	if document.OpenAPI.Paths == nil {
		return fmt.Errorf("module %q category %q OpenAPI paths must be a JSON object", identity.Key, document.Category.Key)
	}
	operationCount, err := validateOpenAPI(document.OpenAPI, document.Category.Key, operationIDs, endpoints)
	if err != nil {
		return fmt.Errorf("module %q category %q: %w", identity.Key, document.Category.Key, err)
	}
	if operationCount != document.Category.OperationCount {
		return fmt.Errorf("module %q category %q declares %d operations but contains %d", identity.Key, document.Category.Key, document.Category.OperationCount, operationCount)
	}
	if len(document.Projections) != document.Category.ProjectionCount {
		return fmt.Errorf("module %q category %q declares %d projections but contains %d", identity.Key, document.Category.Key, document.Category.ProjectionCount, len(document.Projections))
	}
	if len(document.ValidationContracts) != len(document.Category.ValidationScopes) {
		return fmt.Errorf("module %q category %q declares %d validation scopes but contains %d validation contracts", identity.Key, document.Category.Key, len(document.Category.ValidationScopes), len(document.ValidationContracts))
	}
	for index, contract := range document.ValidationContracts {
		if err := ValidateValidationScopeContract(contract); err != nil {
			return fmt.Errorf("module %q category %q: %w", identity.Key, document.Category.Key, err)
		}
		if contract.Kind != document.Category.ValidationScopes[index] {
			return fmt.Errorf("module %q category %q validation contracts must exactly match its sorted validation scope index", identity.Key, document.Category.Key)
		}
	}
	previousProjection := ""
	for _, projection := range document.Projections {
		if !keyPattern.MatchString(projection.Kind) || strings.TrimSpace(projection.Key) == "" || !validJSONValue(projection.Payload) {
			return fmt.Errorf("module %q category %q contains an invalid source projection", identity.Key, document.Category.Key)
		}
		identityKey := projection.Kind + "\x00" + projection.Key
		if identityKey <= previousProjection {
			return fmt.Errorf("module %q category %q projections must be uniquely sorted", identity.Key, document.Category.Key)
		}
		if previous := projectionKeys[identityKey]; previous != "" {
			return fmt.Errorf("module %q projection %q/%q is repeated by categories %q and %q", identity.Key, projection.Kind, projection.Key, previous, document.Category.Key)
		}
		projectionKeys[identityKey] = document.Category.Key
		previousProjection = identityKey
	}
	if payload, err := CanonicalJSON(document); err != nil {
		return err
	} else if len(payload) > MaxCategoryCanonicalBytes {
		return fmt.Errorf("module %q category %q exceeds the %d-byte canonical budget", identity.Key, document.Category.Key, MaxCategoryCanonicalBytes)
	}
	return nil
}

func ValidateValidationRequest(request ValidationRequest) error {
	if request.ContractVersion != ValidationContractVersion || !keyPattern.MatchString(request.ModuleKey) || !keyPattern.MatchString(request.CategoryKey) || !sha256Pattern(request.ContractSHA256) || !keyPattern.MatchString(request.Kind) {
		return fmt.Errorf("candidate validation identity is invalid")
	}
	if err := ValidateAuthoringFragment(request.Candidate); err != nil {
		return fmt.Errorf("candidate: %w", err)
	}
	if len(request.ReferencedContext) > MaxReferencedFragments {
		return fmt.Errorf("referenced context exceeds %d fragments", MaxReferencedFragments)
	}
	previous := ""
	for _, fragment := range request.ReferencedContext {
		if err := ValidateAuthoringFragment(fragment); err != nil {
			return fmt.Errorf("referenced context: %w", err)
		}
		identity := fragment.Collection + "\x00" + fragment.Key
		if fragment.Collection == request.Candidate.Collection && fragment.Key == request.Candidate.Key {
			return fmt.Errorf("referenced context must not repeat the candidate fragment")
		}
		if previous != "" && identity <= previous {
			return fmt.Errorf("referenced context must be uniquely sorted by collection and key")
		}
		previous = identity
	}
	payload, err := CanonicalJSON(request)
	if err != nil {
		return err
	}
	if len(payload) > MaxValidationRequestBytes {
		return fmt.Errorf("candidate validation request exceeds %d bytes", MaxValidationRequestBytes)
	}
	return nil
}

func ValidateAuthoringFragment(fragment AuthoringFragment) error {
	if !keyPattern.MatchString(fragment.Collection) || !keyPattern.MatchString(fragment.Key) {
		return fmt.Errorf("authoring fragment collection and key are invalid")
	}
	if !validJSONValue(fragment.Value) {
		return fmt.Errorf("authoring fragment value must be one valid non-null JSON value")
	}
	return nil
}

func ValidateValidationScopeContract(contract ValidationScopeContract) error {
	if !keyPattern.MatchString(contract.Kind) || strings.TrimSpace(contract.Description) == "" {
		return fmt.Errorf("module validation contract identity and description are required")
	}
	if contract.Coverage != ValidationCoverageAllCandidates && contract.Coverage != ValidationCoverageExplicit {
		return fmt.Errorf("validation contract %q coverage must be all_candidates or explicit", contract.Kind)
	}
	if err := validateUniqueKeys("candidate_collections", contract.CandidateCollections, true); err != nil {
		return fmt.Errorf("validation contract %q: %w", contract.Kind, err)
	}
	if err := validateUniqueKeys("referenced_collections", contract.ReferencedCollections, false); err != nil {
		return fmt.Errorf("validation contract %q: %w", contract.Kind, err)
	}
	return nil
}

func validateUniqueKeys(name string, values []string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%s requires at least one value", name)
	}
	previous := ""
	for _, value := range values {
		if !keyPattern.MatchString(value) || previous != "" && value <= previous {
			return fmt.Errorf("%s must contain uniquely sorted keys", name)
		}
		previous = value
	}
	return nil
}

func ValidateValidationResult(result ValidationResult, identity ModuleIdentity, categoryKey string) error {
	if result.ContractVersion != ValidationContractVersion || result.ModuleKey != identity.Key || result.CategoryKey != categoryKey || result.ContractSHA256 != identity.ContractSHA256 {
		return fmt.Errorf("validation result identity differs")
	}
	if result.Diagnostics == nil {
		return fmt.Errorf("module %q validation diagnostics must be a JSON array", identity.Key)
	}
	for _, diagnostic := range result.Diagnostics {
		if !keyPattern.MatchString(diagnostic.Owner) || !keyPattern.MatchString(diagnostic.RuleKey) || diagnostic.Severity != SeverityError && diagnostic.Severity != SeverityWarning || strings.TrimSpace(diagnostic.FieldPath) == "" || strings.TrimSpace(diagnostic.Message) == "" {
			return fmt.Errorf("module %q returned an invalid diagnostic", identity.Key)
		}
	}
	return nil
}

func validateScenarios(moduleKey string, scenarios AdaptationScenarios) error {
	required := []struct {
		name   string
		values []string
	}{
		{"use_when", scenarios.UseWhen}, {"do_not_use_when", scenarios.DoNotUseWhen},
		{"requirement_signals", scenarios.RequirementSignals}, {"provided_capabilities", scenarios.ProvidedCapabilities},
		{"assembly_chains", scenarios.AssemblyChains},
	}
	for _, field := range required {
		if err := validateUniqueNonBlank(field.name, field.values, true); err != nil {
			return fmt.Errorf("module %q scenarios: %w", moduleKey, err)
		}
	}
	// Every module Binding exposes ValidateCapabilityCandidate so Plane and the
	// CLI have one stable SDK shape. A module that owns no authorable candidate
	// parameters must disclose no scopes instead of inventing a validation
	// model; StaticBinding then rejects every attempted kind as out of scope.
	if err := validateUniqueNonBlank("validation_scopes", scenarios.ValidationScopes, false); err != nil {
		return fmt.Errorf("module %q scenarios: %w", moduleKey, err)
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"required_modules", scenarios.RequiredModules}, {"optional_modules", scenarios.OptionalModules}, {"conflicting_modules", scenarios.ConflictingModules}} {
		if err := validateModuleKeys(moduleKey, field.name, field.values); err != nil {
			return err
		}
	}
	groups := map[string]string{}
	for _, value := range scenarios.RequiredModules {
		groups[value] = "required_modules"
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"optional_modules", scenarios.OptionalModules}, {"conflicting_modules", scenarios.ConflictingModules}} {
		for _, value := range field.values {
			if previous := groups[value]; previous != "" {
				return fmt.Errorf("module %q scenarios place %q in both %s and %s", moduleKey, value, previous, field.name)
			}
			groups[value] = field.name
		}
	}
	if err := validateExamples("selection_examples", scenarios.SelectionExamples); err != nil {
		return fmt.Errorf("module %q scenarios: %w", moduleKey, err)
	}
	if err := validateExamples("rejection_examples", scenarios.RejectionExamples); err != nil {
		return fmt.Errorf("module %q scenarios: %w", moduleKey, err)
	}
	return nil
}

func validateOpenAPI(document OpenAPIFragment, categoryKey string, operationIDs, endpoints map[string]string) (int, error) {
	if strings.TrimSpace(document.OpenAPI) == "" {
		return 0, fmt.Errorf("OpenAPI fragment requires a version")
	}
	references := map[string]bool{}
	operationCount := 0
	for path, item := range document.Paths {
		if !strings.HasPrefix(path, "/") || len(item) == 0 {
			return 0, fmt.Errorf("OpenAPI path %q is invalid", path)
		}
		for method, raw := range item {
			method = strings.ToLower(strings.TrimSpace(method))
			if !openAPIMethods[method] {
				return 0, fmt.Errorf("OpenAPI path %q contains unsupported member %q", path, method)
			}
			var operation map[string]any
			operationID := ""
			responses, responsesValid := operation["responses"].(map[string]any)
			if err := decodeStrict(raw, &operation); err == nil {
				operationID = strings.TrimSpace(stringValue(operation["operationId"]))
				responses, responsesValid = operation["responses"].(map[string]any)
			}
			if operationID == "" || !responsesValid || len(responses) == 0 {
				return 0, fmt.Errorf("OpenAPI operation %s %s is incomplete", strings.ToUpper(method), path)
			}
			if previous := operationIDs[operationID]; previous != "" {
				return 0, fmt.Errorf("OpenAPI operationId %q is repeated by categories %q and %q", operationID, previous, categoryKey)
			}
			operationIDs[operationID] = categoryKey
			endpoint := strings.ToUpper(method) + " " + path
			if previous := endpoints[endpoint]; previous != "" {
				return 0, fmt.Errorf("OpenAPI endpoint %q is repeated by categories %q and %q", endpoint, previous, categoryKey)
			}
			endpoints[endpoint] = categoryKey
			if err := validateOperationExtension(operation[OperationExtensionKey]); err != nil {
				return 0, fmt.Errorf("OpenAPI operation %s %s: %w", strings.ToUpper(method), path, err)
			}
			if err := collectReferences(operation, references); err != nil {
				return 0, fmt.Errorf("OpenAPI operation %s %s: %w", strings.ToUpper(method), path, err)
			}
			if err := collectSecuritySchemeReferences(operation["security"], references); err != nil {
				return 0, fmt.Errorf("OpenAPI operation %s %s: %w", strings.ToUpper(method), path, err)
			}
			operationCount++
		}
	}
	visited := map[string]bool{}
	for reference := range references {
		if err := resolveComponentReference(reference, document.Components, references, visited); err != nil {
			return 0, err
		}
	}
	declared := []string{}
	for group, values := range document.Components {
		for key := range values {
			declared = append(declared, "#/components/"+escapeJSONPointer(group)+"/"+escapeJSONPointer(key))
		}
	}
	sort.Strings(declared)
	referencedComponents := map[string]bool{}
	for reference := range references {
		root, err := componentRootReference(reference)
		if err != nil {
			return 0, err
		}
		referencedComponents[root] = true
	}
	for _, reference := range declared {
		if !referencedComponents[reference] {
			return 0, fmt.Errorf("OpenAPI component %q is outside the referenced closure", reference)
		}
	}
	return operationCount, nil
}

func collectSecuritySchemeReferences(value any, references map[string]bool) error {
	if value == nil {
		return nil
	}
	requirements, ok := value.([]any)
	if !ok {
		return fmt.Errorf("security requirements are invalid")
	}
	for _, raw := range requirements {
		requirement, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("security requirement is invalid")
		}
		for name, scopes := range requirement {
			if _, ok := scopes.([]any); !ok || strings.TrimSpace(name) == "" {
				return fmt.Errorf("security requirement %q is invalid", name)
			}
			references["#/components/securitySchemes/"+escapeJSONPointer(name)] = true
		}
	}
	return nil
}

func validateOperationExtension(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%s is invalid", OperationExtensionKey)
	}
	var extension OperationExtension
	if err := decodeStrict(payload, &extension); err != nil || !keyPattern.MatchString(extension.Owner) || extension.Effect != EffectRead && extension.Effect != EffectWrite || strings.TrimSpace(extension.Idempotency.Mode) == "" {
		return fmt.Errorf("%s is incomplete", OperationExtensionKey)
	}
	if extension.ProviderKey != "" && !keyPattern.MatchString(extension.ProviderKey) {
		return fmt.Errorf("%s provider key is invalid", OperationExtensionKey)
	}
	authorization := extension.Authorization
	switch authorization.Strategy {
	case actioncontract.AuthorizationExactRolePermission:
		if !keyPattern.MatchString(authorization.Permission) || authorization.PolicyKey != "" || len(authorization.Audiences) != 0 || strings.TrimSpace(authorization.WorkspaceScope) == "" {
			return fmt.Errorf("exact permission authorization is invalid")
		}
	case actioncontract.AuthorizationAnonymousProtocol:
		if authorization.Permission != "" || !keyPattern.MatchString(authorization.PolicyKey) || len(authorization.Audiences) != 0 || authorization.WorkspaceScope != "" {
			return fmt.Errorf("anonymous authorization is invalid")
		}
	case actioncontract.AuthorizationDelegatedCredential:
		if authorization.Permission != "" || !keyPattern.MatchString(authorization.PolicyKey) || len(authorization.Audiences) != 0 || strings.TrimSpace(authorization.WorkspaceScope) == "" {
			return fmt.Errorf("delegated credential authorization is invalid")
		}
	case actioncontract.AuthorizationAuthenticatedPrincipal:
		if authorization.Permission != "" || authorization.PolicyKey != "" || len(authorization.Audiences) != 0 || strings.TrimSpace(authorization.WorkspaceScope) == "" {
			return fmt.Errorf("principal authorization is invalid")
		}
	case actioncontract.AuthorizationSelfOrPermission:
		if !keyPattern.MatchString(authorization.Permission) || !keyPattern.MatchString(authorization.PolicyKey) || len(authorization.Audiences) != 0 || strings.TrimSpace(authorization.WorkspaceScope) == "" {
			return fmt.Errorf("self-or-permission authorization is invalid")
		}
	case actioncontract.AuthorizationServiceIdentity:
		if authorization.Permission != "" || !keyPattern.MatchString(authorization.PolicyKey) || duplicateOrBlankStrings(authorization.Audiences) || len(authorization.Audiences) == 0 || strings.TrimSpace(authorization.WorkspaceScope) == "" {
			return fmt.Errorf("service identity authorization is invalid")
		}
	case actioncontract.AuthorizationOperationsIdentity:
		if authorization.Permission != "" || !keyPattern.MatchString(authorization.PolicyKey) || len(authorization.Audiences) != 0 || strings.TrimSpace(authorization.WorkspaceScope) == "" {
			return fmt.Errorf("operations identity authorization is invalid")
		}
	default:
		return fmt.Errorf("authorization strategy is invalid")
	}
	return nil
}

func duplicateOrBlankStrings(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func collectReferences(value any, result map[string]bool) error {
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			if !strings.HasPrefix(reference, "#/components/") {
				return fmt.Errorf("remote or non-component OpenAPI reference %q is unsupported", reference)
			}
			result[reference] = true
		}
		for _, child := range typed {
			if err := collectReferences(child, result); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := collectReferences(child, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func resolveComponentReference(reference string, components map[string]map[string]json.RawMessage, references, visited map[string]bool) error {
	if visited[reference] {
		return nil
	}
	visited[reference] = true
	parts := strings.Split(strings.TrimPrefix(reference, "#/components/"), "/")
	if len(parts) < 2 {
		return fmt.Errorf("OpenAPI component reference %q is invalid", reference)
	}
	group, key := unescapeJSONPointer(parts[0]), unescapeJSONPointer(parts[1])
	raw, found := components[group][key]
	if !found {
		return fmt.Errorf("OpenAPI component reference %q is missing", reference)
	}
	var value any
	if err := decodeStrict(raw, &value); err != nil {
		return fmt.Errorf("OpenAPI component reference %q is invalid", reference)
	}
	if len(parts) > 2 {
		var pointerErr error
		value, pointerErr = resolveJSONPointer(value, parts[2:])
		if pointerErr != nil {
			return fmt.Errorf("OpenAPI component reference %q is missing", reference)
		}
	}
	before := make(map[string]bool, len(references))
	for existing := range references {
		before[existing] = true
	}
	if err := collectReferences(value, references); err != nil {
		return err
	}
	for child := range references {
		if !before[child] {
			if err := resolveComponentReference(child, components, references, visited); err != nil {
				return err
			}
		}
	}
	return nil
}

func componentRootReference(reference string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(reference, "#/components/"), "/")
	if !strings.HasPrefix(reference, "#/components/") || len(parts) < 2 {
		return "", fmt.Errorf("OpenAPI component reference %q is invalid", reference)
	}
	return "#/components/" + parts[0] + "/" + parts[1], nil
}

func resolveJSONPointer(value any, parts []string) (any, error) {
	current := value
	for _, raw := range parts {
		part := unescapeJSONPointer(raw)
		switch typed := current.(type) {
		case map[string]any:
			var found bool
			current, found = typed[part]
			if !found {
				return nil, fmt.Errorf("JSON pointer member is missing")
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, fmt.Errorf("JSON pointer array index is invalid")
			}
			current = typed[index]
		default:
			return nil, fmt.Errorf("JSON pointer traverses a scalar")
		}
	}
	return current, nil
}

func validateDeploymentModes(values []DeploymentMode) error {
	if len(values) == 0 {
		return fmt.Errorf("supported deployment modes are required")
	}
	seen := map[DeploymentMode]bool{}
	for _, value := range values {
		if value != DeploymentModeModule && value != DeploymentModeSaaS || seen[value] {
			return fmt.Errorf("supported deployment modes are invalid")
		}
		seen[value] = true
	}
	return nil
}

func validateUniqueNonBlank(name string, values []string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("%s are required", name)
	}
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return fmt.Errorf("%s contain a blank or duplicate value", name)
		}
		seen[value] = true
	}
	return nil
}

func validateModuleKeys(moduleKey, name string, values []string) error {
	seen := map[string]bool{}
	for _, value := range values {
		if !keyPattern.MatchString(value) || value == moduleKey || seen[value] {
			return fmt.Errorf("module %q scenarios contain invalid %s", moduleKey, name)
		}
		seen[value] = true
	}
	return nil
}

func validateExamples(name string, values []ScenarioExample) error {
	if len(values) == 0 {
		return fmt.Errorf("%s are required", name)
	}
	seen := map[string]bool{}
	for _, value := range values {
		key := strings.TrimSpace(value.Requirement)
		if key == "" || strings.TrimSpace(value.Reason) == "" || seen[key] {
			return fmt.Errorf("%s contain an incomplete or duplicate example", name)
		}
		seen[key] = true
	}
	return nil
}

func validJSONValue(value json.RawMessage) bool {
	if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return false
	}
	var decoded any
	return decodeStrict(value, &decoded) == nil
}

func decodeStrict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are unsupported")
		}
		return err
	}
	return nil
}

func sha256Pattern(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func escapeJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func unescapeJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~1", "/"), "~0", "~")
}
