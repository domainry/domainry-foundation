package operation

import (
	"context"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	ormschema "github.com/domainry/domainry-orm/schema"
	"github.com/domainry/domainry-orm/sqlhost"
)

const (
	TableName        = "_operations"
	ControlTableName = "_operation_controls"
	MigrationOwner   = "shared/operations"
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

type MigrationRegistrar interface {
	Driver() string
	Schema() string
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
		required("id", ormschema.TextKey(255)), required("workspace_id", ormschema.TextKey(191)), defaulted("system_purpose", ormschema.TextKey(191), ""),
		required("owner", ormschema.TextKey(191)), required("kind", ormschema.TextKey(191)), required("action_key", ormschema.TextKey(255)),
		defaulted("parent_id", ormschema.TextKey(255), ""), required("resource_type", ormschema.TextKey(255)), defaulted("resource_id", ormschema.TextKey(255), ""),
		required("idempotency_key", ormschema.TextKey(191)), required("request_fingerprint", ormschema.TextKey(255)), required("requested_by", ormschema.TextKey(255)),
		required("reason", ormschema.LongText()), defaulted("reference", ormschema.TextKey(255), ""), required("status", ormschema.TextKey(191)),
		required("status_url", ormschema.LongText()), required("result_json", ormschema.LongText()), required("metadata_json", ormschema.LongText()),
		defaulted("error_code", ormschema.TextKey(255), ""), defaulted("failure_class", ormschema.TextKey(255), ""), defaulted("next_action", ormschema.LongText(), ""),
		required("related_ids_json", ormschema.LongText()), defaulted("correlation", ormschema.TextKey(255), ""), required("evidence_json", ormschema.LongText()),
		defaulted("lease_owner", ormschema.TextKey(255), ""), defaulted("lease_expires_at", ormschema.TextKey(191), ""), defaulted("fencing_token", ormschema.BigInt(), 0),
		defaulted("expires_at", ormschema.TextKey(191), ""), required("created_at", ormschema.TextKey(191)), defaulted("started_at", ormschema.TextKey(255), ""),
		defaulted("finished_at", ormschema.TextKey(255), ""), required("updated_at", ormschema.TextKey(191)),
	).PrimaryKey("id").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", TableName, err)
	}
	controls, _, err := ormschema.NewTable(renderer, ControlTableName).IfNotExists().Columns(
		required("system_purpose", ormschema.TextKey(255)), required("control_kind", ormschema.TextKey(255)), required("owner", ormschema.TextKey(255)),
		required("state", ormschema.TextKey(191)), required("reason", ormschema.Text()), defaulted("reference", ormschema.Text(), ""),
		required("updated_by", ormschema.Text()), required("revision", ormschema.BigInt()), required("updated_at", ormschema.Text()),
	).Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", ControlTableName, err)
	}
	statements := []string{operations, controls}
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
		{"uniq_runtime_operation_control", ControlTableName, true, []string{"system_purpose", "control_kind", "owner"}},
		{"idx_runtime_operation_control_state", ControlTableName, false, []string{"system_purpose", "control_kind", "state"}},
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

func OwnedTables() []string { return []string{TableName, ControlTableName} }
