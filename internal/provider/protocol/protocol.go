// Package protocol implements the shared OpenAI-compatible Chat Completions and Responses
// transports selected by external provider definitions.
package protocol

import (
	"context"
	"net/http"
	"sync/atomic"

	"hjh/internal/contracts"
)

// Options selects protocol differences that have an external contract behind them.
type Options struct {
	Store           bool   `json:"store"`
	NamespaceTools  bool   `json:"namespaceTools"`
	ReasoningReplay bool   `json:"reasoningReplay"`
	ReasoningField  string `json:"reasoningField"`
}

// Config contains non-secret endpoint and adapter settings plus transport-only credentials.
type Config struct {
	ProviderID      string
	StateIdentity   string
	API             string
	BaseURL         string
	Path            string
	Headers         map[string]string
	Options         Options
	ResponseTimeout ResponseTimeout
	APIKey          string
	ResolveToken    func(context.Context) (string, error)
	HTTPClient      *http.Client
	SessionID       string
	SessionHeader   string
}

// Provider implements contracts.Provider for OpenAI-compatible JSON/SSE endpoints.
type Provider struct {
	config   Config
	endpoint string
	client   *http.Client
	secrets  *secretCache
	sequence uint64
}

var _ contracts.Provider = (*Provider)(nil)

// New creates a provider. Path defaults to the endpoint for API; BaseURL comes from the external
// provider definition.
func New(config Config) *Provider {
	baseURL := trimRightSlash(config.BaseURL)
	path := config.Path
	if path == "" {
		switch config.API {
		case "responses":
			path = "/responses"
		case "chat-completions":
			path = "/chat/completions"
		}
	}
	if path != "" && path[0] != '/' {
		path = "/" + path
	}
	requestEndpoint := endpoint(baseURL, path)
	resolveToken := config.ResolveToken
	if resolveToken == nil && config.APIKey != "" {
		apiKey := config.APIKey
		resolveToken = func(context.Context) (string, error) { return apiKey, nil }
	}
	secrets := &secretCache{}
	client := newAuthenticatedHTTPClient(config.HTTPClient, requestEndpoint, resolveToken, secrets)
	if config.StateIdentity == "" {
		config.StateIdentity = config.ProviderID
	}
	return &Provider{
		config:   config,
		endpoint: requestEndpoint,
		client:   client,
		secrets:  secrets,
	}
}

// Prepare encodes a common model request into the exact body Generate will send.
func (provider *Provider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	switch provider.config.API {
	case "responses":
		return provider.prepareResponses(request)
	case "chat-completions":
		return provider.prepareChatCompletions(request)
	default:
		return nil, protocolError("unsupported API %q", provider.config.API)
	}
}

// Generate sends a prepared streaming request and assembles its text, tool calls, usage, and
// provider state. Direct callers can omit WireBody; the agent loop sets it before observation.
func (provider *Provider) Generate(ctx context.Context, request contracts.ModelRequest, observer contracts.Observer) (contracts.ModelResult, error) {
	return provider.generate(ctx, request, observer)
}

func (provider *Provider) nextRequestID() string {
	return provider.requestName() + "-" + decimal(atomic.AddUint64(&provider.sequence, 1))
}

func (provider *Provider) requestName() string {
	name := provider.config.ProviderID
	if name == "" {
		name = "provider"
	}
	if provider.config.API != "" {
		name += "." + provider.config.API
	}
	return name
}

func trimRightSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}

func endpoint(baseURL, path string) string {
	if baseURL == "" {
		return ""
	}
	return baseURL + path
}
