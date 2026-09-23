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

// Record is the complete persistence shape of one canonical _operations row.
// Business modules translate their domain models at this boundary; Foundation
// remains the only package that builds SQL against the shared table.
type Record struct {
	ID                 string
	WorkspaceID        string
	SystemPurpose      string
	Owner              string
	Kind               string
	ActionKey          string
	ParentID           string
	ResourceType       string
	ResourceID         string
	IdempotencyKey     string
	RequestFingerprint string
	RequestedBy        string
	Reason             string
	Reference          string
	Status             string
	StatusURL          string
	ResultJSON         json.RawMessage
	MetadataJSON       json.RawMessage
	ErrorCode          string
	FailureClass       string
	NextAction         string
	RelatedIDsJSON     json.RawMessage
	Correlation        string
	EvidenceJSON       json.RawMessage
	LeaseOwner         string
	LeaseExpiresAt     string
	FencingToken       int64
	ExpiresAt          string
	CreatedAt          string
	StartedAt          string
	FinishedAt         string
	UpdatedAt          string
}

type RecordFilter struct {
	AllScopes              bool
	WorkspaceID            string
	SystemPurpose          string
	ID                     string
	Owner                  string
	Kind                   string
	ActionKey              string
	IdempotencyKey         string
	RequestFingerprint     string
	Reference              string
	Status                 string
	Statuses               []string
	FailureClass           string
	ParentID               string
	ResourceType           string
	ResourceID             string
	RequestedBy            string
	Correlation            string
	ResultJSON             json.RawMessage
	MetadataJSON           json.RawMessage
	CreatedFrom            string
	CreatedTo              string
	Search                 string
	LeaseOwner             string
	FencingToken           *int64
	LeaseExpiresAtOrBefore string
	LeaseExpiresAfter      string
	ReclaimableStatus      string
	ExpiredLeaseStatus     string
	Limit                  int
}

type RecordChanges struct {
	ID                    *string
	RequestFingerprint    *string
	RequestedBy           *string
	Status                *string
	Reason                *string
	Reference             *string
	ResultJSON            *json.RawMessage
	MetadataJSON          *json.RawMessage
	ErrorCode             *string
	FailureClass          *string
	NextAction            *string
	RelatedIDsJSON        *json.RawMessage
	Correlation           *string
	EvidenceJSON          *json.RawMessage
	LeaseOwner            *string
	LeaseExpiresAt        *string
	FencingToken          *int64
	IncrementFencingToken bool
	ExpiresAt             *string
	StartedAt             *string
	FinishedAt            *string
	UpdatedAt             *string
}

type RecordSummary struct {
	Statuses       map[string]int
	FailureClasses map[string]int
}

type RecordPage struct {
	Items   []Record
	Count   int
	Summary RecordSummary
}

func (s *SQLStore) SummarizeRecords(ctx context.Context, filter RecordFilter) (RecordSummary, error) {
	if err := s.validate(); err != nil {
		return RecordSummary{}, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return RecordSummary{}, err
	}
	statement, arguments, err := recordSelect(s, filter.WorkspaceID).
		Projections(query.Project(query.Column("status")), query.Project(query.CountAll())).
		Where(predicate).GroupBy(query.Column("status")).Build()
	if err != nil {
		return RecordSummary{}, err
	}
	rows, err := ExecutorFromContext(ctx, s.database).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return RecordSummary{}, err
	}
	defer rows.Close()
	summary := RecordSummary{Statuses: map[string]int{}, FailureClasses: map[string]int{}}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return RecordSummary{}, err
		}
		summary.Statuses[status] += count
	}
	return summary, rows.Err()
}

type LeaseCounts struct {
	Live    int64
	Expired int64
}

func (s *SQLStore) InsertRecord(ctx context.Context, value Record) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := value.validate(); err != nil {
		return false, err
	}
	var builder *query.InsertBuilder
	if strings.TrimSpace(value.WorkspaceID) != "" {
		builder = query.NewWorkspaceInsertBuilder(s.dialect, TableName, strings.TrimSpace(value.WorkspaceID)).
			Columns(operationColumnsWithoutWorkspace()...).Values(recordValuesWithoutWorkspace(value)...)
	} else {
		builder = query.NewInsertBuilder(s.dialect, TableName).Columns(operationColumns...).Values(recordValues(value)...)
	}
	statement, arguments, err := builder.Build()
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

func (s *SQLStore) GetRecord(ctx context.Context, filter RecordFilter) (Record, bool, error) {
	if err := s.validate(); err != nil {
		return Record{}, false, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return Record{}, false, err
	}
	if strings.TrimSpace(filter.ID) == "" && (strings.TrimSpace(filter.Kind) == "" || strings.TrimSpace(filter.IdempotencyKey) == "") {
		return Record{}, false, fmt.Errorf("operation record lookup requires id or kind and idempotency key")
	}
	statement, arguments, err := recordSelect(s, filter.WorkspaceID).Columns(operationColumns...).Where(predicate).Limit(1).Build()
	if err != nil {
		return Record{}, false, err
	}
	value, err := scanRecord(ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	return value, err == nil, err
}

func (s *SQLStore) ListRecords(ctx context.Context, filter RecordFilter) ([]Record, error) {
	page, err := s.SearchRecords(ctx, filter, false)
	return page.Items, err
}

func (s *SQLStore) SearchRecords(ctx context.Context, filter RecordFilter, summarize bool) (RecordPage, error) {
	if err := s.validate(); err != nil {
		return RecordPage{}, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return RecordPage{}, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	executor := ExecutorFromContext(ctx, s.database)
	page := RecordPage{Summary: RecordSummary{Statuses: map[string]int{}, FailureClasses: map[string]int{}}}
	if summarize {
		statement, arguments, buildErr := recordSelect(s, filter.WorkspaceID).Projections(query.Project(query.CountAll())).Where(predicate).Build()
		if buildErr != nil {
			return RecordPage{}, buildErr
		}
		if err := executor.QueryRowContext(ctx, statement, arguments...).Scan(&page.Count); err != nil {
			return RecordPage{}, err
		}
		statement, arguments, buildErr = recordSelect(s, filter.WorkspaceID).
			Projections(query.Project(query.Column("status")), query.Project(query.Column("failure_class")), query.Project(query.CountAll())).
			Where(predicate).GroupBy(query.Column("status"), query.Column("failure_class")).Build()
		if buildErr != nil {
			return RecordPage{}, buildErr
		}
		rows, queryErr := executor.QueryContext(ctx, statement, arguments...)
		if queryErr != nil {
			return RecordPage{}, queryErr
		}
		for rows.Next() {
			var status, failureClass string
			var count int
			if err := rows.Scan(&status, &failureClass, &count); err != nil {
				_ = rows.Close()
				return RecordPage{}, err
			}
			page.Summary.Statuses[status] += count
			page.Summary.FailureClasses[failureClass] += count
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return RecordPage{}, err
		}
		_ = rows.Close()
	}
	statement, arguments, err := recordSelect(s, filter.WorkspaceID).Columns(operationColumns...).Where(predicate).
		OrderBy(query.Descending("created_at"), query.Descending("id")).Limit(limit).Build()
	if err != nil {
		return RecordPage{}, err
	}
	rows, err := executor.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return RecordPage{}, err
	}
	defer rows.Close()
	page.Items = []Record{}
	for rows.Next() {
		value, scanErr := scanRecord(rows)
		if scanErr != nil {
			return RecordPage{}, scanErr
		}
		page.Items = append(page.Items, value)
	}
	if err := rows.Err(); err != nil {
		return RecordPage{}, err
	}
	if !summarize {
		page.Count = len(page.Items)
	}
	return page, nil
}

func (s *SQLStore) UpdateRecord(ctx context.Context, value Record, expectedStatus string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := value.validate(); err != nil {
		return false, err
	}
	if strings.TrimSpace(expectedStatus) == "" {
		return false, fmt.Errorf("operation record expected status is required")
	}
	predicate, err := recordPredicate(RecordFilter{
		WorkspaceID: value.WorkspaceID, SystemPurpose: value.SystemPurpose, ID: value.ID,
		Owner: value.Owner, Kind: value.Kind, Status: expectedStatus,
	})
	if err != nil {
		return false, err
	}
	statement, arguments, err := recordUpdate(s, value.WorkspaceID).
		Set("status", strings.TrimSpace(value.Status)).Set("reason", value.Reason).Set("reference", value.Reference).
		Set("started_at", value.StartedAt).Set("finished_at", value.FinishedAt).Set("updated_at", value.UpdatedAt).
		Set("result_json", string(value.ResultJSON)).Set("metadata_json", string(value.MetadataJSON)).Set("error_code", value.ErrorCode).
		Set("failure_class", value.FailureClass).Set("next_action", value.NextAction).Set("related_ids_json", string(value.RelatedIDsJSON)).
		Set("correlation", value.Correlation).Set("evidence_json", string(value.EvidenceJSON)).Set("lease_owner", value.LeaseOwner).
		Set("lease_expires_at", value.LeaseExpiresAt).Set("fencing_token", value.FencingToken).Set("expires_at", value.ExpiresAt).
		Where(predicate).Build()
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

func (s *SQLStore) PatchRecord(ctx context.Context, filter RecordFilter, changes RecordChanges) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(filter.ID) == "" {
		return false, fmt.Errorf("operation record patch requires id")
	}
	builder := recordUpdate(s, filter.WorkspaceID)
	assignments := 0
	setString := func(column string, value *string) {
		if value != nil {
			builder = builder.Set(column, *value)
			assignments++
		}
	}
	setJSON := func(column string, value *json.RawMessage) error {
		if value == nil {
			return nil
		}
		if !json.Valid(*value) {
			return fmt.Errorf("operation record patch JSON is invalid")
		}
		builder = builder.Set(column, string(*value))
		assignments++
		return nil
	}
	for _, required := range []struct {
		name  string
		value *string
	}{
		{name: "id", value: changes.ID},
		{name: "request fingerprint", value: changes.RequestFingerprint},
		{name: "requested by", value: changes.RequestedBy},
	} {
		if required.value != nil && strings.TrimSpace(*required.value) == "" {
			return false, fmt.Errorf("operation record patch %s is required", required.name)
		}
	}
	setString("id", changes.ID)
	setString("request_fingerprint", changes.RequestFingerprint)
	setString("requested_by", changes.RequestedBy)
	setString("status", changes.Status)
	setString("reason", changes.Reason)
	setString("reference", changes.Reference)
	if err := setJSON("result_json", changes.ResultJSON); err != nil {
		return false, err
	}
	if err := setJSON("metadata_json", changes.MetadataJSON); err != nil {
		return false, err
	}
	setString("error_code", changes.ErrorCode)
	setString("failure_class", changes.FailureClass)
	setString("next_action", changes.NextAction)
	if err := setJSON("related_ids_json", changes.RelatedIDsJSON); err != nil {
		return false, err
	}
	setString("correlation", changes.Correlation)
	if err := setJSON("evidence_json", changes.EvidenceJSON); err != nil {
		return false, err
	}
	setString("lease_owner", changes.LeaseOwner)
	setString("lease_expires_at", changes.LeaseExpiresAt)
	if changes.FencingToken != nil {
		builder = builder.Set("fencing_token", *changes.FencingToken)
		assignments++
	}
	if changes.IncrementFencingToken {
		if changes.FencingToken != nil {
			return false, fmt.Errorf("operation record patch cannot set and increment fencing token")
		}
		builder = builder.SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1)))
		assignments++
	}
	setString("expires_at", changes.ExpiresAt)
	setString("started_at", changes.StartedAt)
	setString("finished_at", changes.FinishedAt)
	setString("updated_at", changes.UpdatedAt)
	if assignments == 0 {
		return false, fmt.Errorf("operation record patch requires changes")
	}
	statement, arguments, err := builder.Where(predicate).Build()
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

func (s *SQLStore) DeleteRecords(ctx context.Context, filter RecordFilter) (int64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return 0, err
	}
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

func (s *SQLStore) CountLeases(ctx context.Context, filter RecordFilter, instanceID, now string) (LeaseCounts, error) {
	if err := s.validate(); err != nil {
		return LeaseCounts{}, err
	}
	predicate, err := recordPredicate(filter)
	if err != nil {
		return LeaseCounts{}, err
	}
	predicate = and(predicate, query.NotEqual("lease_owner", ""))
	if instanceID = strings.TrimSpace(instanceID); instanceID != "" {
		predicate = and(predicate, query.Or(query.Equal("lease_owner", instanceID), query.Like("lease_owner", instanceID+":%"), query.Like("lease_owner", instanceID+"-%")))
	}
	now = strings.TrimSpace(now)
	if now == "" {
		return LeaseCounts{}, fmt.Errorf("operation lease count time is required")
	}
	live := query.Coalesce(query.Sum(query.CaseWhen(query.GreaterThan("lease_expires_at", now), 1).Else(0)), query.Value(0))
	expired := query.Coalesce(query.Sum(query.CaseWhen(query.And(query.NotEqual("lease_expires_at", ""), query.LessThanOrEqual("lease_expires_at", now)), 1).Else(0)), query.Value(0))
	statement, arguments, err := recordSelect(s, filter.WorkspaceID).Projections(query.Project(live), query.Project(expired)).Where(predicate).Build()
	if err != nil {
		return LeaseCounts{}, err
	}
	var liveValue, expiredValue sql.NullInt64
	if err := ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, arguments...).Scan(&liveValue, &expiredValue); err != nil {
		return LeaseCounts{}, err
	}
	return LeaseCounts{Live: liveValue.Int64, Expired: expiredValue.Int64}, nil
}

func (s *SQLStore) ListControls(ctx context.Context, purpose, kind string, limit int) ([]Control, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	purpose, kind = strings.TrimSpace(purpose), strings.TrimSpace(kind)
	if purpose == "" {
		return nil, fmt.Errorf("operation control purpose is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	predicate := query.Predicate(query.Equal("system_purpose", purpose))
	if kind != "" {
		predicate = query.And(predicate, query.Equal("control_kind", kind))
	}
	statement, arguments, err := query.NewSelectBuilder(s.dialect, ControlTableName).Columns(controlColumns...).Where(predicate).
		OrderBy(query.Ascending("control_kind"), query.Ascending("owner")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := ExecutorFromContext(ctx, s.database).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Control{}
	for rows.Next() {
		value, scanErr := scanControl(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (value Record) validate() error {
	for _, field := range []string{value.ID, value.Owner, value.Kind, value.ActionKey, value.ResourceType, value.IdempotencyKey, value.Status, value.CreatedAt, value.UpdatedAt} {
		if strings.TrimSpace(field) == "" {
			return fmt.Errorf("operation record required fields are missing")
		}
	}
	workspace, system := strings.TrimSpace(value.WorkspaceID) != "", strings.TrimSpace(value.SystemPurpose) != ""
	if workspace == system {
		return fmt.Errorf("operation record requires exactly one workspace or system scope")
	}
	for _, raw := range []json.RawMessage{value.ResultJSON, value.MetadataJSON, value.RelatedIDsJSON, value.EvidenceJSON} {
		if !json.Valid(raw) {
			return fmt.Errorf("operation record JSON is invalid")
		}
	}
	return nil
}

func recordPredicate(filter RecordFilter) (query.Predicate, error) {
	workspace, system := strings.TrimSpace(filter.WorkspaceID), strings.TrimSpace(filter.SystemPurpose)
	if filter.AllScopes {
		if workspace != "" || system != "" {
			return nil, fmt.Errorf("operation record all-scopes filter cannot include a workspace or system scope")
		}
	} else if (workspace == "") == (system == "") {
		return nil, fmt.Errorf("operation record filter requires exactly one workspace or system scope")
	}
	var predicate query.Predicate
	if filter.AllScopes {
		predicate = query.AlwaysTrue()
	} else if system != "" {
		predicate = query.And(query.Equal("workspace_id", ""), query.Equal("system_purpose", system))
	} else {
		predicate = query.Equal("system_purpose", "")
	}
	addExact := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			predicate = and(predicate, query.Equal(column, value))
		}
	}
	addExact("id", filter.ID)
	addExact("owner", filter.Owner)
	addExact("kind", filter.Kind)
	addExact("action_key", filter.ActionKey)
	addExact("idempotency_key", filter.IdempotencyKey)
	addExact("request_fingerprint", filter.RequestFingerprint)
	addExact("reference", filter.Reference)
	addExact("status", filter.Status)
	addExact("failure_class", filter.FailureClass)
	addExact("parent_id", filter.ParentID)
	addExact("resource_type", filter.ResourceType)
	addExact("resource_id", filter.ResourceID)
	addExact("requested_by", filter.RequestedBy)
	addExact("correlation", filter.Correlation)
	addExact("lease_owner", filter.LeaseOwner)
	addJSONExact := func(column string, value json.RawMessage) error {
		if value == nil {
			return nil
		}
		if !json.Valid(value) {
			return fmt.Errorf("operation record filter %s JSON is invalid", column)
		}
		predicate = and(predicate, query.Equal(column, string(value)))
		return nil
	}
	if err := addJSONExact("result_json", filter.ResultJSON); err != nil {
		return nil, err
	}
	if err := addJSONExact("metadata_json", filter.MetadataJSON); err != nil {
		return nil, err
	}
	if len(filter.Statuses) != 0 {
		values := make([]any, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			if status = strings.TrimSpace(status); status != "" {
				values = append(values, status)
			}
		}
		if len(values) != 0 {
			predicate = and(predicate, query.In("status", values...))
		}
	}
	if filter.FencingToken != nil {
		predicate = and(predicate, query.Equal("fencing_token", *filter.FencingToken))
	}
	if value := strings.TrimSpace(filter.LeaseExpiresAtOrBefore); value != "" && strings.TrimSpace(filter.ReclaimableStatus) == "" && strings.TrimSpace(filter.ExpiredLeaseStatus) == "" {
		predicate = and(predicate, query.LessThanOrEqual("lease_expires_at", value))
	}
	if value := strings.TrimSpace(filter.LeaseExpiresAfter); value != "" {
		predicate = and(predicate, query.GreaterThan("lease_expires_at", value))
	}
	if reclaimable, leased := strings.TrimSpace(filter.ReclaimableStatus), strings.TrimSpace(filter.ExpiredLeaseStatus); reclaimable != "" || leased != "" {
		expiredAt := strings.TrimSpace(filter.LeaseExpiresAtOrBefore)
		if reclaimable == "" || leased == "" || expiredAt == "" {
			return nil, fmt.Errorf("operation record reclaim filter requires both statuses and lease expiry")
		}
		predicate = and(predicate, query.Or(
			query.Equal("status", reclaimable),
			query.And(query.Equal("status", leased), query.LessThanOrEqual("lease_expires_at", expiredAt)),
		))
	}
	if value := strings.TrimSpace(filter.CreatedFrom); value != "" {
		predicate = and(predicate, query.GreaterThanOrEqual("created_at", value))
	}
	if value := strings.TrimSpace(filter.CreatedTo); value != "" {
		predicate = and(predicate, query.LessThanOrEqual("created_at", value))
	}
	if value := strings.ToLower(strings.TrimSpace(filter.Search)); value != "" {
		columns := []string{"id", "owner", "kind", "parent_id", "resource_type", "resource_id", "requested_by", "reason", "correlation", "error_code", "next_action"}
		terms := make([]query.Predicate, 0, len(columns))
		for _, column := range columns {
			terms = append(terms, query.LikeValue(query.Lower(query.Column(column)), "%"+value+"%"))
		}
		predicate = and(predicate, query.Or(terms...))
	}
	return predicate, nil
}

func recordSelect(s *SQLStore, workspaceID string) *query.SelectBuilder {
	if strings.TrimSpace(workspaceID) != "" {
		return query.NewWorkspaceSelectBuilder(s.dialect, TableName, strings.TrimSpace(workspaceID))
	}
	return query.NewSelectBuilder(s.dialect, TableName)
}

func recordUpdate(s *SQLStore, workspaceID string) *query.UpdateBuilder {
	if strings.TrimSpace(workspaceID) != "" {
		return query.NewWorkspaceUpdateBuilder(s.dialect, TableName, strings.TrimSpace(workspaceID))
	}
	return query.NewUpdateBuilder(s.dialect, TableName)
}

func recordValues(value Record) []any {
	return []any{
		value.ID, value.WorkspaceID, value.SystemPurpose, value.Owner, value.Kind, value.ActionKey, value.ParentID, value.ResourceType, value.ResourceID,
		value.IdempotencyKey, value.RequestFingerprint, value.RequestedBy, value.Reason, value.Reference, value.Status, value.StatusURL,
		string(value.ResultJSON), string(value.MetadataJSON), value.ErrorCode, value.FailureClass, value.NextAction, string(value.RelatedIDsJSON),
		value.Correlation, string(value.EvidenceJSON), value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ExpiresAt,
		value.CreatedAt, value.StartedAt, value.FinishedAt, value.UpdatedAt,
	}
}

func recordValuesWithoutWorkspace(value Record) []any {
	values := recordValues(value)
	return append(append([]any{}, values[:1]...), values[2:]...)
}

func scanRecord(scanner scanner) (Record, error) {
	var value Record
	var resultJSON, metadataJSON, relatedIDsJSON, evidenceJSON string
	err := scanner.Scan(
		&value.ID, &value.WorkspaceID, &value.SystemPurpose, &value.Owner, &value.Kind, &value.ActionKey, &value.ParentID, &value.ResourceType, &value.ResourceID,
		&value.IdempotencyKey, &value.RequestFingerprint, &value.RequestedBy, &value.Reason, &value.Reference, &value.Status, &value.StatusURL,
		&resultJSON, &metadataJSON, &value.ErrorCode, &value.FailureClass, &value.NextAction, &relatedIDsJSON, &value.Correlation, &evidenceJSON,
		&value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.ExpiresAt, &value.CreatedAt, &value.StartedAt, &value.FinishedAt, &value.UpdatedAt,
	)
	if err != nil {
		return value, err
	}
	value.ResultJSON, value.MetadataJSON = json.RawMessage(resultJSON), json.RawMessage(metadataJSON)
	value.RelatedIDsJSON, value.EvidenceJSON = json.RawMessage(relatedIDsJSON), json.RawMessage(evidenceJSON)
	for _, raw := range []json.RawMessage{value.ResultJSON, value.MetadataJSON, value.RelatedIDsJSON, value.EvidenceJSON} {
		if !json.Valid(raw) {
			return Record{}, fmt.Errorf("operation record JSON is invalid")
		}
	}
	return value, nil
}
