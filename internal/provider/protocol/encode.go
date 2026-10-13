package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

const namespaceName = "hjh"

func (provider *Provider) prepareChatCompletions(request contracts.ModelRequest) ([]byte, error) {
	wire := map[string]any{
		"model":    request.Model.ModelID,
		"messages": make([]map[string]any, 0, len(request.Messages)+1),
		"stream":   true,
		"stream_options": map[string]any{
			"include_usage": true,
		},
	}
	messages := wire["messages"].([]map[string]any)
	if request.Instruction != "" {
		messages = append(messages, map[string]any{"role": "system", "content": request.Instruction})
	}
	for _, message := range request.Messages {
		encoded, err := provider.encodeChatMessage(message, request.Model.ModelID)
		if err != nil {
			return nil, err
		}
		messages = append(messages, encoded...)
	}
	wire["messages"] = messages
	if len(request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  tool.InputSchema,
				},
			})
		}
		wire["tools"] = tools
	}
	if request.Model.Effort != "" && request.Model.Effort != "auto" {
		wire["reasoning_effort"] = request.Model.Effort
	}
	return json.Marshal(wire)
}

func (provider *Provider) encodeChatMessage(message contracts.Message, modelID string) ([]map[string]any, error) {
	if message.Role == "assistant" {
		if calls, ok := toolCallsFromContent(message.Content); ok && len(calls) > 0 {
			content := any(nil)
			if message.VisibleText() != "" {
				content = message.VisibleText()
			}
			wire := map[string]any{"role": "assistant", "content": content, "tool_calls": calls}
			provider.addChatReasoning(wire, message.ProviderState, modelID)
			return []map[string]any{wire}, nil
		}
		wire := map[string]any{"role": message.Role, "content": messageText(message)}
		provider.addChatReasoning(wire, message.ProviderState, modelID)
		return []map[string]any{wire}, nil
	}
	if message.Role == "tool" {
		if results, ok := toolResultsFromContent(message.Content); ok && len(results) > 0 {
			wire := make([]map[string]any, 0, len(results))
			for _, result := range results {
				toolMessage := map[string]any{
					"role":         "tool",
					"tool_call_id": result.CallID,
					"content":      result.Text,
				}
				wire = append(wire, toolMessage)
			}
			return wire, nil
		}
	}
	return []map[string]any{{"role": message.Role, "content": messageText(message)}}, nil
}

func (provider *Provider) addChatReasoning(wire map[string]any, state *contracts.ProviderState, modelID string) {
	if !provider.config.Options.ReasoningReplay || !provider.stateMatches(state, modelID) || provider.config.Options.ReasoningField == "" {
		return
	}
	if len(state.ReasoningDetails) > 0 {
		wire[provider.config.Options.ReasoningField] = state.ReasoningDetails
		return
	}
	if state.Reasoning != nil && state.Reasoning.Text != "" {
		wire[provider.config.Options.ReasoningField] = state.Reasoning.Text
	}
}

func (provider *Provider) prepareResponses(request contracts.ModelRequest) ([]byte, error) {
	wire := map[string]any{
		"model":  request.Model.ModelID,
		"input":  make([]any, 0, len(request.Messages)),
		"stream": true,
		"store":  provider.config.Options.Store,
	}
	if request.Instruction != "" {
		wire["instructions"] = request.Instruction
	}
	input := wire["input"].([]any)
	for _, message := range request.Messages {
		encoded, err := provider.encodeResponsesMessage(message, request.Model.ModelID)
		if err != nil {
			return nil, err
		}
		input = append(input, encoded...)
	}
	wire["input"] = input
	if len(request.Tools) > 0 {
		functions := make([]map[string]any, 0, len(request.Tools))
		for _, tool := range request.Tools {
			functions = append(functions, map[string]any{
				"type":        "function",
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
				// Responses may normalize omitted strictness by requiring optional
				// fields. Keep the tool's declared required/optional contract.
				"strict": false,
			})
		}
		if provider.config.Options.NamespaceTools {
			wire["tools"] = []any{map[string]any{
				"type":        "namespace",
				"name":        namespaceName,
				"description": "Tools available to hjh.",
				"tools":       functions,
			}}
		} else {
			wire["tools"] = functions
		}
	}
	reasoning := map[string]string{"summary": "auto"}
	if request.Model.Effort != "" && request.Model.Effort != "auto" {
		reasoning["effort"] = request.Model.Effort
	}
	wire["reasoning"] = reasoning
	return json.Marshal(wire)
}

func (provider *Provider) encodeResponsesMessage(message contracts.Message, modelID string) ([]any, error) {
	if provider.config.Options.ReasoningReplay && provider.config.API == "responses" && provider.stateMatches(message.ProviderState, modelID) && len(message.ProviderState.ReplayItems) > 0 {
		items := make([]any, 0, len(message.ProviderState.ReplayItems))
		for _, item := range message.ProviderState.ReplayItems {
			if !json.Valid(item) {
				return nil, fmt.Errorf("replay Responses item: invalid JSON")
			}
			items = append(items, json.RawMessage(append([]byte(nil), item...)))
		}
		return items, nil
	}
	if message.Role == "tool" {
		if results, ok := toolResultsFromContent(message.Content); ok && len(results) > 0 {
			items := make([]any, 0, len(results))
			for _, result := range results {
				item := map[string]any{
					"type":    "function_call_output",
					"call_id": result.CallID,
					"output":  result.Text,
				}
				if provider.config.Options.NamespaceTools {
					item["namespace"] = namespaceName
				}
				items = append(items, item)
			}
			return items, nil
		}
	}
	if message.Role == "assistant" {
		items := make([]any, 0, 2)
		if calls, ok := toolCallsFromContent(message.Content); ok && len(calls) > 0 {
			if message.VisibleText() != "" {
				items = append(items, map[string]any{"role": "assistant", "content": message.VisibleText()})
			}
			for _, call := range calls {
				item := map[string]any{
					"type":      "function_call",
					"call_id":   call.ID,
					"name":      call.Function["name"],
					"arguments": call.Function["arguments"],
				}
				if provider.config.Options.NamespaceTools {
					item["namespace"] = namespaceName
				}
				items = append(items, item)
			}
			return items, nil
		}
	}
	return []any{map[string]any{"role": message.Role, "content": messageText(message)}}, nil
}

func (provider *Provider) stateMatches(state *contracts.ProviderState, modelID string) bool {
	return state != nil && state.Provider == provider.config.StateIdentity && state.Model == modelID
}

func messageText(message contracts.Message) string {
	if message.Text != nil {
		return *message.Text
	}
	content := bytes.TrimSpace(message.Content)
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		return ""
	}
	var text contracts.TextContent
	if json.Unmarshal(content, &text) == nil && text.Kind == "text" {
		return text.Text
	}
	var value string
	if json.Unmarshal(content, &value) == nil {
		return value
	}
	return string(message.Content)
}

type chatToolCall struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function map[string]any `json:"function"`
}

func toolCallsFromContent(content json.RawMessage) ([]chatToolCall, bool) {
	items, ok := contentItems(content, "tool_call")
	if !ok {
		return nil, false
	}
	calls := make([]chatToolCall, 0, len(items))
	for _, item := range items {
		var call contracts.ToolCall
		if err := json.Unmarshal(item, &call); err != nil {
			return nil, false
		}
		calls = append(calls, chatToolCall{
			ID:   call.CallID,
			Type: "function",
			Function: map[string]any{
				"name":      call.Name,
				"arguments": string(call.Arguments),
			},
		})
	}
	return calls, true
}

func toolResultsFromContent(content json.RawMessage) ([]contracts.ToolResult, bool) {
	return contractItems[contracts.ToolResult](content, "tool_result")
}

func contentItems(content json.RawMessage, kind string) ([]json.RawMessage, bool) {
	return rawItems(content, kind)
}

func rawItems(content json.RawMessage, kind string) ([]json.RawMessage, bool) {
	var items []json.RawMessage
	if json.Unmarshal(content, &items) != nil || len(items) == 0 {
		return nil, false
	}
	for _, item := range items {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(item, &header) != nil || header.Kind != kind {
			return nil, false
		}
	}
	return items, true
}

func contractItems[T any](content json.RawMessage, kind string) ([]T, bool) {
	items, ok := rawItems(content, kind)
	if !ok {
		return nil, false
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		var value T
		if json.Unmarshal(item, &value) != nil {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}
