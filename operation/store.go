package operation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
)

var operationColumns = []string{
	"id", "workspace_id", "system_purpose", "owner", "kind", "action_key", "parent_id", "resource_type", "resource_id",
	"idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "metadata_json",
	"error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "lease_owner", "lease_expires_at",
	"fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at",
}

var controlColumns = []string{
	"system_purpose", "control_kind", "owner", "state", "reason", "reference", "updated_by", "revision", "updated_at",
}

type SQLStore struct {
	database Database
	dialect  Dialect
}

func NewSQLStore(database Database, dialect Dialect) *SQLStore {
	return &SQLStore{database: database, dialect: dialect}
}

func (s *SQLStore) validate() error {
	if s == nil || s.database == nil || s.dialect == nil {
		return fmt.Errorf("shared Operations SQL store is incomplete")
	}
	return nil
}

func (s *SQLStore) Claim(ctx context.Context, command Command) (Receipt, bool, error) {
	if err := s.validate(); err != nil {
		return Receipt{}, false, err
	}
	if err := command.Validate(); err != nil {
		return Receipt{}, false, err
	}
	command = normalizedCommand(command)
	now := command.CreatedAt.UTC().Format(time.RFC3339Nano)
	value := ManagedOperation{Command: command, Status: StatusStarted, Metadata: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), UpdatedAt: command.CreatedAt.UTC()}
	statement, args, err := query.NewInsertBuilder(s.dialect, TableName).Columns(operationColumns...).Values(operationValues(value, now, "", "")...).Build()
	if err != nil {
		return Receipt{}, false, err
	}
	executor := ExecutorFromContext(ctx, s.database)
	if _, err := executor.ExecContext(ctx, statement, args...); err == nil {
		return Receipt{Command: command, Status: StatusStarted, Result: json.RawMessage(`{}`)}, true, nil
	} else if existing, found, readErr := s.getByKey(ctx, command.Scope, command.Owner, command.Kind, command.IdempotencyKey); readErr != nil {
		return Receipt{}, false, readErr
	} else if !found {
		return Receipt{}, false, err
	} else if existing.Command.RequestFingerprint != command.RequestFingerprint {
		return Receipt{}, false, ErrIdempotencyConflict
	} else {
		return Receipt{Command: existing.Command, Status: existing.Status, Result: append(json.RawMessage(nil), existing.Result...)}, false, nil
	}
}

func (s *SQLStore) Complete(ctx context.Context, completion Completion) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := completion.Validate(); err != nil {
		return err
	}
	predicate := and(scopePredicate(completion.Scope),
		query.Equal("id", strings.TrimSpace(completion.ID)), query.Equal("owner", strings.TrimSpace(completion.Owner)),
		query.Equal("kind", strings.TrimSpace(completion.Kind)), query.Equal("idempotency_key", strings.TrimSpace(completion.IdempotencyKey)),
		query.Equal("request_fingerprint", strings.TrimSpace(completion.RequestFingerprint)), query.Equal("status", StatusStarted),
	)
	completedAt := completion.CompletedAt.UTC().Format(time.RFC3339Nano)
	statement, args, err := operationUpdate(s, completion.Scope.WorkspaceID).
		Set("status", StatusSucceeded).Set("result_json", string(completion.Result)).Set("finished_at", completedAt).Set("updated_at", completedAt).
		Where(predicate).Build()
	if err != nil {
		return err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("shared Operation is not started")
	}
	return nil
}

func (s *SQLStore) Create(ctx context.Context, value ManagedOperation) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := value.Validate(); err != nil {
		return err
	}
	value.Command = normalizedCommand(value.Command)
	statement, args, err := query.NewInsertBuilder(s.dialect, TableName).Columns(operationColumns...).Values(operationValues(value, "", "", "")...).Build()
	if err != nil {
		return err
	}
	if _, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, args...); err != nil {
		if _, found, readErr := s.Get(ctx, managedIdentity(value)); readErr == nil && found {
			return ErrIdentityConflict
		}
		return err
	}
	return nil
}

func (s *SQLStore) Get(ctx context.Context, identity ManagedIdentity) (ManagedOperation, bool, error) {
	if err := s.validate(); err != nil {
		return ManagedOperation{}, false, err
	}
	if err := identity.Validate(); err != nil {
		return ManagedOperation{}, false, err
	}
	statement, args, err := operationSelect(s, identity.Scope.WorkspaceID).Columns(operationColumns...).Where(identityPredicate(identity)).Build()
	if err != nil {
		return ManagedOperation{}, false, err
	}
	value, err := scanOperation(ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedOperation{}, false, nil
	}
	return value, err == nil, err
}

func (s *SQLStore) List(ctx context.Context, filter ManagedQuery) ([]ManagedOperation, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := filter.Validate(); err != nil {
		return nil, err
	}
	predicate := and(scopePredicate(filter.Scope), query.Equal("owner", strings.TrimSpace(filter.Owner)), query.Equal("kind", strings.TrimSpace(filter.Kind)))
	if resourceID := strings.TrimSpace(filter.ResourceID); resourceID != "" {
		predicate = and(predicate, query.Equal("resource_id", resourceID))
	}
	if len(filter.Statuses) != 0 {
		values := make([]any, len(filter.Statuses))
		for index, status := range filter.Statuses {
			values[index] = strings.TrimSpace(status)
		}
		predicate = and(predicate, query.In("status", values...))
	}
	if value := strings.TrimSpace(filter.NextActionBefore); value != "" {
		predicate = and(predicate, query.NotEqual("next_action", ""), query.LessThanOrEqual("next_action", value))
	}
	if value := strings.TrimSpace(filter.LeaseExpiresBefore); value != "" {
		predicate = and(predicate, query.NotEqual("lease_expires_at", ""), query.LessThanOrEqual("lease_expires_at", value))
	}
	builder := operationSelect(s, filter.Scope.WorkspaceID).Columns(operationColumns...).Where(predicate).Limit(filter.Limit)
	if filter.OldestFirst {
		builder = builder.OrderBy(query.Ascending("next_action"), query.Ascending("created_at"), query.Ascending("id"))
	} else {
		builder = builder.OrderBy(query.Descending("created_at"), query.Descending("id"))
	}
	statement, args, err := builder.Build()
	if err != nil {
		return nil, err
	}
	rows, err := ExecutorFromContext(ctx, s.database).QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []ManagedOperation{}
	for rows.Next() {
		value, scanErr := scanOperation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) Transition(ctx context.Context, transition ManagedTransition) (ManagedOperation, bool, error) {
	if err := s.validate(); err != nil {
		return ManagedOperation{}, false, err
	}
	if err := transition.Validate(); err != nil {
		return ManagedOperation{}, false, err
	}
	predicate := and(identityPredicate(transition.Identity), query.Equal("status", strings.TrimSpace(transition.ExpectedStatus)))
	if transition.ExpectedFencingToken > 0 {
		predicate = and(predicate, query.Equal("lease_owner", strings.TrimSpace(transition.ExpectedLeaseOwner)), query.Equal("fencing_token", transition.ExpectedFencingToken))
	}
	builder := operationUpdate(s, transition.Identity.Scope.WorkspaceID).
		Set("status", strings.TrimSpace(transition.Status)).Set("metadata_json", string(transition.Metadata)).Set("result_json", string(transition.Result)).
		Set("error_code", strings.TrimSpace(transition.ErrorCode)).Set("next_action", strings.TrimSpace(transition.NextAction)).
		Set("updated_at", transition.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if transition.ClearLease {
		builder = builder.Set("lease_owner", "").Set("lease_expires_at", "")
	}
	statement, args, err := builder.Where(predicate).Build()
	if err != nil {
		return ManagedOperation{}, false, err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, args...)
	if err != nil {
		return ManagedOperation{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return ManagedOperation{}, false, err
	}
	return s.Get(ctx, transition.Identity)
}

func (s *SQLStore) ClaimManaged(ctx context.Context, claim ManagedClaim) (ManagedOperation, bool, error) {
	if err := s.validate(); err != nil {
		return ManagedOperation{}, false, err
	}
	if err := claim.Validate(); err != nil {
		return ManagedOperation{}, false, err
	}
	due := query.Or(
		query.And(query.Equal("status", strings.TrimSpace(claim.DueStatus)), query.NotEqual("next_action", ""), query.LessThanOrEqual("next_action", strings.TrimSpace(claim.Now))),
		query.And(query.Equal("status", strings.TrimSpace(claim.ReclaimStatus)), query.NotEqual("lease_expires_at", ""), query.LessThanOrEqual("lease_expires_at", strings.TrimSpace(claim.Now))),
	)
	predicate := and(identityPredicate(claim.Identity), due)
	statement, args, err := operationUpdate(s, claim.Identity.Scope.WorkspaceID).
		Set("status", strings.TrimSpace(claim.Status)).Set("lease_owner", strings.TrimSpace(claim.LeaseOwner)).
		Set("lease_expires_at", strings.TrimSpace(claim.LeaseExpiresAt)).SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", claim.UpdatedAt.UTC().Format(time.RFC3339Nano)).Where(predicate).Build()
	if err != nil {
		return ManagedOperation{}, false, err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, args...)
	if err != nil {
		return ManagedOperation{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return ManagedOperation{}, false, err
	}
	return s.Get(ctx, claim.Identity)
}

func (s *SQLStore) GetControl(ctx context.Context, purpose, kind, owner string) (Control, bool, error) {
	if err := s.validate(); err != nil {
		return Control{}, false, err
	}
	purpose, kind, owner = strings.TrimSpace(purpose), strings.TrimSpace(kind), strings.TrimSpace(owner)
	if purpose == "" || kind == "" || owner == "" {
		return Control{}, false, fmt.Errorf("operation control identity is required")
	}
	statement, args, err := query.NewSelectBuilder(s.dialect, ControlTableName).Columns(controlColumns...).Where(query.And(
		query.Equal("system_purpose", purpose), query.Equal("control_kind", kind), query.Equal("owner", owner),
	)).Build()
	if err != nil {
		return Control{}, false, err
	}
	value, err := scanControl(ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Control{}, false, nil
	}
	return value, err == nil, err
}

func (s *SQLStore) PutControl(ctx context.Context, value Control, expectedRevision int64) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := value.Validate(); err != nil {
		return false, err
	}
	if expectedRevision < 0 || value.Revision != expectedRevision+1 {
		return false, fmt.Errorf("operation control revision is invalid")
	}
	executor := ExecutorFromContext(ctx, s.database)
	if expectedRevision == 0 {
		statement, args, err := query.NewInsertBuilder(s.dialect, ControlTableName).Columns(controlColumns...).Values(controlValues(value)...).Build()
		if err != nil {
			return false, err
		}
		if _, err := executor.ExecContext(ctx, statement, args...); err != nil {
			if _, found, readErr := s.GetControl(ctx, value.SystemPurpose, value.Kind, value.Owner); readErr == nil && found {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	statement, args, err := query.NewUpdateBuilder(s.dialect, ControlTableName).
		Set("state", strings.TrimSpace(value.State)).Set("reason", value.Reason).Set("reference", value.Reference).
		Set("updated_by", strings.TrimSpace(value.UpdatedBy)).Set("revision", value.Revision).Set("updated_at", value.UpdatedAt.UTC().Format(time.RFC3339Nano)).
		Where(query.And(query.Equal("system_purpose", strings.TrimSpace(value.SystemPurpose)), query.Equal("control_kind", strings.TrimSpace(value.Kind)),
			query.Equal("owner", strings.TrimSpace(value.Owner)), query.Equal("revision", expectedRevision))).Build()
	if err != nil {
		return false, err
	}
	result, err := executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *SQLStore) getByKey(ctx context.Context, scope Scope, owner, kind, key string) (ManagedOperation, bool, error) {
	statement, args, err := operationSelect(s, scope.WorkspaceID).Columns(operationColumns...).Where(and(
		scopePredicate(scope), query.Equal("owner", strings.TrimSpace(owner)), query.Equal("kind", strings.TrimSpace(kind)), query.Equal("idempotency_key", strings.TrimSpace(key)),
	)).Limit(1).Build()
	if err != nil {
		return ManagedOperation{}, false, err
	}
	value, err := scanOperation(ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedOperation{}, false, nil
	}
	return value, err == nil, err
}

func normalizedCommand(value Command) Command {
	value.ID, value.Owner, value.Kind, value.ActionKey = strings.TrimSpace(value.ID), strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind), strings.TrimSpace(value.ActionKey)
	value.Scope.WorkspaceID, value.Scope.SystemPurpose = strings.TrimSpace(value.Scope.WorkspaceID), strings.TrimSpace(value.Scope.SystemPurpose)
	value.Scope.ResourceType, value.Scope.ResourceID = strings.TrimSpace(value.Scope.ResourceType), strings.TrimSpace(value.Scope.ResourceID)
	value.IdempotencyKey, value.RequestFingerprint = strings.TrimSpace(value.IdempotencyKey), strings.TrimSpace(value.RequestFingerprint)
	value.RequestedBy, value.Reason, value.Reference, value.StatusURL = strings.TrimSpace(value.RequestedBy), strings.TrimSpace(value.Reason), strings.TrimSpace(value.Reference), strings.TrimSpace(value.StatusURL)
	value.CreatedAt = value.CreatedAt.UTC()
	return value
}

func managedIdentity(value ManagedOperation) ManagedIdentity {
	return ManagedIdentity{ID: value.Command.ID, Scope: value.Command.Scope, Owner: value.Command.Owner, Kind: value.Command.Kind}
}

func identityPredicate(identity ManagedIdentity) query.Predicate {
	return and(scopePredicate(identity.Scope), query.Equal("id", strings.TrimSpace(identity.ID)), query.Equal("owner", strings.TrimSpace(identity.Owner)), query.Equal("kind", strings.TrimSpace(identity.Kind)))
}

func scopePredicate(scope Scope) query.Predicate {
	if workspaceID := strings.TrimSpace(scope.WorkspaceID); workspaceID != "" {
		return query.And(query.Equal("workspace_id", workspaceID), query.Equal("system_purpose", ""))
	}
	return query.And(query.Equal("workspace_id", ""), query.Equal("system_purpose", strings.TrimSpace(scope.SystemPurpose)))
}

func and(predicates ...query.Predicate) query.Predicate {
	values := make([]query.Predicate, 0, len(predicates))
	for _, predicate := range predicates {
		if predicate != nil {
			values = append(values, predicate)
		}
	}
	return query.And(values...)
}

func operationSelect(s *SQLStore, workspaceID string) *query.SelectBuilder {
	if strings.TrimSpace(workspaceID) != "" {
		return query.NewWorkspaceSelectBuilder(s.dialect, TableName, strings.TrimSpace(workspaceID))
	}
	return query.NewSelectBuilder(s.dialect, TableName)
}

func operationUpdate(s *SQLStore, workspaceID string) *query.UpdateBuilder {
	if strings.TrimSpace(workspaceID) != "" {
		return query.NewWorkspaceUpdateBuilder(s.dialect, TableName, strings.TrimSpace(workspaceID))
	}
	return query.NewUpdateBuilder(s.dialect, TableName)
}

func operationValues(value ManagedOperation, startedAt, finishedAt, expiresAt string) []any {
	command := value.Command
	if startedAt == "" && value.Status == StatusStarted {
		startedAt = command.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return []any{
		command.ID, command.Scope.WorkspaceID, command.Scope.SystemPurpose, command.Owner, command.Kind, command.ActionKey, "", command.Scope.ResourceType, command.Scope.ResourceID,
		command.IdempotencyKey, command.RequestFingerprint, command.RequestedBy, command.Reason, command.Reference, value.Status, command.StatusURL,
		string(value.Result), string(value.Metadata), value.ErrorCode, "", value.NextAction, `[]`, "", `[]`, value.LeaseOwner, value.LeaseExpiresAt,
		value.FencingToken, expiresAt, command.CreatedAt.UTC().Format(time.RFC3339Nano), startedAt, finishedAt, value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

type scanner interface{ Scan(...any) error }

func scanOperation(row scanner) (ManagedOperation, error) {
	var value ManagedOperation
	var workspaceID, systemPurpose, parentID, status, resultJSON, metadataJSON, failureClass, relatedIDs, correlation, evidence, expiresAt, createdAt, startedAt, finishedAt, updatedAt string
	command := &value.Command
	err := row.Scan(
		&command.ID, &workspaceID, &systemPurpose, &command.Owner, &command.Kind, &command.ActionKey, &parentID, &command.Scope.ResourceType, &command.Scope.ResourceID,
		&command.IdempotencyKey, &command.RequestFingerprint, &command.RequestedBy, &command.Reason, &command.Reference, &status, &command.StatusURL,
		&resultJSON, &metadataJSON, &value.ErrorCode, &failureClass, &value.NextAction, &relatedIDs, &correlation, &evidence, &value.LeaseOwner,
		&value.LeaseExpiresAt, &value.FencingToken, &expiresAt, &createdAt, &startedAt, &finishedAt, &updatedAt,
	)
	if err != nil {
		return value, err
	}
	command.Scope.WorkspaceID, command.Scope.SystemPurpose = workspaceID, systemPurpose
	value.Status, value.Result, value.Metadata = status, json.RawMessage(resultJSON), json.RawMessage(metadataJSON)
	command.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return value, err
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	return value, err
}

func controlValues(value Control) []any {
	return []any{strings.TrimSpace(value.SystemPurpose), strings.TrimSpace(value.Kind), strings.TrimSpace(value.Owner), strings.TrimSpace(value.State),
		value.Reason, value.Reference, strings.TrimSpace(value.UpdatedBy), value.Revision, value.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func scanControl(row scanner) (Control, error) {
	var value Control
	var updatedAt string
	err := row.Scan(&value.SystemPurpose, &value.Kind, &value.Owner, &value.State, &value.Reason, &value.Reference, &value.UpdatedBy, &value.Revision, &updatedAt)
	if err != nil {
		return value, err
	}
	value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	return value, err
}

var _ Store = (*SQLStore)(nil)
var _ ManagedStore = (*SQLStore)(nil)
var _ ControlStore = (*SQLStore)(nil)
