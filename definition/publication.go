package definition

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
)

func (s Store) Publish(ctx context.Context, command PublishCommand) (PublishResult, error) {
	if err := s.validate(); err != nil {
		return PublishResult{}, err
	}
	owner, kind, err := normalizeOwnerKind(command.Owner, command.ResourceType)
	if err != nil {
		return PublishResult{}, err
	}
	command.Owner, command.ResourceType = owner, kind
	command.ResourceKey = strings.TrimSpace(command.ResourceKey)
	command.ExpectedCurrentVersionID = strings.TrimSpace(command.ExpectedCurrentVersionID)
	command.SchemaVersion = strings.TrimSpace(command.SchemaVersion)
	command.SourceKind = strings.TrimSpace(command.SourceKind)
	command.SourceID = strings.TrimSpace(command.SourceID)
	command.PublishedBy = strings.TrimSpace(command.PublishedBy)
	if command.ResourceKey == "" || command.ExpectedCurrentVersionID == "" || command.SchemaVersion == "" || command.SourceKind == "" || command.SourceID == "" || command.PublishedBy == "" {
		return PublishResult{}, &Error{StatusCode: 400, Code: "metadata.definition_publication_invalid"}
	}
	hash, err := payloadHash(command.Payload, command.SchemaHash)
	if err != nil {
		return PublishResult{}, err
	}
	command.SchemaHash = hash
	publish := func(executor DBTX) (PublishResult, error) { return s.publish(ctx, executor, command) }
	if executor := ExecutorFromContext(ctx, nil); executor != nil {
		return publish(executor)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return PublishResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := publish(tx)
	if err != nil {
		return PublishResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

func (s Store) publish(ctx context.Context, executor DBTX, command PublishCommand) (PublishResult, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	definitionID := s.definitionID(command.Owner, command.ResourceType, command.ResourceKey)
	versionID, err := s.ensureVersion(ctx, executor, versionValue{
		DefinitionID: definitionID, InstallationID: s.installationID, Owner: command.Owner,
		ResourceType: command.ResourceType, ResourceKey: command.ResourceKey,
		SchemaVersion: command.SchemaVersion, SchemaHash: command.SchemaHash,
		Payload: append(json.RawMessage(nil), command.Payload...), CreatedAt: now,
	})
	if err != nil {
		return PublishResult{}, err
	}
	if command.ExpectedCurrentVersionID != NoCurrentVersion && versionID == command.ExpectedCurrentVersionID {
		current, found, readErr := s.get(ctx, executor, command.Owner, command.ResourceType, command.ResourceKey, true)
		if readErr != nil {
			return PublishResult{}, readErr
		}
		if found && current.CurrentVersionID == versionID && current.SchemaVersion == command.SchemaVersion && current.SchemaHash == command.SchemaHash &&
			current.ObjectKey == strings.TrimSpace(command.ObjectKey) && current.Name == strings.TrimSpace(command.Name) &&
			current.SourceKind == command.SourceKind && current.SourceID == command.SourceID && current.PublishedBy == command.PublishedBy && bytes.Equal(current.Payload, command.Payload) {
			return PublishResult{Definition: current, CurrentVersionID: versionID}, nil
		}
		return PublishResult{}, revisionConflict()
	}
	var affected int64
	if command.ExpectedCurrentVersionID == NoCurrentVersion {
		statement, args, buildErr := query.NewInsertBuilder(s.dialect, TableName).Columns(
			"id", "installation_id", "owner", "kind", "definition_key", "current_version_id", "status", "object_key", "name",
			"payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "published_at", "published_by", "disabled_at", "disabled_by", "created_at", "updated_at",
		).Values(
			definitionID, s.installationID, command.Owner, command.ResourceType, command.ResourceKey, versionID, "active",
			strings.TrimSpace(command.ObjectKey), strings.TrimSpace(command.Name), command.Payload, command.SchemaVersion, command.SchemaHash,
			command.SourceKind, command.SourceID, now, command.PublishedBy, nil, nil, now, now,
		).OnConflictDoNothing("id").Build()
		if buildErr != nil {
			return PublishResult{}, buildErr
		}
		result, execErr := executor.ExecContext(ctx, statement, args...)
		if execErr != nil {
			return PublishResult{}, execErr
		}
		affected, err = result.RowsAffected()
	} else {
		statement, args, buildErr := query.NewUpdateBuilder(s.dialect, TableName).
			Set("current_version_id", versionID).Set("status", "active").
			Set("object_key", strings.TrimSpace(command.ObjectKey)).Set("name", strings.TrimSpace(command.Name)).
			Set("payload_json", command.Payload).Set("schema_version", command.SchemaVersion).Set("schema_hash", command.SchemaHash).
			Set("source_kind", command.SourceKind).Set("source_id", command.SourceID).
			Set("published_at", now).Set("published_by", command.PublishedBy).Set("disabled_at", nil).Set("disabled_by", nil).Set("updated_at", now).
			Where(query.And(
				query.Equal("id", definitionID), query.Equal("installation_id", s.installationID),
				query.Equal("owner", command.Owner), query.Equal("kind", command.ResourceType),
				query.Equal("definition_key", command.ResourceKey), query.Equal("current_version_id", command.ExpectedCurrentVersionID), query.Equal("status", "active"),
			)).Build()
		if buildErr != nil {
			return PublishResult{}, buildErr
		}
		result, execErr := executor.ExecContext(ctx, statement, args...)
		if execErr != nil {
			return PublishResult{}, execErr
		}
		affected, err = result.RowsAffected()
	}
	if err != nil {
		return PublishResult{}, err
	}
	if affected != 1 {
		return PublishResult{}, revisionConflict()
	}
	current, found, err := s.get(ctx, executor, command.Owner, command.ResourceType, command.ResourceKey, true)
	if err != nil {
		return PublishResult{}, err
	}
	if !found || current.CurrentVersionID != versionID {
		return PublishResult{}, revisionConflict()
	}
	return PublishResult{Definition: current, CurrentVersionID: versionID}, nil
}

func (s Store) Disable(ctx context.Context, command DisableCommand) error {
	if err := s.validate(); err != nil {
		return err
	}
	owner, kind, err := normalizeOwnerKind(command.Owner, command.ResourceType)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(command.ResourceKey)
	expected := strings.TrimSpace(command.ExpectedCurrentVersionID)
	disabledBy := strings.TrimSpace(command.DisabledBy)
	if key == "" || expected == "" || expected == NoCurrentVersion || disabledBy == "" {
		return &Error{StatusCode: 400, Code: "metadata.definition_disable_invalid"}
	}
	disable := func(executor DBTX) error {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		statement, args, buildErr := query.NewUpdateBuilder(s.dialect, TableName).
			Set("status", "disabled").Set("disabled_at", now).Set("disabled_by", disabledBy).Set("updated_at", now).
			Where(query.And(
				query.Equal("installation_id", s.installationID), query.Equal("owner", owner), query.Equal("kind", kind),
				query.Equal("definition_key", key), query.Equal("current_version_id", expected), query.Equal("status", "active"),
			)).Build()
		if buildErr != nil {
			return buildErr
		}
		result, execErr := executor.ExecContext(ctx, statement, args...)
		if execErr != nil {
			return execErr
		}
		affected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		if affected != 1 {
			return revisionConflict()
		}
		return nil
	}
	if executor := ExecutorFromContext(ctx, nil); executor != nil {
		return disable(executor)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := disable(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s Store) GetVersion(ctx context.Context, value VersionQuery) (Version, bool, error) {
	if err := s.validate(); err != nil {
		return Version{}, false, err
	}
	owner, kind, err := normalizeOwnerKind(value.Owner, value.ResourceType)
	if err != nil {
		return Version{}, false, err
	}
	key := strings.TrimSpace(value.ResourceKey)
	versionID := strings.TrimSpace(value.VersionID)
	schemaVersion := strings.TrimSpace(value.SchemaVersion)
	if key == "" || (versionID == "") == (schemaVersion == "") {
		return Version{}, false, &Error{StatusCode: 400, Code: "metadata.definition_version_query_invalid"}
	}
	predicates := []query.Predicate{query.Equal("installation_id", s.installationID), query.Equal("owner", owner), query.Equal("kind", kind), query.Equal("definition_key", key)}
	if versionID != "" {
		predicates = append(predicates, query.Equal("id", versionID))
	} else {
		predicates = append(predicates, query.Equal("schema_version", schemaVersion))
	}
	statement, args, err := query.NewSelectBuilder(s.dialect, VersionTableName).Columns("id", "owner", "kind", "definition_key", "schema_version", "schema_hash", "payload_json", "created_at").Where(query.And(predicates...)).Build()
	if err != nil {
		return Version{}, false, err
	}
	var result Version
	var payload string
	err = ExecutorFromContext(ctx, s.database).QueryRowContext(ctx, statement, args...).Scan(&result.ID, &result.Owner, &result.ResourceType, &result.ResourceKey, &result.SchemaVersion, &result.SchemaHash, &payload, &result.CreatedAt)
	if err == sql.ErrNoRows {
		return Version{}, false, nil
	}
	if err != nil {
		return Version{}, false, err
	}
	result.Payload = json.RawMessage(payload)
	return result, true, nil
}

func revisionConflict() error {
	return &Error{StatusCode: 409, Code: "metadata.definition_revision_conflict"}
}
