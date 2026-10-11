package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebFetchConvertsHTMLAndReportsFinalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/page", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<h1>Alpha &amp; beta</h1><p>first<br>second</p><script>hidden script</script><!-- hidden comment --><style>hidden style</style>`)
	}))
	defer server.Close()
	h := testWebHost(t, t.TempDir())

	result := callWebFetch(t, h, context.Background(), map[string]string{"url": server.URL + "/start"})
	if !strings.Contains(result, "URL: "+server.URL+"/page") ||
		!strings.Contains(result, "Status: 200") ||
		!strings.Contains(result, "Content-Type: text/html; charset=utf-8") ||
		!strings.Contains(result, "truncated: false") ||
		!strings.Contains(result, "Alpha & beta") ||
		!strings.Contains(result, "first\nsecond") {
		t.Fatalf("web_fetch returned unexpected HTML result: %q", result)
	}
	for _, hidden := range []string{"hidden script", "hidden comment", "hidden style"} {
		if strings.Contains(result, hidden) {
			t.Errorf("HTML result retained %q: %q", hidden, result)
		}
	}
}

func TestWebFetchCapsTextAndOmitsNonTextBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/text" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, strings.Repeat("x", 1_048_576+3))
			return
		}
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/problem+json")
			_, _ = io.WriteString(w, `{"title":"kept as text"}`)
			return
		}
		if r.URL.Path == "/xml" {
			w.Header().Set("Content-Type", "application/atom+xml")
			_, _ = io.WriteString(w, `<feed><title>kept as text</title></feed>`)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, "binary-body-must-not-return")
	}))
	defer server.Close()
	h := testWebHost(t, t.TempDir())

	text := callWebFetch(t, h, context.Background(), map[string]string{"url": server.URL + "/text"})
	body, ok := strings.CutPrefix(text, "URL: "+server.URL+"/text\nStatus: 200\nContent-Type: text/plain\ntruncated: true\n\n")
	if !ok || len(body) != 1_048_576+len("\n\n[body truncated]") || !strings.HasSuffix(body, "\n\n[body truncated]") {
		t.Fatalf("web_fetch did not return exactly the bounded body and marker: prefix=%t bodyBytes=%d suffix=%q", ok, len(body), body[len(body)-min(len(body), 40):])
	}
	if strings.TrimSuffix(body, "\n\n[body truncated]") != strings.Repeat("x", 1_048_576) {
		t.Fatal("web_fetch body was not capped at 1 MiB")
	}
	for _, test := range []struct {
		path string
		body string
	}{
		{path: "/json", body: `{"title":"kept as text"}`},
		{path: "/xml", body: `<feed><title>kept as text</title></feed>`},
	} {
		result := callWebFetch(t, h, context.Background(), map[string]string{"url": server.URL + test.path})
		if !strings.HasSuffix(result, "\n\n"+test.body) {
			t.Errorf("web_fetch omitted %s body text: %q", test.path, result)
		}
	}

	binary := callWebFetch(t, h, context.Background(), map[string]string{"url": server.URL + "/binary"})
	if !strings.Contains(binary, "Content-Type: application/octet-stream") || !strings.Contains(binary, "truncated: false") || strings.Contains(binary, "binary-body-must-not-return") {
		t.Fatalf("web_fetch did not return metadata only for a binary response: %q", binary)
	}
}

func TestWebFetchRequestHasThirtySecondDeadline(t *testing.T) {
	h, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.HTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining < 25*time.Second || remaining > 30*time.Second {
			return nil, fmt.Errorf("request deadline remaining %s (present=%t), want about 30s", remaining, ok)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("ready")),
			Request:    request,
		}, nil
	})}
	result := callWebFetch(t, h, context.Background(), map[string]string{"url": "https://example.test/page"})
	if !strings.HasSuffix(result, "\n\nready") {
		t.Fatalf("web_fetch returned unexpected response: %q", result)
	}
}

func TestWebFetchSaveToReplacesFileWithOriginalUnboundedBytes(t *testing.T) {
	body := []byte(strings.Repeat("original-bytes-", 80_000))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	workspace := t.TempDir()
	path := filepath.Join(workspace, "page.bin")
	if err := os.WriteFile(path, []byte("old contents"), 0644); err != nil {
		t.Fatal(err)
	}
	h := testWebHost(t, workspace)

	result := callWebFetch(t, h, context.Background(), map[string]string{"url": server.URL, "save_to": "page.bin"})
	if !strings.Contains(result, "Saved: page.bin") || !strings.Contains(result, "Status: 200") || !strings.Contains(result, fmt.Sprintf("Bytes: %d", len(body))) || strings.Contains(result, "original-bytes-") {
		t.Fatalf("web_fetch returned unexpected save metadata: %q", result)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(body) {
		t.Fatalf("saved body differs from original: got %d bytes, want %d", len(saved), len(body))
	}

	absolutePath := filepath.Join(workspace, "absolute-page.bin")
	result = callWebFetch(t, h, context.Background(), map[string]string{"url": server.URL, "save_to": absolutePath})
	if !strings.Contains(result, "Saved: "+absolutePath) || !strings.Contains(result, fmt.Sprintf("Bytes: %d", len(body))) {
		t.Fatalf("web_fetch did not report absolute save_to path: %q", result)
	}
	saved, err = os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(body) {
		t.Fatalf("absolute save_to body differs from original: got %d bytes, want %d", len(saved), len(body))
	}
}

func TestWebFetchReportsNonSuccessStatusAndFollowsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	h := testWebHost(t, t.TempDir())

	input, err := json.Marshal(map[string]string{"url": server.URL + "/error"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Call(context.Background(), "webFetch", input); err == nil || !strings.Contains(err.Error(), "(503)") {
		t.Fatalf("web_fetch returned unexpected status error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		args, _ := json.Marshal(map[string]string{"url": server.URL + "/wait"})
		_, callErr := h.Call(ctx, "webFetch", args)
		result <- callErr
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("web_fetch request did not start")
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("web_fetch returned %v after caller cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("web_fetch did not return after caller cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("web_fetch did not cancel the HTTP request")
	}
}

func testWebHost(t *testing.T, workspace string) *Host {
	t.Helper()
	h, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func callWebFetch(t *testing.T, h *Host, ctx context.Context, args map[string]string) string {
	t.Helper()
	input, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	value, err := h.Call(ctx, "webFetch", input)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := value.(string)
	if !ok {
		t.Fatalf("webFetch returned %T, want scalar string", value)
	}
	return result
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
