package modulecapability

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// StaticBinding binds source-owned immutable disclosure to an owner validator.
// It is suitable for both an in-process module and the SaaS server behind a
// Remote Binding.
type StaticBinding struct {
	summary    ModuleSummary
	categories map[string]CategoryDocument
	validator  Validator
}

func NewStaticBinding(summary ModuleSummary, categories []CategoryDocument, validator Validator) (*StaticBinding, error) {
	normalizeModuleSummaryCollections(&summary)
	values := append([]CategoryDocument(nil), categories...)
	sort.Slice(values, func(i, j int) bool { return values[i].Category.Key < values[j].Category.Key })
	for index := range values {
		normalizeCategoryDocumentCollections(&values[index])
		sort.Slice(values[index].Projections, func(i, j int) bool {
			left := values[index].Projections[i].Kind + "\x00" + values[index].Projections[i].Key
			right := values[index].Projections[j].Kind + "\x00" + values[index].Projections[j].Key
			return left < right
		})
		values[index].Category.ProjectionCount = len(values[index].Projections)
	}
	summary.Categories = make([]CategorySummary, len(values))
	for index := range values {
		summary.Categories[index] = values[index].Category
		values[index].ContractVersion = ContractVersion
		values[index].ModuleKey = summary.Identity.Key
		values[index].ContractSHA256 = ""
	}
	summary.Identity.ContractVersion = ContractVersion
	if strings.TrimSpace(summary.Identity.ValidationContractVersion) == "" {
		summary.Identity.ValidationContractVersion = ValidationContractVersion
	}
	digest, err := contractSHA256(summary, values)
	if err != nil {
		return nil, err
	}
	summary.Identity.ContractSHA256 = digest
	byKey := make(map[string]CategoryDocument, len(values))
	for index := range values {
		values[index].ContractSHA256 = digest
		byKey[values[index].Category.Key] = values[index]
	}
	if err := ValidateBundle(summary, values); err != nil {
		return nil, err
	}
	return &StaticBinding{summary: summary, categories: byKey, validator: validator}, nil
}

// normalizeModuleSummaryCollections gives every required JSON collection one
// wire shape. Optional fields use omitempty; required arrays must encode as []
// rather than changing between null and [] according to a constructor's nil
// slice choices.
func normalizeModuleSummaryCollections(summary *ModuleSummary) {
	if summary == nil {
		return
	}
	if summary.Identity.SupportedDeploymentModes == nil {
		summary.Identity.SupportedDeploymentModes = []DeploymentMode{}
	}
	collections := []*[]string{
		&summary.Scenarios.UseWhen,
		&summary.Scenarios.DoNotUseWhen,
		&summary.Scenarios.RequirementSignals,
		&summary.Scenarios.ProvidedCapabilities,
		&summary.Scenarios.RequiredModules,
		&summary.Scenarios.OptionalModules,
		&summary.Scenarios.ConflictingModules,
		&summary.Scenarios.AssemblyChains,
		&summary.Scenarios.ValidationScopes,
	}
	for _, collection := range collections {
		if *collection == nil {
			*collection = []string{}
		}
	}
	if summary.Scenarios.SelectionExamples == nil {
		summary.Scenarios.SelectionExamples = []ScenarioExample{}
	}
	if summary.Scenarios.RejectionExamples == nil {
		summary.Scenarios.RejectionExamples = []ScenarioExample{}
	}
	if summary.Categories == nil {
		summary.Categories = []CategorySummary{}
	}
}

func normalizeCategoryDocumentCollections(document *CategoryDocument) {
	if document == nil {
		return
	}
	if document.Category.AssemblyChains == nil {
		document.Category.AssemblyChains = []string{}
	}
	if document.Category.ValidationScopes == nil {
		document.Category.ValidationScopes = []string{}
	}
	if document.OpenAPI.Paths == nil {
		document.OpenAPI.Paths = map[string]map[string]json.RawMessage{}
	}
}

func (b *StaticBinding) CapabilitySummary(context.Context) (ModuleSummary, error) {
	if b == nil {
		return ModuleSummary{}, fmt.Errorf("module capability binding is unavailable")
	}
	return cloneJSON(b.summary)
}

func (b *StaticBinding) CapabilityCategory(_ context.Context, key string) (CategoryDocument, error) {
	if b == nil {
		return CategoryDocument{}, fmt.Errorf("module capability binding is unavailable")
	}
	value, found := b.categories[strings.TrimSpace(key)]
	if !found {
		return CategoryDocument{}, &Error{StatusCode: 404, Code: "module_capability.category_not_found", Message: "unknown module capability category"}
	}
	return cloneJSON(value)
}

func (b *StaticBinding) ValidateCapabilityCandidate(ctx context.Context, request ValidationRequest) (ValidationResult, error) {
	if b == nil {
		return ValidationResult{}, fmt.Errorf("module capability binding is unavailable")
	}
	request.ContractVersion = strings.TrimSpace(request.ContractVersion)
	request.ModuleKey = strings.TrimSpace(request.ModuleKey)
	request.CategoryKey = strings.TrimSpace(request.CategoryKey)
	request.ContractSHA256 = strings.TrimSpace(request.ContractSHA256)
	if request.ContractVersion != ValidationContractVersion || request.ModuleKey != b.summary.Identity.Key || request.ContractSHA256 != b.summary.Identity.ContractSHA256 {
		return ValidationResult{}, &Error{StatusCode: 409, Code: "module_capability.contract_mismatch", Message: "candidate validation contract identity differs"}
	}
	if _, found := b.categories[request.CategoryKey]; !found {
		return ValidationResult{}, &Error{StatusCode: 404, Code: "module_capability.category_not_found", Message: "unknown module capability category"}
	}
	if err := ValidateValidationRequest(request); err != nil {
		return ValidationResult{}, &Error{StatusCode: 400, Code: "module_capability.validation_request_invalid", Message: err.Error()}
	}
	category := b.categories[request.CategoryKey]
	var validationContract ValidationScopeContract
	for _, contract := range category.ValidationContracts {
		if request.Kind == contract.Kind {
			validationContract = contract
			break
		}
	}
	if validationContract.Kind == "" {
		return ValidationResult{}, &Error{StatusCode: 400, Code: "module_capability.validation_scope_invalid", Message: "candidate kind is outside the category validation scopes"}
	}
	if !containsString(validationContract.CandidateCollections, request.Candidate.Collection) {
		return ValidationResult{}, &Error{StatusCode: 400, Code: "module_capability.candidate_collection_invalid", Message: "candidate collection is outside the validation contract"}
	}
	for _, fragment := range request.ReferencedContext {
		if !containsString(validationContract.ReferencedCollections, fragment.Collection) {
			return ValidationResult{}, &Error{StatusCode: 400, Code: "module_capability.referenced_collection_invalid", Message: "referenced context collection is outside the validation contract"}
		}
	}
	result := ValidationResult{ContractVersion: ValidationContractVersion, ModuleKey: b.summary.Identity.Key, CategoryKey: request.CategoryKey, ContractSHA256: b.summary.Identity.ContractSHA256, Diagnostics: []Diagnostic{}}
	if b.validator != nil {
		value, err := b.validator(ctx, request)
		if err != nil {
			return ValidationResult{}, err
		}
		result.Diagnostics = append(result.Diagnostics, value.Diagnostics...)
	}
	for index := range result.Diagnostics {
		if strings.TrimSpace(result.Diagnostics[index].Owner) == "" {
			result.Diagnostics[index].Owner = b.summary.Identity.SourceOwner
		}
	}
	if err := ValidateValidationResult(result, b.summary.Identity, request.CategoryKey); err != nil {
		return ValidationResult{}, fmt.Errorf("module validator returned an invalid result: %w", err)
	}
	return cloneJSON(result)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneJSON[T any](value T) (T, error) {
	var result T
	payload, err := CanonicalJSON(value)
	if err != nil {
		return result, err
	}
	if err := decodeStrict(payload, &result); err != nil {
		return result, err
	}
	return result, nil
}

var _ Binding = (*StaticBinding)(nil)
