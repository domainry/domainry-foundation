package idempotency

import (
	"strings"
	"time"
)

// Status is the persisted lifecycle of one idempotent execution receipt.
type Status string

const (
	StatusProcessing      Status = "processing"
	StatusSucceeded       Status = "succeeded"
	StatusFailedRetryable Status = "failed_retryable"
	StatusFailedTerminal  Status = "failed_terminal"
)

// Decision tells an Application use case whether it owns execution or must
// return an existing receipt outcome.
type Decision string

const (
	DecisionAcquired            Decision = "acquired"
	DecisionReplay              Decision = "replay"
	DecisionInProgress          Decision = "in_progress"
	DecisionFingerprintConflict Decision = "fingerprint_conflict"
)

const (
	ErrorCodeMissingKey         = "backend.idempotency.key_required"
	ErrorCodeKeyReused          = "backend.idempotency.key_reused"
	ErrorCodeInProgress         = "backend.idempotency.in_progress"
	ErrorCodeLeaseLost          = "backend.idempotency.lease_lost"
	ErrorCodeReceiptUnavailable = "backend.idempotency.receipt_unavailable"
)

// Scope is the cross-instance identity of one idempotent use case. Business
// owners remain responsible for choosing its fields.
type Scope struct {
	WorkspaceID  string `json:"workspace_id"`
	UseCase      string `json:"use_case"`
	ResourceType string `json:"resource_type"`
	TargetID     string `json:"target_id,omitempty"`
	Key          string `json:"idempotency_key"`
}

func (scope Scope) Normalized() Scope {
	scope.WorkspaceID = strings.TrimSpace(scope.WorkspaceID)
	scope.UseCase = strings.TrimSpace(scope.UseCase)
	scope.ResourceType = strings.TrimSpace(scope.ResourceType)
	scope.TargetID = strings.TrimSpace(scope.TargetID)
	scope.Key = strings.TrimSpace(scope.Key)
	return scope
}

func (scope Scope) IsComplete() bool {
	scope = scope.Normalized()
	return scope.WorkspaceID != "" && scope.UseCase != "" && scope.ResourceType != "" && scope.Key != ""
}

// Lease fences completion and heartbeat writes from an expired execution
// owner. Token values must be allocated atomically by persistence adapters.
type Lease struct {
	Owner     string    `json:"owner"`
	Token     int64     `json:"fencing_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (lease Lease) IsLive(now time.Time) bool {
	return strings.TrimSpace(lease.Owner) != "" && lease.Token > 0 && lease.ExpiresAt.After(now)
}

func (lease Lease) Matches(owner string, token int64) bool {
	return strings.TrimSpace(owner) != "" && strings.TrimSpace(owner) == strings.TrimSpace(lease.Owner) && token > 0 && token == lease.Token
}

// ReceiptState contains only the generic facts needed to classify a claim.
type ReceiptState struct {
	Status      Status
	Fingerprint string
	Lease       Lease
}

// Classify returns the deterministic response for an existing receipt.
func Classify(state ReceiptState, fingerprint string, now time.Time) Decision {
	if strings.TrimSpace(state.Fingerprint) != strings.TrimSpace(fingerprint) {
		return DecisionFingerprintConflict
	}
	switch state.Status {
	case StatusSucceeded, StatusFailedTerminal:
		return DecisionReplay
	case StatusProcessing:
		if state.Lease.IsLive(now) {
			return DecisionInProgress
		}
		return DecisionAcquired
	case StatusFailedRetryable:
		return DecisionAcquired
	default:
		return DecisionInProgress
	}
}
