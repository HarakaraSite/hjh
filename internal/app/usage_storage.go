package app

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// contextModelStorageJSON edits only the usage field of model.generate metadata.
// Leave the original result bytes alone when its usage needs no projection.
func contextModelStorageJSON(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw, nil
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("encode saved model.generate result: %w", err)
	}
	usage, err := usageStorageJSON(result["usage"], "attribution")
	if err != nil {
		return nil, err
	}
	if bytes.Equal(usage, result["usage"]) {
		return raw, nil
	}
	result["usage"] = usage
	return json.Marshal(result)
}

// usageStorageJSON removes only the named direct field from an object. Other
// raw JSON forms and objects that need no edit retain their original bytes.
// Decoding values as RawMessage preserves provider fields and number precision.
func usageStorageJSON(raw json.RawMessage, field string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("encode saved usage: %w", err)
	}
	if _, present := object[field]; !present {
		return raw, nil
	}
	delete(object, field)
	return json.Marshal(object)
}
