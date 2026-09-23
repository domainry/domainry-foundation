package definition

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-orm/query"
)

type Store struct {
	database       Database
	dialect        Dialect
	installationID string
	writeMu        *sync.Mutex
}

func NewStore(database Database, dialect Dialect, installationID string) Store {
	return Store{database: database, dialect: dialect, installationID: strings.TrimSpace(installationID), writeMu: &sync.Mutex{}}
}

var registeredKinds = map[string]map[string]bool{
	OwnerMetadata: {
		"application": true, "object": true, "field": true, "validation": true, "action": true, "dictionary": true,
	},
	OwnerIdentity:     {"identity_profile_binding": true, "role": true},
	OwnerWorkflow:     {"workflow": true},
	OwnerAutomation:   {"automation_rule": true},
	OwnerIntegration:  {"integration_connector": true, "integration_event_mapping": true},
	OwnerReport:       {"report": true},
	OwnerAgent:        {"skill": true, "agent": true},
	OwnerScheduler:    {"scheduler": true},
	OwnerNotification: {"delivery_policy": true, "notification_template": true, "notification_template_version": true},
	OwnerLifecycle:    {"retention_policy": true},
}

func normalizeOwnerKind(owner, kind string) (string, string, error) {
	owner, kind = strings.TrimSpace(owner), strings.TrimSpace(kind)
	if owner == "" {
		return "", "", &Error{StatusCode: 400, Code: "metadata.definition_owner_required"}
	}
	registered, found := registeredKinds[owner]
	if !found || !registered[kind] {
		return "", "", &Error{StatusCode: 400, Code: "metadata.definition_kind_unregistered"}
	}
	return owner, kind, nil
}

func registeredKind(kind string) bool {
	kind = strings.TrimSpace(kind)
	for _, registered := range registeredKinds {
		if registered[kind] {
			return true
		}
	}
	return false
}

func (s Store) ReplaceSourceSnapshot(ctx context.Context, snapshot SourceSnapshot) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := validateSourceIdentity(snapshot.Owner, snapshot.SchemaVersion, snapshot.SourceKind, snapshot.SourceID); err != nil {
		return err
	}
	snapshot.Owner = strings.TrimSpace(snapshot.Owner)
	snapshot.SchemaVersion = strings.TrimSpace(snapshot.SchemaVersion)
	snapshot.SourceKind = strings.TrimSpace(snapshot.SourceKind)
	snapshot.SourceID = strings.TrimSpace(snapshot.SourceID)
	syncRows := func(executor DBTX) error {
		return s.syncDefinitionRows(ctx, executor, snapshot.Owner, snapshot.SchemaVersion, snapshot.SourceKind, snapshot.SourceID, snapshot.Definitions, time.Now().UTC().Format(time.RFC3339Nano))
	}
	if executor := ExecutorFromContext(ctx, nil); executor != nil {
		return syncRows(executor)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := syncRows(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func validateSourceIdentity(owner, schemaVersion, sourceKind, sourceID string) error {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(schemaVersion) == "" || strings.TrimSpace(sourceKind) == "" || strings.TrimSpace(sourceID) == "" {
		return &Error{StatusCode: 400, Code: "metadata.projection_identity_required"}
	}
	if _, found := registeredKinds[strings.TrimSpace(owner)]; !found {
		return &Error{StatusCode: 400, Code: "metadata.definition_owner_unregistered"}
	}
	return nil
}

func (s Store) validate() error {
	if s.database == nil || s.dialect == nil || s.installationID == "" || s.writeMu == nil {
		return &Error{StatusCode: 503, Code: "metadata.store_unavailable"}
	}
	return nil
}

func (s Store) syncDefinitionRows(ctx context.Context, executor DBTX, owner, schemaVersion, sourceKind, sourceID string, definitions []Definition, now string) error {
	disable, args, err := query.NewUpdateBuilder(s.dialect, TableName).
		Set("status", "disabled").Set("disabled_at", now).Set("updated_at", now).
		Where(query.And(
			query.Equal("installation_id", s.installationID), query.Equal("owner", owner),
			query.Equal("source_kind", sourceKind), query.Equal("source_id", sourceID), query.Equal("status", "active"),
		)).Build()
	if err != nil {
		return err
	}
	if _, err := executor.ExecContext(ctx, disable, args...); err != nil {
		return err
	}
	for _, value := range definitions {
		if value.Owner != "" && strings.TrimSpace(value.Owner) != owner {
			return &Error{StatusCode: 400, Code: "metadata.definition_owner_mismatch"}
		}
		_, kind, err := normalizeOwnerKind(owner, value.ResourceType)
		if err != nil {
			return err
		}
		key := strings.TrimSpace(value.ResourceKey)
		if key == "" || len(value.Payload) == 0 || !json.Valid(value.Payload) {
			return &Error{StatusCode: 400, Code: "metadata.definition_invalid"}
		}
		hash, err := payloadHash(value.Payload, value.SchemaHash)
		if err != nil {
			return err
		}
		current, found, err := s.get(ctx, executor, owner, kind, key, false)
		if err != nil {
			return err
		}
		if found && (current.SourceKind != sourceKind || current.SourceID != sourceID) {
			continue
		}
		if found && current.SchemaVersion == schemaVersion && current.SchemaHash != hash {
			return versionConflict()
		}
		definitionID := s.definitionID(owner, kind, key)
		versionID, err := s.ensureVersion(ctx, executor, versionValue{
			DefinitionID: definitionID, InstallationID: s.installationID, Owner: owner,
			ResourceType: kind, ResourceKey: key, SchemaVersion: schemaVersion,
			SchemaHash: hash, Payload: append(json.RawMessage(nil), value.Payload...), CreatedAt: now,
		})
		if err != nil {
			return err
		}
		publishedBy := sourceKind + ":" + sourceID
		if found {
			statement, values, buildErr := query.NewUpdateBuilder(s.dialect, TableName).
				Set("current_version_id", versionID).Set("status", "active").
				Set("object_key", strings.TrimSpace(value.ObjectKey)).Set("name", strings.TrimSpace(value.Name)).
				Set("payload_json", value.Payload).Set("schema_version", schemaVersion).Set("schema_hash", hash).
				Set("source_kind", sourceKind).Set("source_id", sourceID).
				Set("published_at", now).Set("published_by", publishedBy).Set("disabled_at", nil).Set("disabled_by", nil).Set("updated_at", now).
				Where(query.Equal("id", definitionID)).Build()
			if buildErr != nil {
				return buildErr
			}
			if _, err := executor.ExecContext(ctx, statement, values...); err != nil {
				return err
			}
			continue
		}
		statement, values, buildErr := query.NewInsertBuilder(s.dialect, TableName).Columns(
			"id", "installation_id", "owner", "kind", "definition_key", "current_version_id", "status", "object_key", "name",
			"payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "published_at", "published_by", "disabled_at", "disabled_by", "created_at", "updated_at",
		).Values(
			definitionID, s.installationID, owner, kind, key, versionID, "active", strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.Name),
			value.Payload, schemaVersion, hash, sourceKind, sourceID, now, publishedBy, nil, nil, now, now,
		).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := executor.ExecContext(ctx, statement, values...); err != nil {
			return err
		}
	}
	purge, purgeArgs, err := query.NewDeleteBuilder(s.dialect, TableName).Where(query.And(
		query.Equal("installation_id", s.installationID), query.Equal("owner", owner),
		query.Equal("source_kind", sourceKind), query.Equal("source_id", sourceID), query.Equal("status", "disabled"),
	)).Build()
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, purge, purgeArgs...)
	return err
}

func (s Store) List(ctx context.Context, value Query) ([]Definition, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	owner := strings.TrimSpace(value.Owner)
	if owner == "" && !value.CrossOwner {
		return nil, &Error{StatusCode: 400, Code: "metadata.definition_owner_required"}
	}
	if owner != "" {
		if _, found := registeredKinds[owner]; !found {
			return nil, &Error{StatusCode: 400, Code: "metadata.definition_owner_unregistered"}
		}
	}
	predicates := []query.Predicate{query.Equal("installation_id", s.installationID), query.Equal("status", "active")}
	if owner != "" {
		predicates = append(predicates, query.Equal("owner", owner))
	}
	if kind := strings.TrimSpace(value.ResourceType); kind != "" {
		if owner != "" {
			if _, _, err := normalizeOwnerKind(owner, kind); err != nil {
				return nil, err
			}
		} else if !registeredKind(kind) {
			return nil, &Error{StatusCode: 400, Code: "metadata.definition_kind_unregistered"}
		}
		predicates = append(predicates, query.Equal("kind", kind))
	}
	if sourceID := strings.TrimSpace(value.SourceID); sourceID != "" {
		predicates = append(predicates, query.Equal("source_id", sourceID))
	}
	statement, args, err := query.NewSelectBuilder(s.dialect, TableName).Columns(columns()...).Where(query.And(predicates...)).OrderBy(query.Ascending("owner"), query.Ascending("kind"), query.Ascending("definition_key")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := ExecutorFromContext(ctx, s.database).QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []Definition{}
	for rows.Next() {
		item, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, item)
	}
	return values, rows.Err()
}

func (s Store) Get(ctx context.Context, owner, resourceType, key string) (Definition, bool, error) {
	if err := s.validate(); err != nil {
		return Definition{}, false, err
	}
	owner, resourceType, err := normalizeOwnerKind(owner, resourceType)
	if err != nil {
		return Definition{}, false, err
	}
	return s.get(ctx, ExecutorFromContext(ctx, s.database), owner, resourceType, strings.TrimSpace(key), true)
}

func (s Store) Snapshot(ctx context.Context, value Query) (Snapshot, error) {
	values, err := s.List(ctx, value)
	return Snapshot{Definitions: values}, err
}

func (s Store) get(ctx context.Context, executor DBTX, owner, resourceType, key string, activeOnly bool) (Definition, bool, error) {
	predicates := []query.Predicate{query.Equal("installation_id", s.installationID), query.Equal("owner", owner), query.Equal("kind", resourceType), query.Equal("definition_key", key)}
	if activeOnly {
		predicates = append(predicates, query.Equal("status", "active"))
	}
	statement, args, err := query.NewSelectBuilder(s.dialect, TableName).Columns(columns()...).Where(query.And(predicates...)).Build()
	if err != nil {
		return Definition{}, false, err
	}
	rows, err := executor.QueryContext(ctx, statement, args...)
	if err != nil {
		return Definition{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Definition{}, false, rows.Err()
	}
	value, err := scanDefinition(rows)
	return value, err == nil, err
}

func (s Store) definitionID(owner, resourceType, key string) string {
	sum := sha256.Sum256([]byte(s.installationID + "\x00" + owner + "\x00" + resourceType + "\x00" + key))
	return "definition:" + hex.EncodeToString(sum[:16])
}

type rowScanner interface{ Scan(...any) error }

func scanDefinition(row rowScanner) (Definition, error) {
	var value Definition
	var payload string
	var disabled, disabledBy sql.NullString
	err := row.Scan(&value.Owner, &value.ResourceType, &value.ResourceKey, &value.CurrentVersionID, &value.Status,
		&value.ObjectKey, &value.Name, &payload, &value.SchemaVersion, &value.SchemaHash,
		&value.SourceKind, &value.SourceID, &value.PublishedAt, &value.PublishedBy,
		&disabled, &disabledBy, &value.CreatedAt, &value.UpdatedAt)
	value.Payload = json.RawMessage(payload)
	if disabled.Valid {
		value.DisabledAt = disabled.String
	}
	if disabledBy.Valid {
		value.DisabledBy = disabledBy.String
	}
	return value, err
}

func columns() []string {
	return []string{"owner", "kind", "definition_key", "current_version_id", "status", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "published_at", "published_by", "disabled_at", "disabled_by", "created_at", "updated_at"}
}

func payloadHash(payload json.RawMessage, declared string) (string, error) {
	if len(payload) == 0 || !json.Valid(payload) {
		return "", &Error{StatusCode: 400, Code: "metadata.definition_invalid"}
	}
	sum := sha256.Sum256(payload)
	actual := hex.EncodeToString(sum[:])
	if declared = strings.TrimSpace(declared); declared != "" && declared != actual {
		return "", &Error{StatusCode: 400, Code: "metadata.definition_hash_mismatch"}
	}
	return actual, nil
}

var _ StorePort = Store{}
