package operation

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCommandAndCompletionRequireClosedSharedLedgerIdentity(t *testing.T) {
	now := time.Now().UTC()
	command := Command{
		ID: "operation-1", Scope: Scope{SystemPurpose: "scheduler_management", ResourceType: "scheduler_command", ResourceID: "daily"},
		Owner: "scheduler", Kind: "management_command", ActionKey: "scheduler.run", IdempotencyKey: "runtime-a:key",
		RequestFingerprint: "sha256", RequestedBy: "actor-a", Reason: "run schedule", StatusURL: "/scheduler/runs/1", CreatedAt: now,
	}
	if err := command.Validate(); err != nil {
		t.Fatal(err)
	}
	command.Scope.WorkspaceID = "workspace-a"
	if err := command.Validate(); err == nil {
		t.Fatal("command accepted two execution scopes")
	}
	completion := Completion{
		ID:    "operation-1",
		Scope: Scope{SystemPurpose: "scheduler_management"}, Owner: "scheduler", Kind: "management_command",
		IdempotencyKey: "runtime-a:key", RequestFingerprint: "sha256", Result: json.RawMessage(`{"ok":true}`), CompletedAt: now,
	}
	if err := completion.Validate(); err != nil {
		t.Fatal(err)
	}
	completion.Result = json.RawMessage(`{`)
	if err := completion.Validate(); err == nil {
		t.Fatal("completion accepted invalid JSON")
	}
}

func TestRecordFilterValidatesExactJSONCAS(t *testing.T) {
	filter := RecordFilter{
		WorkspaceID: "workspace-a", ID: "operation-1",
		ResultJSON: json.RawMessage(`{"payload":"frozen"}`), MetadataJSON: json.RawMessage(`{"caller":"hashed"}`),
	}
	if _, err := recordPredicate(filter); err != nil {
		t.Fatal(err)
	}
	filter.ResultJSON = json.RawMessage(`{`)
	if _, err := recordPredicate(filter); err == nil {
		t.Fatal("record filter accepted invalid exact-result JSON")
	}
}

func TestOperationsKernelOwnsBreakGlassSchema(t *testing.T) {
	if !slices.Contains(OwnedTables(), BreakGlassTableName) {
		t.Fatal("break-glass table is not owned by the Operations kernel")
	}
	migrations, err := SchemaMigrations("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(migrations[0].Statements, "\n")
	for _, required := range []string{BreakGlassTableName, "idx_runtime_break_glass_active", "uniq_runtime_break_glass_audit"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("Operations migration is missing %s", required)
		}
	}
}

func TestOperationsMySQLMigrationDoesNotDefaultTextColumns(t *testing.T) {
	migrations, err := SchemaMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.ToUpper(strings.Join(migrations[0].Statements, "\n"))
	for _, forbidden := range []string{"TEXT NOT NULL DEFAULT", "LONGTEXT NOT NULL DEFAULT"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("Operations MySQL migration contains unsupported %q clause", forbidden)
		}
	}
	if count := strings.Count(joined, "VARCHAR(128)"); count < 4 {
		t.Fatalf("Operations MySQL migration has %d bounded composite-key columns, want at least 4", count)
	}
	if !strings.Contains(joined, "VARCHAR(64)") || !strings.Contains(joined, "VARCHAR(40)") {
		t.Fatal("Operations MySQL migration does not bound indexed status and timestamp columns")
	}
}

type minimalRenderer struct{}

func (minimalRenderer) Identifier(value string) string { return `"` + value + `"` }
func (minimalRenderer) Table(value string) string      { return `"` + value + `"` }
func (minimalRenderer) Placeholder(index int) string   { return "?" }

func TestAdaptDialectAddsCanonicalInsertRendering(t *testing.T) {
	dialect := AdaptDialect(minimalRenderer{})
	got := dialect.Insert("_operations", []string{"id", "status"})
	want := `INSERT INTO "_operations" ("id", "status") VALUES (?, ?)`
	if got != want {
		t.Fatalf("adapted INSERT=%q want=%q", got, want)
	}
}
