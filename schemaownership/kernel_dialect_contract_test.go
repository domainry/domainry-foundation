package schemaownership_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-foundation/definition"
	"github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-foundation/schemaownership"
	"github.com/domainry/domainry-foundation/subjectlifecycle"
	"github.com/domainry/domainry-foundation/workerscope"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
)

type sharedKernelContract struct {
	name       string
	owner      string
	ownership  func() []schemaownership.Table
	migrations func(string, string) ([]ormmigration.Migration, error)
}

var sharedKernelContracts = []sharedKernelContract{
	{"definitions", definition.MigrationOwner, definition.SchemaOwnership, definition.SchemaMigrations},
	{"operations", operation.MigrationOwner, operation.SchemaOwnership, operation.SchemaMigrations},
	{"artifacts", artifact.MigrationOwner, artifact.SchemaOwnership, artifact.SchemaMigrations},
	{"subject_lifecycle", subjectlifecycle.MigrationOwner, subjectlifecycle.SchemaOwnership, subjectlifecycle.SchemaMigrations},
	{"worker_scopes", workerscope.MigrationOwner, workerscope.SchemaOwnership, workerscope.SchemaMigrations},
}

func sharedKernelTables() []string {
	return []string{
		artifact.BindingTableName, artifact.TableName,
		definition.TableName, definition.VersionTableName,
		operation.BreakGlassTableName, operation.ControlTableName, operation.TableName,
		subjectlifecycle.RequestTableName, subjectlifecycle.StepTableName,
		workerscope.TableName,
	}
}

func TestSharedKernelSchemaContractAcrossSQLitePostgresAndMySQL(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			parsed, err := ormdialect.Parse(driver)
			if err != nil {
				t.Fatal(err)
			}
			renderer, err := ormdialect.New(parsed.Name())
			if err != nil {
				t.Fatal(err)
			}
			schema := ""
			if driver != "sqlite" {
				schema = "foundation_contract"
			}
			dialect := renderer.WithSchema(schema)
			seenTables := map[string]string{}
			for _, current := range sharedKernelContracts {
				t.Run(current.name, func(t *testing.T) {
					tables := current.ownership()
					if err := schemaownership.ValidateAll(tables); err != nil {
						t.Fatal(err)
					}
					migrations, err := current.migrations(driver, schema)
					if err != nil {
						t.Fatal(err)
					}
					if len(migrations) == 0 {
						t.Fatal("canonical migrations are empty")
					}
					statements := []string{}
					versions := map[uint]bool{}
					for _, migration := range migrations {
						if migration.Version == 0 || strings.TrimSpace(migration.Name) == "" || len(migration.Statements) == 0 || versions[migration.Version] {
							t.Fatalf("invalid migration identity: %#v", migration)
						}
						versions[migration.Version] = true
						statements = append(statements, migration.Statements...)
					}
					joined := strings.Join(statements, "\n")
					if driver == "mysql" && strings.Contains(strings.ToUpper(joined), "TEXT NOT NULL DEFAULT") {
						t.Fatal("MySQL schema contains an unsupported TEXT default")
					}
					for _, table := range tables {
						if previous := seenTables[table.Name]; previous != "" {
							t.Fatalf("table %s is emitted by both %s and %s", table.Name, previous, current.owner)
						}
						seenTables[table.Name] = current.owner
						declaration := "CREATE TABLE IF NOT EXISTS " + dialect.Table(table.Name)
						if count := strings.Count(joined, declaration); count != 1 {
							t.Fatalf("table %s canonical declaration count=%d in %q", table.Name, count, joined)
						}
						for _, column := range table.IdentityKey() {
							if !strings.Contains(joined, dialect.Identifier(column)) {
								t.Fatalf("table %s omits ownership identity-key column %s", table.Name, column)
							}
						}
					}
				})
			}
			want := sharedKernelTables()
			slices.Sort(want)
			got := make([]string, 0, len(seenTables))
			for table := range seenTables {
				got = append(got, table)
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("%s shared-kernel tables=%v want=%v (%s)", driver, got, want, fmt.Sprint(seenTables))
			}
		})
	}
}
