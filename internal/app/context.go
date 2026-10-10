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
	store          *history.Store
	ctx            context.Context
	executionID    string
	previousDigest string
	previousCount  int
	requests       []map[string]any
}

func digest(b []byte) string { sum := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(sum[:]) }
func canonicalDigest(v any) (string, error) {
	b, e := contracts.EncodeCanonicalJSON(contracts.JSON(v))
	if e != nil {
		return "", e
	}
	return digest(b), nil
}

func (a *contextAttribution) request(step int, request contracts.ModelRequest) error {
	type item struct {
		kind, resource, identity, media string
		bytes                           []byte
	}
	items := make([]item, 0, len(request.Messages)+len(request.Tools)+2)
	if request.Instruction != "" {
		items = append(items, item{"system", "instruction_component", "system", "text/plain; charset=utf-8", []byte(request.Instruction)})
	}
	for i, m := range request.Messages {
		b, e := contracts.EncodeJSON(m)
		if e != nil {
			return e
		}
		items = append(items, item{"message", "message", fmt.Sprintf("request/%d/message/%d", step, i), "application/vnd.henji.message+json", b})
	}
	for _, tool := range request.Tools {
		b, e := contracts.EncodeJSON(tool)
		if e != nil {
			return e
		}
		items = append(items, item{"tool_contract", "tool_contract", "tool:" + tool.Name, "application/vnd.henji.tool+json", b})
	}
	wire := request.WireBody
	items = append(items, item{"provider_wire_body", "provider_wire_body", fmt.Sprintf("request/%d/wire", step), "application/json", wire})
	insertions := make([]map[string]any, 0, len(items))
	occurrences := make([]map[string]any, 0, len(items))
	for _, item := range items {
		content := map[string]any{"digest": digest(item.bytes), "byteLength": len(item.bytes), "mediaType": item.media}
		sources := []map[string]any{{"stage": "projected", "resourceKind": item.resource, "logicalIdentity": item.identity, "contentDigest": digest(item.bytes), "lane": "parent", "modelStep": step}}
		record, e := a.store.AppendOccurrence(a.ctx, a.executionID, history.OccurrenceInput{Kind: "context_item", Payload: contracts.JSON(map[string]any{"kind": item.kind, "content": content, "sourceRelations": sources}), Content: item.bytes})
		if e != nil {
			return e
		}
		body := map[string]any{"occurrenceId": record.ID, "kind": item.kind, "content": content, "sourceRelations": sources}
		identity, e := canonicalDigest(body)
		if e != nil {
			return e
		}
		body["occurrenceDigest"] = identity
		insertions = append(insertions, map[string]any{"occurrenceId": record.ID, "occurrenceDigest": identity})
		occurrences = append(occurrences, body)
	}
	splices := []map[string]any{{"start": 0, "deleteCount": a.previousCount, "insertions": insertions}}
	var base any
	if a.previousDigest != "" {
		base = a.previousDigest
	}
	revision, e := canonicalDigest(map[string]any{"schemaVersion": 2, "lane": "parent", "sequenceKind": "user_turn", "baseRevisionDigest": base, "splices": splices, "resultItemCount": len(items)})
	if e != nil {
		return e
	}
	delta := map[string]any{"schemaVersion": 2, "requestOrdinal": step, "lane": "parent", "purpose": "user_turn", "modelStep": step, "modelSelection": request.Model, "revisionDigest": revision, "resultItemCount": len(items), "splices": splices, "occurrences": occurrences}
	if a.previousDigest != "" {
		delta["baseRevisionDigest"] = a.previousDigest
	}
	if _, e = a.store.AppendOccurrence(a.ctx, a.executionID, history.OccurrenceInput{Kind: "model_request", Payload: contracts.JSON(delta), Content: wire}); e != nil {
		return e
	}
	a.requests = append(a.requests, map[string]any{"requestOrdinal": step, "revisionDigest": revision})
	body := map[string]any{"schemaVersion": 2, "requestCount": len(a.requests), "requests": a.requests, "externalRelations": []any{}}
	manifestDigest, e := canonicalDigest(body)
	if e != nil {
		return e
	}
	body["digest"] = manifestDigest
	manifest, e := contracts.EncodeCanonicalJSON(json.RawMessage(contracts.JSON(body)))
	if e != nil {
		return e
	}
	if e = a.store.SetContext(a.ctx, a.executionID, manifest); e != nil {
		return e
	}
	a.previousDigest = revision
	a.previousCount = len(items)
	return nil
}
