// Package agent runs the model/tool conversation without owning SQL or a VM.
package agent

import (
	"context"
	"errors"
	"fmt"

	"hjh/internal/contracts"
)

type Input struct {
	Task            string
	Model           contracts.ModelSelection
	Instruction     string
	History         []contracts.Message
	Tools           []contracts.ToolDefinition
	MaxSteps        int
	AdditionalInput AdditionalInput
	// ProjectRequest selects a provider request from the full current transcript.
	// It never changes messages retained by the loop or persistence.
	ProjectRequest func(context.Context, int, contracts.ModelRequest) (contracts.ModelRequest, error)
}
type Result struct {
	OK         bool                         `json:"ok"`
	Task       string                       `json:"task"`
	Outcome    string                       `json:"outcome"`
	StopReason string                       `json:"stopReason"`
	FinalText  string                       `json:"finalText,omitempty"`
	Error      string                       `json:"error,omitempty"`
	Diagnostic *contracts.FailureDiagnostic `json:"diagnostic,omitempty"`
}
type AppendMessage func(contracts.Message) error
type BeforeRequest func(int, contracts.ModelRequest) error

// AdditionalInput lets the host apply typed user messages at parent-loop
// boundaries without giving the agent package access to persistence.
type AdditionalInput interface {
	Apply(context.Context, bool) ([]contracts.Message, error)
	Close(string) error
}

func Run(ctx context.Context, input Input, provider contracts.Provider, executor contracts.ToolExecutor,
	appendMessage AppendMessage, observe contracts.Observer, before BeforeRequest) (result Result, err error) {
	result.Task = input.Task
	result.Outcome = "contract_failure"
	result.StopReason = "contract_failure"
	closeReason := ""
	defer func() {
		if input.AdditionalInput != nil {
			reason := closeReason
			if reason == "" {
				reason = additionalInputCloseReason(ctx, result, err)
			}
			if closeErr := input.AdditionalInput.Close(reason); closeErr != nil {
				err = errors.Join(err, closeErr)
				if result.OK {
					result.OK = false
					result.Outcome = "failed"
					result.StopReason = "failed"
				}
			}
		}
		if err != nil {
			result.Error = err.Error()
			result.Diagnostic = contracts.FailureFor(err)
			if errors.Is(err, context.Canceled) {
				result.Outcome = "cancelled"
				result.StopReason = "cancelled"
			}
		}
	}()
	user := contracts.User(input.Task)
	user.ExecutionFact = &contracts.ExecutionFact{Kind: "user_message", Role: "user", Text: input.Task,
		Conversation: contracts.ConversationMetadata{EntityKind: "task", LocalKey: "task"}}
	if err = appendMessage(user); err != nil {
		return result, err
	}
	messages := append(append([]contracts.Message(nil), input.History...), user)
	applyInput := func(final bool) error {
		if input.AdditionalInput == nil {
			return nil
		}
		additional, applyErr := input.AdditionalInput.Apply(ctx, final)
		if applyErr != nil {
			return applyErr
		}
		messages = append(messages, additional...)
		return nil
	}
	for step := 1; ; step++ {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if input.MaxSteps > 0 && step > input.MaxSteps {
			result.StopReason = "max_steps"
			result.Outcome = "max_steps"
			return result, contracts.WithFailure(fmt.Errorf("model step limit reached (%d)", input.MaxSteps),
				contracts.FailureDiagnostic{Code: contracts.FailureStepLimit})
		}
		if err = applyInput(false); err != nil {
			return result, err
		}
		req := contracts.ModelRequest{Model: input.Model, Instruction: input.Instruction, Messages: messages, Tools: input.Tools}
		if input.ProjectRequest != nil {
			req, err = input.ProjectRequest(ctx, step, req)
		} else {
			req.WireBody, err = provider.Prepare(req)
		}
		if err != nil {
			var outcome interface{ Outcome() string }
			if errors.As(err, &outcome) {
				result.Outcome, result.StopReason = outcome.Outcome(), outcome.Outcome()
			}
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
			message := contracts.Assistant(response.Text, response.ProviderState)
			message.ExecutionFact = assistantFact(response, step, "incomplete")
			if err = appendMessage(message); err != nil {
				return result, err
			}
			result.FinalText = response.Text
			return result, fmt.Errorf("model response incomplete: finish_reason=%s", response.FinishReason)
		}
		if len(response.Calls) == 0 {
			message := contracts.Assistant(response.Text, response.ProviderState)
			message.ExecutionFact = assistantFact(response, step, "completed")
			if err = appendMessage(message); err != nil {
				return result, err
			}
			messages = append(messages, message)
			if input.AdditionalInput != nil {
				if input.MaxSteps > 0 && step >= input.MaxSteps {
					closeReason = "max_steps"
				} else {
					var additional []contracts.Message
					additional, err = input.AdditionalInput.Apply(ctx, true)
					if err != nil {
						return result, err
					}
					if len(additional) > 0 {
						messages = append(messages, additional...)
						result.OK = false
						result.Outcome = "contract_failure"
						result.StopReason = "contract_failure"
						result.FinalText = ""
						continue
					}
				}
			}
			result.OK = true
			result.Outcome = "final"
			result.StopReason = "final"
			result.FinalText = response.Text
			return result, nil
		}
		assistant := contracts.AssistantCalls(response.Calls, response.Text, response.ProviderState)
		assistant.ExecutionFact = assistantFact(response, step, "completed")
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
			value, callErr := executor.Call(contracts.WithModelToolCall(ctx, call.CallID), call.Name, call.Arguments)
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
			message.ExecutionFact = &contracts.ExecutionFact{Kind: "tool_result", Role: "tool", Name: call.Name,
				CallID: call.CallID, Outcome: toolResult.Outcome, Error: func() string {
					if callErr != nil {
						return callErr.Error()
					}
					return ""
				}(), Request: contracts.RequestIdentity{Lane: "parent", RequestOrdinal: step, ModelStep: step},
				Conversation: contracts.ConversationMetadata{EntityKind: "tool", LocalKey: call.CallID}}
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

func additionalInputCloseReason(ctx context.Context, result Result, runErr error) string {
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) || result.StopReason == "cancelled" {
		return "cancelled"
	}
	if result.StopReason == "max_steps" {
		return "max_steps"
	}
	if runErr != nil || !result.OK {
		return "run_failed"
	}
	return "completed"
}

func assistantFact(response contracts.ModelResult, step int, phase string) *contracts.ExecutionFact {
	request := contracts.RequestIdentity{Lane: "parent", RequestOrdinal: step, ModelStep: step,
		PhysicalRequestID: response.PhysicalRequestID}
	return &contracts.ExecutionFact{Kind: "assistant_message", Phase: phase, Role: "assistant", Text: response.Text,
		Request: request, Thinking: append([]contracts.ThinkingItem(nil), response.Thinking...),
		Conversation: contracts.ConversationMetadata{EntityKind: "assistant", LocalKey: "parent/" + fmt.Sprint(step) + "/answer"}}
}
