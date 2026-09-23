package operation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
)

var breakGlassColumns = []string{
	"id", "workspace_id", "state", "actor_id", "approver_ids_json", "reason", "incident_ref", "alert_target", "audit_event_id",
	"expires_at", "revision", "created_at", "updated_at", "revoked_at", "revoked_by", "revocation_note",
}

type BreakGlassGrant struct {
	ID              string
	WorkspaceID     string
	State           string
	ActorID         string
	ApproverIDsJSON json.RawMessage
	Reason          string
	IncidentRef     string
	AlertTarget     string
	AuditEventID    string
	ExpiresAt       string
	Revision        int64
	CreatedAt       string
	UpdatedAt       string
	RevokedAt       string
	RevokedBy       string
	RevocationNote  string
}

func (s *SQLStore) CountActiveBreakGlass(ctx context.Context, workspaceID, state, expiresAfter string) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	workspaceID, state, expiresAfter = strings.TrimSpace(workspaceID), strings.TrimSpace(state), strings.TrimSpace(expiresAfter)
	if workspaceID == "" || state == "" || expiresAfter == "" {
		return 0, fmt.Errorf("break-glass active scope is incomplete")
	}
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.dialect, BreakGlassTableName, workspaceID).
		Projections(query.Project(query.CountAll())).Where(query.And(query.Equal("state", state), query.GreaterThan("expires_at", expiresAfter))).Build()
	if err != nil {
		return 0, err
	}
	var count int64
	err = ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, arguments...).Scan(&count)
	return count, err
}

func (s *SQLStore) InsertBreakGlass(ctx context.Context, value BreakGlassGrant) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := value.validate(); err != nil {
		return false, err
	}
	statement, arguments, err := query.NewWorkspaceInsertBuilder(s.dialect, BreakGlassTableName, strings.TrimSpace(value.WorkspaceID)).
		Columns(breakGlassColumnsWithoutWorkspace()...).Values(breakGlassValuesWithoutWorkspace(value)...).Build()
	if err != nil {
		return false, err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *SQLStore) GetBreakGlass(ctx context.Context, id string) (BreakGlassGrant, bool, error) {
	if err := s.validate(); err != nil {
		return BreakGlassGrant{}, false, err
	}
	if id = strings.TrimSpace(id); id == "" {
		return BreakGlassGrant{}, false, fmt.Errorf("break-glass id is required")
	}
	statement, arguments, err := query.NewSelectBuilder(s.dialect, BreakGlassTableName).Columns(breakGlassColumns...).Where(query.Equal("id", id)).Limit(1).Build()
	if err != nil {
		return BreakGlassGrant{}, false, err
	}
	value, err := scanBreakGlass(ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return BreakGlassGrant{}, false, nil
	}
	return value, err == nil, err
}

func (s *SQLStore) ListBreakGlass(ctx context.Context, workspaceID string, limit int) ([]BreakGlassGrant, error) {
	return s.listBreakGlass(ctx, workspaceID, "", limit, false)
}

func (s *SQLStore) ListBreakGlassByActor(ctx context.Context, workspaceID, actorID string, limit int, forUpdate bool) ([]BreakGlassGrant, error) {
	if actorID = strings.TrimSpace(actorID); actorID == "" {
		return nil, fmt.Errorf("break-glass actor is required")
	}
	return s.listBreakGlass(ctx, workspaceID, actorID, limit, forUpdate)
}

func (s *SQLStore) listBreakGlass(ctx context.Context, workspaceID, actorID string, limit int, forUpdate bool) ([]BreakGlassGrant, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if workspaceID = strings.TrimSpace(workspaceID); workspaceID == "" {
		return nil, fmt.Errorf("break-glass workspace is required")
	}
	if limit <= 0 || limit > 10000 {
		limit = 100
	}
	predicate := query.Predicate(query.AlwaysTrue())
	if actorID != "" {
		predicate = query.Equal("actor_id", actorID)
	}
	builder := query.NewWorkspaceSelectBuilder(s.dialect, BreakGlassTableName, workspaceID).Columns(breakGlassColumns...).Where(predicate).
		OrderBy(query.Descending("created_at"), query.Descending("id")).Limit(limit)
	if forUpdate {
		builder.ForUpdate()
	}
	statement, arguments, err := builder.Build()
	if err != nil {
		return nil, err
	}
	rows, err := ExecutorFromContext(ctx, s.database).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []BreakGlassGrant{}
	for rows.Next() {
		value, scanErr := scanBreakGlass(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) RevokeBreakGlass(ctx context.Context, value BreakGlassGrant, expectedState string, expectedRevision int64) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := value.validate(); err != nil {
		return false, err
	}
	if strings.TrimSpace(expectedState) == "" || expectedRevision < 1 {
		return false, fmt.Errorf("break-glass expected state and revision are required")
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.dialect, BreakGlassTableName, value.WorkspaceID).
		Set("state", value.State).Set("revision", value.Revision).Set("updated_at", value.UpdatedAt).Set("revoked_at", value.RevokedAt).
		Set("revoked_by", value.RevokedBy).Set("revocation_note", value.RevocationNote).
		Where(query.And(query.Equal("id", value.ID), query.Equal("state", expectedState), query.Equal("revision", expectedRevision))).Build()
	if err != nil {
		return false, err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *SQLStore) EraseBreakGlass(ctx context.Context, workspaceID, id string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	workspaceID, id = strings.TrimSpace(workspaceID), strings.TrimSpace(id)
	if workspaceID == "" || id == "" {
		return false, fmt.Errorf("break-glass erasure identity is required")
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.dialect, BreakGlassTableName, workspaceID).
		Set("actor_id", "anonymous").Set("approver_ids_json", "[]").Set("reason", "").Set("incident_ref", "").Set("alert_target", "").
		Set("revocation_note", "").Set("revoked_by", "anonymous").Set("state", "revoked").Where(query.Equal("id", id)).Build()
	if err != nil {
		return false, err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (value BreakGlassGrant) validate() error {
	for _, required := range []string{value.ID, value.WorkspaceID, value.State, value.ActorID, value.IncidentRef, value.AlertTarget, value.AuditEventID, value.ExpiresAt, value.CreatedAt, value.UpdatedAt} {
		if strings.TrimSpace(required) == "" {
			return fmt.Errorf("break-glass required fields are missing")
		}
	}
	if value.Revision < 1 || !json.Valid(value.ApproverIDsJSON) {
		return fmt.Errorf("break-glass revision or approver JSON is invalid")
	}
	return nil
}

func breakGlassValues(value BreakGlassGrant) []any {
	return []any{value.ID, value.WorkspaceID, value.State, value.ActorID, string(value.ApproverIDsJSON), value.Reason, value.IncidentRef, value.AlertTarget,
		value.AuditEventID, value.ExpiresAt, value.Revision, value.CreatedAt, value.UpdatedAt, value.RevokedAt, value.RevokedBy, value.RevocationNote}
}

func breakGlassColumnsWithoutWorkspace() []string {
	return append(append([]string{}, breakGlassColumns[:1]...), breakGlassColumns[2:]...)
}

func breakGlassValuesWithoutWorkspace(value BreakGlassGrant) []any {
	values := breakGlassValues(value)
	return append(append([]any{}, values[:1]...), values[2:]...)
}

func scanBreakGlass(scanner scanner) (BreakGlassGrant, error) {
	var value BreakGlassGrant
	var approvers string
	err := scanner.Scan(&value.ID, &value.WorkspaceID, &value.State, &value.ActorID, &approvers, &value.Reason, &value.IncidentRef, &value.AlertTarget,
		&value.AuditEventID, &value.ExpiresAt, &value.Revision, &value.CreatedAt, &value.UpdatedAt, &value.RevokedAt, &value.RevokedBy, &value.RevocationNote)
	if err != nil {
		return value, err
	}
	value.ApproverIDsJSON = json.RawMessage(approvers)
	if !json.Valid(value.ApproverIDsJSON) {
		return BreakGlassGrant{}, fmt.Errorf("break-glass approver JSON is invalid")
	}
	return value, nil
}
