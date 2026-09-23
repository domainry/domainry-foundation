package operation

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/schemaownership"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	ormschema "github.com/domainry/domainry-orm/schema"
	"github.com/domainry/domainry-orm/sqlhost"
)

const (
	TableName           = "_operations"
	ControlTableName    = "_operation_controls"
	BreakGlassTableName = "_operation_break_glass_grants"
	MigrationOwner      = "shared/operations"
	compositeKeyLength  = 128
	statusKeyLength     = 64
	timestampKeyLength  = 40
)

type Database = sqlhost.Database
type DBTX = sqlhost.DBTX
type SchemaMigration = ormmigration.Migration

type Dialect interface {
	Identifier(string) string
	Table(string) string
	Placeholder(int) string
	Insert(string, []string) string
}

// Renderer is the narrow dialect surface exposed by module hosts. AdaptDialect
// adds the canonical INSERT rendering needed by the Operations SQL Store so
// business modules do not each maintain an adapter.
type Renderer interface {
	Identifier(string) string
	Table(string) string
	Placeholder(int) string
}

type adaptedDialect struct{ Renderer }

func AdaptDialect(renderer Renderer) Dialect {
	if renderer == nil {
		return nil
	}
	if dialect, ok := renderer.(Dialect); ok {
		return dialect
	}
	return adaptedDialect{Renderer: renderer}
}

func (d adaptedDialect) Insert(table string, columns []string) string {
	quoted := make([]string, len(columns))
	values := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = d.Identifier(column)
		values[index] = d.Placeholder(index + 1)
	}
	return "INSERT INTO " + d.Table(table) + " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(values, ", ") + ")"
}

type MigrationRegistrar interface {
	ApplyOwnedMigrations(context.Context, string, []SchemaMigration) error
}

func Open(ctx context.Context, database Database, dialect Dialect, migrations MigrationRegistrar) (*SQLStore, error) {
	if database == nil || dialect == nil || migrations == nil {
		return nil, fmt.Errorf("shared Operations persistence host is incomplete")
	}
	values, err := SchemaMigrationsForDialect(dialect)
	if err != nil {
		return nil, err
	}
	if err := migrations.ApplyOwnedMigrations(ctx, MigrationOwner, values); err != nil {
		return nil, fmt.Errorf("apply shared Operations migrations: %w", err)
	}
	return NewSQLStore(database, dialect), nil
}

func SchemaMigrations(driver, schema string) ([]SchemaMigration, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("Operations database driver %q is unsupported: %w", driver, err)
	}
	renderer, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, err
	}
	return SchemaMigrationsForDialect(renderer.WithSchema(strings.TrimSpace(schema)))
}

func SchemaMigrationsForDialect(renderer Dialect) ([]SchemaMigration, error) {
	operations, _, err := ormschema.NewTable(renderer, TableName).IfNotExists().Columns(
		required("id", ormschema.TextKey(255)), required("workspace_id", ormschema.TextKey(191)), defaulted("system_purpose", ormschema.TextKey(compositeKeyLength), ""),
		required("owner", ormschema.TextKey(compositeKeyLength)), required("kind", ormschema.TextKey(compositeKeyLength)), required("action_key", ormschema.TextKey(255)),
		defaulted("parent_id", ormschema.TextKey(255), ""), required("resource_type", ormschema.TextKey(255)), defaulted("resource_id", ormschema.TextKey(255), ""),
		required("idempotency_key", ormschema.TextKey(compositeKeyLength)), required("request_fingerprint", ormschema.TextKey(255)), required("requested_by", ormschema.TextKey(255)),
		required("reason", ormschema.LongText()), defaulted("reference", ormschema.TextKey(255), ""), required("status", ormschema.TextKey(statusKeyLength)),
		required("status_url", ormschema.LongText()), required("result_json", ormschema.LongText()), required("metadata_json", ormschema.LongText()),
		defaulted("error_code", ormschema.TextKey(255), ""), defaulted("failure_class", ormschema.TextKey(255), ""), required("next_action", ormschema.LongText()),
		required("related_ids_json", ormschema.LongText()), defaulted("correlation", ormschema.TextKey(255), ""), required("evidence_json", ormschema.LongText()),
		defaulted("lease_owner", ormschema.TextKey(255), ""), defaulted("lease_expires_at", ormschema.TextKey(timestampKeyLength), ""), defaulted("fencing_token", ormschema.BigInt(), 0),
		defaulted("expires_at", ormschema.TextKey(timestampKeyLength), ""), required("created_at", ormschema.TextKey(timestampKeyLength)), defaulted("started_at", ormschema.TextKey(timestampKeyLength), ""),
		defaulted("finished_at", ormschema.TextKey(timestampKeyLength), ""), required("updated_at", ormschema.TextKey(timestampKeyLength)),
	).PrimaryKey("id").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", TableName, err)
	}
	controls, _, err := ormschema.NewTable(renderer, ControlTableName).IfNotExists().Columns(
		required("system_purpose", ormschema.TextKey(255)), required("control_kind", ormschema.TextKey(255)), required("owner", ormschema.TextKey(255)),
		required("state", ormschema.TextKey(191)), required("reason", ormschema.Text()), required("reference", ormschema.Text()),
		required("updated_by", ormschema.Text()), required("revision", ormschema.BigInt()), required("updated_at", ormschema.Text()),
	).PrimaryKey("system_purpose", "control_kind", "owner").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", ControlTableName, err)
	}
	breakGlass, _, err := ormschema.NewTable(renderer, BreakGlassTableName).IfNotExists().Columns(
		required("id", ormschema.TextKey(255)), required("workspace_id", ormschema.TextKey(191)), required("state", ormschema.TextKey(191)),
		required("actor_id", ormschema.TextKey(255)), required("approver_ids_json", ormschema.LongText()), required("reason", ormschema.LongText()),
		required("incident_ref", ormschema.TextKey(255)), required("alert_target", ormschema.TextKey(255)), required("audit_event_id", ormschema.TextKey(255)),
		required("expires_at", ormschema.TextKey(40)), required("revision", ormschema.BigInt()), required("created_at", ormschema.TextKey(40)),
		required("updated_at", ormschema.TextKey(40)), defaulted("revoked_at", ormschema.TextKey(40), ""),
		defaulted("revoked_by", ormschema.TextKey(255), ""), required("revocation_note", ormschema.LongText()),
	).PrimaryKey("id").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", BreakGlassTableName, err)
	}
	statements := []string{operations, controls, breakGlass}
	indexes := []struct {
		name, table string
		unique      bool
		columns     []string
	}{
		{"uniq_runtime_operation_key", TableName, true, []string{"workspace_id", "system_purpose", "owner", "kind", "idempotency_key"}},
		{"idx_runtime_operation_status", TableName, false, []string{"workspace_id", "owner", "status", "created_at"}},
		{"idx_runtime_operation_parent", TableName, false, []string{"workspace_id", "parent_id", "created_at"}},
		{"idx_runtime_operation_lease", TableName, false, []string{"owner", "status", "lease_expires_at"}},
		{"idx_runtime_operation_expiry", TableName, false, []string{"workspace_id", "owner", "kind", "status", "expires_at"}},
		{"idx_runtime_operation_control_state", ControlTableName, false, []string{"system_purpose", "control_kind", "state"}},
		{"idx_runtime_break_glass_active", BreakGlassTableName, false, []string{"workspace_id", "state", "expires_at"}},
		{"uniq_runtime_break_glass_audit", BreakGlassTableName, true, []string{"workspace_id", "audit_event_id"}},
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
	return []SchemaMigration{{Version: 1, Name: "shared_operations", Statements: statements}}, nil
}

func required(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind).NotNull()
}

func defaulted(name string, kind ormschema.ColumnType, value any) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind).NotNull().DefaultValue(value)
}

func SchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{
		{
			Name: TableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeExplicitMixed,
			RetentionClass: schemaownership.RetentionRegisteredRowPolicy, PrimaryKey: []string{"id"},
			BoundedQueryPath: "exact workspace or system scope plus owner/kind/idempotency identity; lists and expiry scans are limited",
			DeletionPolicy:   "registered owner policy sets expires_at; only terminal expired rows outside protected status are deleted",
		},
		{
			Name: ControlTableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeInstallation,
			RetentionClass: schemaownership.RetentionInstallation, PrimaryKey: []string{"system_purpose", "control_kind", "owner"},
			BoundedQueryPath: "exact system_purpose/control_kind/owner identity or registered state existence check",
			DeletionPolicy:   "current desired control state is revision-fenced and retained for the installation lifetime",
		},
		{
			Name: BreakGlassTableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionLegalAudit, PrimaryKey: []string{"id"},
			BoundedQueryPath: "exact id or workspace-scoped active/list query with an enforced result limit",
			DeletionPolicy:   "grant rows are revoked or expire in place and remain as legal audit evidence",
		},
	}
}

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
