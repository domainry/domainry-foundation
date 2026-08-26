package idempotency

type ReceiptSummary struct {
	Owner                  string `json:"owner"`
	ID                     string `json:"id"`
	WorkspaceID            string `json:"workspace_id"`
	Scope                  string `json:"scope"`
	IdempotencyKeyHash     string `json:"idempotency_key_hash"`
	RequestFingerprintHash string `json:"request_fingerprint_hash"`
	Status                 string `json:"status"`
	FencingToken           int64  `json:"fencing_token"`
	UpdatedAt              string `json:"updated_at"`
	ExpiresAt              string `json:"expires_at,omitempty"`
}

func ReceiptSummaryFromValues(owner, id, workspaceID, scope, key, fingerprint, status string, fencingToken int64, updatedAt, expiresAt string) ReceiptSummary {
	metadata := AuditMetadata(AuditFacts{Key: key, RequestFingerprint: fingerprint})
	keyHash, _ := metadata["idempotency_key_hash"].(string)
	fingerprintHash, _ := metadata["request_fingerprint_hash"].(string)
	return ReceiptSummary{Owner: owner, ID: id, WorkspaceID: workspaceID, Scope: scope, IdempotencyKeyHash: keyHash, RequestFingerprintHash: fingerprintHash, Status: status, FencingToken: fencingToken, UpdatedAt: updatedAt, ExpiresAt: expiresAt}
}
