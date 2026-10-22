// Package entityid generates public business identifiers for independently
// addressable Domainry entities. Database row identity is a separate concern:
// entity tables use an auto-increment bigint primary key for storage and
// pagination, while values from this package cross API and module boundaries.
package entityid

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	entropyBytes    = 16
	MaxPrefixLength = 12
)

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// Generator makes entity IDs from an injectable cryptographic entropy source.
// The zero value uses crypto/rand.Reader.
type Generator struct {
	Random io.Reader
}

// New returns an ID in the canonical <prefix>_<128-bit lowercase hex> format.
func New(prefix string) (string, error) {
	return (Generator{}).New(prefix)
}

// New returns an ID in the canonical <prefix>_<128-bit lowercase hex> format.
func (generator Generator) New(prefix string) (string, error) {
	prefix, err := validatePrefix(prefix)
	if err != nil {
		return "", err
	}
	random := generator.Random
	if random == nil {
		random = rand.Reader
	}
	entropy := make([]byte, entropyBytes)
	if _, err := io.ReadFull(random, entropy); err != nil {
		return "", fmt.Errorf("generate %s ID: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(entropy), nil
}

// Derive returns the canonical ID for a source-owned entity whose identity
// must remain stable across reconciliation. Equal namespace and parts
// intentionally produce the same ID; unrelated inputs retain 128 bits of the
// SHA-256 digest.
func Derive(prefix, namespace string, parts ...string) (string, error) {
	prefix, err := validatePrefix(prefix)
	if err != nil {
		return "", err
	}
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", fmt.Errorf("derive %s ID: namespace is required", prefix)
	}
	canonical := make([]string, 0, len(parts)+1)
	canonical = append(canonical, namespace)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", fmt.Errorf("derive %s ID: identity part is required", prefix)
		}
		canonical = append(canonical, part)
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("domainry.entityid.derive.v1"))
	var length [8]byte
	for _, component := range canonical {
		binary.BigEndian.PutUint64(length[:], uint64(len(component)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(component))
	}
	sum := digest.Sum(nil)
	return prefix + "_" + hex.EncodeToString(sum[:entropyBytes]), nil
}

// Validate reports whether value follows the canonical rule for prefix.
func Validate(prefix, value string) error {
	prefix, err := validatePrefix(prefix)
	if err != nil {
		return err
	}
	marker := prefix + "_"
	if !strings.HasPrefix(value, marker) {
		return fmt.Errorf("%s ID must start with %q", prefix, marker)
	}
	encoded := strings.TrimPrefix(value, marker)
	if len(encoded) != entropyBytes*2 {
		return fmt.Errorf("%s ID must contain %d hexadecimal characters", prefix, entropyBytes*2)
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || hex.EncodeToString(decoded) != encoded {
		return fmt.Errorf("%s ID must use lowercase hexadecimal characters", prefix)
	}
	return nil
}

func validatePrefix(prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if len(prefix) > MaxPrefixLength || !prefixPattern.MatchString(prefix) {
		return "", fmt.Errorf("entity ID prefix %q must use 1-%d lowercase ASCII letters or digits and start with a letter", prefix, MaxPrefixLength)
	}
	return prefix, nil
}
