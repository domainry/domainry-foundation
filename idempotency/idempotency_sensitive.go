package idempotency

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// SensitiveValueDigest derives a stable request component without persisting
// the original credential or a guessable unkeyed password digest.
func SensitiveValueDigest(pepper []byte, value string) (string, error) {
	if len(pepper) == 0 {
		return "", errors.New("idempotency sensitive fingerprint pepper is required")
	}
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil)), nil
}
