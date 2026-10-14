package app

import (
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

// observationPayload shapes persistence only. Runtime hooks and live observers
// receive the original observation, including their complete tool contracts.
func observationPayload(observation contracts.Observation) (json.RawMessage, error) {
	if observation.Kind == "model_request" {
		observation.Input = nil
	}
	if observation.Kind != "hook" || len(observation.Input) == 0 {
		return contracts.JSON(observation), nil
	}
	switch observation.Name {
	case "runtimeStart", "beforeTurn", "afterTurn", "runtimeStop":
	default:
		return contracts.JSON(observation), nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(observation.Input, &input); err != nil {
		return nil, fmt.Errorf("encode saved lifecycle input: %w", err)
	}
	var hookContext map[string]json.RawMessage
	if err := json.Unmarshal(input["context"], &hookContext); err != nil {
		return nil, fmt.Errorf("encode saved lifecycle context: %w", err)
	}
	var definitions []contracts.ToolDefinition
	if err := json.Unmarshal(hookContext["tools"], &definitions); err != nil {
		return nil, fmt.Errorf("encode saved lifecycle tools: %w", err)
	}
	names := make([]string, 0, len(definitions))
	for _, tool := range definitions {
		names = append(names, tool.Name)
	}
	delete(hookContext, "tools")
	hookContext["toolNames"] = contracts.JSON(names)
	input["context"] = contracts.JSON(hookContext)
	observation.Input = contracts.JSON(input)
	return contracts.JSON(observation), nil
}
