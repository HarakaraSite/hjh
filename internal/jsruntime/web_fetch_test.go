package jsruntime

import (
	"bytes"
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

	"hjh/internal/contracts"
	"hjh/internal/host"
)

func callWebTool(t *testing.T, w *Worker, generated bool, name string, args any) (json.RawMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if generated {
		return w.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": "return await tools." + name + "(" + string(contracts.JSON(args)) + ");"}))
	}
	return w.Call(ctx, name, contracts.JSON(args))
}

func webToolString(t *testing.T, w *Worker, generated bool, args any) string {
	t.Helper()
	raw, err := callWebTool(t, w, generated, "web_fetch", args)
	if err != nil {
		t.Fatal(err)
	}
	var result string
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("web_fetch did not return a string: %s (%v)", raw, err)
	}
	return result
}

func TestWebFetchWorkerPagesAndBoundedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "hjh" || r.Header.Get("Accept") != "text/*, application/json, application/xml;q=0.9, */*;q=0.1" {
			t.Errorf("web_fetch request headers: %v", r.Header)
		}
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "\ufeff<h1>Alpha &amp; beta</h1><p>first<br>second</p><script>hidden script</script><!-- hidden comment --><style>hidden style</style><p>&amp;lt; &lt; &gt; &quot; &#39; &nbsp;end</p>")
		case "/long":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, strings.Repeat("x", 128*1024))
		case "/json":
			w.Header().Set("Content-Type", "application/problem+json")
			_, _ = w.Write(append([]byte("\xef\xbb\xbf{\"日本語\":\""), 0xff, 0xfe, '"', '}'))
		case "/xml":
			w.Header().Set("Content-Type", "application/atom+xml")
			_, _ = io.WriteString(w, "<feed>unchanged</feed>")
		case "/binary":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0, 255, 1})
		case "/error":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "unavailable")
		default:
			t.Errorf("unexpected URL (request URL changed): %s", r.RequestURI)
		}
	}))
	defer server.Close()
	var observations []contracts.Observation
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: t.TempDir(),
		Observer: func(o contracts.Observation) error { observations = append(observations, o); return nil }})
	for _, generated := range []bool{false, true} {
		t.Run(fmt.Sprintf("generated=%t", generated), func(t *testing.T) {
			page := webToolString(t, w, generated, map[string]string{"url": "  " + server.URL + "/redirect  "})
			want := "URL: " + server.URL + "/page\nStatus: 200\nContent-Type: text/html; charset=utf-8\ntruncated: false\n\nAlpha & beta\nfirst\nsecond\n&lt; < > \" ' end"
			if page != want {
				t.Fatalf("readable HTML or final response URL: %q; want %q", page, want)
			}
			long := webToolString(t, w, generated, map[string]string{"url": server.URL + "/long"})
			want = "URL: " + server.URL + "/long\nStatus: 200\nContent-Type: text/plain\ntruncated: true\n\n" + strings.Repeat("x", 64*1024) + "\n\n[body truncated]"
			if long != want {
				t.Fatalf("bounded body differs: got %d bytes, want %d", len(long), len(want))
			}
			for _, test := range []struct{ path, body string }{{"/json", "{\"日本語\":\"\ufffd\"}"}, {"/xml", "<feed>unchanged</feed>"}} {
				got := webToolString(t, w, generated, map[string]string{"url": server.URL + test.path})
				if !strings.HasSuffix(got, "\n\n"+test.body) {
					t.Fatalf("text type or UTF-8 decode: %q", got)
				}
			}
			binary := webToolString(t, w, generated, map[string]string{"url": server.URL + "/binary"})
			if binary != "URL: "+server.URL+"/binary\nStatus: 200\nContent-Type: application/octet-stream\ntruncated: false" {
				t.Fatalf("binary metadata: %q", binary)
			}
			_, err := callWebTool(t, w, generated, "web_fetch", map[string]string{"url": server.URL + "/error"})
			if err == nil || !strings.Contains(err.Error(), "(503)") || !strings.Contains(err.Error(), server.URL+"/error") {
				t.Fatalf("HTTP failure: %v", err)
			}
		})
	}
	for _, o := range observations {
		if o.Kind == "host" {
			t.Fatalf("JS fetch tool used a legacy Host operation: %+v", o)
		}
	}
}

func TestHTTPWorkerFullBodyAndConfiguredClient(t *testing.T) {
	// TLS requires the configured client: the public default client cannot trust
	// this local server. Test the same client through the captured index exports.
	body := append([]byte("\xef\xbb\xbf日本語"), 0xc0, 0xaf, 0xed, 0xa0, 0x80, 0xff, 0xfe)
	body = append(body, bytes.Repeat([]byte("full-body"), 12_000)...)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.RequestURI != "/raw%2fpath?q=%2f" {
			t.Errorf("raw HTTP request changed: %s %s", r.Method, r.RequestURI)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	workspace := t.TempDir()
	h, err := host.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	h.HTTPClient = server.Client()
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace, Host: h})
	var want string
	encoded, _ := json.Marshal(string(body))
	if err := json.Unmarshal(encoded, &want); err != nil {
		t.Fatal(err)
	}
	for _, generated := range []bool{false, true} {
		raw, err := callWebTool(t, w, generated, "http", map[string]string{"url": server.URL + "/raw%2fpath?q=%2f"})
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Status int
			Body   string
		}
		if err := json.Unmarshal(raw, &got); err != nil || got.Status != 404 || got.Body != want {
			t.Fatalf("raw HTTP body/non-2xx/UTF-8: status=%d bytes=%d want=%d (%v)", got.Status, len(got.Body), len(want), err)
		}
	}
}

func TestWebFetchWorkerStopsBodyAndCallerCancellation(t *testing.T) {
	for _, bounded := range []bool{true, false} {
		t.Run(fmt.Sprintf("bounded=%t", bounded), func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				if bounded {
					_, _ = io.WriteString(w, strings.Repeat("x", 96*1024))
				} else {
					_, _ = io.WriteString(w, "waiting for the rest")
				}
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
				close(stopped)
			}))
			defer server.Close()
			w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: t.TempDir()})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := w.Call(ctx, "web_fetch", contracts.JSON(map[string]string{"url": server.URL}))
				done <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("request did not start")
			}
			if !bounded {
				cancel()
			}
			select {
			case err := <-done:
				if (bounded && err != nil) || (!bounded && !errors.Is(err, context.Canceled)) {
					t.Fatalf("body completion/cancellation: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("fetch did not finish after stopping the body")
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("HTTP body request remained active")
			}
		})
	}
}

func TestWebFetchWorkerFullBinarySave(t *testing.T) {
	// Cross the upstream fetch default 32 MiB cap, preserving all binary bytes.
	body := bytes.Repeat([]byte{0, 255, 254, '\r', '\n', 0xef, 0xbb, 0xbf}, (32<<20)/8+33)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "*/*" || r.Header.Get("User-Agent") != "hjh" {
			t.Errorf("download headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	workspace := t.TempDir()
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace})
	for _, generated := range []bool{false, true} {
		filename := "download.bin"
		if generated {
			filename = filepath.Join(t.TempDir(), "absolute.bin")
		}
		absolute := filename
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(workspace, filename)
		}
		if err := os.WriteFile(absolute, []byte("previous contents"), 0644); err != nil {
			t.Fatal(err)
		}
		got := webToolString(t, w, generated, map[string]string{"url": server.URL, "save_to": filename})
		want := fmt.Sprintf("Saved: %s\nURL: %s\nStatus: 200\nContent-Type: application/octet-stream\nBytes: %d", filename, server.URL, len(body))
		if got != want {
			t.Fatalf("download metadata: %q; want %q", got, want)
		}
		saved, err := os.ReadFile(absolute)
		if err != nil || !bytes.Equal(saved, body) {
			t.Fatalf("full saved bytes differ: got %d, want %d (%v)", len(saved), len(body), err)
		}
	}
}

func TestWebFetchWorkerTimeoutAbortsResponseBody(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "waiting for the rest")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: t.TempDir()})
	// Allow the tool's own 30-second AbortController timer to fire, rather than
	// cancelling its caller or inspecting a Go request-context deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	done := make(chan error, 1)
	begin := time.Now()
	go func() {
		_, err := w.Call(ctx, "web_fetch", contracts.JSON(map[string]string{"url": server.URL}))
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "web_fetch request failed") || ctx.Err() != nil {
			t.Fatalf("tool did not abort its own stalled body: %v (caller: %v)", err, ctx.Err())
		}
		if elapsed := time.Since(begin); elapsed < 25*time.Second {
			t.Fatalf("tool timed out before its 30-second budget: %s", elapsed)
		}
	case <-ctx.Done():
		t.Fatal("tool timeout did not finish before caller deadline")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("AbortController left the HTTP response body active")
	}
	if !w.Available() {
		t.Fatal("tool timeout made Worker unavailable")
	}
}
