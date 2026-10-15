package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hjh/internal/contracts"
)

func (provider *Provider) generate(ctx context.Context, request contracts.ModelRequest, observer contracts.Observer) (contracts.ModelResult, error) {
	requestID := provider.nextRequestID()
	body := request.WireBody
	if len(body) == 0 {
		var err error
		body, err = provider.Prepare(request)
		if err != nil {
			return contracts.ModelResult{}, err
		}
	}
	body = append([]byte(nil), body...)
	if err := provider.observe(observer, contracts.Observation{
		ID:    requestID,
		Kind:  "model_request",
		Name:  provider.requestName(),
		Phase: "start",
		Time:  time.Now().UTC(),
		Input: append(json.RawMessage(nil), body...),
	}); err != nil {
		return contracts.ModelResult{}, err
	}
	if provider.endpoint == "" {
		return contracts.ModelResult{}, provider.fail(observer, requestID, errors.New("provider endpoint is empty"))
	}
	wait := newResponseWait(ctx, provider.config.ResponseTimeout)
	defer wait.cancel(nil)
	httpRequest, err := http.NewRequestWithContext(wait.ctx, http.MethodPost, provider.endpoint, bytes.NewReader(body))
	if err != nil {
		return contracts.ModelResult{}, provider.fail(observer, requestID, fmt.Errorf("create provider request: %w", err))
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	for name, value := range provider.config.Headers {
		httpRequest.Header.Set(name, value)
	}
	if provider.config.SessionHeader != "" && provider.config.SessionID != "" {
		httpRequest.Header.Set(provider.config.SessionHeader, provider.config.SessionID)
	}
	stopWait := wait.start()
	response, err := provider.client.Do(httpRequest)
	stopWait()
	if response != nil {
		response.Body = &responseWaitBody{ReadCloser: response.Body, wait: wait}
		defer response.Body.Close()
	}
	if err != nil {
		err = wait.transportError(err)
		if ctx.Err() != nil {
			err = classifyProviderTransport(fmt.Errorf("provider request canceled: %w", ctx.Err()))
		} else {
			err = classifyProviderTransport(fmt.Errorf("provider request failed: %w", err))
		}
		return contracts.ModelResult{}, provider.fail(observer, requestID, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(response.Body)
		if ctx.Err() != nil {
			return contracts.ModelResult{}, provider.fail(observer, requestID,
				classifyProviderTransport(fmt.Errorf("provider error response read canceled: %w", ctx.Err())))
		}
		message := fmt.Sprintf("provider HTTP %s", response.Status)
		if detail := strings.TrimSpace(string(responseBody)); detail != "" {
			message += ": " + detail
		}
		if readErr != nil {
			message += "; reading error body: " + readErr.Error()
		}
		return contracts.ModelResult{}, provider.fail(observer, requestID, contracts.WithFailure(errors.New(message), contracts.FailureDiagnostic{
			Code:       contracts.FailureHTTP,
			HTTPStatus: response.StatusCode,
		}))
	}

	assembly := newStreamAssembly(provider.config.API)
	var progressSequence uint64
	streamErr := consumeSSE(response.Body, func(event []byte) error {
		return assembly.addEvent(provider, event, func(text string, thinking []contracts.ThinkingItem) error {
			if text == "" && len(thinking) == 0 {
				return nil
			}
			progressSequence++
			return provider.observe(observer, contracts.Observation{
				ID:                requestID + "-text-" + strconv.FormatUint(progressSequence, 10),
				Parent:            requestID,
				Kind:              "assistant_text",
				Name:              provider.requestName(),
				Phase:             "progress",
				Time:              time.Now().UTC(),
				Text:              text,
				Thinking:          thinking,
				PhysicalRequestID: requestID,
			})
		})
	})
	if streamErr != nil {
		if ctx.Err() != nil {
			streamErr = classifyProviderTransport(fmt.Errorf("provider stream canceled: %w", ctx.Err()))
		} else if !isObservationError(streamErr) && wait.timeout() != nil &&
			(errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded)) {
			streamErr = classifyProviderTransport(wait.timeout())
		} else if contracts.FailureFor(streamErr) == nil && !isObservationError(streamErr) {
			streamErr = classifyProviderTransport(fmt.Errorf("provider stream failed: %w", streamErr))
		} else {
			streamErr = fmt.Errorf("provider stream failed: %w", streamErr)
		}
		return contracts.ModelResult{}, provider.fail(observer, requestID, assembly.diagnoseFailure(streamErr))
	}
	result, err := assembly.result(provider, request)
	if err != nil {
		return contracts.ModelResult{}, provider.fail(observer, requestID, err)
	}
	result.PhysicalRequestID = requestID
	if presentJSON(result.Usage) {
		if err := provider.observe(observer, contracts.Observation{
			ID:     requestID + "-usage",
			Parent: requestID,
			Kind:   "usage",
			Name:   provider.requestName(),
			Phase:  "end",
			Time:   time.Now().UTC(),
			Result: append(json.RawMessage(nil), result.Usage...),
		}); err != nil {
			return contracts.ModelResult{}, err
		}
	}
	modelResult := map[string]any{"text": result.Text, "calls": result.Calls}
	if result.FinishReason != "" {
		modelResult["finishReason"] = result.FinishReason
	}
	if result.ProviderState != nil {
		modelResult["providerState"] = result.ProviderState
	}
	if presentJSON(result.Usage) {
		modelResult["usage"] = result.Usage
	}
	if err := provider.observe(observer, contracts.Observation{
		ID:                requestID + "-result",
		Parent:            requestID,
		Kind:              "model_result",
		Name:              provider.requestName(),
		Phase:             "end",
		Time:              time.Now().UTC(),
		Result:            contracts.JSON(modelResult),
		Text:              result.Text,
		Thinking:          result.Thinking,
		PhysicalRequestID: requestID,
	}); err != nil {
		return contracts.ModelResult{}, err
	}
	end := map[string]string{}
	if result.FinishReason != "" {
		end["finishReason"] = result.FinishReason
	}
	generationID := response.Header.Get("X-Generation-Id")
	if generationID == "" {
		generationID = assembly.responseID
	}
	if generationID != "" {
		end["generationId"] = generationID
	}
	if err := provider.observe(observer, contracts.Observation{
		ID:     requestID,
		Kind:   "model_request",
		Name:   provider.requestName(),
		Phase:  "end",
		Time:   time.Now().UTC(),
		Result: contracts.JSON(end),
	}); err != nil {
		return contracts.ModelResult{}, err
	}
	return result, nil
}

func (provider *Provider) fail(observer contracts.Observer, requestID string, err error) error {
	message := provider.redact(err.Error())
	var diagnostic *contracts.FailureDiagnostic
	if !isObservationError(err) {
		diagnostic = contracts.FailureFor(err)
	}
	_ = provider.observe(observer, contracts.Observation{
		ID:         requestID,
		Kind:       "model_request",
		Name:       provider.requestName(),
		Phase:      "error",
		Time:       time.Now().UTC(),
		Error:      message,
		Diagnostic: contracts.CloneFailure(diagnostic),
	})
	var safeErr error = err
	if message != err.Error() {
		var cause error
		switch {
		case errors.Is(err, context.Canceled):
			cause = context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			cause = context.DeadlineExceeded
		}
		safeErr = redactedProviderError{message: message, cause: cause, observation: isObservationError(err)}
	}
	if diagnostic != nil {
		return contracts.WithFailure(safeErr, *diagnostic)
	}
	return safeErr
}

func (provider *Provider) observe(observer contracts.Observer, observation contracts.Observation) error {
	if observer == nil {
		return nil
	}
	if err := observer(observation); err != nil {
		return observationError{err: fmt.Errorf("record provider observation: %w", err)}
	}
	return nil
}

type observationError struct{ err error }

func (err observationError) Error() string { return err.err.Error() }
func (err observationError) Unwrap() error { return err.err }

func isObservationError(err error) bool {
	var observationErr observationError
	if errors.As(err, &observationErr) {
		return true
	}
	var redactedErr redactedProviderError
	return errors.As(err, &redactedErr) && redactedErr.observation
}

type redactedProviderError struct {
	message     string
	cause       error
	observation bool
}

func (err redactedProviderError) Error() string { return err.message }
func (err redactedProviderError) Unwrap() error { return err.cause }

func classifyProviderTransport(err error) error {
	if contracts.FailureFor(err) != nil {
		return err
	}
	code := contracts.FailureTransport
	switch {
	case errors.Is(err, context.Canceled):
		code = contracts.FailureCancelled
	case errors.Is(err, context.DeadlineExceeded):
		code = contracts.FailureProviderTimeout
	default:
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			code = contracts.FailureProviderTimeout
		}
	}
	return contracts.WithFailure(err, contracts.FailureDiagnostic{Code: code})
}

func (provider *Provider) redact(message string) string {
	return provider.secrets.redact(message, provider.config.APIKey)
}

func protocolError(format string, values ...any) error {
	return fmt.Errorf(format, values...)
}

func decimal(value uint64) string {
	return strconv.FormatUint(value, 10)
}
