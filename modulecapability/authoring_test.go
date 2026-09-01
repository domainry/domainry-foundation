package modulecapability

import (
	"encoding/json"
	"testing"
)

func TestAuthoringFragmentHelpersPreserveSourceValue(t *testing.T) {
	fragment, err := NewAuthoringFragment("scheduled_jobs", "daily", map[string]any{"interval_seconds": 60})
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		IntervalSeconds int `json:"interval_seconds"`
	}
	if err := DecodeAuthoringValue(fragment, &value); err != nil || value.IntervalSeconds != 60 {
		t.Fatalf("value=%+v error=%v", value, err)
	}
	request := ValidationRequest{ReferencedContext: []AuthoringFragment{fragment}}
	if found, ok := FindReferencedFragment(request, "scheduled_jobs", "daily"); !ok || string(found.Value) != string(fragment.Value) {
		t.Fatalf("reference=%+v found=%v", found, ok)
	}
}

func TestDecodeKeyedAuthoringValueUsesEnvelopeIdentityWithoutMutatingSource(t *testing.T) {
	fragment := AuthoringFragment{Collection: "objects", Key: "order", Value: json.RawMessage(`{"name":"Order"}`)}
	var value struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := DecodeKeyedAuthoringValue(fragment, "key", &value); err != nil {
		t.Fatal(err)
	}
	if value.Key != "order" || value.Name != "Order" || string(fragment.Value) != `{"name":"Order"}` {
		t.Fatalf("decoded=%+v fragment=%s", value, fragment.Value)
	}
	fragment.Value = json.RawMessage(`{"key":"invoice","name":"Order"}`)
	if err := DecodeKeyedAuthoringValue(fragment, "key", &value); err == nil {
		t.Fatal("mismatched repeated identity was accepted")
	}
}
