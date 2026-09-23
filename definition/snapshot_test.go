package definition

import "testing"

func TestSnapshotRevisionIsOrderIndependentAndContentSensitive(t *testing.T) {
	left := Snapshot{Definitions: []Definition{
		{Owner: OwnerIdentity, ResourceType: "role", ResourceKey: "manager", SchemaHash: "hash-manager"},
		{Owner: OwnerMetadata, ResourceType: "object", ResourceKey: "order", SchemaHash: "hash-order"},
	}}
	right := Snapshot{Definitions: []Definition{left.Definitions[1], left.Definitions[0]}}
	if SnapshotRevision(left) != SnapshotRevision(right) {
		t.Fatal("snapshot revision depends on row order")
	}
	right.Definitions[0].SchemaHash = "changed"
	if SnapshotRevision(left) == SnapshotRevision(right) {
		t.Fatal("snapshot revision ignored effective definition content")
	}
}

func TestSnapshotRevisionUsesUnambiguousFieldBoundaries(t *testing.T) {
	left := Snapshot{Definitions: []Definition{{Owner: "ab", ResourceType: "c", ResourceKey: "d", SchemaHash: "e"}}}
	right := Snapshot{Definitions: []Definition{{Owner: "a", ResourceType: "bc", ResourceKey: "d", SchemaHash: "e"}}}
	if SnapshotRevision(left) == SnapshotRevision(right) {
		t.Fatal("snapshot revision field boundaries are ambiguous")
	}
}

func TestAgentDefinitionKindsShareOneRegisteredOwner(t *testing.T) {
	for _, kind := range []string{"skill", "agent", "agent_task", "agent_entrypoint", "agent_service_principal"} {
		owner, normalized, err := normalizeOwnerKind(OwnerAgent, kind)
		if err != nil || owner != OwnerAgent || normalized != kind {
			t.Errorf("Agent definition kind %q: owner=%q kind=%q err=%v", kind, owner, normalized, err)
		}
	}
}
