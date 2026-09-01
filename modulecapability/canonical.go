package modulecapability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// CanonicalJSON normalizes arbitrary JSON before hashing or parity comparison.
// encoding/json deterministically orders string-keyed maps.
func CanonicalJSON(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal module capability contract: %w", err)
	}
	var normalized any
	if err := json.Unmarshal(payload, &normalized); err != nil {
		return nil, fmt.Errorf("normalize module capability contract: %w", err)
	}
	result, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("canonicalize module capability contract: %w", err)
	}
	return result, nil
}

func SHA256(value any) (string, error) {
	payload, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

type digestContract struct {
	Summary    ModuleSummary      `json:"summary"`
	Categories []CategoryDocument `json:"categories"`
}

func contractSHA256(summary ModuleSummary, categories []CategoryDocument) (string, error) {
	summary.Identity.ContractSHA256 = ""
	values := append([]CategoryDocument(nil), categories...)
	for index := range values {
		values[index].ContractSHA256 = ""
	}
	return SHA256(digestContract{Summary: summary, Categories: values})
}
