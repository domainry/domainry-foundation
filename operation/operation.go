// Package operation defines the deployment-neutral port for the shared
// operation ledger. Business modules own command semantics; the host owns the
// physical _operations table and its installation-wide idempotency boundary.
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	StatusStarted   = "started"
	StatusSucceeded = "succeeded"
)

var ErrIdempotencyConflict = errors.New("operation idempotency conflict")

type Scope struct {
	WorkspaceID   string
	SystemPurpose string
	ResourceType  string
	ResourceID    string
}

type Command struct {
	ID                 string
	Scope              Scope
	Owner              string
	Kind               string
	ActionKey          string
	IdempotencyKey     string
	RequestFingerprint string
	RequestedBy        string
	Reason             string
	Reference          string
	StatusURL          string
	CreatedAt          time.Time
}

func (c Command) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Owner) == "" || strings.TrimSpace(c.Kind) == "" || strings.TrimSpace(c.ActionKey) == "" {
		return fmt.Errorf("operation command identity is required")
	}
	if strings.TrimSpace(c.IdempotencyKey) == "" || strings.TrimSpace(c.RequestFingerprint) == "" {
		return fmt.Errorf("operation idempotency identity is required")
	}
	if strings.TrimSpace(c.RequestedBy) == "" || strings.TrimSpace(c.Reason) == "" || strings.TrimSpace(c.StatusURL) == "" {
		return fmt.Errorf("operation audit context is required")
	}
	if strings.TrimSpace(c.Scope.ResourceType) == "" {
		return fmt.Errorf("operation resource type is required")
	}
	workspace, system := strings.TrimSpace(c.Scope.WorkspaceID) != "", strings.TrimSpace(c.Scope.SystemPurpose) != ""
	if workspace == system {
		return fmt.Errorf("operation requires exactly one workspace or system scope")
	}
	if c.CreatedAt.IsZero() {
		return fmt.Errorf("operation creation time is required")
	}
	return nil
}

type Receipt struct {
	Command Command
	Status  string
	Result  json.RawMessage
}

type Completion struct {
	ID                 string
	Scope              Scope
	Owner              string
	Kind               string
	IdempotencyKey     string
	RequestFingerprint string
	Result             json.RawMessage
	CompletedAt        time.Time
}

func (c Completion) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Owner) == "" || strings.TrimSpace(c.Kind) == "" || strings.TrimSpace(c.IdempotencyKey) == "" || strings.TrimSpace(c.RequestFingerprint) == "" {
		return fmt.Errorf("operation completion identity is required")
	}
	workspace, system := strings.TrimSpace(c.Scope.WorkspaceID) != "", strings.TrimSpace(c.Scope.SystemPurpose) != ""
	if workspace == system || c.CompletedAt.IsZero() || !json.Valid(c.Result) {
		return fmt.Errorf("operation completion scope, time, and JSON result are required")
	}
	return nil
}

// Store claims a command atomically in started state and completes that exact
// fingerprint. A replay returns the original receipt with claimed=false; a
// reused key with a different fingerprint returns ErrIdempotencyConflict.
type Store interface {
	Claim(context.Context, Command) (Receipt, bool, error)
	Complete(context.Context, Completion) error
}
