package action

type AuthorizationStrategy string

const (
	AuthorizationExactRolePermission    AuthorizationStrategy = "exact_role_permission"
	AuthorizationAnonymousProtocol      AuthorizationStrategy = "anonymous_protocol"
	AuthorizationDelegatedCredential    AuthorizationStrategy = "delegated_credential"
	AuthorizationAuthenticatedPrincipal AuthorizationStrategy = "authenticated_principal"
	AuthorizationSelfOrPermission       AuthorizationStrategy = "self_or_permission"
	AuthorizationServiceIdentity        AuthorizationStrategy = "service_identity"
	AuthorizationOperationsIdentity     AuthorizationStrategy = "operations_identity"
)

// Authorization identifies the one explicit policy shape executed for an
// Action. ExactRolePermission always checks the Action-owned, same-key
// Permission. DelegatedCredential identifies a source-handler-verified,
// workspace-scoped credential rather than an anonymous or bearer principal.
// PolicyKey names source-owned protocol, credential, self, service or
// operations policy; Audiences carries exact service-token audiences. None of
// these fields is a persisted executable pointer.
type Authorization struct {
	Strategy  AuthorizationStrategy `json:"strategy,omitempty"`
	PolicyKey string                `json:"policy_key,omitempty"`
	Audiences []string              `json:"audiences,omitempty"`
}

type Exposure string

const (
	ExposurePublic      Exposure = "public"
	ExposureTenantAdmin Exposure = "tenant_admin"
	ExposureOps         Exposure = "ops"
)

type EffectClass string

const (
	EffectRead  EffectClass = "read"
	EffectWrite EffectClass = "write"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type ApprovalPolicy string

const (
	ApprovalReason       ApprovalPolicy = "reason_required"
	ApprovalConfirmation ApprovalPolicy = "confirmation_required"
	ApprovalBreakGlass   ApprovalPolicy = "break_glass_required"
	ApprovalMakerChecker ApprovalPolicy = "maker_checker"
	ApprovalWorkflow     ApprovalPolicy = "workflow_approval"
)

type LifecycleStatus string

const (
	LifecycleActive     LifecycleStatus = "active"
	LifecycleDeprecated LifecycleStatus = "deprecated"
	LifecycleRetired    LifecycleStatus = "retired"
)

type HTTPBinding struct {
	Method               string `json:"method"`
	RouteTemplate        string `json:"route_template"`
	DisplayRouteTemplate string `json:"display_route_template,omitempty"`
}

type PageBinding struct {
	Route string `json:"route"`
	Label string `json:"label"`
}

type NonHTTPBinding struct {
	Kind          string `json:"kind"`
	InvocationKey string `json:"invocation_key"`
}

// PermissionDefinition contains source-owned definition metadata only. Its
// current administrative enablement and audit timestamps belong to Identity's
// current database snapshot, not this code-owned contract.
type PermissionDefinition struct {
	Key             string          `json:"key"`
	Owner           string          `json:"owner"`
	ResourceKey     string          `json:"resource_key"`
	OperationKey    string          `json:"operation_key"`
	Label           string          `json:"label"`
	Description     string          `json:"description,omitempty"`
	Category        string          `json:"category"`
	LifecycleStatus LifecycleStatus `json:"lifecycle_status"`
}

// ActionDefinition is the normalized executable authorization boundary. Each
// Action has one transport binding identity and at most one same-key
// Permission. A nil Permission means the Action is governed by an exceptional
// anonymous, authenticated, service or operations identity policy.
type ActionDefinition struct {
	Key                 string                `json:"key"`
	Owner               string                `json:"owner"`
	SourceKind          string                `json:"source_kind"`
	CapabilityKey       string                `json:"capability_key"`
	CapabilityLabel     string                `json:"capability_label"`
	OperationKey        string                `json:"operation_key"`
	OperationLabel      string                `json:"operation_label"`
	Label               string                `json:"label"`
	Exposures           []Exposure            `json:"exposures"`
	Authorization       Authorization         `json:"authorization"`
	HTTP                *HTTPBinding          `json:"http,omitempty"`
	Pages               []PageBinding         `json:"pages,omitempty"`
	NonHTTP             []NonHTTPBinding      `json:"non_http,omitempty"`
	Permission          *PermissionDefinition `json:"permission,omitempty"`
	EffectClass         EffectClass           `json:"effect_class"`
	RiskLevel           RiskLevel             `json:"risk_level"`
	AssuranceRequired   []string              `json:"assurance_required,omitempty"`
	ApprovalPolicies    []ApprovalPolicy      `json:"approval_policies,omitempty"`
	IdempotencyDecision string                `json:"idempotency_decision"`
	AuditClass          string                `json:"audit_class"`
	AuditEvent          string                `json:"audit_event,omitempty"`
	LifecycleStatus     LifecycleStatus       `json:"lifecycle_status"`
}

// Provider contributes one module's complete source-owned Action manifest to
// its embedding host. HTTP surfaces, non-HTTP dispatch, permission reconcile
// and configuration projections must all be derived from this same batch.
// Implementations must return detached definitions so callers cannot mutate
// the module's canonical manifest.
type Provider interface {
	AuthorizationActions() ([]ActionDefinition, error)
}

// PermissionUsage is a live reverse projection from a frozen registry. It is
// never a persistence model.
type PermissionUsage struct {
	Permission PermissionDefinition `json:"permission"`
	Action     ActionDefinition     `json:"action"`
}
