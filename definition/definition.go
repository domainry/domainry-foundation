// Package definition owns the deployment-neutral contract and SQL kernel for
// the shared _definitions and _definition_versions tables. Business modules
// install the kernel into their own database and construct a local Store; no
// Store instance crosses a module boundary.
package definition

import (
	"context"
	"encoding/json"
	"strings"

	ormmigration "github.com/domainry/domainry-orm/migration"
	"github.com/domainry/domainry-orm/sqlhost"
)

const (
	OwnerMetadata     = "metadata"
	OwnerIdentity     = "identity"
	OwnerWorkflow     = "workflow"
	OwnerAutomation   = "automation"
	OwnerIntegration  = "integration"
	OwnerReport       = "report"
	OwnerAgent        = "agent"
	OwnerScheduler    = "scheduler"
	OwnerNotification = "notification"
	OwnerLifecycle    = "lifecycle"

	NoCurrentVersion = "none"
	MigrationOwner   = "shared/definitions"
)

type Error struct {
	StatusCode int
	Code       string
	Cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Code) != "" {
		return e.Code
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "definition.error"
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type Definition struct {
	Owner            string          `json:"owner"`
	ResourceType     string          `json:"resource_type"`
	ResourceKey      string          `json:"resource_key"`
	CurrentVersionID string          `json:"current_version_id"`
	Status           string          `json:"status"`
	ObjectKey        string          `json:"object_key,omitempty"`
	Name             string          `json:"name,omitempty"`
	Payload          json.RawMessage `json:"payload"`
	SchemaVersion    string          `json:"schema_version,omitempty"`
	SchemaHash       string          `json:"schema_hash,omitempty"`
	SourceKind       string          `json:"source_kind,omitempty"`
	SourceID         string          `json:"source_id,omitempty"`
	PublishedAt      string          `json:"published_at,omitempty"`
	PublishedBy      string          `json:"published_by,omitempty"`
	DisabledAt       string          `json:"disabled_at,omitempty"`
	DisabledBy       string          `json:"disabled_by,omitempty"`
	CreatedAt        string          `json:"created_at,omitempty"`
	UpdatedAt        string          `json:"updated_at,omitempty"`
}

type Query struct {
	Owner        string
	CrossOwner   bool
	ResourceType string
	SourceID     string
}

type Snapshot struct{ Definitions []Definition }

type SourceSnapshot struct {
	Owner         string
	SchemaVersion string
	SourceKind    string
	SourceID      string
	Definitions   []Definition
}

type PublishCommand struct {
	Owner                    string
	ResourceType             string
	ResourceKey              string
	ExpectedCurrentVersionID string
	SchemaVersion            string
	SchemaHash               string
	ObjectKey                string
	Name                     string
	Payload                  json.RawMessage
	SourceKind               string
	SourceID                 string
	PublishedBy              string
}

type PublishResult struct {
	Definition       Definition
	CurrentVersionID string
}

type DisableCommand struct {
	Owner                    string
	ResourceType             string
	ResourceKey              string
	ExpectedCurrentVersionID string
	DisabledBy               string
}

type VersionQuery struct {
	Owner         string
	ResourceType  string
	ResourceKey   string
	VersionID     string
	SchemaVersion string
}

type VersionListQuery struct {
	Owner        string
	ResourceType string
	ResourceKey  string
}

type Version struct {
	ID            string          `json:"id"`
	Owner         string          `json:"owner"`
	ResourceType  string          `json:"resource_type"`
	ResourceKey   string          `json:"resource_key"`
	SchemaVersion string          `json:"schema_version"`
	SchemaHash    string          `json:"schema_hash"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     string          `json:"created_at"`
}

type StorePort interface {
	List(context.Context, Query) ([]Definition, error)
	Get(context.Context, string, string, string) (Definition, bool, error)
	Snapshot(context.Context, Query) (Snapshot, error)
	ReplaceSourceSnapshot(context.Context, SourceSnapshot) error
	Publish(context.Context, PublishCommand) (PublishResult, error)
	Disable(context.Context, DisableCommand) error
	GetVersion(context.Context, VersionQuery) (Version, bool, error)
	ListVersions(context.Context, VersionListQuery) ([]Version, error)
}

type Database = sqlhost.Database
type DBTX = sqlhost.DBTX
type SchemaMigration = ormmigration.Migration

type Dialect interface {
	Identifier(string) string
	Table(string) string
	Placeholder(int) string
	Insert(string, []string) string
}

type MigrationRegistrar interface {
	Driver() string
	Schema() string
	ApplyOwnedMigrations(context.Context, string, []SchemaMigration) error
}

type executorContextKey struct{}

func WithExecutor(ctx context.Context, executor DBTX) context.Context {
	if executor == nil {
		return ctx
	}
	return context.WithValue(ctx, executorContextKey{}, executor)
}

func ExecutorFromContext(ctx context.Context, fallback DBTX) DBTX {
	if ctx != nil {
		if executor, ok := ctx.Value(executorContextKey{}).(DBTX); ok && executor != nil {
			return executor
		}
	}
	return fallback
}
