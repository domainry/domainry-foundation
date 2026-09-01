package action

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const PermissionUsageContractVersion = "domainry-action-permission-usage-v1"

// PermissionUsageQuery asks one canonical Action owner for the live usages of
// a batch of Permission keys. It is a query over a frozen registry, never a
// persistence or reconciliation payload.
type PermissionUsageQuery struct {
	SourceOwner    string   `json:"source_owner"`
	PermissionKeys []string `json:"permission_keys"`
}

type PermissionUsageRequest struct {
	ContractVersion string                 `json:"contract_version"`
	Queries         []PermissionUsageQuery `json:"queries"`
}

// PermissionUsageOwnerSnapshot distinguishes a reachable registry that has no
// matching Action from an owner whose registry is not hosted by the provider.
type PermissionUsageOwnerSnapshot struct {
	SourceOwner string            `json:"source_owner"`
	Available   bool              `json:"available"`
	Usages      []PermissionUsage `json:"usages,omitempty"`
}

type PermissionUsageSnapshot struct {
	ContractVersion  string                         `json:"contract_version"`
	RegistryRevision string                         `json:"registry_revision"`
	Owners           []PermissionUsageOwnerSnapshot `json:"owners"`
}

type PermissionUsageProvider interface {
	QueryPermissionUsages(context.Context, PermissionUsageRequest) (PermissionUsageSnapshot, error)
}

func NewPermissionUsageRequest(queries []PermissionUsageQuery) (PermissionUsageRequest, error) {
	queries = append([]PermissionUsageQuery(nil), queries...)
	for index := range queries {
		queries[index].SourceOwner = strings.TrimSpace(queries[index].SourceOwner)
		queries[index].PermissionKeys = normalizeUsageKeys(queries[index].PermissionKeys)
	}
	sort.Slice(queries, func(left, right int) bool { return queries[left].SourceOwner < queries[right].SourceOwner })
	request := PermissionUsageRequest{ContractVersion: PermissionUsageContractVersion, Queries: queries}
	if err := request.Validate(); err != nil {
		return PermissionUsageRequest{}, err
	}
	return request, nil
}

func (request PermissionUsageRequest) Validate() error {
	if request.ContractVersion != PermissionUsageContractVersion || len(request.Queries) == 0 {
		return fmt.Errorf("Action permission usage request contract is invalid")
	}
	owners := make(map[string]struct{}, len(request.Queries))
	for _, query := range request.Queries {
		owner := strings.TrimSpace(query.SourceOwner)
		if !stableKeyPattern.MatchString(owner) || len(query.PermissionKeys) == 0 {
			return fmt.Errorf("Action permission usage query owner %q is invalid", owner)
		}
		if _, duplicate := owners[owner]; duplicate {
			return fmt.Errorf("Action permission usage query repeats owner %q", owner)
		}
		owners[owner] = struct{}{}
		keys := map[string]struct{}{}
		for _, rawKey := range query.PermissionKeys {
			key := strings.TrimSpace(rawKey)
			if !stableKeyPattern.MatchString(key) {
				return fmt.Errorf("Action permission usage query key %q is invalid", key)
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("Action permission usage query repeats key %q", key)
			}
			keys[key] = struct{}{}
		}
	}
	return nil
}

func (snapshot PermissionUsageSnapshot) ValidateFor(request PermissionUsageRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if snapshot.ContractVersion != PermissionUsageContractVersion || strings.TrimSpace(snapshot.RegistryRevision) == "" || len(snapshot.Owners) != len(request.Queries) {
		return fmt.Errorf("Action permission usage snapshot contract is invalid")
	}
	requested := make(map[string]map[string]struct{}, len(request.Queries))
	for _, query := range request.Queries {
		keys := make(map[string]struct{}, len(query.PermissionKeys))
		for _, key := range query.PermissionKeys {
			keys[strings.TrimSpace(key)] = struct{}{}
		}
		requested[strings.TrimSpace(query.SourceOwner)] = keys
	}
	seenOwners := make(map[string]struct{}, len(snapshot.Owners))
	for _, ownerSnapshot := range snapshot.Owners {
		owner := strings.TrimSpace(ownerSnapshot.SourceOwner)
		keys, found := requested[owner]
		if !found {
			return fmt.Errorf("Action permission usage snapshot contains unrequested owner %q", owner)
		}
		if _, duplicate := seenOwners[owner]; duplicate {
			return fmt.Errorf("Action permission usage snapshot repeats owner %q", owner)
		}
		seenOwners[owner] = struct{}{}
		if !ownerSnapshot.Available && len(ownerSnapshot.Usages) != 0 {
			return fmt.Errorf("unavailable Action owner %q returned usages", owner)
		}
		seenActions := map[string]struct{}{}
		for _, usage := range ownerSnapshot.Usages {
			if usage.Permission.Owner != owner || usage.Action.Owner != owner || usage.Action.Permission == nil || usage.Action.Permission.Key != usage.Permission.Key {
				return fmt.Errorf("Action permission usage for owner %q has mismatched authority", owner)
			}
			if _, queried := keys[usage.Permission.Key]; !queried {
				return fmt.Errorf("Action permission usage returned unrequested key %q", usage.Permission.Key)
			}
			if _, duplicate := seenActions[usage.Action.Key]; duplicate {
				return fmt.Errorf("Action permission usage repeats Action %q", usage.Action.Key)
			}
			seenActions[usage.Action.Key] = struct{}{}
			if _, err := NormalizeDefinition(usage.Action); err != nil {
				return fmt.Errorf("Action permission usage %q is invalid: %w", usage.Action.Key, err)
			}
		}
	}
	return nil
}

// QueryPermissionUsages projects a batch from this frozen registry. An owner
// is available only when this registry contains at least one Permission it
// canonically owns; missing owners are reported explicitly rather than
// pretending that they have an empty registry.
func (registry *Registry) QueryPermissionUsages(ctx context.Context, request PermissionUsageRequest) (PermissionUsageSnapshot, error) {
	if ctx == nil {
		return PermissionUsageSnapshot{}, fmt.Errorf("Action permission usage context is required")
	}
	if err := ctx.Err(); err != nil {
		return PermissionUsageSnapshot{}, err
	}
	if err := request.Validate(); err != nil {
		return PermissionUsageSnapshot{}, err
	}
	if registry == nil {
		return PermissionUsageSnapshot{}, fmt.Errorf("Action registry is unavailable")
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return PermissionUsageSnapshot{}, fmt.Errorf("Action registry is not frozen")
	}
	availableOwners := map[string]struct{}{}
	for _, owner := range registry.permissionOwners {
		availableOwners[owner] = struct{}{}
	}
	owners := make([]PermissionUsageOwnerSnapshot, 0, len(request.Queries))
	for _, query := range request.Queries {
		owner := strings.TrimSpace(query.SourceOwner)
		_, available := availableOwners[owner]
		ownerSnapshot := PermissionUsageOwnerSnapshot{SourceOwner: owner, Available: available}
		if available {
			for _, permissionKey := range query.PermissionKeys {
				for _, usage := range registry.usages[permissionIdentity(owner, strings.TrimSpace(permissionKey))] {
					ownerSnapshot.Usages = append(ownerSnapshot.Usages, PermissionUsage{
						Permission: usage.Permission,
						Action:     cloneDefinition(usage.Action),
					})
				}
			}
			sort.Slice(ownerSnapshot.Usages, func(left, right int) bool {
				return ownerSnapshot.Usages[left].Action.Key < ownerSnapshot.Usages[right].Action.Key
			})
		}
		owners = append(owners, ownerSnapshot)
	}
	return PermissionUsageSnapshot{
		ContractVersion:  PermissionUsageContractVersion,
		RegistryRevision: registry.revision,
		Owners:           owners,
	}, nil
}

func normalizeUsageKeys(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if key := strings.TrimSpace(value); key != "" {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}
