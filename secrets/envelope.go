package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const EnvelopeVersion = "v2"

type Envelope struct {
	Version    string `json:"version"`
	KeyID      string `json:"key_id"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Cipher struct {
	Keys    KeyProvider
	Random  io.Reader
	Purpose string
}

func (c Cipher) Encrypt(ctx context.Context, workspaceID, secretKey string, plaintext []byte) (string, error) {
	if c.Keys == nil {
		return "", ErrProviderUnavailable
	}
	key, err := c.Keys.Active(ctx, c.Purpose)
	if err != nil {
		return "", err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	random := c.Random
	if random == nil {
		random = rand.Reader
	}
	if _, err := io.ReadFull(random, nonce); err != nil {
		return "", fmt.Errorf("generate secret nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, plaintext, aad(c.Purpose, workspaceID, secretKey))
	payload, _ := json.Marshal(Envelope{Version: EnvelopeVersion, KeyID: key.ID, Algorithm: key.Algorithm, Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(sealed)})
	return EnvelopeVersion + ":" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (c Cipher) Decrypt(ctx context.Context, workspaceID, secretKey, encoded string) ([]byte, error) {
	if c.Keys == nil {
		return nil, ErrProviderUnavailable
	}
	if !strings.HasPrefix(encoded, EnvelopeVersion+":") {
		return nil, fmt.Errorf("secret ciphertext version unsupported")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, EnvelopeVersion+":"))
	if err != nil {
		return nil, fmt.Errorf("secret ciphertext invalid")
	}
	var envelope Envelope
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Version != EnvelopeVersion || envelope.KeyID == "" {
		return nil, fmt.Errorf("secret envelope invalid")
	}
	key, err := c.Keys.ByID(ctx, c.Purpose, envelope.KeyID)
	if err != nil {
		return nil, err
	}
	if envelope.Algorithm != key.Algorithm {
		return nil, fmt.Errorf("secret envelope algorithm mismatch")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	nonce, nonceErr := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	sealed, sealedErr := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if nonceErr != nil || sealedErr != nil || len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("secret envelope payload invalid")
	}
	plain, err := aead.Open(nil, nonce, sealed, aad(c.Purpose, workspaceID, secretKey))
	if err != nil {
		return nil, fmt.Errorf("decrypt secret material: %w", err)
	}
	return plain, nil
}

func (c Cipher) KeyID(encoded string) string {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, EnvelopeVersion+":"))
	if err != nil {
		return ""
	}
	var envelope Envelope
	if json.Unmarshal(payload, &envelope) != nil {
		return ""
	}
	return envelope.KeyID
}

func newAEAD(key Key) (cipher.AEAD, error) {
	if key.Algorithm != "AES-256-GCM" {
		return nil, fmt.Errorf("unsupported secret algorithm %q", key.Algorithm)
	}
	material := key.Material
	if len(material) != 32 {
		return nil, fmt.Errorf("AES-256-GCM key must contain 32 bytes")
	}
	block, _ := aes.NewCipher(material)
	return cipher.NewGCM(block)
}

func aad(purpose, workspaceID, secretKey string) []byte {
	return []byte(strings.TrimSpace(purpose) + "\x00" + strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(secretKey))
}
