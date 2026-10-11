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
	state := api.RunState{CoreID: "core-1", RunID: "run-1", Seq: 0, Phase: "preparing"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs":
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs/run-1/cancel":
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-1/events":
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-1":
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

func TestWaitRequiresFinalConversationSnapshotBeforeRunTerminal(t *testing.T) {
	allowFinal := make(chan struct{})
	allowTerminal := make(chan struct{})
	initialSent := make(chan struct{})
	finalSent := make(chan struct{})
	var finalOnce, terminalOnce sync.Once
	defer finalOnce.Do(func() { close(allowFinal) })
	defer terminalOnce.Do(func() { close(allowTerminal) })

	terminal := api.RunState{CoreID: "core-cut", RunID: "run-cut", Seq: 5, Phase: "terminal",
		Result: &api.RunResult{OK: true, AgentResult: &api.AgentResult{OK: true, Outcome: "completed", FinalText: "result"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs":
			writeJSONForTest(w, http.StatusAccepted, terminal)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-cut/events":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			oldCut := api.ConversationSnapshot{ScopeID: "run-cut", ViewID: "view-cut", Revision: 4,
				ThroughSequence: uint64Pointer(4), Order: []string{"cv:assistant"},
				Entities: map[string]api.ConversationEntity{"cv:assistant": {ID: "cv:assistant", Kind: "assistant", Text: "途中", Status: "incomplete"}}}
			writeRunSnapshotForTest(w, flusher, terminal, oldCut)
			close(initialSent)
			<-allowFinal
			finalCut := oldCut
			finalCut.Revision = 5
			finalCut.ThroughSequence = uint64Pointer(5)
			entity := finalCut.Entities["cv:assistant"]
			entity.Text = "最終本文"
			entity.Status = "completed"
			finalCut.Entities[entity.ID] = entity
			writeRunSnapshotForTest(w, flusher, terminal, finalCut)
			close(finalSent)
			<-allowTerminal
			writeSSEForTest(w, flusher, "run.terminal", terminal)
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

	events := make(chan RunEvent, 8)
	handle := client.StartRun(api.StartRunRequest{CoreID: "core-cut", RunID: "run-cut", Task: "task"}, func(event RunEvent) {
		events <- event
	})
	select {
	case <-initialSent:
	case <-time.After(2 * time.Second):
		t.Fatal("initial terminal cut was not sent")
	}
	first := receiveRunEvent(t, events, "run.snapshot")
	if first.State.Phase != "terminal" || first.Conversation == nil || first.Conversation.Revision != 4 {
		t.Fatalf("initial fixed cut did not preserve terminal phase and old revision: %+v", first)
	}

	waitDone := make(chan struct{})
	var result api.RunState
	var waitErr error
	go func() {
		result, waitErr = handle.Wait()
		close(waitDone)
	}()
	assertWaitBlocked(t, waitDone, "initial terminal phase completed Wait before final snapshot")

	finalOnce.Do(func() { close(allowFinal) })
	select {
	case <-finalSent:
	case <-time.After(2 * time.Second):
		t.Fatal("final durable conversation cut was not sent")
	}
	final := receiveRunEvent(t, events, "run.snapshot")
	if final.State.Seq != first.State.Seq || final.Conversation == nil || final.Conversation.Revision != 5 || final.Conversation.Entities["cv:assistant"].Text != "最終本文" {
		t.Fatalf("same run seq did not accept newer final conversation revision: %+v", final)
	}
	assertWaitBlocked(t, waitDone, "final snapshot alone completed Wait before run.terminal")

	terminalOnce.Do(func() { close(allowTerminal) })
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not complete after final snapshot followed by run.terminal")
	}
	if waitErr != nil || result.Phase != "terminal" || result.Result == nil || result.Result.AgentResult == nil || result.Result.AgentResult.FinalText != "result" {
		t.Fatalf("terminal wait result = %+v, %v", result, waitErr)
	}
	terminalEvent := receiveRunEvent(t, events, "run.terminal")
	if terminalEvent.Conversation != nil || terminalEvent.State.Result == nil {
		t.Fatalf("run.terminal was not the light terminal result after the final snapshot: %+v", terminalEvent)
	}
}

func TestRunUpdateAppliesNewConversationRevisionAtSameRunSequence(t *testing.T) {
	var received []RunEvent
	handle := &runHandle{request: api.StartRunRequest{CoreID: "core-seq", RunID: "run-seq"}, observer: func(event RunEvent) {
		received = append(received, event)
	}}
	state := api.RunState{CoreID: "core-seq", RunID: "run-seq", Seq: 12, Phase: "running"}
	if !handle.publish(RunEvent{Kind: "run.snapshot", State: state, Conversation: &api.ConversationSnapshot{
		ScopeID: "run-seq", ViewID: "view-1", Revision: 3, Order: []string{}, Entities: map[string]api.ConversationEntity{},
	}}) {
		t.Fatal("initial run snapshot unexpectedly disconnected")
	}
	entity := api.ConversationEntity{ID: "cv:answer", Kind: "assistant", Text: "最新本文", Status: "streaming"}
	if !handle.publish(RunEvent{Kind: "run.update", State: state, Update: &api.ConversationUpdate{
		ScopeID: "run-seq", ViewID: "view-1", BaseRevision: 3, Revision: 4, Upserts: []api.ConversationEntity{entity},
	}}) {
		t.Fatal("same-sequence conversation update unexpectedly disconnected")
	}
	if handle.state.Seq != 12 || handle.conversationRevision != 4 || len(received) != 2 || received[1].Update == nil || received[1].Update.Upserts[0].Text != "最新本文" {
		t.Fatalf("run seq gate discarded an independent conversation revision: state=%+v revision=%d events=%+v", handle.state, handle.conversationRevision, received)
	}
}

func assertWaitBlocked(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
		t.Fatal(message)
	default:
	}
}

func receiveRunEvent(t *testing.T, events <-chan RunEvent, kind string) RunEvent {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind == kind {
				return event
			}
		case <-deadline.C:
			t.Fatalf("did not receive %s event", kind)
			return RunEvent{}
		}
	}
}

func writeRunSnapshotForTest(w http.ResponseWriter, flusher http.Flusher, state api.RunState, conversation api.ConversationSnapshot) {
	data, _ := json.Marshal(api.RunSnapshot{RunState: state, Conversation: conversation})
	_, _ = fmt.Fprintf(w, "event: run.snapshot\nid: %s/%s/%d\ndata: %s\n\n", state.CoreID, state.RunID, state.Seq, data)
	flusher.Flush()
}

func uint64Pointer(value uint64) *uint64 { return &value }

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
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs":
			writeJSONForTest(w, http.StatusAccepted, state)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs/run-retry/cancel":
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-retry":
			mu.Lock()
			stateReadCount++
			current := state
			mu.Unlock()
			writeJSONForTest(w, http.StatusOK, current)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-retry/events":
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
		Result: &api.RunResult{OK: true, AgentResult: &api.AgentResult{OK: true, Outcome: "final", FinalText: "done"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs":
			mu.Lock()
			postCount++
			mu.Unlock()
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack response: %v", err)
				return
			}
			_ = connection.Close()
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-stable":
			writeJSONForTest(w, http.StatusOK, terminal)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-stable/events":
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSEForTest(w, w.(http.Flusher), "run.snapshot", terminal)
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
	}
	disconnected := make(chan RunEvent, 8)
	recoveredSnapshot := make(chan struct{}, 1)
	terminalRequested := make(chan struct{})
	var terminalOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs":
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/"+runID:
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/"+runID+"/events":
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs/"+runID+"/cancel":
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
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs":
			dropHTTPResponse(t, w)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-close":
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
	conversation := api.ConversationSnapshot{ScopeID: "run", ViewID: "view-1", Order: []string{}, Entities: map[string]api.ConversationEntity{}}
	var data []byte
	if event == "run.terminal" {
		data, _ = json.Marshal(state)
	} else {
		data, _ = json.Marshal(api.RunSnapshot{RunState: state, Conversation: conversation})
	}
	_, _ = fmt.Fprintf(w, "event: %s\nid: %s/%s/%d\ndata: %s\n\n", event, state.CoreID, state.RunID, state.Seq, data)
	flusher.Flush()
}
