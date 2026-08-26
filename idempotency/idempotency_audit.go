package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// AuditFacts is the deliberately small, payload-free idempotency event shape.
// Keys and request fingerprints are hashed again before crossing into Audit so
// operational evidence cannot be used to recover or replay a caller request.
type AuditFacts struct {
	WorkspaceID        string
	Scope              string
	Key                string
	RequestFingerprint string
	Status             string
	FencingToken       int64
}

func AuditMetadata(facts AuditFacts) map[string]any {
	metadata := map[string]any{
		"workspace_id":       strings.TrimSpace(facts.WorkspaceID),
		"idempotency_scope":  strings.TrimSpace(facts.Scope),
		"idempotency_status": strings.TrimSpace(facts.Status),
	}
	if value := auditDigest(facts.Key); value != "" {
		metadata["idempotency_key_hash"] = value
	}
	if value := auditDigest(facts.RequestFingerprint); value != "" {
		metadata["request_fingerprint_hash"] = value
	}
	if facts.FencingToken > 0 {
		metadata["fencing_token"] = facts.FencingToken
	}
	return metadata
}

func MergeAuditMetadata(metadata map[string]any, facts AuditFacts) map[string]any {
	merged := make(map[string]any, len(metadata)+6)
	for key, value := range metadata {
		merged[key] = value
	}
	for key, value := range AuditMetadata(facts) {
		merged[key] = value
	}
	return merged
}

// LogMetadata is the canonical structured-log shape for every receipt
// decision. Unlike Audit metadata, fields are always present so log queries do
// not need owner-specific fallbacks.
func LogMetadata(facts AuditFacts, requestID string) map[string]any {
	workspaceID := strings.TrimSpace(facts.WorkspaceID)
	if workspaceID == "" {
		workspaceID = "default"
	}
	return map[string]any{
		"workspace_id":         workspaceID,
		"idempotency_scope":    strings.TrimSpace(facts.Scope),
		"idempotency_key_hash": auditDigest(facts.Key),
		"idempotency_status":   strings.TrimSpace(facts.Status),
		"fencing_token":        facts.FencingToken,
		"request_id":           strings.TrimSpace(requestID),
	}
}

func auditDigest(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
