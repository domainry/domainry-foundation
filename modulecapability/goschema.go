package modulecapability

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
)

// JSONSchemaForGoValue projects the JSON-visible shape of a source-owned Go
// wire type into an inline OpenAPI 3.1 schema. It intentionally derives only
// structural facts; semantic constraints remain explicit owner validation.
func JSONSchemaForGoValue(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return jsonSchemaForGoType(reflect.TypeOf(value), map[reflect.Type]bool{})
}

func jsonSchemaForGoType(value reflect.Type, stack map[reflect.Type]bool) map[string]any {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if value == reflect.TypeOf(json.RawMessage{}) {
		return map[string]any{}
	}
	switch value.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Interface:
		return map[string]any{}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": jsonSchemaForGoType(value.Elem(), stack)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": jsonSchemaForGoType(value.Elem(), stack)}
	case reflect.Struct:
		if stack[value] {
			return map[string]any{"type": "object"}
		}
		stack[value] = true
		defer delete(stack, value)
		properties := map[string]any{}
		required := []string{}
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			if field.PkgPath != "" {
				continue
			}
			tag := field.Tag.Get("json")
			name, options, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			properties[name] = jsonSchemaForGoType(field.Type, stack)
			if !strings.Contains(options, "omitempty") {
				required = append(required, name)
			}
		}
		sort.Strings(required)
		result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) != 0 {
			result["required"] = required
		}
		return result
	default:
		return map[string]any{}
	}
}
