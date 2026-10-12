package history

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"hjh/internal/contracts"
)

type ChildInstructionDecisionInput struct {
	Receipt  contracts.ChildInstructionReceipt
	Decision string
	Content  []byte
}

// AppendChildInstructionDecision stores accepted text once as Message content
// and records later state transitions as control_decision occurrences.
func (s *Store) AppendChildInstructionDecision(ctx context.Context, input ChildInstructionDecisionInput) (contracts.ChildInstructionReceipt, Record, error) {
	receipt := input.Receipt
	switch input.Decision {
	case "accepted":
		if receipt.Status != "accepted" || len(input.Content) == 0 {
			return contracts.ChildInstructionReceipt{}, Record{}, fmt.Errorf("history child instruction acceptance is incomplete")
		}
		var message contracts.Message
		if err := json.Unmarshal(input.Content, &message); err != nil || message.Role != "user" || message.Steering == nil || !*message.Steering {
			return contracts.ChildInstructionReceipt{}, Record{}, fmt.Errorf("history child instruction content is not a steering user message")
		}
		receipt.ContentDigest = contentDigest(input.Content)
	case "unapplied":
		if receipt.Status != "unapplied" || receipt.ContentDigest == "" || receipt.Reason == "" || len(input.Content) != 0 {
			return contracts.ChildInstructionReceipt{}, Record{}, fmt.Errorf("history child instruction unapplied decision is incomplete")
		}
	default:
		return contracts.ChildInstructionReceipt{}, Record{}, fmt.Errorf("history child instruction decision %q is unsupported", input.Decision)
	}
	payload := contracts.JSON(map[string]any{
		"operation": "agent_instruction",
		"decision":  input.Decision,
		"receipt":   receipt,
	})
	record, err := s.AppendOccurrence(ctx, receipt.ExecutionID, OccurrenceInput{
		Kind: "control_decision", Payload: payload, Content: input.Content,
	})
	if err != nil {
		return contracts.ChildInstructionReceipt{}, Record{}, err
	}
	return receipt, record, nil
}

// ChildInstructionReceipts returns the latest persisted decision for each
// child instruction in admission order.
func (s *Store) ChildInstructionReceipts(ctx context.Context, executionID string) ([]contracts.ChildInstructionReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM semantic_records
		WHERE execution_id=? AND kind='control_decision'
		AND json_extract(payload_json,'$.operation')='agent_instruction' ORDER BY ordinal`, executionID)
	if err != nil {
		return nil, fmt.Errorf("history read child instruction decisions: %w", err)
	}
	defer rows.Close()
	type decision struct {
		Operation string                            `json:"operation"`
		Receipt   contracts.ChildInstructionReceipt `json:"receipt"`
	}
	latest := map[string]contracts.ChildInstructionReceipt{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("history scan child instruction decision: %w", err)
		}
		var saved decision
		if err := json.Unmarshal(payload, &saved); err != nil {
			return nil, fmt.Errorf("history decode child instruction decision: %w", err)
		}
		if saved.Operation == "agent_instruction" && saved.Receipt.InstructionID != "" {
			latest[saved.Receipt.InstructionID] = saved.Receipt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history read child instruction decisions: %w", err)
	}
	receipts := make([]contracts.ChildInstructionReceipt, 0, len(latest))
	for _, receipt := range latest {
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].Sequence < receipts[j].Sequence })
	return receipts, nil
}
