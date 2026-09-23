package operation

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
)

type SubjectResource struct {
	Type string
	ID   string
}

type RecordErasure struct {
	ID             string
	IdempotencyKey string
}

func (s *SQLStore) ListSubjectRecords(ctx context.Context, workspaceID, subjectID string, resources []SubjectResource, limit int, forUpdate bool) ([]Record, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	workspaceID, subjectID = strings.TrimSpace(workspaceID), strings.TrimSpace(subjectID)
	if workspaceID == "" || subjectID == "" {
		return nil, fmt.Errorf("operation subject scope is incomplete")
	}
	if limit <= 0 || limit > 10001 {
		limit = 10001
	}
	resourcePredicates := []query.Predicate{query.AlwaysFalse()}
	for _, resource := range resources {
		if resource.Type = strings.TrimSpace(resource.Type); resource.Type != "" {
			if resource.ID = strings.TrimSpace(resource.ID); resource.ID != "" {
				resourcePredicates = append(resourcePredicates, query.And(query.Equal("resource_type", resource.Type), query.Equal("resource_id", resource.ID)))
			}
		}
	}
	predicate := query.Or(
		query.Equal("requested_by", subjectID),
		query.And(query.In("owner", "action", "record"), query.Or(resourcePredicates...)),
	)
	builder := recordSelect(s, workspaceID).Columns(operationColumns...).Where(predicate).OrderBy(query.Ascending("id")).Limit(limit)
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
	values := []Record{}
	for rows.Next() {
		value, scanErr := scanRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) FenceRecordsForErasure(ctx context.Context, workspaceID string, ids []string) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	predicate, err := recordIDsPredicate(workspaceID, ids)
	if err != nil {
		return 0, err
	}
	statement, arguments, err := recordUpdate(s, workspaceID).
		Set("status", "failed").Set("lease_owner", "").Set("lease_expires_at", "").
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).Where(predicate).Build()
	if err != nil {
		return 0, err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *SQLStore) EraseRecords(ctx context.Context, workspaceID string, values []RecordErasure) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return 0, fmt.Errorf("operation erasure workspace is required")
	}
	var total int64
	for _, value := range values {
		value.ID, value.IdempotencyKey = strings.TrimSpace(value.ID), strings.TrimSpace(value.IdempotencyKey)
		if value.ID == "" || value.IdempotencyKey == "" {
			return total, fmt.Errorf("operation erasure identity is incomplete")
		}
		statement, arguments, err := recordUpdate(s, workspaceID).
			Set("requested_by", "anonymous").Set("reason", "").Set("reference", "").Set("result_json", "{}").Set("metadata_json", "{}").
			Set("related_ids_json", "[]").Set("evidence_json", "[]").Set("next_action", "").Set("request_fingerprint", "").
			Set("idempotency_key", value.IdempotencyKey).Set("status", "failed").Set("error_code", "runtime.subject_erased").
			Set("lease_owner", "").Set("lease_expires_at", "").Where(query.Equal("id", value.ID)).Build()
		if err != nil {
			return total, err
		}
		result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, arguments...)
		if err != nil {
			return total, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return total, err
		}
		total += rows
	}
	return total, nil
}

func (s *SQLStore) ListExpiredRecords(ctx context.Context, filter RecordFilter, now, protectedStatus string, limit int) ([]Record, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return nil, err
	}
	now, protectedStatus = strings.TrimSpace(now), strings.TrimSpace(protectedStatus)
	if now == "" || protectedStatus == "" {
		return nil, fmt.Errorf("operation expiry boundary and protected status are required")
	}
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	predicate = and(predicate, query.NotEqual("expires_at", ""), query.LessThanOrEqual("expires_at", now), query.NotEqual("status", protectedStatus))
	statement, arguments, err := recordSelect(s, filter.WorkspaceID).Columns(operationColumns...).Where(predicate).
		OrderBy(query.Ascending("expires_at"), query.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := ExecutorFromContext(ctx, s.database).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Record{}
	for rows.Next() {
		value, scanErr := scanRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) DeleteExpiredRecords(ctx context.Context, filter RecordFilter, ids []string, now, protectedStatus string) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return 0, err
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			values = append(values, id)
		}
	}
	if len(values) == 0 {
		return 0, nil
	}
	now, protectedStatus = strings.TrimSpace(now), strings.TrimSpace(protectedStatus)
	if now == "" || protectedStatus == "" {
		return 0, fmt.Errorf("operation expiry boundary and protected status are required")
	}
	predicate = and(predicate, query.In("id", values...), query.NotEqual("expires_at", ""), query.LessThanOrEqual("expires_at", now), query.NotEqual("status", protectedStatus))
	var builder *query.DeleteBuilder
	if strings.TrimSpace(filter.WorkspaceID) != "" {
		builder = query.NewWorkspaceDeleteBuilder(s.dialect, TableName, strings.TrimSpace(filter.WorkspaceID))
	} else {
		builder = query.NewDeleteBuilder(s.dialect, TableName)
	}
	statement, arguments, err := builder.Where(predicate).Build()
	if err != nil {
		return 0, err
	}
	result, err := ExecutorFromContext(ctx, s.database).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func recordIDsPredicate(workspaceID string, ids []string) (query.Predicate, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, fmt.Errorf("operation workspace is required")
	}
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			values = append(values, id)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("operation ids are required")
	}
	return query.In("id", values...), nil
}
