package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestEnvelopeBindsScopeAndSupportsOnlineRotation(t *testing.T) {
	oldKey := Key{ID: "key-1", Material: []byte(strings.Repeat("1", 32))}
	ring, err := NewMemoryKeyRing(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	cipher := Cipher{Keys: ring, Purpose: "connector-credential"}
	oldCiphertext, err := cipher.Encrypt(t.Context(), "workspace-a", "stripe", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if cipher.KeyID(oldCiphertext) != "key-1" {
		t.Fatalf("ciphertext did not preserve key version: %s", oldCiphertext)
	}
	if _, err := cipher.Decrypt(t.Context(), "workspace-b", "stripe", oldCiphertext); err == nil {
		t.Fatal("ciphertext moved across workspaces decrypted")
	}
	if _, err := cipher.Decrypt(t.Context(), "workspace-a", "other", oldCiphertext); err == nil {
		t.Fatal("ciphertext moved across secret keys decrypted")
	}
	if err := ring.Rotate(Key{ID: "key-2", Material: []byte(strings.Repeat("2", 32))}); err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(t.Context(), "workspace-a", "stripe", oldCiphertext)
	if err != nil || string(plain) != "secret" {
		t.Fatalf("decrypt-only overlap failed: plain=%q err=%v", plain, err)
	}
	newCiphertext, err := cipher.Encrypt(t.Context(), "workspace-a", "stripe", []byte("new-secret"))
	if err != nil || cipher.KeyID(newCiphertext) != "key-2" {
		t.Fatalf("new active key not used: ciphertext=%s err=%v", newCiphertext, err)
	}
	if err := ring.Revoke("key-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.Decrypt(t.Context(), "workspace-a", "stripe", oldCiphertext); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("revoked key did not fail closed: %v", err)
	}
}

func TestProviderOutageFailsClosed(t *testing.T) {
	cipher := Cipher{Keys: unavailableKeys{}, Purpose: "test"}
	if _, err := cipher.Encrypt(t.Context(), "w", "k", []byte("value")); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected provider unavailable, got %v", err)
	}
}

type unavailableKeys struct{}

func (unavailableKeys) Active(context.Context, string) (Key, error) {
	return Key{}, ErrProviderUnavailable
}
func (unavailableKeys) ByID(context.Context, string, string) (Key, error) {
	return Key{}, ErrProviderUnavailable
}
