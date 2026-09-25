package schemaownership_test

import (
	"crypto/sha256"
	"encoding/hex"
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
	ormmigration "github.com/domainry/domainry-orm/migration"
)

func TestFoundationPublishedMySQLMigrationChecksumsAreStable(t *testing.T) {
	sources := []struct {
		name       string
		migrations func(string, string) ([]ormmigration.Migration, error)
		checksum   string
	}{
		{"definitions", definition.SchemaMigrations, "07619f55458e53eae36a5bab0038055aa67cc4733c01d6aa2a40cd06a4710dbd"},
		{"operations", operation.SchemaMigrations, "ac08f1003d6b2081a8aec1f0312b75ec5e99a552be57c1abca9a6021abf90f82"},
		{"artifacts", artifact.SchemaMigrations, "d230358132b49261e8446901d54396cea75c73eb6b5d9eeca9d1d20dd354e455"},
		{"subject lifecycle", subjectlifecycle.SchemaMigrations, "7b3125d30472421ad75b104afb0c395e363a952b9811f91f9b6b8021b672dc19"},
		{"worker scopes", workerscope.SchemaMigrations, "69b49402ba50646936e1e98efc5f7804036c275ac3e0671761523f227e8ebd2d"},
	}
	for _, source := range sources {
		t.Run(source.name, func(t *testing.T) {
			migrations, err := source.migrations("mysql", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(migrations) == 0 {
				t.Fatal("missing published migration")
			}
			if got := runtimeOwnedMigrationChecksum(migrations[0]); got != source.checksum {
				t.Fatalf("published migration checksum=%s want=%s", got, source.checksum)
			}
		})
	}
}

func runtimeOwnedMigrationChecksum(migration ormmigration.Migration) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%d\x00%s\x00", migration.Version, strings.TrimSpace(migration.Name))
	for _, statement := range migration.Statements {
		_, _ = fmt.Fprintf(hash, "%s\x00", statement)
	}
	if migration.Baseline != nil {
		for _, table := range migration.Baseline.Tables {
			_, _ = fmt.Fprintf(hash, "table\x00%s\x00", table.Name)
			for _, column := range table.Columns {
				_, _ = fmt.Fprintf(hash, "column\x00%s\x00%s\x00%t\x00%t\x00", column.Name, column.Type, column.Nullable, column.PrimaryKey)
			}
			for _, index := range table.Indexes {
				_, _ = fmt.Fprintf(hash, "index\x00%s\x00%t\x00%s\x00", index.Name, index.Unique, strings.Join(index.Columns, ","))
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

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
