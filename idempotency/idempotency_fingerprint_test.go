package idempotency

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestFingerprintIsCanonicalAndCoversPreconditions(t *testing.T) {
	first, err := Fingerprint(FingerprintInput{UseCase: "action.execute", ResourceType: "record", TargetID: "r-1", Payload: map[string]any{"b": 2, "a": "<value>"}, Preconditions: map[string]any{"expected_version": 3}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Fingerprint(FingerprintInput{UseCase: "action.execute", ResourceType: "record", TargetID: "r-1", Payload: map[string]any{"a": "<value>", "b": 2}, Preconditions: map[string]any{"expected_version": 3}})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Fingerprint(FingerprintInput{UseCase: "action.execute", ResourceType: "record", TargetID: "r-1", Payload: map[string]any{"a": "<value>", "b": 2}, Preconditions: map[string]any{"expected_version": 4}})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("map order changed fingerprint: %s != %s", first, second)
	}
	if first == changed {
		t.Fatal("semantic precondition change must change fingerprint")
	}
}

func TestFingerprintCanonicalizesNumbersTimeAndUnicode(t *testing.T) {
	instant := time.Date(2026, 7, 19, 12, 34, 56, 123, time.UTC)
	first, err := Fingerprint(FingerprintInput{UseCase: "caf\u00e9", ResourceType: "record", TargetID: "r\u00e9sum\u00e9", Payload: map[string]any{
		"number": 1, "zero": float64(-0), "time": instant, "unicode": "\u00e9", "nested": []any{json.Number("2.0")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Fingerprint(FingerprintInput{UseCase: "cafe\u0301", ResourceType: "record", TargetID: "re\u0301sume\u0301", Payload: map[string]any{
		"nested": []any{2.0}, "unicode": "e\u0301", "time": instant.In(time.FixedZone("offset", 8*60*60)), "zero": 0, "number": 1.0,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("equivalent semantic inputs differ: %s != %s", first, second)
	}
}

func TestFingerprintPreservesLargeAndHighPrecisionJSONNumbers(t *testing.T) {
	left, err := Fingerprint(FingerprintInput{Payload: map[string]any{"value": json.Number("9007199254740992")}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Fingerprint(FingerprintInput{Payload: map[string]any{"value": json.Number("9007199254740993")}})
	if err != nil {
		t.Fatal(err)
	}
	if left == right {
		t.Fatal("distinct integers above float64 exact range collided")
	}
	decimal, err := Fingerprint(FingerprintInput{Payload: map[string]any{"value": json.Number("12345678901234567890.1000")}})
	if err != nil {
		t.Fatal(err)
	}
	scientific, err := Fingerprint(FingerprintInput{Payload: map[string]any{"value": json.Number("123456789012345678901e-1")}})
	if err != nil {
		t.Fatal(err)
	}
	if decimal != scientific {
		t.Fatalf("equivalent exact decimals differ: %s != %s", decimal, scientific)
	}
}

func TestCanonicalFingerprintNumberDoesNotAllocateExpandedExponent(t *testing.T) {
	value, err := canonicalFingerprintNumber("1e1000000")
	if err != nil || value != json.Number("1e1000000") {
		t.Fatalf("canonical large exponent=%q err=%v", value, err)
	}
	for _, raw := range []string{"01", "+1", "1.", ".1", "1e", "1e999999999999999999999999", strings.Repeat("1", maxCanonicalJSONNumberBytes+1)} {
		if _, err := canonicalFingerprintNumber(raw); err == nil {
			t.Fatalf("invalid number %q accepted", raw)
		}
	}
}

func TestFingerprintRejectsInvalidCanonicalValues(t *testing.T) {
	for _, payload := range []any{math.Inf(1), math.NaN(), json.Number("bad"), json.Number("Inf"), json.Number("NaN"), map[string]any{"\u00e9": 1, "e\u0301": 2}, []any{math.Inf(1)}, map[string]any{"bad": math.Inf(1)}, struct{ Value any }{make(chan int)}} {
		if _, err := Fingerprint(FingerprintInput{Payload: payload}); err == nil {
			t.Errorf("payload %#v was accepted", payload)
		}
	}
	if _, err := Fingerprint(FingerprintInput{Payload: "ok", Preconditions: math.Inf(1)}); err == nil {
		t.Fatal("invalid precondition was accepted")
	}
}

func TestCanonicalFingerprintValueEdges(t *testing.T) {
	value := "e\u0301"
	if got, err := canonicalFingerprintValue(&value); err != nil || got != "\u00e9" {
		t.Fatalf("pointer canonicalization = %#v, %v", got, err)
	}
	var nilPointer *string
	if got, err := canonicalFingerprintValue(nilPointer); err != nil || got != nil {
		t.Fatalf("nil pointer = %#v, %v", got, err)
	}
	if got, err := canonicalFingerprintValue([]any{}); err != nil || len(got.([]any)) != 0 {
		t.Fatalf("empty slice = %#v, %v", got, err)
	}
	if got, err := canonicalFingerprintValue(map[int]string{1: "one"}); err != nil || got == nil {
		t.Fatalf("non-string map = %#v, %v", got, err)
	}
}

func TestFingerprintRejectsUnsupportedValues(t *testing.T) {
	if _, err := Fingerprint(FingerprintInput{Payload: make(chan int)}); err == nil {
		t.Fatal("expected unsupported payload error")
	}
}
