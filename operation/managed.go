package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/sqlhost"
)

var ErrIdentityConflict = errors.New("managed operation identity conflict")

type ManagedOperation struct {
	Command        Command
	Status         string
	Metadata       json.RawMessage
	Result         json.RawMessage
	ErrorCode      string
	NextAction     string
	LeaseOwner     string
	LeaseExpiresAt string
	FencingToken   int64
	UpdatedAt      time.Time
}

func (value ManagedOperation) Validate() error {
	if err := value.Command.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(value.Status) == "" || value.UpdatedAt.IsZero() {
		return fmt.Errorf("managed operation status and update time are required")
	}
	if !validJSONObject(value.Metadata) || !validJSONObject(value.Result) {
		return fmt.Errorf("managed operation metadata and result must be JSON objects")
	}
	return nil
}

type ManagedIdentity struct {
	ID    string
	Scope Scope
	Owner string
	Kind  string
}

func (value ManagedIdentity) Validate() error {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.Owner) == "" || strings.TrimSpace(value.Kind) == "" {
		return fmt.Errorf("managed operation identity is required")
	}
	workspace, system := strings.TrimSpace(value.Scope.WorkspaceID) != "", strings.TrimSpace(value.Scope.SystemPurpose) != ""
	if workspace == system {
		return fmt.Errorf("managed operation requires exactly one workspace or system scope")
	}
	return nil
}

type ManagedQuery struct {
	Scope              Scope
	Owner              string
	Kind               string
	ResourceID         string
	Statuses           []string
	NextActionBefore   string
	LeaseExpiresBefore string
	Limit              int
	OldestFirst        bool
}

func (value ManagedQuery) Validate() error {
	if strings.TrimSpace(value.Owner) == "" || strings.TrimSpace(value.Kind) == "" {
		return fmt.Errorf("managed operation query owner and kind are required")
	}
	workspace, system := strings.TrimSpace(value.Scope.WorkspaceID) != "", strings.TrimSpace(value.Scope.SystemPurpose) != ""
	if workspace == system || value.Limit < 1 || value.Limit > 1000 {
		return fmt.Errorf("managed operation query scope and limit are invalid")
	}
	for _, status := range value.Statuses {
		if strings.TrimSpace(status) == "" {
			return fmt.Errorf("managed operation query status is required")
		}
	}
	return nil
}

type ManagedTransition struct {
	Identity             ManagedIdentity
	ExpectedStatus       string
	ExpectedLeaseOwner   string
	ExpectedFencingToken int64
	Status               string
	Metadata             json.RawMessage
	Result               json.RawMessage
	ErrorCode            string
	NextAction           string
	ClearLease           bool
	UpdatedAt            time.Time
}

func (value ManagedTransition) Validate() error {
	if err := value.Identity.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(value.ExpectedStatus) == "" || strings.TrimSpace(value.Status) == "" || value.UpdatedAt.IsZero() {
		return fmt.Errorf("managed operation transition statuses and update time are required")
	}
	if !validJSONObject(value.Metadata) || !validJSONObject(value.Result) {
		return fmt.Errorf("managed operation transition metadata and result must be JSON objects")
	}
	if (strings.TrimSpace(value.ExpectedLeaseOwner) == "") != (value.ExpectedFencingToken == 0) {
		return fmt.Errorf("managed operation transition lease owner and fencing token must be supplied together")
	}
	return nil
}

type ManagedClaim struct {
	Identity       ManagedIdentity
	DueStatus      string
	ReclaimStatus  string
	Now            string
	Status         string
	LeaseOwner     string
	LeaseExpiresAt string
	UpdatedAt      time.Time
}

func (value ManagedClaim) Validate() error {
	if err := value.Identity.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(value.DueStatus) == "" || strings.TrimSpace(value.ReclaimStatus) == "" || strings.TrimSpace(value.Now) == "" ||
		strings.TrimSpace(value.Status) == "" || strings.TrimSpace(value.LeaseOwner) == "" || strings.TrimSpace(value.LeaseExpiresAt) == "" || value.UpdatedAt.IsZero() {
		return fmt.Errorf("managed operation claim lifecycle and lease are required")
	}
	return nil
}

type ManagedStore interface {
	Create(context.Context, ManagedOperation) error
	Get(context.Context, ManagedIdentity) (ManagedOperation, bool, error)
	List(context.Context, ManagedQuery) ([]ManagedOperation, error)
	Transition(context.Context, ManagedTransition) (ManagedOperation, bool, error)
	ClaimManaged(context.Context, ManagedClaim) (ManagedOperation, bool, error)
}

type Control struct {
	SystemPurpose string
	Kind          string
	Owner         string
	State         string
	Reason        string
	Reference     string
	UpdatedBy     string
	Revision      int64
	UpdatedAt     time.Time
}

func (value Control) Validate() error {
	if strings.TrimSpace(value.SystemPurpose) == "" || strings.TrimSpace(value.Kind) == "" || strings.TrimSpace(value.Owner) == "" {
		return fmt.Errorf("operation control identity is required")
	}
	if strings.TrimSpace(value.State) == "" || strings.TrimSpace(value.Reason) == "" || strings.TrimSpace(value.UpdatedBy) == "" {
		return fmt.Errorf("operation control state and audit context are required")
	}
	if value.Revision < 1 || value.UpdatedAt.IsZero() {
		return fmt.Errorf("operation control revision and update time are required")
	}
	return nil
}

type ControlStore interface {
	GetControl(context.Context, string, string, string) (Control, bool, error)
	PutControl(context.Context, Control, int64) (bool, error)
	ControlStateExists(context.Context, string, string, string) (bool, error)
}

type executorContextKey struct{}

func WithExecutor(ctx context.Context, executor sqlhost.DBTX) context.Context {
	if executor == nil {
		return ctx
	}
	return context.WithValue(ctx, executorContextKey{}, executor)
}

func ExecutorFromContext(ctx context.Context, fallback sqlhost.DBTX) sqlhost.DBTX {
	if ctx != nil {
		if executor, ok := ctx.Value(executorContextKey{}).(sqlhost.DBTX); ok && executor != nil {
			return executor
		}
	}
	return fallback
}

func validJSONObject(value json.RawMessage) bool {
	if len(value) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}
