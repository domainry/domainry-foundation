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
		{"definitions", definition.SchemaMigrations, "2a26d67a67e7f5c0c44b925681e4035364389c5def5d71507eb9f43cc9e77150"},
		{"operations", operation.SchemaMigrations, "986f5aff5155737a393ed03a9d6f4657ff628f691da9da0c4941f2cf93eeea7c"},
		{"artifacts", artifact.SchemaMigrations, "6e24a01adba5f13ad443ceb49580f67c1eb754b80385609de6e9bda0fc818a8c"},
		{"subject lifecycle", subjectlifecycle.SchemaMigrations, "ab13c34501fa82f0da9bbf3e3018ecb36365291647650b4d0d136d67d67b3f7c"},
		{"worker scopes", workerscope.SchemaMigrations, "8d23a9cfab318318befcf1eb32313ca9ad82c5efb14a614aacd084dfd072e140"},
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
			for _, table := range group.tables {
				if !slices.Equal(table.PrimaryKey, []string{"id"}) {
					t.Fatalf("Foundation table %s physical primary key=%v want [id]", table.Name, table.PrimaryKey)
				}
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
				if !strings.Contains(create, `"id" INTEGER PRIMARY KEY AUTOINCREMENT`) {
					t.Fatalf("%s has no SQLite BIGINT physical auto-increment ID: %s", table.Name, create)
				}
				identity := table.IdentityKey()
				quoted := make([]string, len(identity))
				for index, column := range identity {
					quoted[index] = `"` + column + `"`
				}
				if len(table.PrimaryKey) > 0 {
					primaryKey := "PRIMARY KEY (" + strings.Join(quoted, ", ") + ")"
					inlineAutoPrimaryKey := len(table.PrimaryKey) == 1 && strings.Contains(create, `"`+table.PrimaryKey[0]+`" INTEGER PRIMARY KEY AUTOINCREMENT`)
					if !strings.Contains(create, primaryKey) && !inlineAutoPrimaryKey {
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
