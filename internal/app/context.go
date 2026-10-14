package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

type contextAttribution struct {
	store       *history.Store
	ctx         context.Context
	executionID string
	recall      *recallAttribution
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// request records only the small input facts used by /context and recall.
// The encoded WireBody is owned by this record and is committed before the
// provider is called. A later prepared request replaces that one body.
func (a *contextAttribution) request(step int, request contracts.ModelRequest) error {
	instructionCount := 0
	if request.Instruction != "" {
		instructionCount = 1
	}
	// Keep /context's existing item count: message, tool, instruction, and the
	// one saved provider WireBody item.
	itemCount := len(request.Messages) + len(request.Tools) + instructionCount + 1
	metadata := map[string]any{
		"schemaVersion":     1,
		"requestOrdinal":    step,
		"modelStep":         step,
		"modelSelection":    request.Model,
		"itemCount":         itemCount,
		"messageCount":      len(request.Messages),
		"toolContractCount": len(request.Tools),
		"instructionCount":  instructionCount,
		"wireBytes":         len(request.WireBody),
	}
	if a.recall != nil {
		metadata["recallRelationRecordId"] = a.recall.recordID
	}
	externalRelations := json.RawMessage(`[]`)
	if a.recall != nil {
		externalRelations = contracts.JSON([]map[string]any{a.recall.externalRelation()})
	}
	_, err := a.store.AppendLatestRequest(a.ctx, a.executionID, history.OccurrenceInput{
		Kind: "model_request", Payload: contracts.JSON(metadata), Content: request.WireBody,
	}, externalRelations)
	if err != nil {
		return fmt.Errorf("app save latest model request: %w", err)
	}
	return nil
}
