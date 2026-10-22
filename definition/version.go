package definition

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/domainry/domainry-foundation/entityid"
	"github.com/domainry/domainry-orm/query"
)

type versionValue struct {
	InstallationID string
	Owner          string
	ResourceType   string
	ResourceKey    string
	SchemaVersion  string
	SchemaHash     string
	Payload        json.RawMessage
	CreatedAt      int64
}

func (s Store) ensureVersion(ctx context.Context, executor DBTX, value versionValue) (string, error) {
	if id, hash, found, err := s.versionByVersion(ctx, executor, value); err != nil {
		return "", err
	} else if found {
		if hash != strings.TrimSpace(value.SchemaHash) {
			return "", versionConflict()
		}
		return id, nil
	}
	id, err := entityid.New("defver")
	if err != nil {
		return "", err
	}
	statement, args, err := query.NewInsertBuilder(s.dialect, VersionTableName).Columns(
		"version_id", "installation_id", "owner", "kind", "definition_key", "schema_version", "schema_hash", "payload_json", "created_at",
	).Values(id, value.InstallationID, value.Owner, value.ResourceType, value.ResourceKey, value.SchemaVersion, value.SchemaHash, value.Payload, value.CreatedAt).OnConflictDoNothing("installation_id", "owner", "kind", "definition_key", "schema_version").Build()
	if err != nil {
		return "", err
	}
	if _, err := executor.ExecContext(ctx, statement, args...); err != nil {
		if existingID, existingHash, found, readErr := s.versionByVersion(ctx, executor, value); readErr == nil && found {
			if existingHash == strings.TrimSpace(value.SchemaHash) {
				return existingID, nil
			}
			return "", versionConflict()
		}
		return "", err
	}
	if existingID, existingHash, found, err := s.versionByVersion(ctx, executor, value); err != nil {
		return "", err
	} else if !found || existingHash != strings.TrimSpace(value.SchemaHash) {
		return "", versionConflict()
	} else {
		return existingID, nil
	}
}

func (s Store) versionByVersion(ctx context.Context, executor DBTX, value versionValue) (string, string, bool, error) {
	statement, args, err := query.NewSelectBuilder(s.dialect, VersionTableName).Columns("version_id", "schema_hash").Where(query.And(
		query.Equal("installation_id", strings.TrimSpace(value.InstallationID)), query.Equal("owner", strings.TrimSpace(value.Owner)),
		query.Equal("kind", strings.TrimSpace(value.ResourceType)), query.Equal("definition_key", strings.TrimSpace(value.ResourceKey)),
		query.Equal("schema_version", strings.TrimSpace(value.SchemaVersion)),
	)).Build()
	if err != nil {
		return "", "", false, err
	}
	var id, hash string
	err = executor.QueryRowContext(ctx, statement, args...).Scan(&id, &hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return strings.TrimSpace(id), strings.TrimSpace(hash), true, nil
}

func versionConflict() error {
	return &Error{Kind: KindConflict, Code: "backend.metadata.definition_version_conflict"}
}
