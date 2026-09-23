package artifact

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/schemaownership"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	"github.com/domainry/domainry-orm/sqlhost"
)

const MigrationOwner = "shared/artifacts"

type Database = sqlhost.Database
type SchemaMigration = ormmigration.Migration

type Dialect interface {
	Identifier(string) string
	Table(string) string
	Placeholder(int) string
}

type MigrationRegistrar interface {
	ApplyOwnedMigrations(context.Context, string, []SchemaMigration) error
}

// Open applies the one canonical shared Artifact schema to the caller's own
// database and returns a Store bound to that database. Embedded modules that
// share a database therefore share the physical tables; standalone services
// create the same tables independently in their own databases.
func Open(ctx context.Context, database Database, dialect Dialect, migrations MigrationRegistrar) (*SQLStore, error) {
	if database == nil || dialect == nil || migrations == nil {
		return nil, fmt.Errorf("shared Artifact persistence host is incomplete")
	}
	migration, err := SchemaMigrationForDialect(dialect)
	if err != nil {
		return nil, err
	}
	if err := migrations.ApplyOwnedMigrations(ctx, MigrationOwner, []SchemaMigration{migration}); err != nil {
		return nil, fmt.Errorf("apply shared Artifact migrations: %w", err)
	}
	return NewSQLStore(database, dialect), nil
}

func SchemaMigrations(driver, schema string) ([]SchemaMigration, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("Artifact database driver %q is unsupported: %w", driver, err)
	}
	renderer, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, err
	}
	migration, err := SchemaMigrationForDialect(renderer.WithSchema(strings.TrimSpace(schema)))
	if err != nil {
		return nil, err
	}
	return []SchemaMigration{migration}, nil
}

func SchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{
		{
			Name: TableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionRegisteredRowPolicy, PrimaryKey: []string{"id"},
			BoundedQueryPath: "workspace plus id/token/registered owner-kind identity; cursor and cleanup queries enforce limits",
			DeletionPolicy:   "expired or rejected blob content is deleted idempotently and the registry row becomes a deleted tombstone",
		},
		{
			Name: BindingTableName, Owner: MigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionRegisteredRowPolicy, PrimaryKey: []string{"id"},
			BoundedQueryPath: "workspace plus artifact identity or registered owner-kind resource identity with an enforced limit",
			DeletionPolicy:   "bindings follow the registered artifact owner policy and remain attached to retained artifact tombstones",
		},
	}
}

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
