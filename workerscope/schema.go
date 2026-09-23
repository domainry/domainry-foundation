// Package workerscope owns the canonical persistence kernel for worker-scope
// discovery, transactional capacity guards, and process-wide maintenance
// leases shared by independently deployable Domainry modules.
package workerscope

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
	TableName      = "_worker_scopes"
	MigrationOwner = "shared/worker-scopes"
)

type Database = sqlhost.Database
type DBTX = sqlhost.DBTX
type Executor = sqlhost.Executor
type Queryer = sqlhost.Queryer
type SchemaMigration = ormmigration.Migration

type Dialect interface {
	Identifier(string) string
	Table(string) string
	Placeholder(int) string
}

type MigrationRegistrar interface {
	ApplyOwnedMigrations(context.Context, string, []SchemaMigration) error
}

// Open installs the source-owned worker-scope schema in the caller's database
// and returns a Store bound only to that database. Module deployments therefore
// share one physical table, while standalone SaaS deployments create the same
// table in their own database.
func Open(ctx context.Context, database Database, dialect Dialect, migrations MigrationRegistrar) (*Store, error) {
	if database == nil || dialect == nil || migrations == nil {
		return nil, fmt.Errorf("shared Worker Scope persistence host is incomplete")
	}
	values, err := SchemaMigrationsForDialect(dialect)
	if err != nil {
		return nil, err
	}
	if err := migrations.ApplyOwnedMigrations(ctx, MigrationOwner, values); err != nil {
		return nil, fmt.Errorf("apply shared Worker Scope migrations: %w", err)
	}
	return NewStore(database, dialect), nil
}

func SchemaMigrations(driver, schema string) ([]SchemaMigration, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("Worker Scope database driver %q is unsupported: %w", driver, err)
	}
	renderer, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, err
	}
	return SchemaMigrationsForDialect(renderer.WithSchema(strings.TrimSpace(schema)))
}

func SchemaMigrationsForDialect(renderer Dialect) ([]SchemaMigration, error) {
	table, _, err := ormschema.NewTable(renderer, TableName).IfNotExists().Columns(
		required("id", ormschema.TextKey(191)),
		required("owner", ormschema.TextKey(191)),
		required("scope_key", ormschema.TextKey(191)),
		defaulted("cursor", ormschema.TextKey(255), ""),
		defaulted("checkpoint", ormschema.BigInt(), 0),
		defaulted("capacity", ormschema.BigInt(), 0),
		defaulted("lease_owner", ormschema.TextKey(191), ""),
		defaulted("lease_expires_at", ormschema.TextKey(40), ""),
		defaulted("fencing_token", ormschema.BigInt(), 0),
		defaulted("last_started_at", ormschema.TextKey(40), ""),
		defaulted("last_completed_at", ormschema.TextKey(40), ""),
		required("last_error", ormschema.LongText()),
		defaulted("updated_at", ormschema.TextKey(40), ""),
	).PrimaryKey("id").Unique("owner", "scope_key").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", TableName, err)
	}
	leaseIndex, _, err := ormschema.NewIndex(renderer, "idx_worker_scope_lease", TableName).Columns("owner", "lease_expires_at").Build()
	if err != nil {
		return nil, fmt.Errorf("build Worker Scope lease index: %w", err)
	}
	return []SchemaMigration{{Version: 1, Name: "shared_worker_scopes", Statements: []string{table, leaseIndex}}}, nil
}

func required(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind).NotNull()
}

func defaulted(name string, kind ormschema.ColumnType, value any) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind).NotNull().DefaultValue(value)
}

func SchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{{
		Name: TableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeExplicitMixed,
		RetentionClass: schemaownership.RetentionRegisteredRowPolicy, PrimaryKey: []string{"id"},
		BoundedQueryPath: "registered owner plus scope_key cursor; exact identity and owner-scoped lease queries",
		DeletionPolicy:   "one row lives for each registered owner scope and is retired when that owner scope is removed",
	}}
}

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
