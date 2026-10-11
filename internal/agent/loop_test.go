package agent

import (
	"context"
	"errors"
	"testing"

	"hjh/internal/contracts"
)

type loopProvider func(context.Context, contracts.ModelRequest) (contracts.ModelResult, error)

func (p loopProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return contracts.EncodeJSON(request)
}
func (p loopProvider) Generate(ctx context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	return p(ctx, request)
}

type loopAdditionalInput struct {
	finalMessages []contracts.Message
	applyCalls    []bool
	closeReason   string
}

func (a *loopAdditionalInput) Apply(_ context.Context, final bool) ([]contracts.Message, error) {
	a.applyCalls = append(a.applyCalls, final)
	if final && len(a.finalMessages) > 0 {
		messages := a.finalMessages
		a.finalMessages = nil
		return messages, nil
	}
	return nil, nil
}
func (a *loopAdditionalInput) Close(reason string) error { a.closeReason = reason; return nil }

func TestFinalSteeringContinuesSameRunWithMessageInNextRequest(t *testing.T) {
	steering := contracts.User("include the deadline")
	marker := true
	steering.Steering = &marker
	additional := &loopAdditionalInput{finalMessages: []contracts.Message{steering}}
	var requests []contracts.ModelRequest
	provider := loopProvider(func(_ context.Context, request contracts.ModelRequest) (contracts.ModelResult, error) {
		requests = append(requests, request)
		return contracts.ModelResult{Text: "answer"}, nil
	})
	result, err := Run(context.Background(), Input{Task: "draft", AdditionalInput: additional}, provider, nil,
		func(contracts.Message) error { return nil }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.FinalText != "answer" || len(requests) != 2 {
		t.Fatalf("loop result=%+v requests=%d", result, len(requests))
	}
	if len(requests[0].Messages) != 1 || len(requests[1].Messages) != 3 || requests[1].Messages[1].VisibleText() != "answer" || requests[1].Messages[2].VisibleText() != "include the deadline" {
		t.Fatalf("steering was not included in the next request: first=%+v second=%+v", requests[0].Messages, requests[1].Messages)
	}
	if additional.closeReason != "completed" {
		t.Fatalf("input close reason = %q, want completed", additional.closeReason)
	}
}

func TestFinalSteeringDoesNotApplyAfterStepBudgetIsExhausted(t *testing.T) {
	additional := &loopAdditionalInput{}
	provider := loopProvider(func(context.Context, contracts.ModelRequest) (contracts.ModelResult, error) {
		return contracts.ModelResult{Text: "finished"}, nil
	})
	result, err := Run(context.Background(), Input{Task: "draft", MaxSteps: 1, AdditionalInput: additional}, provider, nil,
		func(contracts.Message) error { return nil }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(additional.applyCalls) != 1 || additional.applyCalls[0] || additional.closeReason != "max_steps" {
		t.Fatalf("additional input calls=%v close=%q", additional.applyCalls, additional.closeReason)
	}
	if !result.OK || result.StopReason != "final" {
		t.Fatalf("completed final response changed by steering budget: %+v", result)
	}
}

func TestFailureAfterFinalSteeringIsNotReportedAsSuccessful(t *testing.T) {
	steering := contracts.User("continue")
	marker := true
	steering.Steering = &marker
	additional := &loopAdditionalInput{finalMessages: []contracts.Message{steering}}
	requests := 0
	provider := loopProvider(func(context.Context, contracts.ModelRequest) (contracts.ModelResult, error) {
		requests++
		if requests == 1 {
			return contracts.ModelResult{Text: "provisional"}, nil
		}
		return contracts.ModelResult{}, errors.New("provider stopped")
	})
	result, err := Run(context.Background(), Input{Task: "draft", AdditionalInput: additional}, provider, nil,
		func(contracts.Message) error { return nil }, nil, nil)
	if err == nil || result.OK || result.Outcome == "final" || result.FinalText != "" || additional.closeReason != "run_failed" {
		t.Fatalf("failed continued loop result=%+v error=%v close=%q", result, err, additional.closeReason)
	}
}
