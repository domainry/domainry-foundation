package schemaownership_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/artifact"
	"github.com/domainry/domainry-foundation/definition"
	"github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-foundation/schemaownership"
	"github.com/domainry/domainry-foundation/subjectlifecycle"
	"github.com/domainry/domainry-foundation/workerscope"
	ormmigration "github.com/domainry/domainry-orm/migration"
)

func TestEveryFoundationTableHasOneValidOwnershipContract(t *testing.T) {
	groups := []struct {
		name   string
		tables []schemaownership.Table
		owned  []string
	}{
		{"definitions", definition.SchemaOwnership(), definition.OwnedTables()},
		{"operations", operation.SchemaOwnership(), operation.OwnedTables()},
		{"artifacts", artifact.SchemaOwnership(), artifact.OwnedTables()},
		{"subject lifecycle", subjectlifecycle.SchemaOwnership(), subjectlifecycle.OwnedTables()},
		{"worker scopes", workerscope.SchemaOwnership(), workerscope.OwnedTables()},
	}
	all := []schemaownership.Table{}
	for _, group := range groups {
		t.Run(group.name, func(t *testing.T) {
			if err := schemaownership.ValidateAll(group.tables); err != nil {
				t.Fatal(err)
			}
			if names := schemaownership.Names(group.tables); !slices.Equal(names, group.owned) {
				t.Fatalf("ownership names=%v owned tables=%v", names, group.owned)
			}
		})
		all = append(all, group.tables...)
	}
	if err := schemaownership.ValidateAll(all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 10 {
		t.Fatalf("Foundation ownership contracts=%d want=10", len(all))
	}
}

func TestFoundationOwnershipIdentityKeysMatchCanonicalDDL(t *testing.T) {
	sources := []struct {
		name       string
		tables     []schemaownership.Table
		migrations func(string, string) ([]ormmigration.Migration, error)
	}{
		{"definitions", definition.SchemaOwnership(), definition.SchemaMigrations},
		{"operations", operation.SchemaOwnership(), operation.SchemaMigrations},
		{"artifacts", artifact.SchemaOwnership(), artifact.SchemaMigrations},
		{"subject lifecycle", subjectlifecycle.SchemaOwnership(), subjectlifecycle.SchemaMigrations},
		{"worker scopes", workerscope.SchemaOwnership(), workerscope.SchemaMigrations},
	}
	for _, source := range sources {
		t.Run(source.name, func(t *testing.T) {
			migrations, err := source.migrations("sqlite", "")
			if err != nil {
				t.Fatal(err)
			}
			statements := []string{}
			for _, migration := range migrations {
				statements = append(statements, migration.Statements...)
			}
			for _, table := range source.tables {
				create := ""
				for _, statement := range statements {
					if strings.HasPrefix(statement, `CREATE TABLE IF NOT EXISTS "`+table.Name+`" `) {
						create = statement
						break
					}
				}
				if create == "" {
					t.Fatalf("%s has no canonical CREATE TABLE statement", table.Name)
				}
				identity := table.IdentityKey()
				quoted := make([]string, len(identity))
				for index, column := range identity {
					quoted[index] = `"` + column + `"`
				}
				if len(table.PrimaryKey) > 0 {
					primaryKey := "PRIMARY KEY (" + strings.Join(quoted, ", ") + ")"
					if !strings.Contains(create, primaryKey) {
						t.Fatalf("%s declares primary key %v but DDL is %s", table.Name, table.PrimaryKey, create)
					}
					continue
				}
				uniqueKey := "(" + strings.Join(quoted, ", ") + ")"
				found := false
				for _, statement := range statements {
					if strings.HasPrefix(statement, `CREATE UNIQUE INDEX `) && strings.Contains(statement, `ON "`+table.Name+`" `) && strings.Contains(statement, uniqueKey) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%s declares unique identity key %v but migrations are %v", table.Name, table.UniqueKey, statements)
				}
			}
		})
	}
}
