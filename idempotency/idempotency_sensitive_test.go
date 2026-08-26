package idempotency

import (
	"strings"
	"testing"
)

func TestSensitiveValueDigestRequiresPepperAndDoesNotExposePlaintext(t *testing.T) {
	if _, err := SensitiveValueDigest(nil, "password"); err == nil {
		t.Fatal("missing pepper accepted")
	}
	first, err := SensitiveValueDigest([]byte("runtime-owned-pepper"), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := SensitiveValueDigest([]byte("runtime-owned-pepper"), "correct horse battery staple")
	changed, _ := SensitiveValueDigest([]byte("runtime-owned-pepper"), "different")
	if first != second || first == changed || strings.Contains(first, "correct horse") {
		t.Fatalf("unexpected sensitive digest first=%q second=%q changed=%q", first, second, changed)
	}
}
