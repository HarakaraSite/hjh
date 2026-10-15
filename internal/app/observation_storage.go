package app

import (
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

// observationForStorage shapes the journal copy only. Its Result is used for
// both the payload and ObservedResult; hooks and live observers keep the original.
func observationForStorage(observation contracts.Observation) (contracts.Observation, error) {
	var usageField string
	switch observation.Kind {
	case "usage":
		usageField = "attribution"
	case "model_result":
		usageField = "usage"
	}
	if usageField != "" {
		result, err := usageStorageJSON(observation.Result, usageField)
		if err != nil {
			return contracts.Observation{}, err
		}
		observation.Result = result
	}
	if observation.Kind == "host" && observation.Name == "context.model.generate" {
		result, err := contextModelStorageJSON(observation.Result)
		if err != nil {
			return contracts.Observation{}, err
		}
		observation.Result = result
	}
	if observation.Kind == "model_request" {
		observation.Input = nil
	}
	if observation.Kind != "hook" || len(observation.Input) == 0 {
		return observation, nil
	}
	switch observation.Name {
	case "runtimeStart", "beforeTurn", "afterTurn", "runtimeStop":
	default:
		return observation, nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(observation.Input, &input); err != nil {
		return contracts.Observation{}, fmt.Errorf("encode saved lifecycle input: %w", err)
	}
	var hookContext map[string]json.RawMessage
	if err := json.Unmarshal(input["context"], &hookContext); err != nil {
		return contracts.Observation{}, fmt.Errorf("encode saved lifecycle context: %w", err)
	}
	var definitions []contracts.ToolDefinition
	if err := json.Unmarshal(hookContext["tools"], &definitions); err != nil {
		return contracts.Observation{}, fmt.Errorf("encode saved lifecycle tools: %w", err)
	}
	names := make([]string, 0, len(definitions))
	for _, tool := range definitions {
		names = append(names, tool.Name)
	}
	delete(hookContext, "tools")
	hookContext["toolNames"] = contracts.JSON(names)
	input["context"] = contracts.JSON(hookContext)
	observation.Input = contracts.JSON(input)
	return observation, nil
}
