// Package artifact owns the canonical shared Artifact schema and SQL store.
package artifact

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	ormschema "github.com/domainry/domainry-orm/schema"
)

const TableName = "_artifacts"
const BindingTableName = "_artifact_bindings"

type SQLStore struct {
	db       Database
	renderer Dialect
}

func NewSQLStore(db Database, renderer Dialect) *SQLStore {
	return &SQLStore{db: db, renderer: renderer}
}

func (s *SQLStore) executor(ctx context.Context) Executor {
	return ExecutorFromContext(ctx, s.db)
}

func (s *SQLStore) Register(ctx context.Context, value Artifact) (Artifact, bool, error) {
	value = normalizeArtifact(value)
	if err := validateArtifact(value); err != nil {
		return Artifact{}, false, err
	}
	statement, arguments, err := query.NewWorkspaceInsertBuilder(s.renderer, TableName, value.WorkspaceID).
		Columns(artifactColumns()[1:]...).Values(artifactValues(value)[1:]...).Build()
	if err != nil {
		return Artifact{}, false, err
	}
	if _, err = s.executor(ctx).ExecContext(ctx, statement, arguments...); err == nil {
		return value, true, nil
	}
	existing, found, readErr := s.find(ctx, value.WorkspaceID, query.And(query.Equal("owner", value.Owner), query.Equal("kind", value.Kind), query.Equal("idempotency_key", value.IdempotencyKey)))
	if readErr != nil {
		return Artifact{}, false, readErr
	}
	if !found || !sameArtifact(existing, value) {
		return Artifact{}, false, ErrIdentityConflict
	}
	return existing, false, nil
}

func (s *SQLStore) ByID(ctx context.Context, workspaceID, id string) (Artifact, bool, error) {
	return s.find(ctx, workspaceID, query.Equal("id", strings.TrimSpace(id)))
}

func (s *SQLStore) ByDownloadTokenHash(ctx context.Context, workspaceID, token string) (Artifact, bool, error) {
	return s.find(ctx, workspaceID, query.Equal("download_token_sha256", strings.TrimSpace(token)))
}

func (s *SQLStore) Transition(ctx context.Context, workspaceID, id string, expected, next Status, scan ScanStatus, at time.Time) (bool, error) {
	workspaceID, err := normalizeWorkspace(workspaceID)
	if err != nil || strings.TrimSpace(id) == "" || !validStatus(expected) || !validStatus(next) || !validScan(scan) || at.IsZero() {
		return false, fmt.Errorf("artifact transition is invalid")
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.renderer, TableName, workspaceID).
		Set("status", string(next)).Set("scan_status", string(scan)).Set("updated_at", at.UTC().Format(time.RFC3339Nano)).
		Where(query.And(query.Equal("id", strings.TrimSpace(id)), query.Equal("status", string(expected)))).Build()
	if err != nil {
		return false, err
	}
	result, err := s.executor(ctx).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *SQLStore) Bind(ctx context.Context, value Binding) (Binding, bool, error) {
	value = normalizeBinding(value)
	if err := validateBinding(value); err != nil {
		return Binding{}, false, err
	}
	if _, found, err := s.ByID(ctx, value.WorkspaceID, value.ArtifactID); err != nil {
		return Binding{}, false, err
	} else if !found {
		return Binding{}, false, fmt.Errorf("artifact binding references an unknown artifact")
	}
	statement, arguments, err := query.NewWorkspaceInsertBuilder(s.renderer, BindingTableName, value.WorkspaceID).
		Columns(bindingColumns()[1:]...).Values(bindingValues(value)[1:]...).Build()
	if err != nil {
		return Binding{}, false, err
	}
	if _, err = s.executor(ctx).ExecContext(ctx, statement, arguments...); err == nil {
		return value, true, nil
	}
	existing, found, readErr := s.bindingByIdentity(ctx, value)
	if readErr != nil {
		return Binding{}, false, readErr
	}
	if !found || existing.ArtifactID != value.ArtifactID {
		return Binding{}, false, ErrBindingConflict
	}
	return existing, false, nil
}

func (s *SQLStore) Bindings(ctx context.Context, workspaceID, artifactID string) ([]Binding, error) {
	workspaceID, err := normalizeWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.renderer, BindingTableName, workspaceID).
		Columns(bindingColumns()...).Where(query.Equal("artifact_id", strings.TrimSpace(artifactID))).
		OrderBy(query.Ascending("created_at"), query.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.executor(ctx).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Binding{}
	for rows.Next() {
		value, scanErr := scanBinding(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *SQLStore) List(ctx context.Context, workspaceID string, value Query) ([]Artifact, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID != "" {
		var err error
		workspaceID, err = normalizeWorkspace(workspaceID)
		if err != nil {
			return nil, err
		}
	}
	limit := value.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	predicates := artifactPredicates(value)
	if value.Binding != nil {
		binding := normalizeBindingQuery(*value.Binding)
		if !BindingKindRegistered(binding.Kind) {
			return nil, fmt.Errorf("artifact binding query kind is invalid")
		}
		ids, err := s.bindingArtifactIDs(ctx, workspaceID, binding, limit)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []Artifact{}, nil
		}
		values := make([]any, len(ids))
		for i := range ids {
			values[i] = ids[i]
		}
		predicates = append(predicates, query.In("id", values...))
	}
	var builder *query.SelectBuilder
	if workspaceID == "" {
		builder = query.NewSelectBuilder(s.renderer, TableName)
	} else {
		builder = query.NewWorkspaceSelectBuilder(s.renderer, TableName, workspaceID)
	}
	builder.Columns(artifactColumns()...)
	if len(predicates) > 0 {
		builder.Where(query.And(predicates...))
	}
	if value.NewestFirst {
		builder.OrderBy(query.Descending("created_at"), query.Descending("id"))
	} else {
		builder.OrderBy(query.Ascending("created_at"), query.Ascending("id"))
	}
	statement, arguments, err := builder.Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.executor(ctx).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Artifact{}
	for rows.Next() {
		item, scanErr := scanArtifact(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLStore) Update(ctx context.Context, value Mutation) (bool, error) {
	workspaceID, err := normalizeWorkspace(value.WorkspaceID)
	if err != nil {
		return false, err
	}
	value.ID, value.Owner, value.Kind = strings.TrimSpace(value.ID), strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind)
	if value.ID == "" || value.Owner == "" || value.Kind == "" || !validStatus(value.ExpectedStatus) || !validStatus(value.Status) || !validScan(value.ExpectedScanStatus) || !validScan(value.ScanStatus) || value.UpdatedAt.IsZero() || !json.Valid(value.Metadata) {
		return false, fmt.Errorf("artifact mutation is invalid")
	}
	predicates := []query.Predicate{query.Equal("id", value.ID), query.Equal("owner", value.Owner), query.Equal("kind", value.Kind), query.Equal("status", string(value.ExpectedStatus)), query.Equal("scan_status", string(value.ExpectedScanStatus))}
	if !value.ExpectedUpdatedAt.IsZero() {
		predicates = append(predicates, query.Equal("updated_at", value.ExpectedUpdatedAt.UTC().Format(time.RFC3339Nano)))
	}
	statement, arguments, err := query.NewWorkspaceUpdateBuilder(s.renderer, TableName, workspaceID).
		Set("status", string(value.Status)).Set("scan_status", string(value.ScanStatus)).Set("expires_at", optionalTime(value.ExpiresAt)).
		Set("metadata_json", string(value.Metadata)).Set("updated_at", value.UpdatedAt.UTC().Format(time.RFC3339Nano)).
		Where(query.And(predicates...)).Build()
	if err != nil {
		return false, err
	}
	result, err := s.executor(ctx).ExecContext(ctx, statement, arguments...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (s *SQLStore) find(ctx context.Context, workspaceID string, predicate query.Predicate) (Artifact, bool, error) {
	workspaceID, err := normalizeWorkspace(workspaceID)
	if err != nil {
		return Artifact{}, false, err
	}
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.renderer, TableName, workspaceID).
		Columns(artifactColumns()...).Where(predicate).Limit(1).Build()
	if err != nil {
		return Artifact{}, false, err
	}
	value, err := scanArtifact(s.executor(ctx).QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, false, nil
	}
	return value, err == nil, err
}

func (s *SQLStore) bindingByIdentity(ctx context.Context, value Binding) (Binding, bool, error) {
	statement, arguments, err := query.NewWorkspaceSelectBuilder(s.renderer, BindingTableName, value.WorkspaceID).
		Columns(bindingColumns()...).Where(query.And(query.Equal("artifact_id", value.ArtifactID), query.Equal("owner", value.Owner), query.Equal("kind", value.Kind), query.Equal("resource_type", value.ResourceType), query.Equal("resource_id", value.ResourceID), query.Equal("field_key", value.FieldKey))).Limit(1).Build()
	if err != nil {
		return Binding{}, false, err
	}
	existing, err := scanBinding(s.executor(ctx).QueryRowContext(ctx, statement, arguments...))
	if errors.Is(err, sql.ErrNoRows) {
		return Binding{}, false, nil
	}
	return existing, err == nil, err
}

func (s *SQLStore) bindingArtifactIDs(ctx context.Context, workspaceID string, value BindingQuery, limit int) ([]string, error) {
	predicates := []query.Predicate{query.Equal("kind", value.Kind)}
	if value.Owner != "" {
		predicates = append(predicates, query.Equal("owner", value.Owner))
	}
	if value.ResourceType != "" {
		predicates = append(predicates, query.Equal("resource_type", value.ResourceType))
	}
	if value.ResourceID != "" {
		predicates = append(predicates, query.Equal("resource_id", value.ResourceID))
	}
	if value.FieldKey != "" {
		predicates = append(predicates, query.Equal("field_key", value.FieldKey))
	}
	var builder *query.SelectBuilder
	if workspaceID == "" {
		builder = query.NewSelectBuilder(s.renderer, BindingTableName)
	} else {
		builder = query.NewWorkspaceSelectBuilder(s.renderer, BindingTableName, workspaceID)
	}
	statement, arguments, err := builder.Columns("artifact_id").Where(query.And(predicates...)).OrderBy(query.Ascending("created_at"), query.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.executor(ctx).QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func artifactPredicates(value Query) []query.Predicate {
	result := []query.Predicate{}
	for _, pair := range []struct{ column, value string }{{"owner", value.Owner}, {"kind", value.Kind}, {"filename", value.Filename}, {"storage_reference", value.StorageReference}} {
		if normalized := strings.TrimSpace(pair.value); normalized != "" {
			result = append(result, query.Equal(pair.column, normalized))
		}
	}
	if !value.ExpiresAtOrBefore.IsZero() {
		expiresAt := value.ExpiresAtOrBefore.UTC().Format(time.RFC3339Nano)
		result = append(result, query.NotEqual("expires_at", ""), query.LessThanOrEqual("expires_at", expiresAt))
	}
	if values := statusValues(value.Statuses); len(values) > 0 {
		result = append(result, query.In("status", values...))
	}
	if values := scanValues(value.ScanStatuses); len(values) > 0 {
		result = append(result, query.In("scan_status", values...))
	}
	owners := []query.Predicate{}
	if values := stringValues(value.CreatedBy); len(values) > 0 {
		owners = append(owners, query.In("created_by", values...))
	}
	if values := stringValues(value.OwnerOrgIDs); len(values) > 0 {
		owners = append(owners, query.In("owner_org_id", values...))
	}
	if len(owners) > 0 {
		result = append(result, query.Or(owners...))
	}
	return result
}

func artifactColumns() []string {
	return []string{"workspace_id", "id", "owner", "kind", "idempotency_key", "created_by", "owner_org_id", "filename", "media_type", "content_sha256", "size_bytes", "storage_reference", "status", "expires_at", "scan_status", "download_token_sha256", "authorization_scope_sha256", "metadata_json", "created_at", "updated_at"}
}

func artifactValues(value Artifact) []any {
	return []any{value.WorkspaceID, value.ID, value.Owner, value.Kind, value.IdempotencyKey, value.CreatedBy, value.OwnerOrgID, value.Filename, value.MediaType, value.ContentSHA256, value.SizeBytes, value.StorageReference, string(value.Status), optionalTime(value.ExpiresAt), string(value.ScanStatus), value.DownloadTokenSHA256, value.AuthorizationScopeSHA256, string(value.Metadata), value.CreatedAt.UTC().Format(time.RFC3339Nano), value.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func bindingColumns() []string {
	return []string{"workspace_id", "id", "artifact_id", "owner", "kind", "resource_type", "resource_id", "field_key", "metadata_json", "created_at"}
}

func bindingValues(value Binding) []any {
	return []any{value.WorkspaceID, value.ID, value.ArtifactID, value.Owner, value.Kind, value.ResourceType, value.ResourceID, value.FieldKey, string(value.Metadata), value.CreatedAt.UTC().Format(time.RFC3339Nano)}
}

type scanner interface{ Scan(...any) error }

func scanArtifact(row scanner) (Artifact, error) {
	var value Artifact
	var status, scanStatus, metadata, createdAt, updatedAt, expiresAt string
	err := row.Scan(&value.WorkspaceID, &value.ID, &value.Owner, &value.Kind, &value.IdempotencyKey, &value.CreatedBy, &value.OwnerOrgID, &value.Filename, &value.MediaType, &value.ContentSHA256, &value.SizeBytes, &value.StorageReference, &status, &expiresAt, &scanStatus, &value.DownloadTokenSHA256, &value.AuthorizationScopeSHA256, &metadata, &createdAt, &updatedAt)
	if err != nil {
		return value, err
	}
	value.Status, value.ScanStatus, value.Metadata = Status(status), ScanStatus(scanStatus), json.RawMessage(metadata)
	if value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return value, err
	}
	if value.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return value, err
	}
	if expiresAt != "" {
		value.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	}
	return value, err
}

func scanBinding(row scanner) (Binding, error) {
	var value Binding
	var metadata, createdAt string
	err := row.Scan(&value.WorkspaceID, &value.ID, &value.ArtifactID, &value.Owner, &value.Kind, &value.ResourceType, &value.ResourceID, &value.FieldKey, &metadata, &createdAt)
	if err != nil {
		return value, err
	}
	value.Metadata = json.RawMessage(metadata)
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	return value, err
}

func normalizeArtifact(value Artifact) Artifact {
	value.ID, value.WorkspaceID, value.Owner, value.Kind = strings.TrimSpace(value.ID), strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind)
	value.IdempotencyKey, value.CreatedBy, value.OwnerOrgID = strings.TrimSpace(value.IdempotencyKey), strings.TrimSpace(value.CreatedBy), strings.TrimSpace(value.OwnerOrgID)
	value.Filename, value.MediaType, value.ContentSHA256, value.StorageReference = strings.TrimSpace(value.Filename), strings.TrimSpace(value.MediaType), strings.TrimSpace(value.ContentSHA256), strings.TrimSpace(value.StorageReference)
	value.DownloadTokenSHA256, value.AuthorizationScopeSHA256 = strings.TrimSpace(value.DownloadTokenSHA256), strings.TrimSpace(value.AuthorizationScopeSHA256)
	if len(bytes.TrimSpace(value.Metadata)) == 0 {
		value.Metadata = json.RawMessage(`{}`)
	}
	return value
}

func validateArtifact(value Artifact) error {
	if _, err := normalizeWorkspace(value.WorkspaceID); err != nil {
		return err
	}
	if _, found := RegistrationFor(value.Owner, value.Kind); !found {
		return fmt.Errorf("artifact owner and kind are not registered")
	}
	if value.ID == "" || value.IdempotencyKey == "" || value.CreatedBy == "" || value.Filename == "" || value.MediaType == "" || value.ContentSHA256 == "" || value.StorageReference == "" || value.SizeBytes < 0 || !validStatus(value.Status) || !validScan(value.ScanStatus) || value.CreatedAt.IsZero() || !value.UpdatedAt.Equal(value.CreatedAt) || !json.Valid(value.Metadata) {
		return fmt.Errorf("artifact identity, content, or state is invalid")
	}
	return nil
}

func normalizeBinding(value Binding) Binding {
	value.ID, value.WorkspaceID, value.ArtifactID = strings.TrimSpace(value.ID), strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.ArtifactID)
	value.Owner, value.Kind, value.ResourceType, value.ResourceID, value.FieldKey = strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind), strings.TrimSpace(value.ResourceType), strings.TrimSpace(value.ResourceID), strings.TrimSpace(value.FieldKey)
	if len(bytes.TrimSpace(value.Metadata)) == 0 {
		value.Metadata = json.RawMessage(`{}`)
	}
	return value
}

func validateBinding(value Binding) error {
	if _, err := normalizeWorkspace(value.WorkspaceID); err != nil {
		return err
	}
	if value.ID == "" || value.ArtifactID == "" || value.Owner == "" || !BindingKindRegistered(value.Kind) || value.ResourceType == "" || value.ResourceID == "" || value.CreatedAt.IsZero() || !json.Valid(value.Metadata) {
		return fmt.Errorf("artifact binding is invalid")
	}
	return nil
}

func sameArtifact(left, right Artifact) bool {
	return left.Owner == right.Owner && left.Kind == right.Kind && left.IdempotencyKey == right.IdempotencyKey && left.CreatedBy == right.CreatedBy && left.OwnerOrgID == right.OwnerOrgID && left.Filename == right.Filename && left.MediaType == right.MediaType && left.ContentSHA256 == right.ContentSHA256 && left.SizeBytes == right.SizeBytes && left.StorageReference == right.StorageReference && left.DownloadTokenSHA256 == right.DownloadTokenSHA256 && left.AuthorizationScopeSHA256 == right.AuthorizationScopeSHA256 && left.ExpiresAt.Equal(right.ExpiresAt) && bytes.Equal(left.Metadata, right.Metadata)
}

func normalizeWorkspace(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 {
		return "", fmt.Errorf("artifact workspace is invalid")
	}
	return value, nil
}

func normalizeBindingQuery(value BindingQuery) BindingQuery {
	value.Owner, value.Kind, value.ResourceType, value.ResourceID, value.FieldKey = strings.TrimSpace(value.Owner), strings.TrimSpace(value.Kind), strings.TrimSpace(value.ResourceType), strings.TrimSpace(value.ResourceID), strings.TrimSpace(value.FieldKey)
	return value
}

func validStatus(value Status) bool {
	switch value {
	case StatusPending, StatusAvailable, StatusRejected, StatusExpired, StatusDeleted:
		return true
	}
	return false
}

func validScan(value ScanStatus) bool {
	switch value {
	case ScanNotRequired, ScanPending, ScanClean, ScanRejected, ScanFailed:
		return true
	}
	return false
}

func statusValues(values []Status) []any {
	result := []any{}
	for _, value := range values {
		if validStatus(value) {
			result = append(result, string(value))
		}
	}
	return result
}

func scanValues(values []ScanStatus) []any {
	result := []any{}
	for _, value := range values {
		if validScan(value) {
			result = append(result, string(value))
		}
	}
	return result
}

func stringValues(values []string) []any {
	seen, result := map[string]bool{}, []any{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func optionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func SchemaMigrationForDialect(renderer Dialect) (SchemaMigration, error) {
	migration := SchemaMigration{Version: 1, Name: "shared_artifacts"}
	tables := []*ormschema.TableBuilder{
		ormschema.NewTable(renderer, TableName).IfNotExists().Columns(
			required("workspace_id", ormschema.TextKey(191)), required("id", ormschema.TextKey(255)), required("owner", ormschema.TextKey(191)), required("kind", ormschema.TextKey(191)), required("idempotency_key", ormschema.TextKey(191)), required("created_by", ormschema.TextKey(255)), ormschema.Column("owner_org_id", ormschema.TextKey(255)).NotNull().DefaultValue(""), required("filename", ormschema.Text()), required("media_type", ormschema.TextKey(255)), required("content_sha256", ormschema.TextKey(255)), required("size_bytes", ormschema.BigInt()), required("storage_reference", ormschema.Text()), required("status", ormschema.TextKey(64)), ormschema.Column("expires_at", ormschema.TextKey(255)).NotNull().DefaultValue(""), required("scan_status", ormschema.TextKey(64)), ormschema.Column("download_token_sha256", ormschema.TextKey(255)).NotNull().DefaultValue(""), ormschema.Column("authorization_scope_sha256", ormschema.TextKey(255)).NotNull().DefaultValue(""), required("metadata_json", ormschema.LongText()), required("created_at", ormschema.TextKey(255)), required("updated_at", ormschema.TextKey(255)),
		).PrimaryKey("id").Unique("workspace_id", "owner", "kind", "idempotency_key").Unique("workspace_id", "owner", "kind", "storage_reference"),
		ormschema.NewTable(renderer, BindingTableName).IfNotExists().Columns(
			required("workspace_id", ormschema.TextKey(191)), required("id", ormschema.TextKey(255)), required("artifact_id", ormschema.TextKey(255)), required("owner", ormschema.TextKey(191)), required("kind", ormschema.TextKey(191)), required("resource_type", ormschema.TextKey(191)), required("resource_id", ormschema.TextKey(255)), ormschema.Column("field_key", ormschema.TextKey(255)).NotNull().DefaultValue(""), required("metadata_json", ormschema.LongText()), required("created_at", ormschema.TextKey(255)),
		).PrimaryKey("id").Unique("workspace_id", "artifact_id", "owner", "kind", "resource_type", "resource_id", "field_key"),
	}
	for _, table := range tables {
		statement, _, err := table.Build()
		if err != nil {
			return migration, err
		}
		migration.Statements = append(migration.Statements, statement)
	}
	indexes := []struct {
		name, table string
		columns     []string
	}{
		{"idx_artifact_download_token", TableName, []string{"workspace_id", "download_token_sha256"}},
		{"idx_artifact_expiry", TableName, []string{"workspace_id", "status", "expires_at"}},
		{"idx_artifact_storage_reference", TableName, []string{"workspace_id", "storage_reference"}},
		{"idx_artifact_owner_cursor", TableName, []string{"workspace_id", "owner", "kind", "status", "created_at", "id"}},
		{"idx_artifact_creator_cursor", TableName, []string{"workspace_id", "owner", "kind", "created_by", "created_at", "id"}},
		{"idx_artifact_owner_org_cursor", TableName, []string{"workspace_id", "owner", "kind", "owner_org_id", "created_at", "id"}},
		{"idx_artifact_scan_queue", TableName, []string{"owner", "kind", "scan_status", "created_at", "id"}},
		{"idx_artifact_binding_artifact", BindingTableName, []string{"workspace_id", "artifact_id"}},
		{"idx_artifact_binding_resource", BindingTableName, []string{"workspace_id", "owner", "kind", "resource_type", "resource_id", "artifact_id"}},
	}
	for _, index := range indexes {
		statement, _, err := ormschema.NewIndex(renderer, index.name, index.table).Columns(index.columns...).Build()
		if err != nil {
			return migration, err
		}
		migration.Statements = append(migration.Statements, statement)
	}
	return migration, nil
}

func required(name string, dataType ormschema.ColumnType) ormschema.ColumnDefinition {
	return ormschema.Column(name, dataType).NotNull()
}

var _ ManagedStore = (*SQLStore)(nil)
