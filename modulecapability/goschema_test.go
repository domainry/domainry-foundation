package modulecapability

import "testing"

func TestJSONSchemaForGoValueKeepsJSONWireShape(t *testing.T) {
	type child struct {
		Key string `json:"key"`
	}
	type sample struct {
		Name     string         `json:"name"`
		Optional string         `json:"optional,omitempty"`
		Children []child        `json:"children"`
		Labels   map[string]any `json:"labels,omitempty"`
	}
	schema := JSONSchemaForGoValue(sample{})
	properties := schema["properties"].(map[string]any)
	if properties["children"].(map[string]any)["type"] != "array" || properties["labels"].(map[string]any)["type"] != "object" {
		t.Fatalf("schema=%#v", schema)
	}
	required := schema["required"].([]string)
	if len(required) != 2 || required[0] != "children" || required[1] != "name" {
		t.Fatalf("required=%v", required)
	}
}
