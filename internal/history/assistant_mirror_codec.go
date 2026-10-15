package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// compactAssistantMirrorPayload keeps mirror metadata in the record. The
// existing content_digest owns the Message envelope; an explicitly handed-off
// thinking record owns thinking text. Independent Message writes keep text inline.
func compactAssistantMirrorPayload(kind string, payload json.RawMessage, thinkingRecordID string) (json.RawMessage, error) {
	if kind != "assistant_message" {
		return payload, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("history decode assistant mirror: %w", err)
	}
	if string(fields["role"]) != `"assistant"` {
		return payload, nil
	}
	delete(fields, "content")
	delete(fields, "providerState")
	delete(fields, "providerStateRecordId")
	if thinkingRecordID != "" {
		delete(fields, "text")
		fields["thinkingRecordId"], _ = json.Marshal(thinkingRecordID)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("history encode assistant mirror: %w", err)
	}
	return encoded, nil
}

// expandAssistantMirrorPayload restores only the two Message-owned fields.
// In particular, a thinking mirror's text, phase, request and conversation
// identity belong to the mirror, not to its referenced Message.
func expandAssistantMirrorPayload(kind string, payload json.RawMessage, message []byte) (json.RawMessage, error) {
	if kind != "assistant_message" {
		return payload, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("history decode assistant mirror: %w", err)
	}
	if string(fields["role"]) != `"assistant"` {
		return payload, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(message, &envelope); err != nil {
		return nil, fmt.Errorf("history decode assistant mirror Message: %w", err)
	}
	for _, key := range []string{"content", "providerState"} {
		if value, ok := envelope[key]; ok {
			fields[key] = value
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("history expand assistant mirror: %w", err)
	}
	return encoded, nil
}

// expandThinkingMirrorPayload restores only source-owned text and removes the
// storage-only reference. All identity, phase and other metadata stay local.
func expandThinkingMirrorPayload(kind string, payload json.RawMessage, text sql.NullString) (json.RawMessage, error) {
	if kind != "assistant_message" {
		return payload, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("history decode thinking mirror: %w", err)
	}
	if _, referenced := fields["thinkingRecordId"]; !referenced {
		return payload, nil
	}
	if !text.Valid {
		return nil, ErrNotFound
	}
	fields["text"], _ = json.Marshal(text.String)
	delete(fields, "thinkingRecordId")
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("history expand thinking mirror: %w", err)
	}
	return encoded, nil
}
