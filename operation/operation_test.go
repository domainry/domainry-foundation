package operation

import (
	"encoding/json"
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
