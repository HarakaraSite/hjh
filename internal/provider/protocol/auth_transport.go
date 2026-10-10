package protocol

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// NewAuthenticatedHTTPClient returns a shallow copy of client whose transport
// attaches bearerToken only when a request has the same scheme and host as
// endpoint. Authorization is removed for other origins, including a redirect
// to another port on the same hostname.
func NewAuthenticatedHTTPClient(client *http.Client, endpoint, bearerToken string) *http.Client {
	return newAuthenticatedHTTPClient(client, endpoint, func(context.Context) (string, error) {
		return bearerToken, nil
	}, nil)
}

type bearerTokenResolver func(context.Context) (string, error)

func newAuthenticatedHTTPClient(client *http.Client, endpoint string, resolveToken bearerTokenResolver, secrets *secretCache) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clientCopy := *client
	base := clientCopy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	target, _ := url.Parse(endpoint)
	scheme, host := "", ""
	if target != nil {
		scheme, host = target.Scheme, target.Host
	}
	clientCopy.Transport = bearerTransport{base: base, resolveToken: resolveToken, scheme: scheme, host: host, secrets: secrets}
	return &clientCopy
}

type bearerTransport struct {
	base         http.RoundTripper
	resolveToken bearerTokenResolver
	scheme       string
	host         string
	secrets      *secretCache
}

var _ http.RoundTripper = bearerTransport{}

func (transport bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	requestWithAuth := request.Clone(request.Context())
	requestWithAuth.Header = request.Header.Clone()
	if requestWithAuth.Header == nil {
		requestWithAuth.Header = make(http.Header)
	}
	if request.URL != nil && transport.host != "" &&
		strings.EqualFold(request.URL.Scheme, transport.scheme) &&
		strings.EqualFold(request.URL.Host, transport.host) {
		if transport.resolveToken != nil {
			token, err := transport.resolveToken(request.Context())
			if err != nil {
				return nil, fmt.Errorf("resolve credential for provider request: %w", err)
			}
			if token != "" {
				requestWithAuth.Header.Set("Authorization", "Bearer "+token)
				transport.secrets.add(token)
			}
		}
	} else {
		requestWithAuth.Header.Del("Authorization")
	}
	return transport.base.RoundTrip(requestWithAuth)
}

// secretCache keeps the latest request credential available for error
// redaction. Initial static credentials remain in Config.APIKey.
type secretCache struct {
	mu     sync.RWMutex
	latest string
}

func (cache *secretCache) add(secret string) {
	if cache == nil || secret == "" {
		return
	}
	cache.mu.Lock()
	cache.latest = secret
	cache.mu.Unlock()
}

func (cache *secretCache) redact(message, initial string) string {
	var latest string
	if cache != nil {
		cache.mu.RLock()
		latest = cache.latest
		cache.mu.RUnlock()
	}
	values := []string{initial, latest}
	if len(values[0]) < len(values[1]) {
		values[0], values[1] = values[1], values[0]
	}
	for _, secret := range values {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	return message
}
