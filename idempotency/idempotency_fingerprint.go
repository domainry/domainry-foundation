package idempotency

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

const maxCanonicalJSONNumberBytes = 4096

var canonicalJSONNumberPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// FingerprintInput contains the semantic inputs that can change execution.
// Callers must exclude transport-only and volatile values.
type FingerprintInput struct {
	UseCase       string `json:"use_case"`
	ResourceType  string `json:"resource_type"`
	TargetID      string `json:"target_id,omitempty"`
	Payload       any    `json:"payload"`
	Preconditions any    `json:"preconditions,omitempty"`
}

// Fingerprint returns a stable SHA-256 digest of canonical JSON. Go's JSON
// encoder sorts string map keys; disabling HTML escaping also keeps equivalent
// business strings independent from representation-only escaping.
func Fingerprint(input FingerprintInput) (string, error) {
	canonicalInput, err := canonicalFingerprintInput(input)
	if err != nil {
		return "", fmt.Errorf("canonicalize idempotency fingerprint input: %w", err)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonicalInput); err != nil {
		return "", fmt.Errorf("encode idempotency fingerprint input: %w", err)
	}
	canonical := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalFingerprintInput(input FingerprintInput) (FingerprintInput, error) {
	payload, err := canonicalFingerprintValue(input.Payload)
	if err != nil {
		return FingerprintInput{}, err
	}
	preconditions, err := canonicalFingerprintValue(input.Preconditions)
	if err != nil {
		return FingerprintInput{}, err
	}
	input.UseCase = norm.NFC.String(input.UseCase)
	input.ResourceType = norm.NFC.String(input.ResourceType)
	input.TargetID = norm.NFC.String(input.TargetID)
	input.Payload = payload
	input.Preconditions = preconditions
	return input, nil
}

func canonicalFingerprintValue(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	if timestamp, ok := value.(time.Time); ok {
		return timestamp.UTC().Format(time.RFC3339Nano), nil
	}
	if number, ok := value.(json.Number); ok {
		return canonicalFingerprintNumber(string(number))
	}
	reflected := reflect.ValueOf(value)
	for reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return nil, nil
		}
		reflected = reflected.Elem()
	}
	switch reflected.Kind() {
	case reflect.String:
		return norm.NFC.String(reflected.String()), nil
	case reflect.Bool:
		return reflected.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return canonicalFingerprintNumber(strconv.FormatInt(reflected.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return canonicalFingerprintNumber(strconv.FormatUint(reflected.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		floatValue := reflected.Float()
		if math.IsInf(floatValue, 0) || math.IsNaN(floatValue) {
			return nil, fmt.Errorf("unsupported non-finite number")
		}
		return canonicalFingerprintNumber(strings.ToLower(strconv.FormatFloat(floatValue, 'g', -1, reflected.Type().Bits())))
	case reflect.Slice, reflect.Array:
		items := make([]any, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			item, err := canonicalFingerprintValue(reflected.Index(index).Interface())
			if err != nil {
				return nil, err
			}
			items[index] = item
		}
		return items, nil
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return value, nil
		}
		mapped := make(map[string]any, reflected.Len())
		iterator := reflected.MapRange()
		for iterator.Next() {
			key := norm.NFC.String(iterator.Key().String())
			if _, exists := mapped[key]; exists {
				return nil, fmt.Errorf("Unicode-normalized map key collision %q", key)
			}
			item, err := canonicalFingerprintValue(iterator.Value().Interface())
			if err != nil {
				return nil, err
			}
			mapped[key] = item
		}
		return mapped, nil
	case reflect.Struct:
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		var decoded any
		_ = decoder.Decode(&decoded)
		return canonicalFingerprintValue(decoded)
	default:
		return value, nil
	}
}

func canonicalFingerprintNumber(raw string) (json.Number, error) {
	if len(raw) == 0 || len(raw) > maxCanonicalJSONNumberBytes || !canonicalJSONNumberPattern.MatchString(raw) {
		return "", fmt.Errorf("invalid JSON number %q", raw)
	}
	negative := strings.HasPrefix(raw, "-")
	unsigned := strings.TrimPrefix(raw, "-")
	exponent := int64(0)
	if exponentIndex := strings.IndexAny(unsigned, "eE"); exponentIndex >= 0 {
		parsed, err := strconv.ParseInt(unsigned[exponentIndex+1:], 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid JSON number exponent %q", raw)
		}
		exponent = parsed
		unsigned = unsigned[:exponentIndex]
	}
	fractionLength := 0
	if decimalIndex := strings.IndexByte(unsigned, '.'); decimalIndex >= 0 {
		fractionLength = len(unsigned) - decimalIndex - 1
		unsigned = unsigned[:decimalIndex] + unsigned[decimalIndex+1:]
	}
	digits := strings.TrimLeft(unsigned, "0")
	if digits == "" {
		return json.Number("0"), nil
	}
	trimmed := strings.TrimRight(digits, "0")
	trailingZeros := len(digits) - len(trimmed)
	digits = trimmed
	adjustment := int64(trailingZeros - fractionLength)
	if (adjustment > 0 && exponent > math.MaxInt64-adjustment) || (adjustment < 0 && exponent < math.MinInt64-adjustment) {
		return "", fmt.Errorf("JSON number exponent out of range %q", raw)
	}
	exponent += adjustment
	canonical := digits
	if exponent != 0 {
		canonical += "e" + strconv.FormatInt(exponent, 10)
	}
	if negative {
		canonical = "-" + canonical
	}
	return json.Number(canonical), nil
}
