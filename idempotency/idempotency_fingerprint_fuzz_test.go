package idempotency

import "testing"

func FuzzFingerprintMapOrderInvariant(f *testing.F) {
	f.Add("alpha", "beta", int64(1))
	f.Add("caf\u00e9", "e\u0301", int64(-42))
	f.Fuzz(func(t *testing.T, firstValue, secondValue string, number int64) {
		first, err := Fingerprint(FingerprintInput{UseCase: "fuzz", ResourceType: "record", Payload: map[string]any{"first": firstValue, "second": secondValue, "number": number}})
		if err != nil {
			t.Fatal(err)
		}
		second, err := Fingerprint(FingerprintInput{UseCase: "fuzz", ResourceType: "record", Payload: map[string]any{"number": number, "second": secondValue, "first": firstValue}})
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("map insertion order changed fingerprint: %s != %s", first, second)
		}
	})
}
