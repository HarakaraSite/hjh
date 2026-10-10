// Package agent runs the model/tool conversation without owning SQL or a VM.
package agent

import (
	"context"
	"errors"
	"fmt"

	"hjh/internal/contracts"
)

type Input struct {
	Task        string
	Model       contracts.ModelSelection
	Instruction string
	History     []contracts.Message
	Tools       []contracts.ToolDefinition
	MaxSteps    int
}
type Result struct {
	OK         bool   `json:"ok"`
	Task       string `json:"task"`
	Outcome    string `json:"outcome"`
	StopReason string `json:"stopReason"`
	FinalText  string `json:"finalText,omitempty"`
	Error      string `json:"error,omitempty"`
}
type AppendMessage func(contracts.Message) error
type BeforeRequest func(int, contracts.ModelRequest) error

func Run(ctx context.Context, input Input, provider contracts.Provider, executor contracts.ToolExecutor,
	appendMessage AppendMessage, observe contracts.Observer, before BeforeRequest) (result Result, err error) {
	result.Task = input.Task
	result.Outcome = "contract_failure"
	result.StopReason = "contract_failure"
	defer func() {
		if err != nil {
			result.Error = err.Error()
			if errors.Is(err, context.Canceled) {
				result.Outcome = "cancelled"
				result.StopReason = "cancelled"
			}
		}
	}()
	user := contracts.User(input.Task)
	if err = appendMessage(user); err != nil {
		return result, err
	}
	messages := append(append([]contracts.Message(nil), input.History...), user)
	for step := 1; ; step++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if input.MaxSteps > 0 && step > input.MaxSteps {
			result.StopReason = "max_steps"
			result.Outcome = "max_steps"
			return result, fmt.Errorf("model step limit reached (%d)", input.MaxSteps)
		}
		req := contracts.ModelRequest{Model: input.Model, Instruction: input.Instruction, Messages: messages, Tools: input.Tools}
		req.WireBody, err = provider.Prepare(req)
		if err != nil {
			return result, err
		}
		if before != nil {
			if err = before(step, req); err != nil {
				return result, err
			}
		}
		stepObserver := func(o contracts.Observation) error {
			o.ModelStep = step
			if observe != nil {
				return observe(o)
			}
			return nil
		}
		var response contracts.ModelResult
		response, err = provider.Generate(ctx, req, stepObserver)
		if err != nil {
			return result, err
		}
		if response.FinishReason == "length" {
			// Preserve the observed answer without adopting an unfinished turn or
			// executing partially generated tool arguments.
			if err = appendMessage(contracts.Assistant(response.Text, response.ProviderState)); err != nil {
				return result, err
			}
			result.FinalText = response.Text
			return result, fmt.Errorf("model response incomplete: finish_reason=%s", response.FinishReason)
		}
		if len(response.Calls) == 0 {
			if err = appendMessage(contracts.Assistant(response.Text, response.ProviderState)); err != nil {
				return result, err
			}
			result.OK = true
			result.Outcome = "final"
			result.StopReason = "final"
			result.FinalText = response.Text
			return result, nil
		}
		assistant := contracts.AssistantCalls(response.Calls, response.Text, response.ProviderState)
		if err = appendMessage(assistant); err != nil {
			return result, err
		}
		messages = append(messages, assistant)
		for _, call := range response.Calls {
			if err = ctx.Err(); err != nil {
				return result, err
			}
			if observe != nil {
				if err = stepObserver(contracts.Observation{ID: call.CallID, Kind: "model_tool", Name: call.Name, Phase: "started", Input: call.Arguments}); err != nil {
					return result, err
				}
			}
			value, callErr := executor.Call(contracts.WithCallParent(ctx, call.CallID), call.Name, call.Arguments)
			var executionErr *contracts.ExecutionError
			if errors.As(callErr, &executionErr) {
				return result, callErr
			}
			toolResult := contracts.ToolResult{Kind: "tool_result", CallID: call.CallID, Name: call.Name, Text: string(value), Outcome: "success"}
			if callErr != nil {
				toolResult.Outcome = "error"
				toolResult.Text = callErr.Error()
				toolResult.Failure = contracts.JSON(map[string]any{"operation": call.Name, "message": callErr.Error()})
			}
			if observe != nil {
				o := contracts.Observation{ID: call.CallID, Kind: "model_tool", Name: call.Name, Phase: "completed", Result: value}
				if callErr != nil {
					o.Phase = "failed"
					o.Error = callErr.Error()
				}
				if err = stepObserver(o); err != nil {
					return result, err
				}
			}
			// Persist each completed tool result before starting another call, so
			// cancellation preserves results already returned to this execution.
			message := contracts.ToolResults([]contracts.ToolResult{toolResult})
			if err = appendMessage(message); err != nil {
				return result, err
			}
			messages = append(messages, message)
			if callErr != nil && ctx.Err() != nil {
				return result, ctx.Err()
			}
		}
	}
}
