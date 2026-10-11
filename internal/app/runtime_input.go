package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"hjh/internal/agent"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

type steeringAdapter struct {
	store    *history.Store
	journal  *journal
	source   contracts.SteeringSource
	close    sync.Once
	closeErr error
}

func (a *steeringAdapter) Apply(ctx context.Context, final bool) ([]contracts.Message, error) {
	pending, err := a.source.Pending(ctx, final)
	if err != nil {
		return nil, err
	}
	messages := make([]contracts.Message, 0, len(pending))
	for _, input := range pending {
		body, err := a.store.Content(a.journal.ctx, input.Receipt.ContentDigest)
		if err != nil {
			return messages, err
		}
		var message contracts.Message
		if err := json.Unmarshal(body, &message); err != nil {
			return messages, fmt.Errorf("app decode accepted steering message: %w", err)
		}
		if message.Role != "user" {
			return messages, fmt.Errorf("app accepted steering message has role %q", message.Role)
		}
		receipt := input.Receipt
		if err := a.journal.appendSteeringMessage(message, body, &receipt); err != nil {
			return messages, err
		}
		a.source.Applied(input, receipt.Revision)
		messages = append(messages, message)
	}
	return messages, nil
}

func (a *steeringAdapter) Close(reason string) error {
	a.close.Do(func() { a.closeErr = a.source.Close(reason) })
	return a.closeErr
}

func steeringCloseReason(ctx context.Context, result agent.Result, runErr error) string {
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
