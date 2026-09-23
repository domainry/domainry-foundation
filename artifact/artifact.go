// Package artifact defines the deployment-neutral shared artifact contract.
// Owners persist immutable content outside SQL and store only governed metadata
// plus resource bindings through this port.
package artifact

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

var (
	ErrIdentityConflict = errors.New("artifact identity conflicts with an existing artifact")
	ErrBindingConflict  = errors.New("artifact binding identity conflicts with an existing binding")
	ErrContentNotFound  = errors.New("artifact content not found")
)

// ContentInfo describes immutable bytes in deployment-owned storage. The
// shared SQL tables retain only Reference and integrity metadata.
type ContentInfo struct {
	Reference string
	SHA256    string
	Size      int64
}

type ContentStore interface {
	Open(context.Context, string, string) (io.ReadCloser, error)
	Stat(context.Context, string, string) (ContentInfo, error)
	Delete(context.Context, string, string) error
}

type ContentWriter interface {
	PutImmutable(context.Context, string, string, []byte) (ContentInfo, error)
}

type Status string

const (
	StatusPending   Status = "pending"
	StatusAvailable Status = "available"
	StatusRejected  Status = "rejected"
	StatusExpired   Status = "expired"
	StatusDeleted   Status = "deleted"
)

type ScanStatus string

const (
	ScanNotRequired ScanStatus = "not_required"
	ScanPending     ScanStatus = "pending"
	ScanClean       ScanStatus = "clean"
	ScanRejected    ScanStatus = "rejected"
	ScanFailed      ScanStatus = "failed"
)

const (
	OwnerAgent        = "agent"
	OwnerAudit        = "audit"
	OwnerDataExchange = "data_exchange"
	OwnerLifecycle    = "lifecycle"
	OwnerOperations   = "operations"
	OwnerReport       = "report"
	OwnerUploads      = "uploads"

	BindingObjectField  = "object_field"
	BindingConversation = "conversation"
	BindingJob          = "job"
	BindingOperation    = "operation"
	BindingSubject      = "subject"
)

// Artifact is metadata for immutable binary content. StorageReference points
// to deployment-owned content; database rows never contain the bytes.
type Artifact struct {
	ID                       string          `json:"id"`
	WorkspaceID              string          `json:"workspace_id"`
	Owner                    string          `json:"owner"`
	Kind                     string          `json:"kind"`
	IdempotencyKey           string          `json:"idempotency_key"`
	CreatedBy                string          `json:"created_by"`
	OwnerOrgID               string          `json:"owner_org_id,omitempty"`
	Filename                 string          `json:"filename"`
	MediaType                string          `json:"media_type"`
	ContentSHA256            string          `json:"content_sha256"`
	SizeBytes                int64           `json:"size_bytes"`
	StorageReference         string          `json:"storage_reference"`
	Status                   Status          `json:"status"`
	ExpiresAt                time.Time       `json:"expires_at,omitempty"`
	ScanStatus               ScanStatus      `json:"scan_status"`
	DownloadTokenSHA256      string          `json:"-"`
	AuthorizationScopeSHA256 string          `json:"authorization_scope_sha256,omitempty"`
	Metadata                 json.RawMessage `json:"metadata,omitempty"`
	CreatedAt                time.Time       `json:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at"`
}

// Binding associates an artifact with a durable owner resource without
// copying content or authorization metadata.
type Binding struct {
	ID           string          `json:"id"`
	WorkspaceID  string          `json:"workspace_id"`
	ArtifactID   string          `json:"artifact_id"`
	Owner        string          `json:"owner"`
	Kind         string          `json:"kind"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	FieldKey     string          `json:"field_key,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
}

type Registration struct {
	Owner          string
	Kind           string
	ContentStorage ContentStorage
}

type ContentStorage string

const (
	ContentStorageBlob  ContentStorage = "blob"
	ContentStorageOwner ContentStorage = "owner"
)

var registrations = map[string]Registration{
	"agent:attachment":            {Owner: OwnerAgent, Kind: "attachment", ContentStorage: ContentStorageBlob},
	"agent:generated":             {Owner: OwnerAgent, Kind: "generated", ContentStorage: ContentStorageBlob},
	"audit:export":                {Owner: OwnerAudit, Kind: "export", ContentStorage: ContentStorageBlob},
	"data_exchange:import_source": {Owner: OwnerDataExchange, Kind: "import_source", ContentStorage: ContentStorageOwner},
	"data_exchange:output":        {Owner: OwnerDataExchange, Kind: "output", ContentStorage: ContentStorageOwner},
	"lifecycle:archive":           {Owner: OwnerLifecycle, Kind: "archive", ContentStorage: ContentStorageBlob},
	"lifecycle:subject_export":    {Owner: OwnerLifecycle, Kind: "subject_export", ContentStorage: ContentStorageBlob},
	"operations:result":           {Owner: OwnerOperations, Kind: "result", ContentStorage: ContentStorageBlob},
	"report:export":               {Owner: OwnerReport, Kind: "export", ContentStorage: ContentStorageBlob},
	"uploads:file":                {Owner: OwnerUploads, Kind: "file", ContentStorage: ContentStorageBlob},
}

var bindingKinds = map[string]bool{
	BindingObjectField: true, BindingConversation: true, BindingJob: true,
	BindingOperation: true, BindingSubject: true,
}

func RegistrationFor(owner, kind string) (Registration, bool) {
	owner, kind = strings.TrimSpace(owner), strings.TrimSpace(kind)
	registration, found := registrations[owner+":"+kind]
	return registration, found
}

func BindingKindRegistered(kind string) bool { return bindingKinds[strings.TrimSpace(kind)] }

// Store is the shared durable artifact port. Implementations must honor an
// Executor carried by the context so an owner mutation, artifact registration
// and binding can commit or roll back together.
type Store interface {
	Register(context.Context, Artifact) (Artifact, bool, error)
	ByID(context.Context, string, string) (Artifact, bool, error)
	ByDownloadTokenHash(context.Context, string, string) (Artifact, bool, error)
	Transition(context.Context, string, string, Status, Status, ScanStatus, time.Time) (bool, error)
	Bind(context.Context, Binding) (Binding, bool, error)
	Bindings(context.Context, string, string) ([]Binding, error)
}

// Query is the bounded shared metadata query used by owner modules. An empty
// workspace selects all workspaces and is reserved for callers that already
// established trusted system scope.
type Query struct {
	Owner             string
	Kind              string
	Filename          string
	StorageReference  string
	Binding           *BindingQuery
	Statuses          []Status
	ScanStatuses      []ScanStatus
	CreatedBy         []string
	OwnerOrgIDs       []string
	ExpiresAtOrBefore time.Time
	NewestFirst       bool
	Limit             int
}

// BindingQuery restricts artifacts to one durable resource relationship. All
// non-empty fields are exact matches; Kind is required whenever Binding is
// supplied so a caller cannot accidentally join every relationship class.
type BindingQuery struct {
	Owner        string
	Kind         string
	ResourceType string
	ResourceID   string
	FieldKey     string
}

type Lister interface {
	List(context.Context, string, Query) ([]Artifact, error)
}

// Mutation is an exact compare-and-set transition for shared artifact state
// and owner metadata. Content identity and storage reference remain immutable.
type Mutation struct {
	WorkspaceID        string
	ID                 string
	Owner              string
	Kind               string
	ExpectedStatus     Status
	ExpectedScanStatus ScanStatus
	ExpectedUpdatedAt  time.Time
	Status             Status
	ScanStatus         ScanStatus
	ExpiresAt          time.Time
	Metadata           json.RawMessage
	UpdatedAt          time.Time
}

type Updater interface {
	Update(context.Context, Mutation) (bool, error)
}

type ManagedStore interface {
	Store
	Lister
	Updater
}

// Executor is the SQL transaction surface needed by a shared Store. It is
// intentionally carried only in process; remote/SaaS stores ignore it.
type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type executorContextKey struct{}

func WithExecutor(ctx context.Context, executor Executor) context.Context {
	if executor == nil {
		return ctx
	}
	return context.WithValue(ctx, executorContextKey{}, executor)
}

func ExecutorFromContext(ctx context.Context, fallback Executor) Executor {
	if ctx != nil {
		if executor, ok := ctx.Value(executorContextKey{}).(Executor); ok && executor != nil {
			return executor
		}
	}
	return fallback
}
