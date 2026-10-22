// Package schemaownership defines the source-owned inventory contract for
// durable Domainry tables. The package contains metadata only: schema DDL and
// SQL behavior stay in the package that owns the table.
package schemaownership

import (
	"fmt"
	"regexp"
	"strings"
)

type WorkspaceScope string

const (
	ScopeWorkspace     WorkspaceScope = "workspace"
	ScopeInstallation  WorkspaceScope = "installation"
	ScopeGlobal        WorkspaceScope = "global"
	ScopeExplicitMixed WorkspaceScope = "explicit_mixed"
)

type RetentionClass string

const (
	RetentionProduct      RetentionClass = "product_retention"
	RetentionLegalAudit   RetentionClass = "legal_audit_retention"
	RetentionTechnicalTTL RetentionClass = "technical_ttl"
	RetentionUserErase    RetentionClass = "user_requested_erase"
	RetentionInstallation RetentionClass = "installation_lifetime"
	// RetentionRegisteredRowPolicy is reserved for shared kernels whose owner
	// and kind registrations select a stricter row-level policy.
	RetentionRegisteredRowPolicy RetentionClass = "registered_row_policy"
)

// TableKind makes a table's identity semantics explicit. Only entity tables
// own a public business ID; all other kinds are identified by their natural
// relationship, owner, state, idempotency, or parent key.
type TableKind string

const (
	KindEntity         TableKind = "entity"
	KindRelation       TableKind = "relation"
	KindState          TableKind = "state"
	KindIdempotency    TableKind = "idempotency"
	KindDependent      TableKind = "dependent"
	KindInfrastructure TableKind = "infrastructure"
)

type Table struct {
	Name             string         `json:"name"`
	Owner            string         `json:"owner"`
	Kind             TableKind      `json:"kind"`
	WorkspaceScope   WorkspaceScope `json:"workspace_scope"`
	RetentionClass   RetentionClass `json:"retention_class"`
	PrimaryKey       []string       `json:"primary_key,omitempty"`
	UniqueKey        []string       `json:"unique_key,omitempty"`
	EntityIDColumn   string         `json:"entity_id_column,omitempty"`
	EntityIDPrefix   string         `json:"entity_id_prefix,omitempty"`
	BusinessKey      []string       `json:"business_key,omitempty"`
	BoundedQueryPath string         `json:"bounded_query_path"`
	DeletionPolicy   string         `json:"deletion_policy"`
}

var identifierPattern = regexp.MustCompile(`^_?[a-z][a-z0-9_]*$`)
var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(?:/[a-z][a-z0-9_-]*)*$`)
var entityIDPrefixPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)

func (table Table) Validate() error {
	if !identifierPattern.MatchString(strings.TrimSpace(table.Name)) {
		return fmt.Errorf("schema ownership table name %q is invalid", table.Name)
	}
	if !ownerPattern.MatchString(strings.TrimSpace(table.Owner)) {
		return fmt.Errorf("schema ownership owner %q is invalid", table.Owner)
	}
	switch table.Kind {
	case KindEntity, KindRelation, KindState, KindIdempotency, KindDependent, KindInfrastructure:
	default:
		return fmt.Errorf("schema ownership table %s kind %q is invalid", table.Name, table.Kind)
	}
	switch table.WorkspaceScope {
	case ScopeWorkspace, ScopeInstallation, ScopeGlobal, ScopeExplicitMixed:
	default:
		return fmt.Errorf("schema ownership workspace scope %q is invalid", table.WorkspaceScope)
	}
	switch table.RetentionClass {
	case RetentionProduct, RetentionLegalAudit, RetentionTechnicalTTL, RetentionUserErase, RetentionInstallation, RetentionRegisteredRowPolicy:
	default:
		return fmt.Errorf("schema ownership retention class %q is invalid", table.RetentionClass)
	}
	if len(table.PrimaryKey) != 1 || strings.TrimSpace(table.PrimaryKey[0]) != "id" {
		return fmt.Errorf("schema ownership table %s must use id as its sole physical primary key", table.Name)
	}
	seen := map[string]bool{}
	for _, column := range table.PrimaryKey {
		column = strings.TrimSpace(column)
		if !identifierPattern.MatchString(column) || seen[column] {
			return fmt.Errorf("schema ownership table %s has invalid physical primary key", table.Name)
		}
		seen[column] = true
	}
	seen = map[string]bool{}
	for _, column := range table.UniqueKey {
		column = strings.TrimSpace(column)
		if !identifierPattern.MatchString(column) || column == "id" || seen[column] {
			return fmt.Errorf("schema ownership table %s has invalid natural unique key", table.Name)
		}
		seen[column] = true
	}
	if err := table.validateEntityIdentity(); err != nil {
		return err
	}
	if strings.TrimSpace(table.BoundedQueryPath) == "" {
		return fmt.Errorf("schema ownership table %s has no bounded query path", table.Name)
	}
	if strings.TrimSpace(table.DeletionPolicy) == "" {
		return fmt.Errorf("schema ownership table %s has no deletion policy", table.Name)
	}
	return nil
}

func (table Table) validateEntityIdentity() error {
	entityIDColumn := strings.TrimSpace(table.EntityIDColumn)
	entityIDPrefix := strings.TrimSpace(table.EntityIDPrefix)
	if table.Kind != KindEntity {
		if entityIDColumn != "" || entityIDPrefix != "" || len(table.BusinessKey) != 0 {
			return fmt.Errorf("schema ownership non-entity table %s cannot declare an entity ID, ID prefix or business key", table.Name)
		}
		return nil
	}
	if len(table.PrimaryKey) != 1 || strings.TrimSpace(table.PrimaryKey[0]) != "id" || len(table.UniqueKey) != 0 {
		return fmt.Errorf("schema ownership entity table %s must use id as its sole primary key", table.Name)
	}
	if !identifierPattern.MatchString(entityIDColumn) || entityIDColumn == "id" || !strings.HasSuffix(entityIDColumn, "_id") {
		return fmt.Errorf("schema ownership entity table %s has invalid entity ID column %q", table.Name, table.EntityIDColumn)
	}
	if !entityIDPrefixPattern.MatchString(entityIDPrefix) {
		return fmt.Errorf("schema ownership entity table %s has invalid entity ID prefix %q", table.Name, table.EntityIDPrefix)
	}
	if len(table.BusinessKey) != 1 || strings.TrimSpace(table.BusinessKey[0]) != entityIDColumn {
		return fmt.Errorf("schema ownership entity table %s business key must be the globally unique %s column", table.Name, entityIDColumn)
	}
	return nil
}

// IdentityKey returns the canonical physical row identity. Every managed table
// uses the auto-increment id primary key; UniqueKey, when present on a
// non-entity table, describes its natural uniqueness instead.
func (table Table) IdentityKey() []string {
	return table.PrimaryKey
}

func ValidateAll(tables []Table) error {
	seen := map[string]bool{}
	prefixOwners := map[string]string{}
	for _, table := range tables {
		if err := table.Validate(); err != nil {
			return err
		}
		if seen[table.Name] {
			return fmt.Errorf("schema ownership table %s is registered more than once", table.Name)
		}
		seen[table.Name] = true
		if table.Kind == KindEntity {
			prefix := strings.TrimSpace(table.EntityIDPrefix)
			if owner, exists := prefixOwners[prefix]; exists {
				return fmt.Errorf("schema ownership entity ID prefix %s is shared by %s and %s", prefix, owner, table.Name)
			}
			prefixOwners[prefix] = table.Name
		}
	}
	return nil
}

func Names(tables []Table) []string {
	names := make([]string, len(tables))
	for index, table := range tables {
		names[index] = table.Name
	}
	return names
}

func Clone(tables []Table) []Table {
	result := make([]Table, len(tables))
	for index, table := range tables {
		result[index] = table
		result[index].PrimaryKey = append([]string(nil), table.PrimaryKey...)
		result[index].UniqueKey = append([]string(nil), table.UniqueKey...)
		result[index].BusinessKey = append([]string(nil), table.BusinessKey...)
	}
	return result
}
