package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

// Hold the first generation while real HTTP input operations reach the normal
// app/journal/history path, then let the loop and queued root runs finish.
type inputRouteProvider struct {
	entered  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	requests []contracts.ModelRequest
}

func (p *inputRouteProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}
func (p *inputRouteProvider) Generate(ctx context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	first := len(p.requests) == 1
	p.mu.Unlock()
	if first {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return contracts.ModelResult{}, ctx.Err()
		}
	}
	return contracts.ModelResult{Text: "saved answer", FinishReason: "stop"}, nil
}

func TestRuntimeInputRoutesApplyAndStartFollowUp(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &inputRouteProvider{entered: make(chan struct{}), release: make(chan struct{})}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	var release sync.Once
	defer func() { release.Do(func() { close(provider.release) }); _ = handler.Close(); _ = store.Close() }()
	call := func(method, path string, body any, into any) int {
		t.Helper()
		var encoded []byte
		if body != nil {
			var err error
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewReader(encoded)))
		if response.Code >= 400 {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		if into != nil {
			if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
				t.Fatal(err)
			}
		}
		return response.Code
	}
	coreID := manager.Core().CoreID
	var initial api.RunState
	call(http.MethodPost, "/api/v2/runs", api.StartRunRequest{CoreID: coreID, RunID: "input-routes-root", Task: "initial"}, &initial)
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("generation did not start")
	}
	state, err := manager.Get(coreID, initial.RunID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/runs/" + initial.RunID + "/inputs"
	steer := api.SubmitInputRequest{CoreID: coreID, SessionID: state.SessionID, InputID: "steer-one", Kind: "steering", Text: "apply this instruction"}
	var accepted api.InputReceipt
	if status := call(http.MethodPost, path, steer, &accepted); status != http.StatusAccepted || accepted.Status != "accepted" {
		t.Fatalf("admission: %d %+v", status, accepted)
	}
	if status := call(http.MethodPost, path, steer, &accepted); status != http.StatusOK {
		t.Fatalf("replay status=%d", status)
	}
	var queued api.InputReceipt
	call(http.MethodPost, path, api.SubmitInputRequest{CoreID: coreID, SessionID: state.SessionID, InputID: "follow-one", Kind: "follow-up", Text: "next task"}, &queued)
	if queued.Status != "queued" || queued.Next == nil {
		t.Fatalf("queue=%+v", queued)
	}
	var detail api.InputReceipt
	call(http.MethodGet, path+"/steer-one?coreId="+coreID+"&includeText=true", nil, &detail)
	if detail.Text != steer.Text {
		t.Fatalf("detail=%+v", detail)
	}
	listPath := "/api/v2/sessions/" + state.SessionID + "/inputs"
	var first api.InputReceiptPage
	call(http.MethodGet, listPath, nil, &first)
	if len(first.Receipts) != 2 || first.CoreID != coreID {
		t.Fatalf("list=%+v", first)
	}
	for _, r := range first.Receipts {
		if r.Text != "" {
			t.Fatal("list unexpectedly expanded text")
		}
	}
	release.Do(func() { close(provider.release) })
	var terminal api.RunState
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		terminal, err = manager.Get(coreID, initial.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if terminal.Phase == "terminal" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if terminal.Result == nil || !terminal.Result.OK || terminal.NextRunID == "" {
		t.Fatalf("root=%+v", terminal)
	}
	for time.Now().Before(deadline) {
		next, e := manager.Get(coreID, terminal.NextRunID)
		if e != nil {
			t.Fatal(e)
		}
		if next.Phase == "terminal" {
			if next.Result == nil || !next.Result.OK {
				t.Fatalf("next=%+v", next)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	var changed api.InputReceiptPage
	call(http.MethodGet, listPath+"?coreId="+coreID+"&afterRevision="+strconv.FormatUint(first.Revision, 10), nil, &changed)
	if len(changed.Receipts) != 2 {
		t.Fatalf("changed=%+v", changed)
	}
	for _, r := range changed.Receipts {
		if r.Kind == "steering" && r.Status != "applied" || r.Kind == "follow-up" && r.Status != "started" {
			t.Fatalf("receipt=%+v", r)
		}
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 3 {
		t.Fatalf("requests=%d", len(provider.requests))
	}
	second := provider.requests[1].Messages
	if len(second) != 3 {
		t.Fatalf("steering request=%+v", second)
	}
	var content contracts.TextContent
	if err := json.Unmarshal(second[2].Content, &content); err != nil {
		t.Fatal(err)
	}
	if content.Text != steer.Text || second[2].Steering == nil || !*second[2].Steering {
		t.Fatalf("steering request=%+v", second)
	}
	session, e := service.Session(context.Background(), state.SessionID)
	if e != nil || session.TurnCount != 2 {
		t.Fatalf("session=%+v err=%v", session, e)
	}
	var chain api.InputChainState
	call(http.MethodGet, "/api/v2/runs/"+initial.RunID+"/input-chain?coreId="+coreID, nil, &chain)
	if chain.Phase != "settled" {
		t.Fatalf("chain=%+v", chain)
	}
}
