package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

// compactToolMessageTx stores only the tool-result text locator in a tool
// Message. Result items are deliberately handled at this boundary rather than
// recursively rewriting arbitrary result JSON.
func compactToolMessageTx(ctx context.Context, tx *sql.Tx, executionID string, message json.RawMessage) (json.RawMessage, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(message, &envelope); err != nil || envelope == nil {
		return message, false, nil
	}
	var role string
	if json.Unmarshal(envelope["role"], &role) != nil || role != "tool" {
		return message, false, nil
	}
	var items []json.RawMessage
	if json.Unmarshal(envelope["content"], &items) != nil {
		return message, false, nil
	}
	changed := false
	for index, rawItem := range items {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(rawItem, &item); err != nil || item == nil {
			continue
		}
		var kind string
		if json.Unmarshal(item["kind"], &kind) != nil || kind != "tool_result" {
			continue
		}
		rawText, exists := item["text"]
		if !exists {
			continue
		}
		var text string
		if err := json.Unmarshal(rawText, &text); err != nil {
			continue
		}
		body := []byte(text)
		digest := contentDigest(body)
		if err := ensureToolBodyOwnerTx(ctx, tx, executionID, digest, body); err != nil {
			return nil, false, err
		}
		delete(item, "text")
		item["textRef"] = contracts.JSON(digest)
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, false, fmt.Errorf("history encode tool Message result reference: %w", err)
		}
		items[index] = encoded
		changed = true
	}
	if !changed {
		return message, false, nil
	}
	content, err := json.Marshal(items)
	if err != nil {
		return nil, false, fmt.Errorf("history encode tool Message content: %w", err)
	}
	envelope["content"] = content
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, false, fmt.Errorf("history encode compact tool Message: %w", err)
	}
	canonical, err := contracts.EncodeCanonicalJSON(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("history canonicalize compact tool Message: %w", err)
	}
	return json.RawMessage(canonical), true, nil
}

func ensureToolBodyOwnerTx(ctx context.Context, tx *sql.Tx, executionID, digest string, body []byte) error {
	var exists int
	// Latest model request bodies are replaceable and cannot own durable
	// Message references after the next request replaces them.
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM semantic_records WHERE execution_id=? AND content_digest=? AND kind<>'model_request')`, executionID, digest).Scan(&exists); err != nil {
		return fmt.Errorf("history check same-execution tool body owner: %w", err)
	}
	if exists != 0 {
		return nil
	}
	storedDigest, err := putContent(ctx, tx, body)
	if err != nil {
		return err
	}
	if storedDigest != digest {
		return fmt.Errorf("history tool body digest changed during storage")
	}
	if _, err := appendSemanticRecordTxWithContent(ctx, tx, executionID, "tool_body",
		json.RawMessage(`{"kind":"tool_body"}`), timestampNow(), digest); err != nil {
		return err
	}
	return nil
}

func addSemanticToolFactTextRef(payload json.RawMessage, digest string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("history decode tool Message semantic mirror: %w", ErrInvalidJSON)
	}
	object["factTextRef"] = contracts.JSON(digest)
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("history encode tool Message semantic text reference: %w", err)
	}
	canonical, err := contracts.EncodeCanonicalJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf("history canonicalize tool Message semantic text reference: %w", err)
	}
	return json.RawMessage(canonical), nil
}

// expandStoredToolMessage restores text references only in trusted tool
// Message content items. The owner lookup is constrained to this execution.
func (s *Store) expandStoredToolMessage(ctx context.Context, executionID string, message json.RawMessage) (json.RawMessage, bool, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(message, &envelope); err != nil || envelope == nil {
		return message, false, nil
	}
	var role string
	if json.Unmarshal(envelope["role"], &role) != nil || role != "tool" {
		return message, false, nil
	}
	changed := false
	if rawRef, exists := envelope["factTextRef"]; exists {
		body, err := s.readToolMessageBody(ctx, executionID, rawRef)
		if err != nil {
			return nil, false, err
		}
		delete(envelope, "factTextRef")
		envelope["text"] = contracts.JSON(string(body))
		changed = true
	}
	var items []json.RawMessage
	if json.Unmarshal(envelope["content"], &items) == nil {
		for index, rawItem := range items {
			var item map[string]json.RawMessage
			if err := json.Unmarshal(rawItem, &item); err != nil || item == nil {
				continue
			}
			var kind string
			if json.Unmarshal(item["kind"], &kind) != nil || kind != "tool_result" {
				continue
			}
			rawRef, exists := item["textRef"]
			if !exists {
				continue
			}
			body, err := s.readToolMessageBody(ctx, executionID, rawRef)
			if err != nil {
				return nil, false, err
			}
			delete(item, "textRef")
			item["text"] = contracts.JSON(string(body))
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, false, fmt.Errorf("history encode public tool Message result: %w", err)
			}
			items[index] = encoded
			changed = true
		}
	}
	if !changed {
		return message, false, nil
	}
	if items != nil {
		content, err := json.Marshal(items)
		if err != nil {
			return nil, false, fmt.Errorf("history encode public tool Message content: %w", err)
		}
		envelope["content"] = content
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, false, fmt.Errorf("history encode expanded tool Message: %w", err)
	}
	canonical, err := contracts.EncodeCanonicalJSON(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("history canonicalize expanded tool Message: %w", err)
	}
	return json.RawMessage(canonical), true, nil
}

func (s *Store) readToolMessageBody(ctx context.Context, executionID string, rawRef json.RawMessage) ([]byte, error) {
	var digest string
	if err := json.Unmarshal(rawRef, &digest); err != nil || digest == "" {
		return nil, ErrNotFound
	}
	var body []byte
	if err := s.db.QueryRowContext(ctx, `SELECT c.content_bytes
		FROM semantic_records owner JOIN contents c ON c.content_digest=owner.content_digest
		WHERE owner.execution_id=? AND owner.content_digest=?
		ORDER BY owner.ordinal LIMIT 1`, executionID, digest).Scan(&body); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read same-execution tool Message body: %w", err)
	}
	if contentDigest(body) != digest {
		return nil, ErrNotFound
	}
	return append([]byte(nil), body...), nil
}
