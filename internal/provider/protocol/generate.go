package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint, bytes.NewReader(body))
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
	response, err := provider.client.Do(httpRequest)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("provider request canceled: %w", ctx.Err())
		} else {
			err = fmt.Errorf("provider request failed: %w", err)
		}
		return contracts.ModelResult{}, provider.fail(observer, requestID, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(response.Body)
		message := fmt.Sprintf("provider HTTP %s", response.Status)
		if detail := strings.TrimSpace(string(responseBody)); detail != "" {
			message += ": " + detail
		}
		if readErr != nil {
			message += "; reading error body: " + readErr.Error()
		}
		return contracts.ModelResult{}, provider.fail(observer, requestID, errors.New(message))
	}

	assembly := newStreamAssembly(provider.config.API)
	var progressSequence uint64
	streamErr := consumeSSE(response.Body, func(event []byte) error {
		return assembly.addEvent(provider, event, func(text string) error {
			progressSequence++
			return provider.observe(observer, contracts.Observation{
				ID:     requestID + "-text-" + strconv.FormatUint(progressSequence, 10),
				Parent: requestID,
				Kind:   "assistant_text",
				Name:   provider.requestName(),
				Phase:  "progress",
				Time:   time.Now().UTC(),
				Result: contracts.JSON(map[string]string{"text": text}),
			})
		})
	})
	if streamErr != nil {
		if ctx.Err() != nil {
			streamErr = fmt.Errorf("provider stream canceled: %w", ctx.Err())
		} else {
			streamErr = fmt.Errorf("provider stream failed: %w", streamErr)
		}
		return contracts.ModelResult{}, provider.fail(observer, requestID, streamErr)
	}
	result, err := assembly.result(provider, request)
	if err != nil {
		return contracts.ModelResult{}, provider.fail(observer, requestID, err)
	}
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
	if result.ProviderState != nil {
		modelResult["providerState"] = result.ProviderState
	}
	if presentJSON(result.Usage) {
		modelResult["usage"] = result.Usage
	}
	if err := provider.observe(observer, contracts.Observation{
		ID:     requestID + "-result",
		Parent: requestID,
		Kind:   "model_result",
		Name:   provider.requestName(),
		Phase:  "end",
		Time:   time.Now().UTC(),
		Result: contracts.JSON(modelResult),
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
	_ = provider.observe(observer, contracts.Observation{
		ID:    requestID,
		Kind:  "model_request",
		Name:  provider.requestName(),
		Phase: "error",
		Time:  time.Now().UTC(),
		Error: message,
	})
	if message == err.Error() {
		return err
	}
	return errors.New(message)
}

func (provider *Provider) observe(observer contracts.Observer, observation contracts.Observation) error {
	if observer == nil {
		return nil
	}
	if err := observer(observation); err != nil {
		return fmt.Errorf("record provider observation: %w", err)
	}
	return nil
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
