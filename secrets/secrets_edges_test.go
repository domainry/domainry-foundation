package secrets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func testKey(id, material string) Key {
	return Key{ID: id, Material: []byte(strings.Repeat(material, 32))}
}

func encodeTestEnvelope(t *testing.T, envelope Envelope) string {
	t.Helper()
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return EnvelopeVersion + ":" + base64.RawURLEncoding.EncodeToString(payload)
}

func decodeTestEnvelope(t *testing.T, encoded string) Envelope {
	t.Helper()
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, EnvelopeVersion+":"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestMemoryKeyRingCoversValidationIsolationCancellationAndLifecycle(t *testing.T) {
	for name, active := range map[string]Key{
		"missing id":       {Material: []byte("material")},
		"missing material": {ID: "key"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewMemoryKeyRing(active); err == nil {
				t.Fatal("invalid active key accepted")
			}
		})
	}
	active := testKey(" active ", "a")
	old := testKey(" old ", "o")
	ring, err := NewMemoryKeyRing(active, old)
	if err != nil {
		t.Fatal(err)
	}
	active.Material[0] = 'x'
	old.Material[0] = 'x'
	got, err := ring.Active(t.Context(), "purpose")
	if err != nil || got.ID != "active" || got.Algorithm != "AES-256-GCM" || got.Material[0] == 'x' {
		t.Fatalf("active=%#v err=%v", got, err)
	}
	got.Material[0] = 'y'
	again, _ := ring.Active(t.Context(), "purpose")
	if again.Material[0] == 'y' {
		t.Fatal("key material escaped by reference")
	}
	if decryptOnly, err := ring.ByID(t.Context(), "purpose", " old "); err != nil || decryptOnly.Status != KeyDecryptOnly || decryptOnly.Algorithm != "AES-256-GCM" {
		t.Fatalf("decrypt-only=%#v err=%v", decryptOnly, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ring.Active(cancelled, "purpose"); !errors.Is(err, context.Canceled) {
		t.Fatalf("active cancellation=%v", err)
	}
	if _, err := ring.ByID(cancelled, "purpose", "old"); !errors.Is(err, context.Canceled) {
		t.Fatalf("by-id cancellation=%v", err)
	}
	if _, err := ring.ByID(t.Context(), "purpose", "missing"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing error=%v", err)
	}
	for _, invalid := range []Key{{}, {ID: "next"}} {
		if err := ring.Rotate(invalid); err == nil {
			t.Fatal("invalid rotation accepted")
		}
	}
	if err := ring.Rotate(testKey(" next ", "n")); err != nil {
		t.Fatal(err)
	}
	if previous, err := ring.ByID(t.Context(), "purpose", "active"); err != nil || previous.Status != KeyDecryptOnly {
		t.Fatalf("previous=%#v err=%v", previous, err)
	}
	if err := ring.Revoke("next"); err == nil {
		t.Fatal("active key revoked")
	}
	if err := ring.Revoke("missing"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing revoke=%v", err)
	}
	if err := ring.Revoke(" old "); err != nil {
		t.Fatal(err)
	}
	if _, err := ring.ByID(t.Context(), "purpose", "old"); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("revoked lookup=%v", err)
	}
	ring.mu.Lock()
	delete(ring.keys, ring.activeID)
	ring.mu.Unlock()
	if _, err := ring.Active(t.Context(), "purpose"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("missing active error=%v", err)
	}
	if _, err := NewMemoryKeyRing(testKey("key", "a"), testKey("key", "b")); err == nil {
		t.Fatal("duplicate decrypt-only key accepted")
	}
	for name, decryptOnly := range map[string]Key{
		"missing decrypt-only id":       {Material: []byte("material")},
		"missing decrypt-only material": {ID: "old"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewMemoryKeyRing(testKey("active", "a"), decryptOnly); err == nil {
				t.Fatal("invalid decrypt-only key accepted")
			}
		})
	}
	explicit, err := NewMemoryKeyRing(testKey("explicit-active", "a"), Key{ID: "explicit-old", Algorithm: "AES-256-GCM", Material: bytes.Repeat([]byte{'o'}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	explicit.mu.Lock()
	activeKey := explicit.keys[explicit.activeID]
	activeKey.Status = KeyDecryptOnly
	explicit.keys[explicit.activeID] = activeKey
	explicit.mu.Unlock()
	if _, err := explicit.Active(t.Context(), "purpose"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("inactive active key error=%v", err)
	}

	missingPrevious, err := NewMemoryKeyRing(testKey("previous", "p"))
	if err != nil {
		t.Fatal(err)
	}
	missingPrevious.mu.Lock()
	delete(missingPrevious.keys, missingPrevious.activeID)
	missingPrevious.mu.Unlock()
	if err := missingPrevious.Rotate(Key{ID: "explicit-next", Algorithm: "AES-256-GCM", Material: bytes.Repeat([]byte{'n'}, 32)}); err != nil {
		t.Fatal(err)
	}
}

func TestCipherCoversProviderKeyNonceAndEnvelopeFailures(t *testing.T) {
	if _, err := (Cipher{}).Encrypt(t.Context(), "workspace", "secret", []byte("value")); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("nil encrypt provider=%v", err)
	}
	if _, err := (Cipher{}).Decrypt(t.Context(), "workspace", "secret", "value"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("nil decrypt provider=%v", err)
	}
	badAlgorithm, _ := NewMemoryKeyRing(Key{ID: "bad", Algorithm: "unknown", Material: bytes.Repeat([]byte{'a'}, 32)})
	if _, err := (Cipher{Keys: badAlgorithm}).Encrypt(t.Context(), "workspace", "secret", []byte("value")); err == nil {
		t.Fatal("unsupported algorithm accepted")
	}
	badLength, _ := NewMemoryKeyRing(Key{ID: "short", Material: []byte("short")})
	if _, err := (Cipher{Keys: badLength}).Encrypt(t.Context(), "workspace", "secret", []byte("value")); err == nil {
		t.Fatal("short key accepted")
	}
	randomErr := errors.New("entropy unavailable")
	ring, _ := NewMemoryKeyRing(testKey("key", "k"))
	if _, err := (Cipher{Keys: ring, Random: failingReader{err: randomErr}}).Encrypt(t.Context(), "workspace", "secret", []byte("value")); !errors.Is(err, randomErr) {
		t.Fatalf("random error=%v", err)
	}
	cipher := Cipher{Keys: ring, Purpose: "connector"}
	encoded, err := cipher.Encrypt(t.Context(), "workspace", "secret", []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	envelope := decodeTestEnvelope(t, encoded)
	tests := []struct {
		name  string
		value string
	}{
		{name: "version", value: "v1:payload"},
		{name: "base64", value: EnvelopeVersion + ":%%%"},
		{name: "json", value: EnvelopeVersion + ":" + base64.RawURLEncoding.EncodeToString([]byte("{"))},
		{name: "envelope version", value: encodeTestEnvelope(t, Envelope{Version: "v1", KeyID: envelope.KeyID})},
		{name: "missing key", value: encodeTestEnvelope(t, Envelope{Version: EnvelopeVersion})},
		{name: "unknown key", value: encodeTestEnvelope(t, Envelope{Version: EnvelopeVersion, KeyID: "missing"})},
		{name: "algorithm", value: encodeTestEnvelope(t, Envelope{Version: EnvelopeVersion, KeyID: envelope.KeyID, Algorithm: "other"})},
		{name: "nonce base64", value: encodeTestEnvelope(t, Envelope{Version: EnvelopeVersion, KeyID: envelope.KeyID, Algorithm: envelope.Algorithm, Nonce: "%%%", Ciphertext: envelope.Ciphertext})},
		{name: "cipher base64", value: encodeTestEnvelope(t, Envelope{Version: EnvelopeVersion, KeyID: envelope.KeyID, Algorithm: envelope.Algorithm, Nonce: envelope.Nonce, Ciphertext: "%%%"})},
		{name: "nonce size", value: encodeTestEnvelope(t, Envelope{Version: EnvelopeVersion, KeyID: envelope.KeyID, Algorithm: envelope.Algorithm, Nonce: base64.RawStdEncoding.EncodeToString([]byte("short")), Ciphertext: envelope.Ciphertext})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := cipher.Decrypt(t.Context(), "workspace", "secret", test.value); err == nil {
				t.Fatal("invalid envelope accepted")
			}
		})
	}
	tampered := envelope
	sealed, _ := base64.RawStdEncoding.DecodeString(tampered.Ciphertext)
	sealed[0] ^= 0xff
	tampered.Ciphertext = base64.RawStdEncoding.EncodeToString(sealed)
	if _, err := cipher.Decrypt(t.Context(), "workspace", "secret", encodeTestEnvelope(t, tampered)); err == nil || !strings.Contains(err.Error(), "decrypt secret material") {
		t.Fatalf("tamper error=%v", err)
	}
	shortKeyCipher := Cipher{Keys: RemoteKeyProvider{ByIDFunc: func(context.Context, string, string) (Key, error) {
		return Key{ID: envelope.KeyID, Algorithm: envelope.Algorithm, Material: []byte("short")}, nil
	}}}
	if _, err := shortKeyCipher.Decrypt(t.Context(), "workspace", "secret", encoded); err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("short decrypt key error=%v", err)
	}
	if cipher.KeyID(EnvelopeVersion+":%%") != "" || cipher.KeyID(EnvelopeVersion+":"+base64.RawURLEncoding.EncodeToString([]byte("{"))) != "" {
		t.Fatal("invalid envelope exposed key ID")
	}
}

func TestProvidersCoverCancellationVersionErrorsAndRemoteAdapters(t *testing.T) {
	t.Setenv("RUNTIME_DB_V2", " material ")
	t.Setenv("RUNTIME_EMPTY", "")
	env := EnvProvider{Prefix: "runtime_"}
	value, err := env.Resolve(t.Context(), "db", "v2")
	if err != nil || string(value) != " material " {
		t.Fatalf("env value=%q err=%v", value, err)
	}
	if _, err := env.Resolve(t.Context(), "missing", ""); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing env=%v", err)
	}
	if _, err := env.Resolve(t.Context(), "empty", ""); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("empty env=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := env.Resolve(cancelled, "db", "v2"); !errors.Is(err, context.Canceled) {
		t.Fatalf("env cancellation=%v", err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "db.v2"), []byte(" file material \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := FileProvider{Root: root}
	if value, err := files.Resolve(t.Context(), "db", "v2"); err != nil || string(value) != "file material" {
		t.Fatalf("file value=%q err=%v", value, err)
	}
	if _, err := files.Resolve(t.Context(), "missing", ""); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing file=%v", err)
	}
	if _, err := files.Resolve(cancelled, "db", "v2"); !errors.Is(err, context.Canceled) {
		t.Fatalf("file cancellation=%v", err)
	}
	if _, err := files.Resolve(t.Context(), "", ""); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("directory read=%v", err)
	}
	if _, err := (FileProvider{Root: filepath.Join(root, "missing-root")}).Resolve(t.Context(), "secret", ""); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("missing root=%v", err)
	}
	if err := os.Symlink("loop", filepath.Join(root, "loop")); err == nil {
		if _, err := files.Resolve(t.Context(), "loop", ""); !errors.Is(err, ErrProviderUnavailable) {
			t.Fatalf("symlink loop=%v", err)
		}
	}

	remoteErr := errors.New("remote failed")
	if _, err := (RemoteProvider{}).Resolve(t.Context(), "name", "v1"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("nil remote=%v", err)
	}
	remote := RemoteProvider{ResolveFunc: func(_ context.Context, name, version string) ([]byte, error) {
		if name == "error" {
			return nil, remoteErr
		}
		return []byte(name + ":" + version), nil
	}}
	if value, err := remote.Resolve(t.Context(), "name", "v1"); err != nil || string(value) != "name:v1" {
		t.Fatalf("remote value=%q err=%v", value, err)
	}
	if _, err := remote.Resolve(t.Context(), "error", ""); !errors.Is(err, ErrProviderUnavailable) || !strings.Contains(err.Error(), remoteErr.Error()) {
		t.Fatalf("remote error=%v", err)
	}

	if _, err := (RemoteKeyProvider{}).Active(t.Context(), "purpose"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("nil active=%v", err)
	}
	if _, err := (RemoteKeyProvider{}).ByID(t.Context(), "purpose", "id"); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("nil by-id=%v", err)
	}
	keys := RemoteKeyProvider{
		ActiveFunc: func(context.Context, string) (Key, error) { return testKey("active", "a"), nil },
		ByIDFunc: func(_ context.Context, _, id string) (Key, error) {
			if id == "error" {
				return Key{}, remoteErr
			}
			return testKey(id, "b"), nil
		},
	}
	if key, err := keys.Active(t.Context(), "purpose"); err != nil || key.ID != "active" {
		t.Fatalf("active key=%#v err=%v", key, err)
	}
	activeFailure := RemoteKeyProvider{ActiveFunc: func(context.Context, string) (Key, error) { return Key{}, remoteErr }}
	if _, err := activeFailure.Active(t.Context(), "purpose"); !errors.Is(err, ErrProviderUnavailable) || !strings.Contains(err.Error(), remoteErr.Error()) {
		t.Fatalf("active error=%v", err)
	}
	if key, err := keys.ByID(t.Context(), "purpose", "old"); err != nil || key.ID != "old" {
		t.Fatalf("by-id key=%#v err=%v", key, err)
	}
	if _, err := keys.ByID(t.Context(), "purpose", "error"); !errors.Is(err, ErrProviderUnavailable) || !strings.Contains(err.Error(), remoteErr.Error()) {
		t.Fatalf("by-id error=%v", err)
	}
}

func TestFileProviderRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-secret")
	if err := os.WriteFile(outside, []byte("must-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if value, err := (FileProvider{Root: root}).Resolve(t.Context(), "linked", ""); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("symlink escape value=%q err=%v", value, err)
	}
}

func TestFileProviderDependencyFailures(t *testing.T) {
	originalAbs, originalReadFile := fileProviderAbs, fileProviderReadFile
	t.Cleanup(func() {
		fileProviderAbs, fileProviderReadFile = originalAbs, originalReadFile
	})

	fileProviderAbs = func(string) (string, error) { return "", errors.New("working directory unavailable") }
	if _, err := (FileProvider{Root: "relative"}).Resolve(t.Context(), "secret", ""); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("absolute root failure=%v", err)
	}

	root := t.TempDir()
	path := filepath.Join(root, "secret")
	if err := os.WriteFile(path, []byte("material"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileProviderAbs = originalAbs
	fileProviderReadFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if _, err := (FileProvider{Root: root}).Resolve(t.Context(), "secret", ""); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("secret removed after resolution error=%v", err)
	}
}

func TestRedactionCoversNilTypedCollectionsURLsAndErrors(t *testing.T) {
	if RedactMap(nil) != nil || RedactError(nil) != nil {
		t.Fatal("nil redaction changed value")
	}
	input := []map[string]any{{"password": "secret", "safe": 7}}
	redacted := RedactValue(input).([]map[string]any)
	if redacted[0]["password"] != Redacted || redacted[0]["safe"] != 7 || input[0]["password"] != "secret" {
		t.Fatalf("typed collection redaction=%#v input=%#v", redacted, input)
	}
	text := RedactText("connect https://admin:password@example.com/path and token = material")
	if strings.Contains(text, "admin") || strings.Contains(text, "password") && !strings.Contains(text, Redacted) || strings.Contains(text, "material") {
		t.Fatalf("redacted text=%q", text)
	}
	err := RedactError(fmt.Errorf("api_key=material"))
	if err == nil || err.Error() != "api_key="+Redacted {
		t.Fatalf("redacted error=%v", err)
	}
	if got := RedactText("%"); got != "%" {
		t.Fatalf("malformed URL-like text changed: %q", got)
	}
}
