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

type Queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type SucceededReceipt struct {
	ID                 string
	ResourceID         string
	RequestFingerprint string
	RequestedBy        string
	Reference          string
	ResultJSON         json.RawMessage
	CreatedAt          string
	Status             string
}

type SucceededRecord struct {
	ID                 string
	WorkspaceID        string
	Owner              string
	Kind               string
	ActionKey          string
	ResourceType       string
	ResourceID         string
	IdempotencyKey     string
	RequestFingerprint string
	RequestedBy        string
	Reason             string
	Reference          string
	ResultJSON         json.RawMessage
	MetadataJSON       json.RawMessage
	RelatedIDsJSON     json.RawMessage
	EvidenceJSON       json.RawMessage
	Correlation        string
	CompletedAt        string
}

func LoadSucceeded(ctx context.Context, queryer Queryer, renderer ormdialect.Renderer, workspaceID, owner, kind, idempotencyKey string) (SucceededReceipt, bool, error) {
	return loadSucceeded(ctx, queryer, renderer, workspaceID, owner, kind, idempotencyKey, "", false)
}

func LoadSucceededReferenced(ctx context.Context, queryer Queryer, renderer ormdialect.Renderer, workspaceID, owner, kind, idempotencyKey string) (SucceededReceipt, bool, error) {
	return loadSucceeded(ctx, queryer, renderer, workspaceID, owner, kind, idempotencyKey, "", true)
}

func LoadSucceededByID(ctx context.Context, queryer Queryer, renderer ormdialect.Renderer, workspaceID, owner, kind, id string) (SucceededReceipt, bool, error) {
	return loadSucceeded(ctx, queryer, renderer, workspaceID, owner, kind, "", id, true)
}

func loadSucceeded(ctx context.Context, queryer Queryer, renderer ormdialect.Renderer, workspaceID, owner, kind, idempotencyKey, id string, withReference bool) (SucceededReceipt, bool, error) {
	columns := []string{"id", "resource_id", "request_fingerprint", "requested_by"}
	if withReference {
		columns = append(columns, "reference")
	}
	columns = append(columns, "result_json", "created_at", "status")
	predicates := []query.Predicate{
		query.Equal("system_purpose", ""),
		query.Equal("owner", strings.TrimSpace(owner)),
		query.Equal("kind", strings.TrimSpace(kind)),
	}
	if strings.TrimSpace(id) != "" {
		predicates = append(predicates, query.Equal("id", strings.TrimSpace(id)))
	} else {
		predicates = append(predicates, query.Equal("idempotency_key", strings.TrimSpace(idempotencyKey)))
	}
	statement, arguments, err := query.NewWorkspaceSelectBuilder(renderer, TableName, strings.TrimSpace(workspaceID)).
		Columns(columns...).Where(query.And(predicates...)).Limit(1).Build()
	if err != nil {
		return SucceededReceipt{}, false, err
	}
	return scanSucceededReceipt(queryer.QueryRowContext(ctx, statement, arguments...), withReference)
}

func UpdateSucceededResult(ctx context.Context, execer Execer, renderer ormdialect.Renderer, workspaceID, owner, kind, id string, previous, next json.RawMessage, updatedAt string) (bool, error) {
	if !json.Valid(previous) || !json.Valid(next) || strings.TrimSpace(updatedAt) == "" {
		return false, fmt.Errorf("shared operation result update is invalid")
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(renderer, TableName, strings.TrimSpace(workspaceID)).
		Set("result_json", string(next)).Set("updated_at", strings.TrimSpace(updatedAt)).
		Where(query.And(
			query.Equal("system_purpose", ""), query.Equal("owner", strings.TrimSpace(owner)),
			query.Equal("kind", strings.TrimSpace(kind)), query.Equal("id", strings.TrimSpace(id)),
			query.Equal("status", StatusSucceeded), query.Equal("result_json", string(previous)),
		)).Build()
	if err != nil {
		return false, err
	}
	result, err := execer.ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func InsertSucceeded(ctx context.Context, execer Execer, renderer ormdialect.Renderer, value SucceededRecord) error {
	for _, field := range []string{value.ID, value.WorkspaceID, value.Owner, value.Kind, value.ActionKey, value.ResourceType, value.IdempotencyKey, value.RequestFingerprint, value.RequestedBy, value.Reason, value.CompletedAt} {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("shared operation receipt fields are required")
		}
	}
	if !json.Valid(value.ResultJSON) {
		return fmt.Errorf("shared operation result is invalid")
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
	if !json.Valid(value.MetadataJSON) || !json.Valid(value.RelatedIDsJSON) || !json.Valid(value.EvidenceJSON) {
		return fmt.Errorf("shared operation metadata is invalid")
	}
	statement, arguments, err := query.NewWorkspaceInsertBuilder(renderer, TableName, strings.TrimSpace(value.WorkspaceID)).
		Columns(operationColumnsWithoutWorkspace()...).
		Values(operationRecordValues(
			value.ID, "", value.Owner, value.Kind, value.ActionKey, "", value.ResourceType, value.ResourceID,
			value.IdempotencyKey, value.RequestFingerprint, value.RequestedBy, value.Reason, value.Reference, StatusSucceeded, "",
			string(value.ResultJSON), string(value.MetadataJSON), "", "", "", string(value.RelatedIDsJSON), value.Correlation,
			string(value.EvidenceJSON), "", "", 0, "", value.CompletedAt, value.CompletedAt, value.CompletedAt, value.CompletedAt,
		)...).Build()
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, statement, arguments...)
	return err
}

func scanSucceededReceipt(row *sql.Row, withReference bool) (SucceededReceipt, bool, error) {
	var value SucceededReceipt
	var raw string
	var err error
	if withReference {
		err = row.Scan(&value.ID, &value.ResourceID, &value.RequestFingerprint, &value.RequestedBy, &value.Reference, &raw, &value.CreatedAt, &value.Status)
	} else {
		err = row.Scan(&value.ID, &value.ResourceID, &value.RequestFingerprint, &value.RequestedBy, &raw, &value.CreatedAt, &value.Status)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return SucceededReceipt{}, false, nil
	}
	if err != nil {
		return SucceededReceipt{}, false, err
	}
	value.ResultJSON = json.RawMessage(raw)
	if value.Status != StatusSucceeded || !json.Valid(value.ResultJSON) {
		return SucceededReceipt{}, false, fmt.Errorf("shared operation receipt invalid")
	}
	return value, true, nil
}

func operationColumnsWithoutWorkspace() []string {
	return append(append([]string{}, operationColumns[:1]...), operationColumns[2:]...)
}

func operationRecordValues(values ...any) []any { return values }
