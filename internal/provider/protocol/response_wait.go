package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// ResponseTimeout controls only waits for an HTTP response, not generation's total
// duration. Nil selects the default; zero disables that wait; positive values are seconds.
type ResponseTimeout struct {
	StartSeconds *float64 `json:"startSeconds,omitempty"`
	IdleSeconds  *float64 `json:"idleSeconds,omitempty"`
}

// Validate reports configuration errors during connection preparation.
func (config ResponseTimeout) Validate() error {
	for _, field := range []struct {
		name  string
		value *float64
	}{{"startSeconds", config.StartSeconds}, {"idleSeconds", config.IdleSeconds}} {
		if field.value != nil && *field.value < 0 {
			return fmt.Errorf("responseTimeout.%s must be zero or positive seconds", field.name)
		}
	}
	return nil
}

func timeoutSeconds(value *float64, fallback float64) float64 {
	if value != nil {
		return *value
	}
	return fallback
}

type responseWaitTimeout struct {
	stage   string
	seconds float64
}

func (err *responseWaitTimeout) Error() string {
	return fmt.Sprintf("response %s timeout (%gs)", err.stage, err.seconds)
}
func (err *responseWaitTimeout) Unwrap() error { return context.DeadlineExceeded }

// responseWait belongs to one physical request. Do and every Read remain on the
// calling goroutine. Only the interrupt runs in a timer callback, joined by stop.
type responseWait struct {
	ctx          context.Context
	cancel       context.CancelCauseFunc
	startSeconds float64
	idleSeconds  float64
	started      time.Time
	received     bool
}

func newResponseWait(parent context.Context, config ResponseTimeout) *responseWait {
	ctx, cancel := context.WithCancelCause(parent)
	return &responseWait{ctx: ctx, cancel: cancel,
		startSeconds: timeoutSeconds(config.StartSeconds, 300),
		idleSeconds:  timeoutSeconds(config.IdleSeconds, 600)}
}

func (wait *responseWait) start() func() {
	wait.started = time.Now()
	return wait.arm(nil)
}

func (wait *responseWait) arm(body io.Closer) func() {
	seconds, stage := wait.startSeconds, "start"
	if wait.received {
		seconds, stage = wait.idleSeconds, "read idle"
	}
	if seconds == 0 {
		return func() {}
	}
	duration := time.Duration(seconds * float64(time.Second))
	if !wait.received {
		duration -= time.Since(wait.started)
	}
	done := make(chan struct{})
	timer := time.AfterFunc(duration, func() {
		defer close(done)
		wait.cancel(&responseWaitTimeout{stage: stage, seconds: seconds})
		if body != nil {
			_ = body.Close()
		}
	})
	return func() {
		if !timer.Stop() {
			<-done
		}
	}
}

func (wait *responseWait) timeout() error {
	var timeout *responseWaitTimeout
	if errors.As(context.Cause(wait.ctx), &timeout) {
		return timeout
	}
	return nil
}

// transportError also works outside credential classification wrappers. Only a
// cancellation-derived Resolve failure is replaced; an actual auth failure stays intact.
func (wait *responseWait) transportError(err error) error {
	if timeout := wait.timeout(); timeout != nil &&
		(err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return timeout
	}
	return err
}

type responseWaitBody struct {
	io.ReadCloser
	wait      *responseWait
	closeOnce sync.Once
	closeErr  error
}

func (body *responseWaitBody) Read(buffer []byte) (int, error) {
	stop := body.wait.arm(body)
	n, err := body.ReadCloser.Read(buffer)
	stop()
	if n > 0 {
		body.wait.received = true
	}
	if timeout := body.wait.timeout(); timeout != nil {
		return n, timeout
	}
	return n, err
}

func (body *responseWaitBody) Close() error {
	body.closeOnce.Do(func() { body.closeErr = body.ReadCloser.Close() })
	return body.closeErr
}
