package agent

import (
	"context"
	"fmt"
	"time"

	"hjh/internal/contracts"
)

const providerRetryDelay = 2 * time.Second

// One shared retry budget per prepared model request. Tools run only after this
// returns a successful result; the request and prior tool results stay intact.
func generateWithRetry(ctx context.Context, provider contracts.Provider, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return contracts.ModelResult{}, err
		}
		var text, physicalID string
		var thinking []contracts.ThinkingItem
		positions := map[string]int{}
		result, err := provider.Generate(ctx, request, func(o contracts.Observation) error {
			o.RetryAttempt = attempt
			if o.Diagnostic != nil {
				o.Diagnostic = contracts.CloneFailure(o.Diagnostic)
				o.Diagnostic.RetryAttempt = attempt
			}
			if o.Kind == "model_request" {
				physicalID = o.ID
			}
			if o.Kind == "assistant_text" {
				if o.Text != "" {
					text = o.Text
				}
				for index, item := range o.Thinking {
					if item.Key == "" {
						item.Key = fmt.Sprintf("item/%d", index)
					}
					if position, ok := positions[item.Key]; ok {
						thinking[position] = item
					} else {
						positions[item.Key] = len(thinking)
						thinking = append(thinking, item)
					}
				}
				if o.PhysicalRequestID != "" {
					physicalID = o.PhysicalRequestID
				}
			}
			if observe != nil {
				return observe(o)
			}
			return nil
		})
		result.RetryAttempt = attempt
		if err != nil && attempt != 0 {
			if diagnostic := contracts.FailureFor(err); diagnostic != nil {
				diagnostic.RetryAttempt = attempt
				err = contracts.WithFailure(err, *diagnostic)
			}
		}
		if err == nil || attempt == 1 || ctx.Err() != nil || !retryableModelFailure(err) {
			return result, err
		}
		diagnostic := contracts.FailureFor(err)
		if observe != nil {
			if err := observe(contracts.Observation{ID: physicalID + "-retry", Parent: physicalID, Kind: "model_retry", Name: "provider_retry", Phase: "superseded", Time: time.Now().UTC(),
				Text: text, Thinking: thinking, PhysicalRequestID: physicalID, Error: err.Error(), Diagnostic: diagnostic}); err != nil {
				return contracts.ModelResult{}, err
			}
			if err := observe(contracts.Observation{Kind: "context", Name: "provider_retry", Phase: "completed", Time: time.Now().UTC(), Preview: retryNotice(diagnostic)}); err != nil {
				return contracts.ModelResult{}, err
			}
		}
		timer := time.NewTimer(providerRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return contracts.ModelResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func retryableModelFailure(err error) bool {
	diagnostic := contracts.FailureFor(err)
	if diagnostic == nil || diagnostic.FinishReason == "length" {
		return false
	}
	switch diagnostic.Code {
	case contracts.FailureTransport, contracts.FailureProviderTimeout:
		return true
	case contracts.FailureHTTP:
		return diagnostic.HTTPStatus == 429 || diagnostic.HTTPStatus >= 500 && diagnostic.HTTPStatus < 600
	case contracts.FailureResponse:
		return diagnostic.ParseReason == "incomplete_tool_call" || diagnostic.ParseReason == "invalid_tool_arguments" || diagnostic.ParseReason == "missing_terminal_event"
	}
	return false
}

func retryNotice(diagnostic *contracts.FailureDiagnostic) string {
	reason := "provider response invalid"
	switch diagnostic.Code {
	case contracts.FailureTransport:
		reason = "provider connection failed"
	case contracts.FailureProviderTimeout:
		reason = "provider deadline exceeded"
	case contracts.FailureHTTP:
		reason = "provider request failed"
	}
	return "RETRYING · " + reason + " · 1/1 · 2s"
}
