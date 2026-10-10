package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hjh/internal/api"
)

func TestCancelBeforePostResponseIsSentAfterRegistrationAndWaitsForTerminal(t *testing.T) {
	postEntered := make(chan struct{})
	releasePost := make(chan struct{})
	cancelReceived := make(chan struct{})
	var cancelOnce sync.Once
	var mu sync.Mutex
	registered := false
	state := api.RunState{CoreID: "core-1", RunID: "run-1", Seq: 0, Phase: "preparing", Texts: []api.TextState{}, Operations: []api.OperationState{}, Executions: []api.ExecutionState{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs":
			var request api.StartRunRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode start request: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.CoreID != "core-1" || request.RunID != "run-1" {
				t.Errorf("start request identity = %q/%q", request.CoreID, request.RunID)
			}
			close(postEntered)
			<-releasePost
			mu.Lock()
			registered = true
			mu.Unlock()
			writeJSONForTest(w, http.StatusAccepted, state)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs/run-1/cancel":
			if r.URL.Query().Get("coreId") != "core-1" {
				t.Errorf("cancel coreId query = %q", r.URL.Query().Get("coreId"))
			}
			mu.Lock()
			wasRegistered := registered
			state.Phase = "cancelling"
			state.Seq++
			cancelState := state
			mu.Unlock()
			if !wasRegistered {
				t.Error("cancel reached Core before run registration")
			}
			cancelOnce.Do(func() { close(cancelReceived) })
			writeJSONForTest(w, http.StatusAccepted, cancelState)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/run-1/events":
			if r.URL.Query().Get("coreId") != "core-1" {
				t.Errorf("events coreId query = %q", r.URL.Query().Get("coreId"))
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			mu.Lock()
			initial := state
			mu.Unlock()
			writeSSEForTest(w, flusher, "run.snapshot", initial)
			<-cancelReceived
			mu.Lock()
			state.Phase = "terminal"
			state.Seq++
			state.Result = &api.RunResult{OK: false, Error: &api.Error{Code: "run_cancelled", Message: "cancelled"}}
			terminal := state
			mu.Unlock()
			writeSSEForTest(w, flusher, "run.snapshot", terminal)
			writeSSEForTest(w, flusher, "run.terminal", terminal)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/run-1":
			mu.Lock()
			current := state
			mu.Unlock()
			writeJSONForTest(w, http.StatusOK, current)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer cancelOnce.Do(func() { close(cancelReceived) })
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	var eventsMu sync.Mutex
	var events []RunEvent
	handle := client.StartRun(api.StartRunRequest{CoreID: "core-1", RunID: "run-1", Task: "日本語の依頼"}, func(event RunEvent) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	})
	select {
	case <-postEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("start POST did not begin")
	}
	cancelResult := make(chan error, 1)
	go func() { cancelResult <- handle.Cancel() }()
	select {
	case <-cancelReceived:
		t.Fatal("cancel was sent before the delayed start response registered the run")
	case <-time.After(40 * time.Millisecond):
	}
	close(releasePost)
	select {
	case err := <-cancelResult:
		if err != nil {
			t.Fatalf("Cancel() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("explicit cancel did not complete")
	}
	terminalState, err := handle.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if terminalState.Phase != "terminal" || terminalState.Result == nil || terminalState.Result.Error == nil || terminalState.Result.Error.Code != "run_cancelled" {
		t.Fatalf("terminal state = %#v", terminalState)
	}
	mu.Lock()
	wasRegistered := registered
	mu.Unlock()
	if !wasRegistered {
		t.Fatal("run was not registered")
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	foundTerminal := false
	for _, event := range events {
		if event.Kind == "run.terminal" && event.State.Phase == "terminal" {
			foundTerminal = true
		}
	}
	if !foundTerminal {
		t.Fatalf("terminal SSE event was not delivered: %#v", events)
	}
}

func TestCancelCanBeRetriedAfterCoreStillReportsRunRunning(t *testing.T) {
	streamConnected := make(chan struct{})
	terminalRequested := make(chan struct{})
	firstCancelReceived := make(chan struct{})
	releaseFirstCancel := make(chan struct{})
	var releaseFirstOnce sync.Once
	releaseFirst := func() { releaseFirstOnce.Do(func() { close(releaseFirstCancel) }) }
	defer releaseFirst()
	var terminalOnce sync.Once
	var mu sync.Mutex
	cancelCount := 0
	stateReadCount := 0
	state := api.RunState{
		CoreID: "core-retry", RunID: "run-retry", Seq: 0, Phase: "running",
		Texts: []api.TextState{}, Operations: []api.OperationState{}, Executions: []api.ExecutionState{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs":
			writeJSONForTest(w, http.StatusAccepted, state)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs/run-retry/cancel":
			mu.Lock()
			cancelCount++
			currentAttempt := cancelCount
			if currentAttempt == 2 {
				state.Phase = "cancelling"
				state.Seq++
			}
			cancelState := state
			mu.Unlock()
			if currentAttempt == 1 {
				close(firstCancelReceived)
				<-releaseFirstCancel
				writeJSONForTest(w, http.StatusServiceUnavailable, api.ErrorResponse{
					Error: &api.Error{Code: "temporary_failure", Message: "cancel request failed"},
				})
				return
			}
			terminalOnce.Do(func() { close(terminalRequested) })
			writeJSONForTest(w, http.StatusAccepted, cancelState)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/run-retry":
			mu.Lock()
			stateReadCount++
			current := state
			mu.Unlock()
			writeJSONForTest(w, http.StatusOK, current)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/run-retry/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			mu.Lock()
			initial := state
			mu.Unlock()
			writeSSEForTest(w, flusher, "run.snapshot", initial)
			close(streamConnected)
			<-terminalRequested
			mu.Lock()
			state.Phase = "terminal"
			state.Seq++
			state.Result = &api.RunResult{OK: false, Error: &api.Error{Code: "run_cancelled", Message: "cancelled"}}
			terminal := state
			mu.Unlock()
			writeSSEForTest(w, flusher, "run.snapshot", terminal)
			writeSSEForTest(w, flusher, "run.terminal", terminal)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer releaseFirst()
	defer func() { terminalOnce.Do(func() { close(terminalRequested) }) }()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	handle := client.StartRun(api.StartRunRequest{CoreID: "core-retry", RunID: "run-retry", Task: "retry cancellation"}, nil)
	select {
	case <-streamConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("run event stream did not connect")
	}
	firstCancelResult := make(chan error, 1)
	go func() { firstCancelResult <- handle.Cancel() }()
	select {
	case <-firstCancelReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("first cancellation request did not reach Core")
	}
	joinedCancelResult := make(chan error, 1)
	go func() { joinedCancelResult <- handle.Cancel() }()
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	concurrentCancelCount := cancelCount
	mu.Unlock()
	if concurrentCancelCount != 1 {
		t.Fatalf("concurrent Cancel() did not join the pending attempt: POST count=%d", concurrentCancelCount)
	}
	releaseFirst()
	for name, result := range map[string]<-chan error{"first": firstCancelResult, "joined": joinedCancelResult} {
		select {
		case err := <-result:
			if err == nil {
				t.Fatalf("%s Cancel() unexpectedly succeeded", name)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s Cancel() did not complete", name)
		}
	}
	if err := handle.Cancel(); err != nil {
		t.Fatalf("explicit retry after failed attempt did not succeed: %v", err)
	}
	terminalState, err := handle.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if terminalState.Phase != "terminal" || terminalState.Result == nil || terminalState.Result.Error == nil || terminalState.Result.Error.Code != "run_cancelled" {
		t.Fatalf("terminal state = %#v", terminalState)
	}
	mu.Lock()
	gotCancelCount := cancelCount
	gotStateReadCount := stateReadCount
	mu.Unlock()
	if gotCancelCount != 2 {
		t.Fatalf("cancel POST count = %d, want retry to send exactly one second request", gotCancelCount)
	}
	if gotStateReadCount != 1 {
		t.Fatalf("cancel failure state lookup count = %d, want one running-state check", gotStateReadCount)
	}
}

func TestLostStartResponseLooksUpSameRunIDWithoutDuplicatePost(t *testing.T) {
	var mu sync.Mutex
	postCount := 0
	terminal := api.RunState{
		CoreID: "core-2", RunID: "run-stable", Seq: 4, Phase: "terminal",
		Texts: []api.TextState{}, Operations: []api.OperationState{}, Executions: []api.ExecutionState{},
		Result: &api.RunResult{OK: true, AgentResult: &api.AgentResult{OK: true, Outcome: "final", FinalText: "done"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs":
			mu.Lock()
			postCount++
			mu.Unlock()
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack response: %v", err)
				return
			}
			_ = connection.Close()
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/run-stable":
			writeJSONForTest(w, http.StatusOK, terminal)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/run-stable/events":
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSEForTest(w, w.(http.Flusher), "run.snapshot", terminal)
			writeSSEForTest(w, w.(http.Flusher), "run.terminal", terminal)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	handle := client.StartRun(api.StartRunRequest{CoreID: "core-2", RunID: "run-stable", Task: "complete once"}, nil)
	state, err := handle.Wait()
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if state.Result == nil || !state.Result.OK {
		t.Fatalf("result = %#v", state.Result)
	}
	mu.Lock()
	count := postCount
	mu.Unlock()
	if count != 1 {
		t.Fatalf("POST count = %d, want one request using the original run ID", count)
	}
}

func TestUnconfirmedStartRecoversSameRunHandleAndCanCancel(t *testing.T) {
	const runID = "run-recover"
	var mu sync.Mutex
	online := false
	accepted := false
	postRequests := []api.StartRunRequest{}
	getCount := 0
	state := api.RunState{
		CoreID: "core-recover", RunID: runID, Seq: 0, Phase: "running",
		Texts: []api.TextState{}, Operations: []api.OperationState{}, Executions: []api.ExecutionState{},
	}
	disconnected := make(chan RunEvent, 8)
	recoveredSnapshot := make(chan struct{}, 1)
	terminalRequested := make(chan struct{})
	var terminalOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs":
			var request api.StartRunRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode start request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			mu.Lock()
			postRequests = append(postRequests, request)
			canReach := online
			accepted = true
			current := state
			mu.Unlock()
			if !canReach {
				dropHTTPResponse(t, w)
				return
			}
			writeJSONForTest(w, http.StatusOK, current)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/"+runID:
			mu.Lock()
			getCount++
			canReach := online
			current := state
			mu.Unlock()
			if !canReach {
				dropHTTPResponse(t, w)
				return
			}
			writeJSONForTest(w, http.StatusOK, current)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/"+runID+"/events":
			mu.Lock()
			current := state
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			writeSSEForTest(w, flusher, "run.snapshot", current)
			recoveredSnapshot <- struct{}{}
			<-terminalRequested
			mu.Lock()
			state.Phase = "terminal"
			state.Seq++
			state.Result = &api.RunResult{OK: false, Error: &api.Error{Code: "run_cancelled", Message: "cancelled"}}
			terminal := state
			mu.Unlock()
			writeSSEForTest(w, flusher, "run.snapshot", terminal)
			writeSSEForTest(w, flusher, "run.terminal", terminal)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs/"+runID+"/cancel":
			if r.URL.Query().Get("coreId") != "core-recover" {
				t.Errorf("cancel coreId = %q", r.URL.Query().Get("coreId"))
			}
			mu.Lock()
			state.Phase = "cancelling"
			state.Seq++
			cancelState := state
			mu.Unlock()
			terminalOnce.Do(func() { close(terminalRequested) })
			writeJSONForTest(w, http.StatusAccepted, cancelState)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	defer func() { terminalOnce.Do(func() { close(terminalRequested) }) }()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	handle := client.StartRun(api.StartRunRequest{CoreID: "core-recover", RunID: runID, Task: "recover same run"}, func(event RunEvent) {
		if event.Kind == "run.disconnected" {
			disconnected <- event
		}
	})
	select {
	case event := <-disconnected:
		if event.State.RunID != runID || event.State.CoreID != "core-recover" || event.Err == nil {
			t.Fatalf("disconnected state did not retain run identity: %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unknown start result did not remain recoverable")
	}
	mu.Lock()
	initialPostCount, initialGetCount, initialAccepted := len(postRequests), getCount, accepted
	online = true
	mu.Unlock()
	if initialPostCount != 2 || initialGetCount != 2 || !initialAccepted {
		t.Fatalf("start ambiguity sequence = posts:%d gets:%d accepted:%t", initialPostCount, initialGetCount, initialAccepted)
	}
	select {
	case <-recoveredSnapshot:
	case <-time.After(3 * time.Second):
		t.Fatal("same run handle did not recover after communication returned")
	}
	if err := handle.Cancel(); err != nil {
		t.Fatalf("explicit cancel after recovery: %v", err)
	}
	terminal, err := handle.Wait()
	if err != nil {
		t.Fatalf("Wait() after recovery: %v", err)
	}
	if terminal.RunID != runID || terminal.Phase != "terminal" || terminal.Result == nil || terminal.Result.Error == nil || terminal.Result.Error.Code != "run_cancelled" {
		t.Fatalf("recovered terminal state = %#v", terminal)
	}
	mu.Lock()
	requests := append([]api.StartRunRequest(nil), postRequests...)
	mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("same-run POST count = %d, want original, exact retry, and recovery lookup", len(requests))
	}
	for _, request := range requests {
		if request.RunID != runID || request.Task != "recover same run" {
			t.Fatalf("recovery changed run identity/request: %#v", request)
		}
	}
}

func TestClientCloseStopsUnconfirmedRunLoopAndWaitJoinsIt(t *testing.T) {
	disconnected := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/runs":
			dropHTTPResponse(t, w)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/runs/run-close":
			dropHTTPResponse(t, w)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	handle := client.StartRun(api.StartRunRequest{CoreID: "core-close", RunID: "run-close", Task: "close communication"}, func(event RunEvent) {
		if event.Kind == "run.disconnected" {
			disconnected <- struct{}{}
		}
	})
	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not enter recoverable disconnected state")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Client.Close(): %v", err)
	}
	waited := make(chan struct{})
	var result api.RunState
	var waitErr error
	go func() {
		result, waitErr = handle.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() could not join the closed communication loop")
	}
	if result.RunID != "run-close" || !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("closed communication result = %#v, %v", result, waitErr)
	}
}

func writeJSONForTest(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func dropHTTPResponse(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	connection, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Errorf("hijack response: %v", err)
		return
	}
	_ = connection.Close()
}

func writeSSEForTest(w http.ResponseWriter, flusher http.Flusher, event string, state api.RunState) {
	data, _ := json.Marshal(state)
	_, _ = fmt.Fprintf(w, "event: %s\nid: %s/%s/%d\ndata: %s\n\n", event, state.CoreID, state.RunID, state.Seq, data)
	flusher.Flush()
}
