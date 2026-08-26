package secrets

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRedactionCoversNestedCollectionsAndErrors(t *testing.T) {
	value := RedactMap(map[string]any{"safe": "visible", "nested": []any{map[string]any{"api-key": "material"}}, "failure": errors.New("token=abc123 request failed"), "error_text": "dsn=postgres://admin:secret@db/runtime password=hunter2"})
	if value["safe"] != "visible" {
		t.Fatalf("safe value changed: %#v", value)
	}
	nested := value["nested"].([]any)[0].(map[string]any)
	if nested["api-key"] != Redacted {
		t.Fatalf("nested secret leaked: %#v", value)
	}
	if got := fmt.Sprint(value["failure"]); got != "token="+Redacted+" request failed" {
		t.Fatalf("error secret leaked: %q", got)
	}
	if got := fmt.Sprint(value["error_text"]); strings.Contains(got, "admin") || strings.Contains(got, "secret") || strings.Contains(got, "hunter2") {
		t.Fatalf("string secret leaked: %q", got)
	}
}
