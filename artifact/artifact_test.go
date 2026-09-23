package artifact

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
)

func TestRegisteredOwnersKindsAndBindingsAreClosed(t *testing.T) {
	if registration, found := RegistrationFor(OwnerDataExchange, "output"); !found || registration.Owner != OwnerDataExchange || registration.ContentStorage != ContentStorageOwner {
		t.Fatalf("Data Exchange output registration=%#v found=%v", registration, found)
	}
	if _, found := RegistrationFor(OwnerDataExchange, "arbitrary"); found {
		t.Fatal("unregistered artifact kind was accepted")
	}
	if registration, found := RegistrationFor(OwnerOperations, "result"); !found || registration.Owner != OwnerOperations || registration.ContentStorage != ContentStorageBlob {
		t.Fatalf("Operations result registration=%#v found=%v", registration, found)
	}
	if !BindingKindRegistered(BindingJob) || BindingKindRegistered("arbitrary") {
		t.Fatal("artifact binding registry is not closed")
	}
}

type executorProbe struct{ Executor }

func TestExecutorContextPrefersHostTransaction(t *testing.T) {
	fallback := &executorProbe{}
	tx := &executorProbe{}
	if got := ExecutorFromContext(context.Background(), fallback); got != fallback {
		t.Fatal("fallback executor was not returned")
	}
	if got := ExecutorFromContext(WithExecutor(context.Background(), tx), fallback); got != tx {
		t.Fatal("transaction executor was not returned")
	}
	var _ Executor = (*sql.DB)(nil)
	var _ Executor = (*sql.Tx)(nil)
}

func TestArtifactKernelOwnsCanonicalSchema(t *testing.T) {
	if !slices.Equal(OwnedTables(), []string{TableName, BindingTableName}) {
		t.Fatalf("unexpected Artifact-owned tables: %v", OwnedTables())
	}
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 || migrations[0].Version != 1 {
		t.Fatalf("unexpected Artifact migrations: %#v", migrations)
	}
	joined := strings.Join(migrations[0].Statements, "\n")
	for _, required := range []string{TableName, BindingTableName, "idx_artifact_owner_cursor", "idx_artifact_binding_resource"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("Artifact migration is missing %s", required)
		}
	}
}
