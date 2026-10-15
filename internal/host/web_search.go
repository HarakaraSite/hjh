package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ObservedResult separates a host return value from its execution summary.
// The enclosing public tool owns the complete body in ordinary history.
type ObservedResult struct {
	Value       any
	Observation any
}

type SearchObservation struct {
	Status        int `json:"status"`
	ResultCount   int `json:"resultCount"`
	ResponseBytes int `json:"responseBytes"`
}

func (h *Host) webSearch(ctx context.Context, args ProviderRequestArgs) (ObservedResult, error) {
	if h.RequestProvider == nil {
		return ObservedResult{}, errors.New("credential-aware requests are not configured")
	}
	response, err := h.RequestProvider(ctx, args)
	if err != nil {
		return ObservedResult{}, err
	}
	summary := SearchObservation{Status: response.Status, ResponseBytes: len(response.Body)}
	result := ObservedResult{Observation: summary}
	failed := response.Status < 200 || response.Status >= 300
	if !json.Valid([]byte(response.Body)) {
		if failed {
			return result, fmt.Errorf("web search provider request failed (%d)", response.Status)
		}
		return result, errors.New("web search provider response was not valid JSON")
	}
	var parsed map[string]json.RawMessage
	objectErr := json.Unmarshal([]byte(response.Body), &parsed)
	if failed {
		details := []string{}
		for _, key := range []string{"error", "tag", "requestId"} {
			var value string
			if json.Unmarshal(parsed[key], &value) == nil && strings.TrimSpace(value) != "" {
				details = append(details, value)
			}
		}
		suffix := ""
		if len(details) > 0 {
			suffix = ": " + strings.Join(details, " · ")
		}
		return result, fmt.Errorf("web search provider request failed (%d)%s", response.Status, suffix)
	}
	var results []json.RawMessage
	if objectErr != nil || parsed == nil || json.Unmarshal(parsed["results"], &results) != nil || results == nil {
		return result, errors.New("web search provider response had no results array")
	}
	summary.ResultCount = len(results)
	return ObservedResult{Value: json.RawMessage(response.Body), Observation: summary}, nil
}
