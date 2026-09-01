// Package modulecapability defines the deployment-neutral capability and
// candidate-validation facets embedded by every Domainry module SDK Binding.
//
// A Module Binding implements Binding directly. A SaaS Binding implements the
// same interface through the canonical HTTP mapping in this package. The
// contract intentionally contains no Runtime or Plane types.
package modulecapability

import (
	"context"
	"encoding/json"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

const (
	ContractVersion           = "domainry-module-capability-v1"
	ValidationContractVersion = "domainry-module-capability-validation-v1"
	HTTPPrefix                = "/.well-known/domainry/module-capability/v1"
	SummaryPath               = HTTPPrefix + "/summary"
	CategoriesPath            = HTTPPrefix + "/categories/"
	ValidationPath            = HTTPPrefix + "/validate"
	OperationExtensionKey     = "x-domainry-capability"
	MaxCategoryOperations     = 20
	MaxCategoryProjections    = 50
	MaxCategoryValidations    = 50
	MaxCategoryCanonicalBytes = 1 << 20
	MaxReferencedFragments    = 256
	MaxValidationRequestBytes = 4 << 20
)

type DeploymentMode string

const (
	DeploymentModeModule DeploymentMode = "module"
	DeploymentModeSaaS   DeploymentMode = "saas"
)

// Binding is embedded by a module-specific SDK Binding. It is not a second
// provider lifecycle: the already-opened business Binding owns these methods.
type Binding interface {
	CapabilitySummary(context.Context) (ModuleSummary, error)
	CapabilityCategory(context.Context, string) (CategoryDocument, error)
	ValidateCapabilityCandidate(context.Context, ValidationRequest) (ValidationResult, error)
}

// ModuleIdentity is topology-invariant. Current deployment mode is
// deliberately absent; switching between a direct and Remote Binding cannot
// change the disclosed contract.
type ModuleIdentity struct {
	Key                       string           `json:"key"`
	SourceOwner               string           `json:"source_owner"`
	ModuleVersion             string           `json:"module_version"`
	ContractVersion           string           `json:"contract_version"`
	ContractSHA256            string           `json:"contract_sha256"`
	ValidationContractVersion string           `json:"validation_contract_version"`
	ValidationRevision        string           `json:"validation_revision"`
	SupportedDeploymentModes  []DeploymentMode `json:"supported_deployment_modes"`
}

type AdaptationScenarios struct {
	UseWhen              []string          `json:"use_when"`
	DoNotUseWhen         []string          `json:"do_not_use_when"`
	RequirementSignals   []string          `json:"requirement_signals"`
	ProvidedCapabilities []string          `json:"provided_capabilities"`
	RequiredModules      []string          `json:"required_modules"`
	OptionalModules      []string          `json:"optional_modules"`
	ConflictingModules   []string          `json:"conflicting_modules"`
	AssemblyChains       []string          `json:"assembly_chains"`
	ValidationScopes     []string          `json:"validation_scopes"`
	SelectionExamples    []ScenarioExample `json:"selection_examples"`
	RejectionExamples    []ScenarioExample `json:"rejection_examples"`
}

type ScenarioExample struct {
	Requirement string `json:"requirement"`
	Reason      string `json:"reason"`
}

type ModuleSummary struct {
	Identity    ModuleIdentity      `json:"identity"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Scenarios   AdaptationScenarios `json:"scenarios"`
	Categories  []CategorySummary   `json:"categories"`
}

type CategorySummary struct {
	Key              string   `json:"key"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	OperationCount   int      `json:"operation_count"`
	ProjectionCount  int      `json:"projection_count,omitempty"`
	AssemblyChains   []string `json:"assembly_chains"`
	ValidationScopes []string `json:"validation_scopes"`
}

// CategoryDocument is a bounded, complete batch for one exact category. Paths
// contain full OpenAPI Operation objects. Components contains the exact
// transitive component closure used by those operations.
type CategoryDocument struct {
	ContractVersion     string                    `json:"contract_version"`
	ModuleKey           string                    `json:"module_key"`
	ContractSHA256      string                    `json:"contract_sha256"`
	Category            CategorySummary           `json:"category"`
	OpenAPI             OpenAPIFragment           `json:"openapi"`
	Projections         []SourceProjection        `json:"projections,omitempty"`
	ValidationContracts []ValidationScopeContract `json:"validation_contracts,omitempty"`
}

// ValidationScopeContract makes a category's model-authoring validation
// surface discoverable without teaching Plane a module-specific payload
// shape. The owner still performs every semantic check.
type ValidationScopeContract struct {
	Kind                  string             `json:"kind"`
	Description           string             `json:"description"`
	Coverage              ValidationCoverage `json:"coverage"`
	CandidateCollections  []string           `json:"candidate_collections"`
	ReferencedCollections []string           `json:"referenced_collections,omitempty"`
}

// ValidationCoverage declares whether Plane must require one candidate
// binding for every source emission in CandidateCollections, or whether the
// scope applies only when the model explicitly binds a specialized fragment.
// Plane uses this assembly fact only for coverage; it never interprets Value.
type ValidationCoverage string

const (
	ValidationCoverageAllCandidates ValidationCoverage = "all_candidates"
	ValidationCoverageExplicit      ValidationCoverage = "explicit"
)

// SourceProjection carries one immutable source-owned non-endpoint contract,
// such as a registered Connector definition. Large catalogs are split across
// bounded categories; every projection participates in the module digest.
type SourceProjection struct {
	Kind    string          `json:"kind"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}

type OpenAPIFragment struct {
	OpenAPI    string                                `json:"openapi"`
	Paths      map[string]map[string]json.RawMessage `json:"paths"`
	Components map[string]map[string]json.RawMessage `json:"components,omitempty"`
}

type Authorization struct {
	Strategy       actioncontract.AuthorizationStrategy `json:"strategy,omitempty"`
	Permission     string                               `json:"permission,omitempty"`
	PolicyKey      string                               `json:"policy_key,omitempty"`
	Audiences      []string                             `json:"audiences,omitempty"`
	WorkspaceScope string                               `json:"workspace_scope,omitempty"`
}

type EffectClass string

const (
	EffectRead  EffectClass = "read"
	EffectWrite EffectClass = "write"
)

type Idempotency struct {
	Mode      string `json:"mode"`
	KeySource string `json:"key_source,omitempty"`
}

type Transport struct {
	Mode             string `json:"mode"`
	ResumeSemantics  string `json:"resume_semantics,omitempty"`
	DeliveryOrdering string `json:"delivery_ordering,omitempty"`
}

// OperationExtension contains only non-OpenAPI facts needed for module
// selection or correct authoring. Path, method, operationId, schemas and
// security stay in the standard OpenAPI Operation.
type OperationExtension struct {
	Owner         string        `json:"owner"`
	ProviderKey   string        `json:"provider_key,omitempty"`
	Authorization Authorization `json:"authorization"`
	Effect        EffectClass   `json:"effect"`
	Idempotency   Idempotency   `json:"idempotency"`
	Transport     *Transport    `json:"transport,omitempty"`
}

// AuthoringFragment is the one transport-neutral source envelope passed from
// backend/model to an owning module validator. Value is the unchanged emitted
// value; Plane never rewrites it into an endpoint or owner-specific DTO.
type AuthoringFragment struct {
	Collection string          `json:"collection"`
	Key        string          `json:"key"`
	Value      json.RawMessage `json:"value"`
}

type ValidationRequest struct {
	ContractVersion   string              `json:"contract_version"`
	ModuleKey         string              `json:"module_key"`
	CategoryKey       string              `json:"category_key"`
	ContractSHA256    string              `json:"contract_sha256"`
	Kind              string              `json:"kind"`
	Candidate         AuthoringFragment   `json:"candidate"`
	ReferencedContext []AuthoringFragment `json:"referenced_context,omitempty"`
}

type DiagnosticSeverity string

const (
	SeverityError   DiagnosticSeverity = "error"
	SeverityWarning DiagnosticSeverity = "warning"
)

type Diagnostic struct {
	Owner     string             `json:"owner"`
	RuleKey   string             `json:"rule_key"`
	Severity  DiagnosticSeverity `json:"severity"`
	FieldPath string             `json:"field_path"`
	Message   string             `json:"message"`
	Params    map[string]string  `json:"params,omitempty"`
}

type ValidationResult struct {
	ContractVersion string       `json:"contract_version"`
	ModuleKey       string       `json:"module_key"`
	CategoryKey     string       `json:"category_key"`
	ContractSHA256  string       `json:"contract_sha256"`
	Diagnostics     []Diagnostic `json:"diagnostics"`
}

type Validator func(context.Context, ValidationRequest) (ValidationResult, error)
