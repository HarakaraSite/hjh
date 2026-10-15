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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func seconds(value float64) *float64 { return &value }

func TestResponseTimeoutConfig(t *testing.T) {
	var config ResponseTimeout
	if err := json.Unmarshal([]byte(`{"idleSeconds":0}`), &config); err != nil {
		t.Fatal(err)
	}
	wait := newResponseWait(context.Background(), config)
	defer wait.cancel(nil)
	if wait.startSeconds != 300 || wait.idleSeconds != 0 {
		t.Fatalf("omitted start / disabled idle = %g / %g", wait.startSeconds, wait.idleSeconds)
	}
	defaults := newResponseWait(context.Background(), ResponseTimeout{})
	defer defaults.cancel(nil)
	if defaults.startSeconds != 300 || defaults.idleSeconds != 600 {
		t.Fatalf("defaults = %g / %g", defaults.startSeconds, defaults.idleSeconds)
	}
	if err := (ResponseTimeout{StartSeconds: seconds(-1)}).Validate(); err == nil {
		t.Fatal("negative start accepted")
	}
	if err := (ResponseTimeout{IdleSeconds: seconds(-1)}).Validate(); err == nil {
		t.Fatal("negative idle accepted")
	}
	if err := (ResponseTimeout{StartSeconds: seconds(.01), IdleSeconds: seconds(0)}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestResponseWaitHTTPStalls(t *testing.T) {
	for _, tc := range []struct {
		name        string
		headerDelay time.Duration
		headers     bool
		first       string
		stage       string
	}{
		{name: "before headers", stage: "start"},
		{name: "headers only", headers: true, stage: "start"},
		{name: "headers do not reset start", headers: true, headerDelay: 120 * time.Millisecond, stage: "start"},
		{name: "partial text", headers: true, first: "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", stage: "read idle"},
		{name: "thinking", headers: true, first: "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n", stage: "read idle"},
		{name: "heartbeat", headers: true, first: ": processing\n\n", stage: "read idle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cancelled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if tc.headerDelay > 0 {
					time.Sleep(tc.headerDelay)
				}
				if tc.headers {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, tc.first)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(cancelled)
			}))
			defer server.Close()
			client, tracking := waitTrackingClient()
			provider := New(Config{API: "chat-completions", BaseURL: server.URL, HTTPClient: client,
				Options:         Options{ReasoningField: "reasoning_content"},
				ResponseTimeout: ResponseTimeout{StartSeconds: seconds(.2), IdleSeconds: seconds(.08)}})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var observations []contracts.Observation
			started := time.Now()
			_, err := provider.Generate(ctx, contracts.ModelRequest{}, func(o contracts.Observation) error {
				observations = append(observations, o)
				return nil
			})
			assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureProviderTimeout})
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "response "+tc.stage+" timeout") {
				t.Fatalf("timeout cause/stage = %v", err)
			}
			if tc.headerDelay > 0 && time.Since(started) >= 290*time.Millisecond {
				t.Fatalf("headers reset start clock: %s", time.Since(started))
			}
			assertWaitJoined(t, tracking, cancelled)
		})
	}
}

// This wraps a real HTTP transport, recording the synchronous Body lifetime rather
// than replacing network waits with fixture readers.
type waitTrackedBody struct {
	io.ReadCloser
	active atomic.Int32
	closed atomic.Bool
}

func (body *waitTrackedBody) Read(p []byte) (int, error) {
	body.active.Add(1)
	defer body.active.Add(-1)
	return body.ReadCloser.Read(p)
}
func (body *waitTrackedBody) Close() error {
	err := body.ReadCloser.Close()
	body.closed.Store(true)
	return err
}
func waitTrackingClient() (*http.Client, *atomic.Pointer[waitTrackedBody]) {
	tracking := &atomic.Pointer[waitTrackedBody]{}
	return &http.Client{Transport: protocolFailureRoundTripper(func(r *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(r)
		if response != nil {
			body := &waitTrackedBody{ReadCloser: response.Body}
			response.Body = body
			tracking.Store(body)
		}
		return response, err
	})}, tracking
}
func assertWaitJoined(t *testing.T, tracking *atomic.Pointer[waitTrackedBody], cancelled <-chan struct{}) {
	t.Helper()
	if body := tracking.Load(); body != nil && (body.active.Load() != 0 || !body.closed.Load()) {
		t.Fatalf("Generate left Body active=%d closed=%v", body.active.Load(), body.closed.Load())
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("server request not cancelled")
	}
}

func TestResponseWaitHTTPErrorStatusSurvivesBodyStall(t *testing.T) {
	for _, status := range []int{401, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			cancelled := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "partial secret-token")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(cancelled)
			}))
			defer server.Close()
			client, tracking := waitTrackingClient()
			provider := New(Config{API: "responses", BaseURL: server.URL, APIKey: "secret-token", HTTPClient: client,
				ResponseTimeout: ResponseTimeout{IdleSeconds: seconds(.05)}})
			var observations []contracts.Observation
			_, err := provider.Generate(context.Background(), contracts.ModelRequest{}, func(o contracts.Observation) error {
				observations = append(observations, o)
				return nil
			})
			assertProviderFailure(t, err, observations, contracts.FailureDiagnostic{Code: contracts.FailureHTTP, HTTPStatus: status})
			if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "secret-token") || !strings.Contains(err.Error(), "read idle timeout") {
				t.Fatalf("HTTP stall error = %v", err)
			}
			for _, o := range observations {
				if strings.Contains(o.Error, "secret-token") {
					t.Fatal("credential leaked in observation")
				}
			}
			assertWaitJoined(t, tracking, cancelled)
		})
	}
}

func TestResponseWaitHTTPContinuedBytesHaveNoTotalCap(t *testing.T) {
	pieces := []string{
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n",
		": processing\n\n", ": processing\n\n",
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\"}\n\n",
		// A single SSE line is itself split across reads: even incomplete JSON is progress.
		"data: {\"type\":\"response.function_call_arguments.delta\",", "\"output_index\":0,\"delta\":\"}\"}\n\n",
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc\",\"call_id\":\"c1\",\"name\":\"run_js\",\"arguments\":\"{}\"}}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, piece := range pieces {
			_, _ = io.WriteString(w, piece)
			w.(http.Flusher).Flush()
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer server.Close()
	provider := New(Config{API: "responses", BaseURL: server.URL,
		ResponseTimeout: ResponseTimeout{StartSeconds: seconds(.12), IdleSeconds: seconds(.12)}})
	started := time.Now()
	result, err := provider.Generate(context.Background(), contracts.ModelRequest{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Calls) != 1 || result.Calls[0].Name != "run_js" || time.Since(started) <= 120*time.Millisecond {
		t.Fatalf("continued stream result = %+v, elapsed=%s", result, time.Since(started))
	}
}

func TestResponseWaitHTTPObserverTimeIsNotReceiveWait(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			progress := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"text\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-progress:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			provider := New(Config{API: "chat-completions", BaseURL: server.URL,
				ResponseTimeout: ResponseTimeout{StartSeconds: seconds(.08), IdleSeconds: seconds(.04)}})
			sinkErr := errors.New("save observation failed")
			result, err := provider.Generate(context.Background(), contracts.ModelRequest{}, func(o contracts.Observation) error {
				if o.Kind == "assistant_text" {
					time.Sleep(120 * time.Millisecond)
					close(progress)
					if fail {
						return sinkErr
					}
				}
				return nil
			})
			if fail {
				if !errors.Is(err, sinkErr) || contracts.FailureFor(err) != nil {
					t.Fatalf("observer failure = %v", err)
				}
			} else if err != nil || result.Text != "text" {
				t.Fatalf("slow observer result=%+v err=%v", result, err)
			}
		})
	}
}

func TestResponseWaitHTTPCancellationOverridesTimeout(t *testing.T) {
	ready := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, ": heartbeat\n\n")
		w.(http.Flusher).Flush()
		close(ready)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	client, tracking := waitTrackingClient()
	provider := New(Config{API: "responses", BaseURL: server.URL, HTTPClient: client,
		ResponseTimeout: ResponseTimeout{IdleSeconds: seconds(1)}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { <-ready; cancel() }()
	_, err := provider.Generate(ctx, contracts.ModelRequest{}, nil)
	if !errors.Is(err, context.Canceled) || contracts.FailureFor(err).Code != contracts.FailureCancelled {
		t.Fatalf("outer cancellation = %v", err)
	}
	assertWaitJoined(t, tracking, cancelled)
}

func TestResponseWaitResolveCancellationOutsideAuthWrapper(t *testing.T) {
	for _, mode := range []string{"own timeout", "actual auth failure", "auth failure at timeout", "outer cancellation", "outer at timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authErr := errors.New("refresh rejected")
			resolved := make(chan struct{})
			provider := New(Config{API: "responses", BaseURL: "http://unused.invalid",
				ResponseTimeout: ResponseTimeout{StartSeconds: seconds(.04)},
				ResolveToken: func(requestCtx context.Context) (string, error) {
					defer close(resolved)
					if mode == "actual auth failure" {
						return "", contracts.WithFailure(authErr, contracts.FailureDiagnostic{Code: contracts.FailureMissingCredential})
					}
					if mode == "outer cancellation" {
						cancel()
					}
					<-requestCtx.Done()
					if mode == "outer at timeout" {
						cancel()
					}
					if mode == "auth failure at timeout" {
						return "", contracts.WithFailure(authErr, contracts.FailureDiagnostic{Code: contracts.FailureMissingCredential})
					}
					// Match the current app Resolve wrapper, retaining the cancel cause.
					return "", contracts.WithFailure(requestCtx.Err(), contracts.FailureDiagnostic{Code: contracts.FailureMissingCredential})
				}})
			_, err := provider.Generate(ctx, contracts.ModelRequest{}, nil)
			want := contracts.FailureProviderTimeout
			if mode == "actual auth failure" || mode == "auth failure at timeout" {
				want = contracts.FailureMissingCredential
			}
			if mode == "outer cancellation" || mode == "outer at timeout" {
				want = contracts.FailureCancelled
			}
			if got := contracts.FailureFor(err); got == nil || got.Code != want {
				t.Fatalf("Resolve failure=%v diagnostic=%+v", err, got)
			}
			if mode == "own timeout" && (!errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
				t.Fatalf("timeout chain = %v", err)
			}
			if (mode == "actual auth failure" || mode == "auth failure at timeout") && !errors.Is(err, authErr) {
				t.Fatalf("lost auth error: %v", err)
			}
			select {
			case <-resolved:
			default:
				t.Fatal("Resolve still running after Generate")
			}
		})
	}
}

// The timeout callback may still be closing Body after cancel releases Read.
// Generate must join that callback, not just wait for Read to return.
func TestResponseWaitJoinsTimeoutBodyClose(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, ": heartbeat\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	closing, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseClose := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseClose()
	client := &http.Client{Transport: protocolFailureRoundTripper(func(r *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(r)
		if response != nil {
			response.Body = waitSlowCloseBody{ReadCloser: response.Body, closing: closing, release: release}
		}
		return response, err
	})}
	provider := New(Config{API: "responses", BaseURL: server.URL, HTTPClient: client,
		ResponseTimeout: ResponseTimeout{IdleSeconds: seconds(.04)}})
	done := make(chan error, 1)
	go func() {
		_, err := provider.Generate(context.Background(), contracts.ModelRequest{}, nil)
		done <- err
	}()
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("timeout did not close Body")
	}
	select {
	case err := <-done:
		t.Fatalf("Generate returned before callback/Close completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	releaseClose()
	select {
	case err := <-done:
		if got := contracts.FailureFor(err); got == nil || got.Code != contracts.FailureProviderTimeout {
			t.Fatalf("failure = %v, diagnostic=%+v", err, got)
		}
	case <-time.After(time.Second):
		t.Fatal("Generate did not join timeout callback")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("server did not observe request cancellation")
	}
}

type waitSlowCloseBody struct {
	io.ReadCloser
	closing chan<- struct{}
	release <-chan struct{}
}

func (body waitSlowCloseBody) Close() error {
	close(body.closing)
	<-body.release
	return body.ReadCloser.Close()
}
