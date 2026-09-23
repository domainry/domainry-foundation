package artifact

import (
	"context"
	"database/sql"
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
