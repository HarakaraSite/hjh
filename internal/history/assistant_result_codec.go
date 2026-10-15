package history

import (
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

// compactAssistantResult is used only by SaveAssistantResult. A retry marker
// without a result remains self-contained; it never references a future Message.
func compactAssistantResult(payload json.RawMessage) (json.RawMessage, []byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, nil, fmt.Errorf("history decode assistant result: %w", err)
	}
	result, exists := fields["result"]
	if !exists {
		return payload, nil, nil
	}
	delete(fields, "result")
	fields["resultRef"] = contracts.JSON(contentDigest(result))
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, fmt.Errorf("history encode assistant result reference: %w", err)
	}
	return encoded, append([]byte(nil), result...), nil
}

// expandStoredAssistantResultPayload restores the public result in Payload,
// not Record.Content. Generic OccurrenceInput.Content attachments are unchanged.
func expandStoredAssistantResultPayload(kind string, payload json.RawMessage, content []byte) (json.RawMessage, bool, error) {
	if kind != "model_result" {
		return payload, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, false, fmt.Errorf("history decode stored assistant result: %w", err)
	}
	if _, referenced := fields["resultRef"]; !referenced {
		return payload, false, nil
	}
	delete(fields, "resultRef")
	fields["result"] = append(json.RawMessage(nil), content...)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, false, fmt.Errorf("history expand assistant result: %w", err)
	}
	canonical, err := contracts.EncodeCanonicalJSON(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("history canonicalize assistant result: %w", err)
	}
	return json.RawMessage(canonical), true, nil
}
