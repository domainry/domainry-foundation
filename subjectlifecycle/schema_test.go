package subjectlifecycle

import (
	"context"
	"slices"
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
)

func TestSchemaIsCanonicalAndPortable(t *testing.T) {
	if !slices.Equal(OwnedTables(), []string{RequestTableName, StepTableName}) {
		t.Fatalf("unexpected Subject Lifecycle-owned tables: %v", OwnedTables())
	}
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			migrations, err := SchemaMigrations(driver, "app")
			if err != nil {
				t.Fatal(err)
			}
			if len(migrations) != 1 || migrations[0].Version != 1 || migrations[0].Name != "shared_subject_lifecycle" {
				t.Fatalf("unexpected migrations: %#v", migrations)
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, token := range []string{RequestTableName, StepTableName, "resolved_identity", "backup_pending", "payload_json", "idx_subject_steps_erasure_fence"} {
				if !strings.Contains(joined, token) {
					t.Fatalf("%s schema omitted %s", driver, token)
				}
			}
		})
	}
}

func TestEnsureSchemaUsesSharedOwner(t *testing.T) {
	renderer, err := ormdialect.New(ormdialect.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	registrar := &recordingRegistrar{}
	if err := EnsureSchema(t.Context(), renderer.WithSchema(""), registrar); err != nil {
		t.Fatal(err)
	}
	if registrar.owner != MigrationOwner || len(registrar.migrations) != 1 {
		t.Fatalf("unexpected migration registration: %#v", registrar)
	}
}

func TestMySQLSubjectLifecycleUsesBoundedCompositeKeys(t *testing.T) {
	migrations, err := SchemaMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(migrations[0].Statements, "\n")
	if count := strings.Count(joined, "VARCHAR(128)"); count < 10 {
		t.Fatalf("Subject Lifecycle MySQL migration has %d bounded key columns, want at least 10", count)
	}
}

func TestMySQLSubjectLifecycleUsesNumericUTCMilliseconds(t *testing.T) {
	migrations, err := SchemaMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(migrations[0].Statements, "\n")
	for _, column := range []string{"`download_expires_at` BIGINT", "`updated_at` BIGINT NOT NULL", "`completed_at` BIGINT NOT NULL"} {
		if !strings.Contains(joined, column) {
			t.Fatalf("Subject Lifecycle migration must store %s as UTC milliseconds", column)
		}
	}
}

type recordingRegistrar struct {
	owner      string
	migrations []SchemaMigration
}

func (r *recordingRegistrar) ApplyOwnedMigrations(_ context.Context, owner string, migrations []SchemaMigration) error {
	r.owner = owner
	r.migrations = migrations
	return nil
}
