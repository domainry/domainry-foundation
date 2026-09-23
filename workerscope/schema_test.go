package workerscope

import (
	"strings"
	"testing"
)

func TestSchemaMigrationsOwnOnePortableTable(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			migrations, err := SchemaMigrations(driver, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(migrations) != 1 || migrations[0].Version != 1 || len(migrations[0].Statements) != 2 {
				t.Fatalf("migrations=%+v", migrations)
			}
			joined := strings.Join(migrations[0].Statements, "\n")
			for _, required := range []string{TableName, "owner", "scope_key", "lease_owner", "fencing_token", "idx_worker_scope_lease"} {
				if !strings.Contains(joined, required) {
					t.Fatalf("%s migration missing %s", driver, required)
				}
			}
		})
	}
}

func TestRegisteredOwnersAndStableIdentity(t *testing.T) {
	for _, owner := range []string{
		OwnerAgentConversationCapacity, OwnerAgentTask, OwnerDataExchange, OwnerIdempotencyCleanup,
		OwnerNotificationChannel, OwnerNotificationInbox, OwnerRuntimePublicationOutbox,
		OwnerSchedulerTriggerCapacity, OwnerWorkflowContinuation,
	} {
		if value, found := RegistrationFor(owner); !found || value.Owner != owner || value.RecoveryPolicy == "" {
			t.Fatalf("registration %q=%+v found=%v", owner, value, found)
		}
		first, second := NewIdentity(owner, "scope-a"), NewIdentity(owner, "scope-a")
		if first != second || first.ID == "" {
			t.Fatalf("unstable identity for %q: %+v %+v", owner, first, second)
		}
	}
}

func TestMySQLSchemaDoesNotDefaultLongText(t *testing.T) {
	migrations, err := SchemaMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.ToUpper(strings.Join(migrations[0].Statements, "\n")); strings.Contains(joined, "LONGTEXT NOT NULL DEFAULT") {
		t.Fatal("Worker Scope MySQL schema defaults LONGTEXT")
	}
}
