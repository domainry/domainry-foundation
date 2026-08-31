// Package moduleinfo defines the deployment-neutral inventory contract used
// by an embedding Runtime and its control plane to observe installed modules.
package moduleinfo

import (
	"fmt"
	"sort"
	"strings"
)

const ContractVersion = "domainry-module-inventory-v1"

type DeploymentMode string

const (
	DeploymentModeModule DeploymentMode = "module"
	DeploymentModeSaaS   DeploymentMode = "saas"
)

type HTTPSurface struct {
	Name   string   `json:"name"`
	Routes []string `json:"routes"`
}

type PersistenceMode string

const (
	PersistenceBorrowedHost PersistenceMode = "borrowed_host"
	PersistenceServiceOwned PersistenceMode = "service_owned"
	PersistenceNone         PersistenceMode = "none"
)

type Persistence struct {
	Mode        PersistenceMode `json:"mode"`
	SchemaOwner string          `json:"schema_owner,omitempty"`
}

type Descriptor struct {
	Key          string         `json:"key"`
	Mode         DeploymentMode `json:"mode"`
	Capabilities []string       `json:"capabilities"`
	HTTPSurfaces []HTTPSurface  `json:"http_surfaces,omitempty"`
	Persistence  Persistence    `json:"persistence"`
}

type Inventory struct {
	ContractVersion string       `json:"contract_version"`
	Modules         []Descriptor `json:"modules"`
}

func NewInventory(modules []Descriptor) (Inventory, error) {
	result := Inventory{ContractVersion: ContractVersion, Modules: append([]Descriptor(nil), modules...)}
	sort.Slice(result.Modules, func(i, j int) bool { return result.Modules[i].Key < result.Modules[j].Key })
	seen := map[string]bool{}
	for i := range result.Modules {
		if err := ValidateDescriptor(result.Modules[i]); err != nil {
			return Inventory{}, err
		}
		if seen[result.Modules[i].Key] {
			return Inventory{}, fmt.Errorf("module %q is duplicated", result.Modules[i].Key)
		}
		seen[result.Modules[i].Key] = true
	}
	return result, nil
}

func ValidateDescriptor(value Descriptor) error {
	if strings.TrimSpace(value.Key) == "" {
		return fmt.Errorf("module key is required")
	}
	if value.Mode != DeploymentModeModule && value.Mode != DeploymentModeSaaS {
		return fmt.Errorf("module %q has unsupported deployment mode %q", value.Key, value.Mode)
	}
	seen := map[string]bool{}
	for _, capability := range value.Capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" || seen[capability] {
			return fmt.Errorf("module %q has invalid capabilities", value.Key)
		}
		seen[capability] = true
	}
	if value.Mode == DeploymentModeSaaS && len(value.HTTPSurfaces) != 0 {
		return fmt.Errorf("SaaS module %q exposes in-process HTTP", value.Key)
	}
	switch value.Persistence.Mode {
	case PersistenceBorrowedHost:
		if value.Mode != DeploymentModeModule || strings.TrimSpace(value.Persistence.SchemaOwner) == "" {
			return fmt.Errorf("module %q has invalid borrowed persistence", value.Key)
		}
	case PersistenceServiceOwned:
		if value.Mode != DeploymentModeSaaS || strings.TrimSpace(value.Persistence.SchemaOwner) == "" {
			return fmt.Errorf("module %q has invalid service persistence", value.Key)
		}
	case PersistenceNone:
		if strings.TrimSpace(value.Persistence.SchemaOwner) != "" {
			return fmt.Errorf("module %q has an owner for absent persistence", value.Key)
		}
	default:
		return fmt.Errorf("module %q has unsupported persistence mode %q", value.Key, value.Persistence.Mode)
	}
	return nil
}
