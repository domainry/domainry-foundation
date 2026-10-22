package workerscope

import (
	"fmt"
	"strings"
	"time"
)

const (
	OwnerAgentConversationCapacity = "agent_conversation_capacity"
	OwnerAgentTask                 = "agent_task"
	OwnerDataExchange              = "data_exchange"
	OwnerIdempotencyCleanup        = "idempotency_cleanup"
	OwnerNotificationChannel       = "notification_channel"
	OwnerNotificationInbox         = "notification_inbox"
	OwnerRuntimePublicationOutbox  = "runtime_publication_outbox"
	OwnerSchedulerTriggerCapacity  = "scheduler_trigger_capacity"
	OwnerWorkflowContinuation      = "workflow_continuation"
)

type Registration struct {
	Owner          string
	RecoveryPolicy string
}

var registrations = map[string]Registration{
	OwnerAgentConversationCapacity: {Owner: OwnerAgentConversationCapacity, RecoveryPolicy: "transactional_guard"},
	OwnerAgentTask:                 {Owner: OwnerAgentTask, RecoveryPolicy: "durable_due_scan"},
	OwnerDataExchange:              {Owner: OwnerDataExchange, RecoveryPolicy: "durable_due_scan"},
	OwnerIdempotencyCleanup:        {Owner: OwnerIdempotencyCleanup, RecoveryPolicy: "expired_lease_reclaim"},
	OwnerNotificationChannel:       {Owner: OwnerNotificationChannel, RecoveryPolicy: "durable_due_scan"},
	OwnerNotificationInbox:         {Owner: OwnerNotificationInbox, RecoveryPolicy: "durable_due_scan"},
	OwnerRuntimePublicationOutbox:  {Owner: OwnerRuntimePublicationOutbox, RecoveryPolicy: "durable_due_scan"},
	OwnerSchedulerTriggerCapacity:  {Owner: OwnerSchedulerTriggerCapacity, RecoveryPolicy: "transactional_guard"},
	OwnerWorkflowContinuation:      {Owner: OwnerWorkflowContinuation, RecoveryPolicy: "durable_due_scan"},
}

func RegistrationFor(owner string) (Registration, bool) {
	value, found := registrations[strings.TrimSpace(owner)]
	return value, found
}

type Identity struct {
	Owner    string
	ScopeKey string
}

func NewIdentity(owner, scopeKey string) Identity {
	return Identity{Owner: strings.TrimSpace(owner), ScopeKey: strings.TrimSpace(scopeKey)}
}

func (v Identity) normalized() Identity {
	v.Owner, v.ScopeKey = strings.TrimSpace(v.Owner), strings.TrimSpace(v.ScopeKey)
	return v
}

func (v Identity) validate() error {
	v = v.normalized()
	if v.Owner == "" || v.ScopeKey == "" {
		return fmt.Errorf("worker scope identity is required")
	}
	if _, found := RegistrationFor(v.Owner); !found {
		return fmt.Errorf("worker scope owner %q is not registered", v.Owner)
	}
	return nil
}

type Scope struct {
	Identity
	Cursor          string
	Checkpoint      int64
	Capacity        int64
	LeaseOwner      string
	LeaseExpiresAt  string
	FencingToken    int64
	LastStartedAt   string
	LastCompletedAt string
	LastError       string
	UpdatedAt       string
}

type ScopeOrder uint8

const (
	ScopeKeyAscending ScopeOrder = iota + 1
	UpdatedAtAscending
	UpdatedAtDescending
)

type ScopeQuery struct {
	Owner    string
	AfterKey string
	Order    ScopeOrder
	Limit    int
}

type LeaseClaim struct {
	Identity
	LeaseOwner     string
	Now            time.Time
	LeaseExpiresAt time.Time
}

type LeaseCompletion struct {
	Identity
	LeaseOwner   string
	FencingToken int64
	CompletedAt  time.Time
	Checkpoint   int64
}

type LeaseFailure struct {
	Identity
	LeaseOwner   string
	FencingToken int64
	FailedAt     time.Time
	Cause        error
}

type LeaseCounts struct {
	Live    int64
	Expired int64
}

type LeaseStatus struct {
	LeaseOwner      string
	LeaseExpiresAt  string
	FencingToken    int64
	LastStartedAt   string
	LastCompletedAt string
	Checkpoint      int64
	LastError       string
}

type LeaseRelease struct {
	Identity
	ExpectedLeaseOwner   string
	ExpectedFencingToken int64
	Now                  time.Time
	AllowUnexpired       bool
}

type LeaseReleaseResult struct {
	PreviousLeaseOwner     string
	PreviousFencingToken   int64
	NextFencingToken       int64
	PreviousLeaseExpiresAt time.Time
	Eligibility            string
}
