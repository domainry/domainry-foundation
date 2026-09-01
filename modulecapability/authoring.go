package modulecapability

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// NewAuthoringFragment constructs and validates the shared source envelope.
// Owners may use it in tests and direct Module callers; Plane normally obtains
// the same envelope by resolving a project-owned JSON pointer.
func NewAuthoringFragment(collection, key string, value any) (AuthoringFragment, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return AuthoringFragment{}, err
	}
	fragment := AuthoringFragment{Collection: collection, Key: key, Value: payload}
	if err := ValidateAuthoringFragment(fragment); err != nil {
		return AuthoringFragment{}, err
	}
	return fragment, nil
}

// DecodeAuthoringValue strictly decodes the unchanged fragment value into an
// owner type. Unknown fields fail closed so disclosure and owner validation do
// not silently accept different authoring shapes.
func DecodeAuthoringValue(fragment AuthoringFragment, target any) error {
	if err := ValidateAuthoringFragment(fragment); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(fragment.Value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("authoring fragment value must contain one JSON value")
		}
		return err
	}
	return nil
}

// DecodeKeyedAuthoringValue strictly decodes an emitted collection value and
// supplies its identity from the source envelope when the Ledger author did
// not repeat it inside value. If the value does declare the identity field it
// must equal the envelope key. The request fragment is never modified.
func DecodeKeyedAuthoringValue(fragment AuthoringFragment, identityField string, target any) error {
	if err := ValidateAuthoringFragment(fragment); err != nil {
		return err
	}
	identityField = strings.TrimSpace(identityField)
	if identityField == "" {
		return fmt.Errorf("authoring identity field is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(fragment.Value))
	decoder.UseNumber()
	var source map[string]any
	if err := decoder.Decode(&source); err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("authoring fragment value must be an object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("authoring fragment value must contain one JSON value")
		}
		return err
	}
	if existing, declared := source[identityField]; declared {
		value, ok := existing.(string)
		if !ok || strings.TrimSpace(value) != fragment.Key {
			return fmt.Errorf("authoring value %s must equal fragment key %q", identityField, fragment.Key)
		}
	} else {
		source[identityField] = fragment.Key
	}
	payload, err := json.Marshal(source)
	if err != nil {
		return err
	}
	strict := json.NewDecoder(bytes.NewReader(payload))
	strict.DisallowUnknownFields()
	if err := strict.Decode(target); err != nil {
		return err
	}
	if err := strict.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("authoring fragment value must contain one JSON value")
		}
		return err
	}
	return nil
}

// ReferencedFragments returns the explicitly bound context for one collection
// in its already-validated deterministic order.
func ReferencedFragments(request ValidationRequest, collection string) []AuthoringFragment {
	result := []AuthoringFragment{}
	for _, fragment := range request.ReferencedContext {
		if fragment.Collection == collection {
			result = append(result, fragment)
		}
	}
	return result
}

// FindReferencedFragment resolves one exact context identity.
func FindReferencedFragment(request ValidationRequest, collection, key string) (AuthoringFragment, bool) {
	for _, fragment := range request.ReferencedContext {
		if fragment.Collection == collection && fragment.Key == key {
			return fragment, true
		}
	}
	return AuthoringFragment{}, false
}
