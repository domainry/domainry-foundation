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

type Table struct {
	Name             string         `json:"name"`
	Owner            string         `json:"owner"`
	WorkspaceScope   WorkspaceScope `json:"workspace_scope"`
	RetentionClass   RetentionClass `json:"retention_class"`
	PrimaryKey       []string       `json:"primary_key"`
	BoundedQueryPath string         `json:"bounded_query_path"`
	DeletionPolicy   string         `json:"deletion_policy"`
}

var identifierPattern = regexp.MustCompile(`^_?[a-z][a-z0-9_]*$`)
var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*(?:/[a-z][a-z0-9_-]*)*$`)

func (table Table) Validate() error {
	if !identifierPattern.MatchString(strings.TrimSpace(table.Name)) {
		return fmt.Errorf("schema ownership table name %q is invalid", table.Name)
	}
	if !ownerPattern.MatchString(strings.TrimSpace(table.Owner)) {
		return fmt.Errorf("schema ownership owner %q is invalid", table.Owner)
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
	if len(table.PrimaryKey) == 0 {
		return fmt.Errorf("schema ownership table %s has no primary key", table.Name)
	}
	seen := map[string]bool{}
	for _, column := range table.PrimaryKey {
		column = strings.TrimSpace(column)
		if !identifierPattern.MatchString(column) || seen[column] {
			return fmt.Errorf("schema ownership table %s has invalid primary key", table.Name)
		}
		seen[column] = true
	}
	if strings.TrimSpace(table.BoundedQueryPath) == "" {
		return fmt.Errorf("schema ownership table %s has no bounded query path", table.Name)
	}
	if strings.TrimSpace(table.DeletionPolicy) == "" {
		return fmt.Errorf("schema ownership table %s has no deletion policy", table.Name)
	}
	return nil
}

func ValidateAll(tables []Table) error {
	seen := map[string]bool{}
	for _, table := range tables {
		if err := table.Validate(); err != nil {
			return err
		}
		if seen[table.Name] {
			return fmt.Errorf("schema ownership table %s is registered more than once", table.Name)
		}
		seen[table.Name] = true
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
	}
	return result
}
