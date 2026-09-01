package action

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry accepts batch registration during assembly and becomes immutable
// after Freeze. A failed batch never partially mutates the registry.
type Registry struct {
	mu               sync.RWMutex
	frozen           bool
	definitions      map[string]ActionDefinition
	byHTTP           map[string]string
	byNonHTTP        map[string]string
	permissions      map[string]PermissionDefinition
	permissionOwners map[string]string
	usages           map[string][]PermissionUsage
	ordered          []ActionDefinition
	revision         string
}

func NewRegistry() *Registry {
	return &Registry{
		definitions:      map[string]ActionDefinition{},
		byHTTP:           map[string]string{},
		byNonHTTP:        map[string]string{},
		permissions:      map[string]PermissionDefinition{},
		permissionOwners: map[string]string{},
		usages:           map[string][]PermissionUsage{},
	}
}

func (registry *Registry) Register(definitions ...ActionDefinition) error {
	if registry == nil {
		return fmt.Errorf("action registry is nil")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.frozen {
		return fmt.Errorf("action registry is frozen")
	}
	normalized := make([]ActionDefinition, len(definitions))
	batchActions := map[string]bool{}
	batchHTTP := map[string]string{}
	batchNonHTTP := map[string]string{}
	batchPermissions := map[string]string{}
	for index := range definitions {
		definition := normalizeDefinition(definitions[index])
		if err := validateDefinition(definition); err != nil {
			return fmt.Errorf("action registration %d: %w", index, err)
		}
		if _, exists := registry.definitions[definition.Key]; exists || batchActions[definition.Key] {
			return fmt.Errorf("action key %q is duplicated", definition.Key)
		}
		batchActions[definition.Key] = true
		if definition.LifecycleStatus != LifecycleRetired && definition.HTTP != nil {
			identity := definitionHTTPIdentity(definition)
			if existing := registry.byHTTP[identity]; existing != "" {
				return fmt.Errorf("HTTP binding %q is owned by both %q and %q", identity, existing, definition.Key)
			}
			if existing := batchHTTP[identity]; existing != "" {
				return fmt.Errorf("HTTP binding %q is owned by both %q and %q", identity, existing, definition.Key)
			}
			batchHTTP[identity] = definition.Key
		}
		if definition.LifecycleStatus != LifecycleRetired {
			for _, binding := range definition.NonHTTP {
				identity := nonHTTPIdentity(binding.Kind, binding.InvocationKey)
				if existing := registry.byNonHTTP[identity]; existing != "" {
					return fmt.Errorf("non-HTTP binding %q is owned by both %q and %q", identity, existing, definition.Key)
				}
				if existing := batchNonHTTP[identity]; existing != "" {
					return fmt.Errorf("non-HTTP binding %q is owned by both %q and %q", identity, existing, definition.Key)
				}
				batchNonHTTP[identity] = definition.Key
			}
		}
		if definition.Permission != nil {
			permission := *definition.Permission
			if registry.permissionOwners[permission.Key] != "" {
				return fmt.Errorf("permission %q has more than one declaration owner", permission.Key)
			}
			if existing := batchPermissions[permission.Key]; existing != "" {
				return fmt.Errorf("permission %q is declared by both %q and %q", permission.Key, existing, definition.Key)
			}
			batchPermissions[permission.Key] = definition.Key
		}
		normalized[index] = definition
	}
	for _, definition := range normalized {
		registry.definitions[definition.Key] = definition
		if definition.LifecycleStatus != LifecycleRetired && definition.HTTP != nil {
			registry.byHTTP[definitionHTTPIdentity(definition)] = definition.Key
		}
		if definition.LifecycleStatus != LifecycleRetired {
			for _, binding := range definition.NonHTTP {
				registry.byNonHTTP[nonHTTPIdentity(binding.Kind, binding.InvocationKey)] = definition.Key
			}
		}
		if definition.Permission != nil {
			permission := *definition.Permission
			registry.permissions[permissionIdentity(permission.Owner, permission.Key)] = permission
			registry.permissionOwners[permission.Key] = permission.Owner
		}
	}
	return nil
}

func (registry *Registry) Freeze() error {
	if registry == nil {
		return fmt.Errorf("action registry is nil")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.frozen {
		return nil
	}
	usages := map[string][]PermissionUsage{}
	ordered := make([]ActionDefinition, 0, len(registry.definitions))
	keys := make([]string, 0, len(registry.definitions))
	for key := range registry.definitions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		definition := registry.definitions[key]
		if definition.Permission != nil {
			permission := *definition.Permission
			identity := permissionIdentity(permission.Owner, permission.Key)
			usages[identity] = []PermissionUsage{{Permission: permission, Action: cloneDefinition(definition)}}
		}
		ordered = append(ordered, cloneDefinition(definition))
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Key < ordered[j].Key })
	registry.usages = usages
	registry.ordered = ordered
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return fmt.Errorf("encode action registry revision: %w", err)
	}
	digest := sha256.Sum256(encoded)
	registry.revision = hex.EncodeToString(digest[:])
	registry.frozen = true
	return nil
}

func (registry *Registry) Frozen() bool {
	if registry == nil {
		return false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registry.frozen
}

// Revision is the deterministic hash of the complete frozen Action snapshot.
// It changes when an Action binding, policy, lifecycle or owned Permission
// definition changes and is empty before Freeze.
func (registry *Registry) Revision() string {
	if registry == nil {
		return ""
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return ""
	}
	return registry.revision
}

func (registry *Registry) Definition(key string) (ActionDefinition, bool) {
	if registry == nil {
		return ActionDefinition{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return ActionDefinition{}, false
	}
	definition, exists := registry.definitions[strings.TrimSpace(key)]
	if exists && definition.LifecycleStatus == LifecycleRetired {
		return ActionDefinition{}, false
	}
	return cloneDefinition(definition), exists
}

func (registry *Registry) ResolveHTTP(method, routeTemplate string) (ActionDefinition, bool) {
	if registry == nil {
		return ActionDefinition{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return ActionDefinition{}, false
	}
	key := registry.byHTTP[httpIdentity(method, routeTemplate)]
	definition, exists := registry.definitions[key]
	if exists && definition.LifecycleStatus == LifecycleRetired {
		return ActionDefinition{}, false
	}
	return cloneDefinition(definition), exists
}

func (registry *Registry) ResolveNonHTTP(kind, invocationKey string) (ActionDefinition, bool) {
	if registry == nil {
		return ActionDefinition{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return ActionDefinition{}, false
	}
	key := registry.byNonHTTP[nonHTTPIdentity(kind, invocationKey)]
	definition, exists := registry.definitions[key]
	if exists && definition.LifecycleStatus == LifecycleRetired {
		return ActionDefinition{}, false
	}
	return cloneDefinition(definition), exists
}

func (registry *Registry) Definitions() []ActionDefinition {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return nil
	}
	result := make([]ActionDefinition, len(registry.ordered))
	for index := range registry.ordered {
		result[index] = cloneDefinition(registry.ordered[index])
	}
	return result
}

func (registry *Registry) PermissionDefinitions() []PermissionDefinition {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return nil
	}
	result := make([]PermissionDefinition, 0, len(registry.permissions))
	for _, permission := range registry.permissions {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Key != result[j].Key {
			return result[i].Key < result[j].Key
		}
		return result[i].Owner < result[j].Owner
	})
	return result
}

func (registry *Registry) PermissionUsages(owner, permissionKey string) []PermissionUsage {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if !registry.frozen {
		return nil
	}
	values := registry.usages[permissionIdentity(owner, permissionKey)]
	result := make([]PermissionUsage, len(values))
	for index := range values {
		result[index] = PermissionUsage{Permission: values[index].Permission, Action: cloneDefinition(values[index].Action)}
	}
	return result
}

func cloneDefinition(source ActionDefinition) ActionDefinition {
	result := source
	result.Authorization.Audiences = append([]string(nil), source.Authorization.Audiences...)
	result.Exposures = append([]Exposure(nil), source.Exposures...)
	if source.HTTP != nil {
		http := *source.HTTP
		result.HTTP = &http
	}
	result.Pages = append([]PageBinding(nil), source.Pages...)
	result.NonHTTP = append([]NonHTTPBinding(nil), source.NonHTTP...)
	if source.Permission != nil {
		permission := *source.Permission
		result.Permission = &permission
	}
	result.AssuranceRequired = append([]string(nil), source.AssuranceRequired...)
	result.ApprovalPolicies = append([]ApprovalPolicy(nil), source.ApprovalPolicies...)
	return result
}
