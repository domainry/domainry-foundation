package idempotency

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditMetadataContainsOnlySafeReceiptFacts(t *testing.T) {
	facts := AuditFacts{
		WorkspaceID:        "workspace-a",
		Scope:              "record.create",
		Key:                "customer-provided-secret-key",
		RequestFingerprint: "fingerprint-derived-from-password-payload",
		Status:             "replayed",
		FencingToken:       7,
	}
	metadata := MergeAuditMetadata(map[string]any{"operation": "create"}, facts)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, sensitive := range []string{facts.Key, facts.RequestFingerprint, "password", "payload", "secret-key"} {
		if strings.Contains(text, sensitive) {
			t.Fatalf("audit metadata leaked %q: %s", sensitive, text)
		}
	}
	if metadata["workspace_id"] != "workspace-a" || metadata["idempotency_scope"] != "record.create" || metadata["idempotency_status"] != "replayed" || metadata["fencing_token"] != int64(7) {
		t.Fatalf("missing receipt facts: %#v", metadata)
	}
	keyHash, keyOK := metadata["idempotency_key_hash"].(string)
	fingerprintHash, fingerprintOK := metadata["request_fingerprint_hash"].(string)
	if !keyOK || !fingerprintOK || !strings.HasPrefix(keyHash, "sha256:") || !strings.HasPrefix(fingerprintHash, "sha256:") || keyHash == fingerprintHash {
		t.Fatalf("invalid audit hashes: %#v", metadata)
	}
}

func TestMergeAuditMetadataDoesNotMutateCallerMap(t *testing.T) {
	base := map[string]any{"operation": "create"}
	merged := MergeAuditMetadata(base, AuditFacts{Scope: "record.create", Status: "succeeded"})
	if _, exists := base["idempotency_scope"]; exists || merged["operation"] != "create" {
		t.Fatalf("caller metadata mutated: base=%#v merged=%#v", base, merged)
	}
}

func TestLogMetadataAlwaysContainsCanonicalCorrelationFields(t *testing.T) {
	metadata := LogMetadata(AuditFacts{Scope: "action.execute", Key: "caller-key", Status: "in_progress", FencingToken: 3}, "request-1")
	for _, key := range []string{"workspace_id", "idempotency_scope", "idempotency_key_hash", "idempotency_status", "fencing_token", "request_id"} {
		if _, exists := metadata[key]; !exists {
			t.Fatalf("missing %s: %#v", key, metadata)
		}
	}
	if metadata["workspace_id"] != "default" || metadata["idempotency_key_hash"] == "caller-key" || metadata["fencing_token"] != int64(3) || metadata["request_id"] != "request-1" {
		t.Fatalf("unexpected log metadata: %#v", metadata)
	}
}
