package modulecapability

import "testing"

func TestReferencedComponentsReturnsExactTransitiveClosure(t *testing.T) {
	operations := map[string]map[string]any{
		"GET /values": {
			"security":  []map[string]any{{"BearerAuth": []string{}}},
			"responses": map[string]any{"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Page"}}}}},
		},
	}
	source := map[string]any{
		"schemas": map[string]any{
			"Page":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Value"}},
			"Value":  map[string]any{"type": "object"},
			"Unused": map[string]any{"type": "string"},
		},
		"securitySchemes": map[string]any{"BearerAuth": map[string]any{"type": "http", "scheme": "bearer"}},
	}
	closure, err := ReferencedComponents(operations, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(closure["schemas"]) != 2 || closure["schemas"]["Page"] == nil || closure["schemas"]["Value"] == nil || closure["schemas"]["Unused"] != nil {
		t.Fatalf("schema closure = %#v", closure["schemas"])
	}
	if len(closure["securitySchemes"]) != 1 || closure["securitySchemes"]["BearerAuth"] == nil {
		t.Fatalf("security closure = %#v", closure["securitySchemes"])
	}
}

func TestReferencedComponentsRejectsRemoteAndMissingReferences(t *testing.T) {
	for name, reference := range map[string]string{"remote": "https://example.com/schema.json", "missing": "#/components/schemas/Missing"} {
		t.Run(name, func(t *testing.T) {
			_, err := ReferencedComponents(map[string]map[string]any{"GET /values": {"responses": map[string]any{"200": map[string]any{"$ref": reference}}}}, map[string]any{"schemas": map[string]any{}})
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
