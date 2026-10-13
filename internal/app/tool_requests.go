package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"hjh/internal/credentials"
	"hjh/internal/host"
	"hjh/internal/providers"
)

// toolRequestProvider uses the execution's captured configuration and the same
// credential owner as model connections. Tokens never enter JS or observations.
func (f *connectionFactory) toolRequestProvider(registry *providers.Registry, configPath string) host.ProviderRequest {
	return func(ctx context.Context, args host.ProviderRequestArgs) (host.HTTPResult, error) {
		target := credentials.Target{ConfigPath: configPath, Name: args.AuthProfile,
			Kind: credentials.KindAPIKey, Profile: registry.Config.Credentials[args.AuthProfile]}
		if args.AuthProfile == f.overrideProfile {
			target.OverrideFile = f.overrideFile
		}
		binding, err := f.capture(ctx, target)
		if err != nil {
			return host.HTTPResult{}, err
		}
		resolved, err := binding.Resolve(ctx)
		if err != nil {
			return host.HTTPResult{}, err
		}
		request, err := http.NewRequestWithContext(ctx, args.Method, args.Endpoint, bytes.NewReader(args.Body))
		if err != nil {
			return host.HTTPResult{}, err
		}
		for name, value := range args.Headers {
			request.Header.Set(name, value)
		}
		request.Header.Set("Authorization", "Bearer "+resolved.Token)
		client := f.client
		if client == nil {
			client = http.DefaultClient
		}
		transport := *client
		// The legacy credential request path did not forward authentication on redirects.
		transport.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := transport.Do(request)
		if err != nil {
			return host.HTTPResult{}, &toolRequestError{cause: err, token: resolved.Token}
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return host.HTTPResult{}, &toolRequestError{cause: err, token: resolved.Token}
		}
		return host.HTTPResult{Status: response.StatusCode, Body: string(body)}, nil
	}
}

// A transport error may quote an invalid authentication value. Preserve its
// cancellation cause while omitting the actual credential from saved messages.
type toolRequestError struct {
	cause error
	token string
}

func (e *toolRequestError) Error() string {
	return strings.ReplaceAll(e.cause.Error(), e.token, "[redacted]")
}

func (e *toolRequestError) Unwrap() error { return e.cause }
