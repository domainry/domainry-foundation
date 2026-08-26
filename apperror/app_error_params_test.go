package apperror

import (
	"reflect"
	"testing"
)

func TestSanitizeParamsRedactsSensitiveValues(t *testing.T) {
	params := SanitizeParams(map[string]string{"field_path": "connection.client_secret", "actual": "plain-secret", "secret_value": "another-secret", "connector": "crm"})
	want := map[string]string{"field_path": "connection.client_secret", "actual": "[REDACTED]", "secret_value": "[REDACTED]", "connector": "crm"}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("sensitive authoring params were not redacted: %#v", params)
	}
}

func TestSanitizeParamsNonSensitiveAndValueKeyEdges(t *testing.T) {
	if SanitizeParams(nil) != nil {
		t.Fatal("nil params did not remain nil")
	}
	params := map[string]string{"field": "customer.name", "actual": "Ada", "connector": "crm"}
	if got := SanitizeParams(params); !reflect.DeepEqual(got, params) {
		t.Fatalf("non-sensitive params = %#v", got)
	}
	for _, key := range []string{"actual", "value", "input", "payload", "material"} {
		if !isAuthoringErrorValueKey(key) {
			t.Fatalf("value key rejected: %q", key)
		}
	}
	if isAuthoringErrorValueKey("connector") || isSensitiveAuthoringErrorName("customer.name") {
		t.Fatal("non-sensitive names classified as sensitive")
	}
}
