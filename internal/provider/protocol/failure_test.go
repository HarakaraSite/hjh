package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestGenerateClassifiesMalformedToolCalls(t *testing.T) {
	testCases := []struct {
		name        string
		fragment    string
		parseReason string
		field       string
	}{
		{
			name:        "invalid arguments",
			fragment:    `{"index":0,"id":"call_1","type":"function","function":{"name":"run_js","arguments":"{broken"}}`,
			parseReason: "invalid_tool_arguments",
			field:       "choices[0].delta.tool_calls[0].function.arguments",
		},
		{
			name:        "missing call id",
			fragment:    `{"index":0,"type":"function","function":{"name":"run_js","arguments":"{}"}}`,
			parseReason: "incomplete_tool_call",
			field:       "choices[0].delta.tool_calls[0].id",
		},
		{
			name:        "missing function name",
			fragment:    `{"index":0,"id":"call_1","type":"function","function":{"arguments":"{}"}}`,
			parseReason: "incomplete_tool_call",
			field:       "choices[0].delta.tool_calls[0].function.name",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			body := fmt.Sprintf("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[%s]}}]}\n\ndata: [DONE]\n\n", testCase.fragment)
			observations, err := generateFailureFixture(t, http.StatusOK, body, nil, "")
			if err == nil {
				t.Fatal("Generate succeeded for malformed tool call")
			}
			want := contracts.FailureDiagnostic{Code: contracts.FailureResponse, ParseReason: testCase.parseReason, Field: testCase.field}
			assertProviderFailure(t, err, observations, want)
		})
	}
}

func TestCancellationWhileReadingHTTPErrorBodyPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reading := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &http.Client{Transport: protocolFailureRoundTripper(func(request *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(request)
		if err == nil {
			response.Body = &failureReadSignal{ReadCloser: response.Body, reading: reading}
		}
		return response, err
	})}
	provider := New(Config{ProviderID: "cancel-body", API: "chat-completions", BaseURL: server.URL, Path: "/chat/completions", HTTPClient: client})
	done := make(chan error, 1)
	go func() {
		_, err := provider.Generate(ctx, contracts.ModelRequest{Model: contracts.ModelSelection{ModelID: "test"}}, nil)
		done <- err
	}()
	<-reading
	cancel()
	select {
	case err := <-done:
		diagnostic := contracts.FailureFor(err)
		if !errors.Is(err, context.Canceled) || diagnostic == nil || diagnostic.Code != contracts.FailureCancelled {
			t.Fatalf("HTTP error-body cancellation = %v; diagnostic=%+v", err, diagnostic)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP error-body cancellation did not settle")
	}
}

type failureReadSignal struct {
	io.ReadCloser
	reading chan struct{}
}

func (body *failureReadSignal) Read(buffer []byte) (int, error) {
	if body.reading != nil {
		close(body.reading)
		body.reading = nil
	}
	return body.ReadCloser.Read(buffer)
}

func TestGenerateClassifiesHTTPTransportTimeoutAndCancellation(t *testing.T) {
	t.Run("HTTP status", func(t *testing.T) {
		observations, err := generateFailureFixture(t, http.StatusTooManyRequests, `{"error":"busy"}`, nil, "")
		if err == nil {
			t.Fatal("Generate succeeded for HTTP failure")
		}
		assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureHTTP, HTTPStatus: http.StatusTooManyRequests})
	})

	t.Run("transport read failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack connection: %v", err)
				return
			}
			_ = connection.Close()
		}))
		defer server.Close()
		observations, err := generateFailureFromServer(t, server, nil, "")
		if err == nil {
			t.Fatal("Generate succeeded after the server closed the response connection")
		}
		assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureTransport})
	})

	t.Run("HTTP client deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			_, _ = io.WriteString(w, "late response")
		}))
		defer server.Close()
		client := &http.Client{Timeout: 10 * time.Millisecond}
		observations, err := generateFailureFromServer(t, server, client, "")
		if err == nil {
			t.Fatal("Generate succeeded after the HTTP client deadline")
		}
		assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureProviderTimeout})
	})

	t.Run("stream read deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()
		client := &http.Client{Timeout: 20 * time.Millisecond}
		observations, err := generateFailureFromServer(t, server, client, "")
		if err == nil {
			t.Fatal("Generate succeeded after the stream read deadline")
		}
		assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureProviderTimeout})
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer server.Close()
		provider := New(Config{ProviderID: "failure-test", API: "chat-completions", BaseURL: server.URL})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var observations []contracts.Observation
		_, err := provider.Generate(ctx, contracts.ModelRequest{}, func(observation contracts.Observation) error {
			observations = append(observations, observation)
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Generate error = %v; want errors.Is(context.Canceled)", err)
		}
		assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureCancelled})
	})
}

func TestGenerateRedactsHTTPFailureAndKeepsClassification(t *testing.T) {
	const apiKey = "failure-test-secret"
	observations, err := generateFailureFixture(t, http.StatusUnauthorized, `{"message":"rejected `+apiKey+`"}`, nil, apiKey)
	if err == nil || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("Generate error = %v; want a redacted HTTP failure", err)
	}
	assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureHTTP, HTTPStatus: http.StatusUnauthorized})
	for _, observation := range observations {
		encoded, marshalErr := json.Marshal(observation)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(observation.Error, apiKey) || strings.Contains(string(encoded), apiKey) {
			t.Fatalf("credential entered returned observation: %s", encoded)
		}
	}
}

func TestGeneratePreservesExistingCredentialClassification(t *testing.T) {
	credentialFailure := contracts.WithFailure(errors.New("credential refresh failed"), contracts.FailureDiagnostic{Code: contracts.FailureMissingCredential})
	provider := New(Config{
		ProviderID: "failure-test",
		API:        "chat-completions",
		BaseURL:    "http://provider.invalid",
		HTTPClient: &http.Client{Transport: protocolFailureRoundTripper(func(*http.Request) (*http.Response, error) {
			return nil, credentialFailure
		})},
	})
	var observations []contracts.Observation
	_, err := provider.Generate(context.Background(), contracts.ModelRequest{}, func(observation contracts.Observation) error {
		observations = append(observations, observation)
		return nil
	})
	if err == nil {
		t.Fatal("Generate succeeded after credential resolution failed")
	}
	assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureMissingCredential})
}

func TestGenerateClassifiesStreamReadNetTimeout(t *testing.T) {
	provider := New(Config{
		ProviderID: "failure-test",
		API:        "chat-completions",
		BaseURL:    "http://provider.invalid",
		HTTPClient: &http.Client{Transport: protocolFailureRoundTripper(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       protocolFailureBody{err: protocolTimeoutError{}},
				Request:    request,
			}, nil
		})},
	})
	var observations []contracts.Observation
	_, err := provider.Generate(context.Background(), contracts.ModelRequest{}, func(observation contracts.Observation) error {
		observations = append(observations, observation)
		return nil
	})
	if err == nil {
		t.Fatal("Generate succeeded after the response body timed out")
	}
	assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureProviderTimeout})
}

func TestGenerateDoesNotClassifyObserverFailureAsProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := New(Config{ProviderID: "failure-test", API: "chat-completions", BaseURL: server.URL})
	var observations []contracts.Observation
	_, err := provider.Generate(context.Background(), contracts.ModelRequest{}, func(observation contracts.Observation) error {
		observations = append(observations, observation)
		if observation.Kind == "assistant_text" {
			return errors.New("observation sink unavailable")
		}
		return nil
	})
	if err == nil {
		t.Fatal("Generate succeeded after an observer failure")
	}
	if diagnostic := contracts.FailureFor(err); diagnostic != nil {
		t.Fatalf("observer failure classified as provider failure: %#v", diagnostic)
	}
	for _, observation := range observations {
		if observation.Kind == "model_request" && observation.Phase == "error" && observation.Diagnostic != nil {
			t.Fatalf("observer failure observation has a provider diagnostic: %#v", observation)
		}
	}
}

func generateFailureFixture(t *testing.T, status int, body string, client *http.Client, apiKey string) ([]contracts.Observation, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	observations, err := generateFailureFromServer(t, server, client, apiKey)
	return observations, err
}

func generateFailureFromServer(t *testing.T, server *httptest.Server, client *http.Client, apiKey string) ([]contracts.Observation, error) {
	t.Helper()
	provider := New(Config{ProviderID: "failure-test", API: "chat-completions", BaseURL: server.URL, HTTPClient: client, APIKey: apiKey})
	var observations []contracts.Observation
	_, err := provider.Generate(context.Background(), contracts.ModelRequest{}, func(observation contracts.Observation) error {
		observations = append(observations, observation)
		return nil
	})
	return observations, err
}

func assertProviderFailure(t *testing.T, err error, observations []contracts.Observation, want contracts.FailureDiagnostic) {
	t.Helper()
	got := contracts.FailureFor(err)
	if got == nil || *got != want {
		t.Fatalf("FailureFor(%v) = %#v; want %#v", err, got, want)
	}
	for _, observation := range observations {
		if observation.Kind == "model_request" && observation.Phase == "error" {
			if observation.Diagnostic == nil || *observation.Diagnostic != want {
				t.Fatalf("error observation diagnostic = %#v; want %#v", observation.Diagnostic, want)
			}
			return
		}
	}
	t.Fatalf("no provider error observation found: %#v", observations)
}

type protocolFailureRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper protocolFailureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

type protocolFailureBody struct{ err error }

func (body protocolFailureBody) Read([]byte) (int, error) { return 0, body.err }
func (body protocolFailureBody) Close() error             { return nil }

type protocolTimeoutError struct{}

func (protocolTimeoutError) Error() string   { return "provider stream read timed out" }
func (protocolTimeoutError) Timeout() bool   { return true }
func (protocolTimeoutError) Temporary() bool { return true }
