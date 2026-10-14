package history

import (
	"bytes"
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

// compactToolResult moves an observation's top-level result value into the
// content store while retaining a digest locator in the small payload. When
// the observer supplies its original result bytes, use them instead of the
// result re-encoded as part of the enclosing payload.
func compactToolResult(kind string, payload, observedResult json.RawMessage) (json.RawMessage, []byte, bool, error) {
	if kind != "tool_result" && kind != "effect_observation" {
		return payload, nil, false, nil
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return payload, nil, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, nil, false, fmt.Errorf("history decode tool observation: %w", err)
	}
	result, ok := object["result"]
	if len(observedResult) != 0 {
		if !validJSON(observedResult) {
			return nil, nil, false, ErrInvalidJSON
		}
		result, ok = observedResult, true
	}
	if !ok {
		return payload, nil, false, nil
	}
	digest := contentDigest(result)
	delete(object, "result")
	object["resultRef"] = contracts.JSON(digest)
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, nil, false, fmt.Errorf("history encode tool result reference: %w", err)
	}
	return encoded, append([]byte(nil), result...), true, nil
}

// expandStoredToolResultPayload restores the public observation shape from a
// stored resultRef. It leaves ordinary content references untouched, so a
// large result is not returned in both Payload and Record.Content.
func expandStoredToolResultPayload(kind string, payload json.RawMessage, digest string, content []byte) (json.RawMessage, bool, error) {
	if kind != "tool_result" && kind != "effect_observation" {
		return payload, false, nil
	}
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return payload, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, false, fmt.Errorf("history decode stored tool observation: %w", err)
	}
	refRaw, ok := object["resultRef"]
	if !ok {
		return payload, false, nil
	}
	var ref string
	if err := json.Unmarshal(refRaw, &ref); err != nil {
		return nil, false, fmt.Errorf("history decode tool result reference: %w", err)
	}
	if ref == "" {
		return nil, false, ErrNotFound
	}
	if digest == "" || ref != digest || contentDigest(content) != digest {
		return nil, false, ErrNotFound
	}
	delete(object, "resultRef")
	object["result"] = append(json.RawMessage(nil), content...)
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, false, fmt.Errorf("history encode public tool observation: %w", err)
	}
	canonical, err := contracts.EncodeCanonicalJSON(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("history canonicalize public tool observation: %w", err)
	}
	return json.RawMessage(canonical), true, nil
}

func presentOccurrenceRecord(record Record) (Record, error) {
	if record.Content == nil {
		return record, nil
	}
	payload, expanded, err := expandStoredToolResultPayload(record.Kind, record.Payload,
		contentDigest(record.Content), record.Content)
	if err != nil {
		return Record{}, err
	}
	if expanded {
		record.Payload = payload
		record.Content = nil
	}
	return record, nil
}
