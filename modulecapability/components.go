package modulecapability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ReferencedComponents returns the exact transitive local component closure
// used by a set of OpenAPI Operations. The source is the components object of
// the source-owned OpenAPI document. Remote and non-component references are
// rejected because a bounded category must be self-contained.
func ReferencedComponents(operations map[string]map[string]any, source map[string]any) (map[string]map[string]json.RawMessage, error) {
	requested := map[string]bool{}
	for _, operation := range operations {
		if err := collectComponentReferences(operation, requested); err != nil {
			return nil, err
		}
		if err := collectSecurityComponentReferences(operation["security"], requested); err != nil {
			return nil, err
		}
	}
	queue := make([]string, 0, len(requested))
	for reference := range requested {
		queue = append(queue, reference)
	}
	sort.Strings(queue)
	visited := map[string]bool{}
	result := map[string]map[string]json.RawMessage{}
	for len(queue) != 0 {
		reference := queue[0]
		queue = queue[1:]
		if visited[reference] {
			continue
		}
		visited[reference] = true
		group, key, err := splitComponentReference(reference)
		if err != nil {
			return nil, err
		}
		rawGroup, ok := source[group]
		if !ok {
			return nil, fmt.Errorf("OpenAPI component group %q is missing", group)
		}
		groupValues, ok := rawGroup.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("OpenAPI component group %q is invalid", group)
		}
		value, ok := groupValues[key]
		if !ok {
			return nil, fmt.Errorf("OpenAPI component %q is missing", reference)
		}
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode OpenAPI component %q: %w", reference, err)
		}
		if result[group] == nil {
			result[group] = map[string]json.RawMessage{}
		}
		result[group][key] = json.RawMessage(payload)
		before := len(requested)
		if err := collectComponentReferences(value, requested); err != nil {
			return nil, fmt.Errorf("OpenAPI component %q: %w", reference, err)
		}
		if len(requested) != before {
			queue = queue[:0]
			for candidate := range requested {
				if !visited[candidate] {
					queue = append(queue, candidate)
				}
			}
			sort.Strings(queue)
		}
	}
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

func collectComponentReferences(value any, references map[string]bool) error {
	switch typed := value.(type) {
	case map[string]any:
		if raw, present := typed["$ref"]; present {
			reference, ok := raw.(string)
			if !ok {
				return fmt.Errorf("OpenAPI $ref must be a string")
			}
			if _, _, err := splitComponentReference(reference); err != nil {
				return err
			}
			references[reference] = true
		}
		for key, child := range typed {
			if key == "$ref" {
				continue
			}
			if err := collectComponentReferences(child, references); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := collectComponentReferences(child, references); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectSecurityComponentReferences(value any, references map[string]bool) error {
	if value == nil {
		return nil
	}
	requirements, ok := value.([]map[string]any)
	if !ok {
		if generic, genericOK := value.([]any); genericOK {
			requirements = make([]map[string]any, 0, len(generic))
			for _, item := range generic {
				requirement, requirementOK := item.(map[string]any)
				if !requirementOK {
					return fmt.Errorf("OpenAPI security requirement is invalid")
				}
				requirements = append(requirements, requirement)
			}
		} else {
			return fmt.Errorf("OpenAPI security requirements are invalid")
		}
	}
	for _, requirement := range requirements {
		for name := range requirement {
			name = strings.TrimSpace(name)
			if name == "" {
				return fmt.Errorf("OpenAPI security scheme name is empty")
			}
			references["#/components/securitySchemes/"+escapeJSONPointer(name)] = true
		}
	}
	return nil
}

func splitComponentReference(reference string) (string, string, error) {
	const prefix = "#/components/"
	if !strings.HasPrefix(reference, prefix) {
		return "", "", fmt.Errorf("OpenAPI reference %q is not a local component reference", reference)
	}
	parts := strings.Split(strings.TrimPrefix(reference, prefix), "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("OpenAPI reference %q is not a component root", reference)
	}
	group, err := unescapeComponentJSONPointer(parts[0])
	if err != nil {
		return "", "", err
	}
	key, err := unescapeComponentJSONPointer(parts[1])
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(group) == "" || strings.TrimSpace(key) == "" {
		return "", "", fmt.Errorf("OpenAPI reference %q is invalid", reference)
	}
	return group, key, nil
}

func unescapeComponentJSONPointer(value string) (string, error) {
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '~' {
			result.WriteByte(value[index])
			continue
		}
		if index+1 == len(value) {
			return "", fmt.Errorf("OpenAPI component reference has invalid JSON Pointer escaping")
		}
		index++
		switch value[index] {
		case '0':
			result.WriteByte('~')
		case '1':
			result.WriteByte('/')
		default:
			return "", fmt.Errorf("OpenAPI component reference has invalid JSON Pointer escaping")
		}
	}
	return result.String(), nil
}
