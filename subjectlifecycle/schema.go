// Package subjectlifecycle owns the canonical schema for subject lifecycle
// coordination shared by independently deployable Domainry modules.
package subjectlifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/schemaownership"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	RequestTableName = "_subject_requests"
	StepTableName    = "_subject_steps"
	MigrationOwner   = "shared/subject-lifecycle"
	indexKeyLength   = 128
)

type SchemaMigration = ormmigration.Migration

type Dialect interface {
	Identifier(string) string
	Table(string) string
	Placeholder(int) string
	Insert(string, []string) string
}

type MigrationRegistrar interface {
	ApplyOwnedMigrations(context.Context, string, []SchemaMigration) error
}

// EnsureSchema applies the one canonical subject lifecycle schema to the
// caller's database. Modules sharing a database therefore coordinate through
// the same physical tables, while standalone services create an independent
// copy in their own database.
func EnsureSchema(ctx context.Context, dialect Dialect, migrations MigrationRegistrar) error {
	if dialect == nil || migrations == nil {
		return fmt.Errorf("shared Subject Lifecycle persistence host is incomplete")
	}
	values, err := SchemaMigrationsForDialect(dialect)
	if err != nil {
		return err
	}
	if err := migrations.ApplyOwnedMigrations(ctx, MigrationOwner, values); err != nil {
		return fmt.Errorf("apply shared Subject Lifecycle migrations: %w", err)
	}
	return nil
}

func SchemaMigrations(driver, schema string) ([]SchemaMigration, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("Subject Lifecycle database driver %q is unsupported: %w", driver, err)
	}
	renderer, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, err
	}
	return SchemaMigrationsForDialect(renderer.WithSchema(strings.TrimSpace(schema)))
}

func SchemaMigrationsForDialect(renderer Dialect) ([]SchemaMigration, error) {
	requests, _, err := ormschema.NewTable(renderer, RequestTableName).IfNotExists().Columns(
		requiredKey("id"), requiredKey("workspace_id"), requiredKey("request_type"), requiredKey("kind"),
		requiredKey("status"), requiredKey("subject_id"), optionalKey("resolved_identity"), optionalKey("requested_by"),
		optionalKey("owner_org_id"), optionalKey("download_expires_at"),
		ormschema.Column("backup_pending", ormschema.Boolean()).NotNull().DefaultValue(false),
		requiredKey("updated_at"), ormschema.Column("payload_json", ormschema.Text()).NotNull(),
	).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", RequestTableName, err)
	}
	steps, _, err := ormschema.NewTable(renderer, StepTableName).IfNotExists().Columns(
		requiredKey("workspace_id"), requiredKey("request_id"), requiredKey("owner"), requiredKey("operation"),
		ormschema.Column("payload_json", ormschema.Text()).NotNull(), requiredKey("completed_at"),
	).PrimaryKey("workspace_id", "request_id", "owner", "operation").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", StepTableName, err)
	}

	statements := []string{requests, steps}
	indexes := []struct {
		name, table string
		unique      bool
		columns     []string
	}{
		{"idx_subject_identity", RequestTableName, false, []string{"workspace_id", "subject_id", "status", "updated_at"}},
		{"idx_subject_erasure_identity", RequestTableName, false, []string{"workspace_id", "request_type", "kind", "resolved_identity", "id"}},
		{"idx_subject_request_type", RequestTableName, false, []string{"workspace_id", "request_type", "updated_at", "id"}},
		{"idx_subject_worker", RequestTableName, false, []string{"request_type", "kind", "status", "updated_at", "id"}},
		{"idx_subject_deletion_replay", RequestTableName, false, []string{"workspace_id", "kind", "status", "backup_pending", "updated_at"}},
		{"idx_subject_requester", RequestTableName, false, []string{"workspace_id", "requested_by"}},
		{"idx_subject_owner_org", RequestTableName, false, []string{"workspace_id", "owner_org_id"}},
		{"idx_subject_steps_request", StepTableName, false, []string{"workspace_id", "request_id", "completed_at"}},
		{"idx_subject_steps_erasure_fence", StepTableName, false, []string{"workspace_id", "owner", "operation", "request_id"}},
	}
	for _, value := range indexes {
		builder := ormschema.NewIndex(renderer, value.name, value.table).Columns(value.columns...)
		if value.unique {
			builder.Unique()
		}
		statement, _, buildErr := builder.Build()
		if buildErr != nil {
			return nil, fmt.Errorf("build %s: %w", value.name, buildErr)
		}
		statements = append(statements, statement)
	}
	return []SchemaMigration{{Version: 1, Name: "shared_subject_lifecycle", Statements: statements}}, nil
}

func requiredKey(name string) ormschema.ColumnDefinition {
	return ormschema.Column(name, ormschema.TextKey(indexKeyLength)).NotNull()
}

func optionalKey(name string) ormschema.ColumnDefinition {
	return ormschema.Column(name, ormschema.TextKey(indexKeyLength)).NotNull().DefaultValue("")
}

func SchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{
		{
			Name: RequestTableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionLegalAudit, PrimaryKey: []string{"workspace_id", "id"},
			BoundedQueryPath: "workspace/request identity; subject, request-type and worker indexes bound lifecycle scans",
			DeletionPolicy:   "request state is retained as lifecycle audit evidence; subject payloads follow the governing export/erasure policy",
		},
		{
			Name: StepTableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionLegalAudit, PrimaryKey: []string{"workspace_id", "request_id", "owner", "operation"},
			BoundedQueryPath: "workspace/request identity or workspace/owner/operation erasure-fence identity",
			DeletionPolicy:   "idempotent completion evidence is retained with its lifecycle request",
		},
	}
}

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
