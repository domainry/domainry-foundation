package definition

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/schemaownership"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const (
	TableName        = "_definitions"
	VersionTableName = "_definition_versions"
)

func Open(ctx context.Context, installationID string, database Database, dialect Dialect, migrations MigrationRegistrar) (Store, error) {
	if strings.TrimSpace(installationID) == "" || database == nil || dialect == nil || migrations == nil {
		return Store{}, fmt.Errorf("shared Definition persistence host is incomplete")
	}
	values, err := SchemaMigrationsForDialect(dialect)
	if err != nil {
		return Store{}, err
	}
	if err := migrations.ApplyOwnedMigrations(ctx, MigrationOwner, values); err != nil {
		return Store{}, fmt.Errorf("apply shared Definition migrations: %w", err)
	}
	return NewStore(database, dialect, installationID), nil
}

func SchemaMigrations(driver, schema string) ([]SchemaMigration, error) {
	parsed, err := ormdialect.Parse(driver)
	if err != nil {
		return nil, fmt.Errorf("Definition database driver %q is unsupported: %w", driver, err)
	}
	dialect, err := ormdialect.New(parsed.Name())
	if err != nil {
		return nil, err
	}
	return SchemaMigrationsForDialect(dialect.WithSchema(schema))
}

func SchemaMigrationsForDialect(renderer Dialect) ([]SchemaMigration, error) {
	catalog, _, err := ormschema.NewTableWithRequiredColumns(renderer, TableName, physicalID()).IfNotExists().Columns(
		installationIDColumn(),
		required("owner", ormschema.TextKey(64)), required("kind", ormschema.TextKey(128)),
		required("definition_key", ormschema.TextKey(255)), required("current_version_id", ormschema.TextKey(255)),
		required("status", ormschema.TextKey(32)), required("object_key", ormschema.TextKey(255)),
		required("name", ormschema.Text()), required("payload_json", ormschema.LongText()),
		required("schema_version", ormschema.TextKey(255)), required("schema_hash", ormschema.TextKey(255)),
		required("source_kind", ormschema.TextKey(255)), required("source_id", ormschema.TextKey(255)),
		required("published_at", ormschema.BigInt()), required("published_by", ormschema.TextKey(255)),
		optional("disabled_at", ormschema.BigInt()), optional("disabled_by", ormschema.TextKey(255)),
		required("created_at", ormschema.BigInt()), required("updated_at", ormschema.BigInt()),
	).PrimaryKey("id").Unique("installation_id", "owner", "kind", "definition_key").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", TableName, err)
	}
	versions, _, err := ormschema.NewTableWithRequiredColumns(renderer, VersionTableName, physicalID()).IfNotExists().Columns(
		required("version_id", ormschema.TextKey(255)),
		installationIDColumn(), required("owner", ormschema.TextKey(64)),
		required("kind", ormschema.TextKey(128)), required("definition_key", ormschema.TextKey(255)),
		required("schema_version", ormschema.TextKey(255)), required("schema_hash", ormschema.TextKey(255)),
		required("payload_json", ormschema.LongText()), required("created_at", ormschema.BigInt()),
	).PrimaryKey("id").Unique("version_id").Unique("installation_id", "owner", "kind", "definition_key", "schema_version").Build()
	if err != nil {
		return nil, fmt.Errorf("build %s: %w", VersionTableName, err)
	}
	return []SchemaMigration{{Version: 1, Name: "shared_definitions", Statements: []string{catalog, versions}}}, nil
}

func required(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind).NotNull()
}

func physicalID() ormschema.ColumnDefinition {
	return ormschema.Column("id", ormschema.BigInt()).NotNull().AutoIncrement()
}

func installationIDColumn() ormschema.ColumnDefinition {
	// Single-byte installation identity keeps the complete version unique
	// index within InnoDB's 3072-byte limit under utf8mb4.
	return required("installation_id", ormschema.TextKey(255)).ASCIIKey()
}

func optional(name string, kind ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, kind)
}

func SchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{
		{
			Name: TableName, Owner: MigrationOwner, Kind: schemaownership.KindState, WorkspaceScope: schemaownership.ScopeInstallation,
			RetentionClass: schemaownership.RetentionInstallation, PrimaryKey: []string{"id"},
			BoundedQueryPath: "installation_id plus owner/kind/definition_key identity; registered owner/kind listing",
			DeletionPolicy:   "source snapshot replacement deletes disabled current rows; active rows live for the source lifetime",
		},
		{
			Name: VersionTableName, Owner: MigrationOwner, Kind: schemaownership.KindEntity, WorkspaceScope: schemaownership.ScopeInstallation,
			RetentionClass: schemaownership.RetentionInstallation, PrimaryKey: []string{"id"},
			EntityIDColumn: "version_id", EntityIDPrefix: "defver", BusinessKey: []string{"version_id"},
			BoundedQueryPath: "installation/owner/kind/definition_key plus schema_version identity; scoped version history",
			DeletionPolicy:   "immutable version history is retained for the installation lifetime",
		},
	}
}

func OwnedTables() []string { return schemaownership.Names(SchemaOwnership()) }
