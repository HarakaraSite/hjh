package corecatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"hjh/internal/api"
)

func TestCoreDiscoveryRegistrationAndIdentity(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	entries, err := Entries("")
	if err != nil || len(entries) != 0 {
		t.Fatalf("new state entries = %v, %v", entries, err)
	}
	directory, _ := Directory()
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("query created state: %v", err)
	}
	core := api.Core{CoreID: "12345678-original", Workspace: "/work/one", APIVersion: "v2"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/core" {
			t.Errorf("probe path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(core)
	}))
	defer server.Close()
	entry := Entry{CoreID: core.CoreID, Workspace: core.Workspace, Endpoint: server.URL, PID: 123, StartedAt: "2026-10-12T00:00:00Z"}
	remove, err := Register(entry)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	entries, err = Entries("/work/one")
	if err != nil || len(entries) != 1 || entries[0] != entry {
		t.Fatalf("entries = %v, %v", entries, err)
	}
	selected, err := Select(entries, "12345678")
	if err != nil || selected != entry {
		t.Fatalf("prefix selection = %v, %v", selected, err)
	}
	status, err := Probe(context.Background(), entry)
	if err != nil || status.State != "running" || status.APIVersion != "v2" {
		t.Fatalf("live status = %v, %v", status, err)
	}
	// The persisted Core ID must not identify a new process at the same URL.
	replaced := entry
	replaced.CoreID = "old-core"
	status, err = Probe(context.Background(), replaced)
	if err != nil || status.State != "replaced" {
		t.Fatalf("replacement status = %v, %v", status, err)
	}
	server.Close()
	status, err = Probe(context.Background(), entry)
	if err != nil || status.State != "unreachable" {
		t.Fatalf("crashed status = %v, %v", status, err)
	}
	entries, _ = Entries("")
	if len(entries) != 1 {
		t.Fatal("probe removed the crash record")
	}
	if err := remove(); err != nil {
		t.Fatal(err)
	}
	entries, _ = Entries("")
	if len(entries) != 0 {
		t.Fatal("shutdown registration remains")
	}
}

func TestCorePrefixSelectsOneOfMultipleRegistrations(t *testing.T) {
	entries := []Entry{{CoreID: "aaaa1111"}, {CoreID: "aaaa2222"}}
	if _, err := Select(entries, "aaaa"); err == nil {
		t.Fatal("ambiguous prefix selected one Core")
	}
	entry, err := Select(entries, "aaaa1")
	if err != nil || entry.CoreID != "aaaa1111" {
		t.Fatalf("unique prefix = %v, %v", entry, err)
	}
}
