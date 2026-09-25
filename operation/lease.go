package operation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-orm/query"
)

const StatusFailed = "failed"

type LeasedRecord struct {
	ID                 string
	WorkspaceID        string
	ActionKey          string
	ResourceType       string
	ResourceID         string
	IdempotencyKey     string
	RequestFingerprint string
	RequestedBy        string
	Status             string
	ResultJSON         json.RawMessage
	LeaseOwner         string
	LeaseExpiresAt     string
	FencingToken       int64
	ErrorCode          string
	ExpiresAt          string
	CreatedAt          string
	UpdatedAt          string
}

type StartedRecord struct {
	LeasedRecord
	Owner          string
	Kind           string
	Reason         string
	Reference      string
	MetadataJSON   json.RawMessage
	RelatedIDsJSON json.RawMessage
	EvidenceJSON   json.RawMessage
}

type LeaseReclaim struct {
	WorkspaceID        string
	ID                 string
	RequestFingerprint string
	LeaseOwner         string
	LeaseExpiresAt     string
	ExpectedToken      int64
	ExpiredAt          string
	UpdatedAt          string
}

type LeaseCompletion struct {
	WorkspaceID  string
	ID           string
	LeaseOwner   string
	FencingToken int64
	Status       string
	ResultJSON   json.RawMessage
	ErrorCode    string
	ExpiresAt    string
	CompletedAt  string
}

func InsertStarted(ctx context.Context, execer Execer, renderer ormdialect.Renderer, value StartedRecord) error {
	value.Status = StatusStarted
	for _, field := range []string{
		value.ID, value.WorkspaceID, value.Owner, value.Kind, value.ActionKey, value.ResourceType,
		value.IdempotencyKey, value.RequestFingerprint, value.RequestedBy, value.Reason,
		value.LeaseOwner, value.LeaseExpiresAt, value.CreatedAt, value.UpdatedAt,
	} {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("shared leased operation fields are required")
		}
	}
	if value.FencingToken <= 0 {
		return fmt.Errorf("shared leased operation fencing token is invalid")
	}
	if len(value.ResultJSON) == 0 {
		value.ResultJSON = json.RawMessage(`{}`)
	}
	if len(value.MetadataJSON) == 0 {
		value.MetadataJSON = json.RawMessage(`{}`)
	}
	if len(value.RelatedIDsJSON) == 0 {
		value.RelatedIDsJSON = json.RawMessage(`[]`)
	}
	if len(value.EvidenceJSON) == 0 {
		value.EvidenceJSON = json.RawMessage(`[]`)
	}
	if !json.Valid(value.ResultJSON) || !json.Valid(value.MetadataJSON) || !json.Valid(value.RelatedIDsJSON) || !json.Valid(value.EvidenceJSON) {
		return fmt.Errorf("shared leased operation JSON is invalid")
	}
	leaseExpiresAt, err := operationTimestampMillis(value.LeaseExpiresAt)
	if err != nil {
		return err
	}
	expiresAt, err := operationTimestampMillis(value.ExpiresAt)
	if err != nil {
		return err
	}
	createdAt, err := operationTimestampMillis(value.CreatedAt)
	if err != nil {
		return err
	}
	updatedAt, err := operationTimestampMillis(value.UpdatedAt)
	if err != nil {
		return err
	}
	statement, arguments, err := query.NewWorkspaceInsertBuilder(renderer, TableName, strings.TrimSpace(value.WorkspaceID)).
		Columns(operationColumnsWithoutWorkspace()...).
		Values(operationRecordValues(
			value.ID, "", value.Owner, value.Kind, value.ActionKey, "", value.ResourceType, value.ResourceID,
			value.IdempotencyKey, value.RequestFingerprint, value.RequestedBy, value.Reason, value.Reference, StatusStarted, "",
			string(value.ResultJSON), string(value.MetadataJSON), "", "", "", string(value.RelatedIDsJSON), "",
			string(value.EvidenceJSON), value.LeaseOwner, leaseExpiresAt, value.FencingToken, expiresAt,
			createdAt, createdAt, int64(0), updatedAt,
		)...).Build()
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, statement, arguments...)
	return err
}

func LoadLeasedByKey(ctx context.Context, queryer Queryer, renderer ormdialect.Renderer, workspaceID, owner, kind, idempotencyKey string) (LeasedRecord, bool, error) {
	return loadLeased(ctx, queryer, renderer, workspaceID, query.And(
		query.Equal("system_purpose", ""), query.Equal("owner", strings.TrimSpace(owner)),
		query.Equal("kind", strings.TrimSpace(kind)), query.Equal("idempotency_key", strings.TrimSpace(idempotencyKey)),
	))
}

func LoadLeasedByID(ctx context.Context, queryer Queryer, renderer ormdialect.Renderer, workspaceID, owner, kind, id string) (LeasedRecord, bool, error) {
	return loadLeased(ctx, queryer, renderer, workspaceID, query.And(
		query.Equal("system_purpose", ""), query.Equal("owner", strings.TrimSpace(owner)),
		query.Equal("kind", strings.TrimSpace(kind)), query.Equal("id", strings.TrimSpace(id)),
	))
}

func ReclaimStarted(ctx context.Context, execer Execer, renderer ormdialect.Renderer, value LeaseReclaim) (bool, error) {
	leaseExpiresAt, err := operationTimestampMillis(value.LeaseExpiresAt)
	if err != nil {
		return false, err
	}
	updatedAt, err := operationTimestampMillis(value.UpdatedAt)
	if err != nil {
		return false, err
	}
	expiredAt, err := operationTimestampMillis(value.ExpiredAt)
	if err != nil {
		return false, err
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(renderer, TableName, strings.TrimSpace(value.WorkspaceID)).
		Set("status", StatusStarted).Set("lease_owner", strings.TrimSpace(value.LeaseOwner)).
		Set("lease_expires_at", leaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("started_at", updatedAt).Set("updated_at", updatedAt).
		Where(query.And(
			query.Equal("id", strings.TrimSpace(value.ID)), query.Equal("request_fingerprint", strings.TrimSpace(value.RequestFingerprint)),
			query.Equal("status", StatusStarted), query.Equal("fencing_token", value.ExpectedToken),
			query.LessThanOrEqual("lease_expires_at", expiredAt),
		)).Build()
	if err != nil {
		return false, err
	}
	result, err := execer.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func CompleteLeased(ctx context.Context, execer Execer, renderer ormdialect.Renderer, value LeaseCompletion) (bool, error) {
	if value.Status != StatusSucceeded && value.Status != StatusFailed {
		return false, fmt.Errorf("shared leased operation completion status is invalid")
	}
	if !json.Valid(value.ResultJSON) {
		return false, fmt.Errorf("shared leased operation result is invalid")
	}
	expiresAt, err := operationTimestampMillis(value.ExpiresAt)
	if err != nil {
		return false, err
	}
	completedAt, err := operationTimestampMillis(value.CompletedAt)
	if err != nil {
		return false, err
	}
	failureClass := ""
	if value.Status == StatusFailed {
		failureClass = "terminal"
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(renderer, TableName, strings.TrimSpace(value.WorkspaceID)).
		Set("status", value.Status).Set("result_json", string(value.ResultJSON)).Set("error_code", strings.TrimSpace(value.ErrorCode)).
		Set("failure_class", failureClass).Set("expires_at", expiresAt).
		Set("finished_at", completedAt).Set("updated_at", completedAt).
		Where(query.And(
			query.Equal("id", strings.TrimSpace(value.ID)), query.Equal("lease_owner", strings.TrimSpace(value.LeaseOwner)),
			query.Equal("fencing_token", value.FencingToken), query.Equal("status", StatusStarted),
		)).Build()
	if err != nil {
		return false, err
	}
	result, err := execer.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func loadLeased(ctx context.Context, queryer Queryer, renderer ormdialect.Renderer, workspaceID string, predicate query.Predicate) (LeasedRecord, bool, error) {
	statement, arguments, err := query.NewWorkspaceSelectBuilder(renderer, TableName, strings.TrimSpace(workspaceID)).
		Columns(
			"id", "workspace_id", "action_key", "resource_type", "resource_id", "idempotency_key", "request_fingerprint",
			"requested_by", "status", "result_json", "lease_owner", "lease_expires_at", "fencing_token", "error_code",
			"expires_at", "created_at", "updated_at",
		).Where(predicate).Limit(1).Build()
	if err != nil {
		return LeasedRecord{}, false, err
	}
	var value LeasedRecord
	var resultJSON string
	var leaseExpiresAt, expiresAt, createdAt, updatedAt int64
	err = queryer.QueryRowContext(ctx, statement, arguments...).Scan(
		&value.ID, &value.WorkspaceID, &value.ActionKey, &value.ResourceType, &value.ResourceID, &value.IdempotencyKey,
		&value.RequestFingerprint, &value.RequestedBy, &value.Status, &resultJSON, &value.LeaseOwner, &leaseExpiresAt,
		&value.FencingToken, &value.ErrorCode, &expiresAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return LeasedRecord{}, false, nil
	}
	if err != nil {
		return LeasedRecord{}, false, err
	}
	value.ResultJSON = json.RawMessage(resultJSON)
	value.LeaseExpiresAt, value.ExpiresAt, value.CreatedAt, value.UpdatedAt = operationTimestampText(leaseExpiresAt), operationTimestampText(expiresAt), operationTimestampText(createdAt), operationTimestampText(updatedAt)
	if !json.Valid(value.ResultJSON) {
		return LeasedRecord{}, false, fmt.Errorf("shared leased operation result is invalid")
	}
	if value.Status != StatusStarted && value.Status != StatusSucceeded && value.Status != StatusFailed {
		return LeasedRecord{}, false, fmt.Errorf("shared leased operation status %q is invalid", value.Status)
	}
	return value, true, nil
}
