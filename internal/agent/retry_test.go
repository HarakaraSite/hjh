package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestCancelDuringRetryWaitDoesNotSendAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	provider := loopProvider(func(context.Context, contracts.ModelRequest) (contracts.ModelResult, error) {
		calls++
		return contracts.ModelResult{}, contracts.WithFailure(errors.New("connection lost"), contracts.FailureDiagnostic{Code: contracts.FailureTransport})
	})
	start := time.Now()
	_, err := generateWithRetry(ctx, provider, contracts.ModelRequest{}, func(o contracts.Observation) error {
		if o.Kind == "context" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(start) >= providerRetryDelay {
		t.Fatalf("cancelled wait called again or delayed: calls=%d err=%v elapsed=%s", calls, err, time.Since(start))
	}
}
