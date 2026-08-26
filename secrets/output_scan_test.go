package secrets

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOperationalOutputsShareSecretScanContract(t *testing.T) {
	const material = "golden-literal-secret"
	for _, surface := range []string{"log", "error", "health", "metrics", "trace", "audit"} {
		payload := RedactMap(map[string]any{"surface": surface, "details": []any{map[string]any{"access_token": material, "safe": "visible"}}})
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), material) {
			t.Fatalf("%s output leaked secret: %s", surface, encoded)
		}
		if !strings.Contains(string(encoded), Redacted) {
			t.Fatalf("%s output omitted redaction proof: %s", surface, encoded)
		}
	}
}
