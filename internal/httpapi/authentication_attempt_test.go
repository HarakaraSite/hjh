package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

type authDiscoveryTransport struct{ entered chan struct{} }

func (d *authDiscoveryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	select {
	case d.entered <- struct{}{}:
	default:
	}
	<-request.Context().Done()
	return nil, request.Context().Err()
}

// Begin returns before OAuth network preparation, cancellation remains
// available, and cancelling an attempt preserves its added unconnected slot.
func TestAuthAttemptHTTPClientLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, "config.json")
	config := `{"defaultRole":"default","roles":{"default":{"tools":[]}},"model":{"provider":"chatgpt","modelId":"test"},"providers":{"chatgpt":{"api":"responses","auth":{"kind":"chatgpt-oauth","profile":"account"}}}}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	transport := &authDiscoveryTransport{entered: make(chan struct{}, 1)}
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace, ConfigPath: configPath,
		CredentialStateDir: filepath.Join(workspace, "state"), HTTPClient: &http.Client{Transport: transport}})
	handler := New(service, corerun.New(service, workspace))
	server := httptest.NewServer(handler)
	defer func() { server.Close(); handler.Close() }()
	client, err := apiclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	core, err := client.Core(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := api.AuthRequest{CoreID: core.CoreID, Workspace: workspace, ProviderID: "chatgpt", AuthProfile: "account", AttemptID: "http-attempt"}
	attempt, err := client.BeginAuthAttempt(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.AccountID == "primary" || attempt.AccountID == "" || attempt.ScopeID == "" || attempt.Phase != "starting" {
		t.Fatalf("attempt=%+v", attempt)
	}
	select {
	case <-transport.entered:
	case <-ctx.Done():
		t.Fatal("OAuth preparation not started")
	}
	replay, err := client.BeginAuthAttempt(ctx, request)
	if err != nil || replay.AccountID != attempt.AccountID {
		t.Fatalf("begin replay=%+v err=%v", replay, err)
	}
	if _, err = client.CancelAuthAttempt(ctx, request); err != nil {
		t.Fatal(err)
	}
	for {
		current, err := client.AuthAttempt(ctx, core.CoreID, attempt.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Terminal() {
			if current.Phase != "cancelled" {
				t.Fatalf("terminal=%+v", current)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("attempt did not settle")
		case <-time.After(time.Millisecond):
		}
	}
	accounts, err := client.AuthAccounts(ctx, apiclient.Scope{Workspace: workspace}, "chatgpt", "account")
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts.Accounts) != 2 || accounts.SelectedAccountID != "primary" {
		t.Fatalf("cancel changed stored slots: %+v", accounts)
	}
	if err = client.DeleteAuthAttempt(ctx, core.CoreID, attempt.AttemptID); err != nil {
		t.Fatal(err)
	}
	accounts, err = client.AuthAccounts(ctx, apiclient.Scope{Workspace: workspace}, "chatgpt", "account")
	if err != nil || len(accounts.Attempts) != 0 || len(accounts.Accounts) != 2 {
		t.Fatalf("terminal metadata release=%+v err=%v", accounts, err)
	}
}
