package app

import (
	"context"
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

type contextModelInput struct {
	Instruction string              `json:"instruction"`
	Messages    []contracts.Message `json:"messages"`
}

type contextModelResult struct {
	Text         string          `json:"text"`
	FinishReason string          `json:"finishReason"`
	Usage        json.RawMessage `json:"usage,omitempty"`
}

// requestModelGenerator uses the already resolved connection/selection exactly
// once. It owns no execution, transcript, hooks, tools or persistent snapshots.
func requestModelGenerator(provider contracts.Provider, selection contracts.ModelSelection) func(context.Context, contextModelInput) (contextModelResult, error) {
	return func(ctx context.Context, input contextModelInput) (contextModelResult, error) {
		request := contracts.ModelRequest{Model: selection, Instruction: input.Instruction, Messages: input.Messages}
		wire, err := provider.Prepare(request)
		if err != nil {
			return contextModelResult{}, err
		}
		request.WireBody = wire
		result, err := provider.Generate(ctx, request, func(contracts.Observation) error { return nil })
		if err != nil {
			return contextModelResult{}, err
		}
		if result.FinishReason == "length" {
			return contextModelResult{}, fmt.Errorf("extension model response incomplete: finish_reason=length")
		}
		return contextModelResult{Text: result.Text, FinishReason: result.FinishReason, Usage: result.Usage}, nil
	}
}
