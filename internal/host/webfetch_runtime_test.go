package host_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hjh/internal/extensions"
	"hjh/internal/jsruntime"
)

func TestWebFetchRegisteredToolReturnsHostScalarThroughWrapper(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("wrapper received the response"))
	}))
	defer server.Close()

	extensionDir := t.TempDir()
	if err := extensions.InstallDefaults(extensionDir); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.HasTool("web_fetch") {
		t.Fatal("default role does not register web_fetch")
	}
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	code, err := json.Marshal(map[string]string{
		"code": "return await tools.web_fetch({url:" + jsString(server.URL) + "});",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := worker.Call(context.Background(), "run_js", code)
	if err != nil {
		t.Fatal(err)
	}
	var result string
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("run_js result is not a JSON string scalar: %s: %v", encoded, err)
	}
	if !strings.Contains(result, "wrapper received the response") || !strings.Contains(result, "truncated: false") {
		t.Fatalf("web_fetch wrapper returned unexpected response: %q", result)
	}
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
