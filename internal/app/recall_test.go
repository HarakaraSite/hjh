package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
	"hjh/internal/recall"
)

func TestRecallMessageCarriesShortMarkerAndSourceAttribution(t *testing.T) {
	projection := recall.Projection{SchemaVersion: 1, SourceExecutionID: "source-1", SessionID: "session-1",
		Lifecycle: "settled", Outcome: "cancelled", Task: "original task", AutomaticReplay: false,
		Observations: []recall.Observation{{Kind: "assistant_partial", SourceLocator: contracts.ExecutionLocator{
			ExecutionID: "source-1", RecordID: "source-1:semantic:4", Ordinal: 4}, Text: "partial"}}}
	reference := contracts.RecallReference{CoreID: "core-1", OperationID: "operation-1", SourceExecutionID: "source-1", Generation: 3}
	message, attribution, err := recallMessage(projection, reference, "target-1", "target-1:semantic:1")
	if err != nil {
		t.Fatal(err)
	}
	if message.Role != "user" || !strings.Contains(message.VisibleText(), "original task") || message.ExecutionFact == nil ||
		message.ExecutionFact.Recall == nil || message.ExecutionFact.Text != "停止したexecutionの参考情報: source-1" {
		t.Fatalf("reference message = %#v", message)
	}
	if marker := message.ExecutionFact.Recall; marker.SourceExecutionID != "source-1" || marker.RelationRecordID != "target-1:semantic:1" || marker.OperationID != "operation-1" || marker.Generation != 3 {
		t.Fatalf("durable marker = %+v", marker)
	}
	external := attribution.externalRelation()
	if external["kind"] != "recall" || external["recordId"] != "target-1:semantic:1" {
		t.Fatalf("external relation = %#v", external)
	}
	locators, ok := external["sourceLocators"].([]contracts.ExecutionLocator)
	if !ok || len(locators) == 0 || locators[0].ExecutionID != "source-1" {
		t.Fatalf("recall source locator mapping = %#v", external["sourceLocators"])
	}
}

func TestRunAdmitsRecallBeforeProviderAndAttributesReference(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var step int
	var mu sync.Mutex
	var admissionSeen atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		step++
		current := step
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if current == 1 {
			sendChunk(w, map[string]any{"content": "途中で止まった本文"}, nil)
			fmt.Fprint(w, "data: {\"error\":{\"message\":\"stop source after visible text\"}}\n\n")
			return
		}
		if !admissionSeen.Load() {
			t.Error("provider started before app reported execution admission")
		}
		if len(request.Messages) != 3 || request.Messages[0].Role != "system" ||
			request.Messages[1].Role != "user" ||
			!strings.Contains(string(request.Messages[1].Content), "停止した過去execution") ||
			!strings.Contains(string(request.Messages[1].Content), "途中で止まった本文") ||
			request.Messages[2].Role != "user" || !strings.Contains(string(request.Messages[2].Content), "次のtask") {
			t.Errorf("provider request did not place the public reference before the new task: %+v", request.Messages)
		}
		if strings.Contains(string(request.Messages[1].Content), "providerState") || strings.Contains(string(request.Messages[1].Content), "replayItems") {
			t.Error("provider replay state entered the reference request")
		}
		sendChunk(w, map[string]any{"content": "続行完了"}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	provider := chatFixtureProvider(server.URL, "", "i10-app-recall", protocol.Options{})

	stopped, err := Run(context.Background(), store, provider, RunOptions{Task: "元のtask", Workspace: workspace, ModelID: "old/model"})
	if err == nil {
		t.Fatal("source stream failure unexpectedly completed")
	}
	source, err := store.Execution(context.Background(), stopped.ExecutionID)
	if err != nil || source.Lifecycle != "settled" || source.Outcome != string(history.OutcomeFailed) {
		t.Fatalf("stopped source = %+v, %v", source, err)
	}
	var admission contracts.ExecutionAdmission
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	continued, err := service.Run(context.Background(), RunRequest{Task: "次のtask", SessionID: stopped.SessionID,
		Workspace: workspace, ModelID: "new/model", AdmissionExecutionID: "target-candidate",
		Recall: &contracts.RecallReference{CoreID: "core-1", OperationID: "operation-1", SourceExecutionID: stopped.ExecutionID, Generation: 7},
		OnAdmission: func(value contracts.ExecutionAdmission) {
			admission = value
			admissionSeen.Store(true)
		}})
	if err != nil || !continued.Result.OK {
		t.Fatalf("continued run = %+v, %v", continued, err)
	}
	if admission.ExecutionID != "target-candidate" || admission.RecallRecordID == "" || admission.RecallGeneration != 7 {
		t.Fatalf("admission notice = %+v", admission)
	}

	var relationFound, markerIsLight bool
	var requestRecallFound bool
	records, err := store.Records(context.Background(), continued.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Kind == "recall_relation" && record.ID == admission.RecallRecordID {
			relationFound = strings.Contains(string(record.Payload), stopped.ExecutionID)
		}
		if record.Kind == "user_message" && strings.Contains(string(record.Payload), `"entityKind":"recall"`) {
			markerIsLight = !strings.Contains(string(record.Payload), "途中で止まった本文") &&
				strings.Contains(string(record.Payload), admission.RecallRecordID)
		}
		if record.Kind == "model_request" {
			var payload struct {
				SchemaVersion          int    `json:"schemaVersion"`
				RecallRelationRecordID string `json:"recallRelationRecordId"`
			}
			_ = json.Unmarshal(record.Payload, &payload)
			requestRecallFound = requestRecallFound || (payload.SchemaVersion == 1 && payload.RecallRelationRecordID == admission.RecallRecordID)
		}
	}
	if !relationFound || !markerIsLight || !requestRecallFound {
		t.Fatalf("saved recall path: relation=%t lightMarker=%t requestMapping=%t", relationFound, markerIsLight, requestRecallFound)
	}
	manifest, err := store.Context(context.Background(), continued.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var contextManifest struct {
		ExternalRelations []struct {
			Kind              string `json:"kind"`
			SourceExecutionID string `json:"sourceExecutionId"`
			TargetExecutionID string `json:"targetExecutionId"`
			RecordID          string `json:"recordId"`
		} `json:"externalRelations"`
	}
	if err := json.Unmarshal(manifest, &contextManifest); err != nil {
		t.Fatal(err)
	}
	if len(contextManifest.ExternalRelations) != 1 || contextManifest.ExternalRelations[0].Kind != "recall" ||
		contextManifest.ExternalRelations[0].SourceExecutionID != stopped.ExecutionID ||
		contextManifest.ExternalRelations[0].TargetExecutionID != continued.ExecutionID ||
		contextManifest.ExternalRelations[0].RecordID != admission.RecallRecordID {
		t.Fatalf("context external relation = %+v", contextManifest.ExternalRelations)
	}
	sessionContext, err := store.SessionContext(context.Background(), continued.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sessionContext.LatestRecallAdmission == nil || sessionContext.LatestRecallAdmission.RequestRecordOrdinal == nil ||
		*sessionContext.LatestRecallAdmission.RequestRecordOrdinal == 0 {
		t.Fatalf("saved parent request does not attribute recall admission: %+v", sessionContext.LatestRecallAdmission)
	}
}

func TestRecalledRootCanDelegateWithoutPassingReferenceToChild(t *testing.T) {
	store, workspace := delegationStore(t)
	var childCalled bool
	provider := delegationProvider(func(ctx context.Context, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		last := request.Messages[len(request.Messages)-1]
		if last.VisibleText() == "stopped source" {
			return contracts.ModelResult{}, errors.New("source stopped")
		}
		if last.VisibleText() == "child task" {
			childCalled = true
			if len(request.Messages) != 1 {
				t.Fatalf("child inherited parent reference: %d messages", len(request.Messages))
			}
			return contracts.ModelResult{Text: "child completed", FinishReason: "stop"}, nil
		}
		if last.VisibleText() == "parent task" {
			if len(request.Messages) != 2 || !strings.Contains(request.Messages[0].VisibleText(), "sourceExecutionId") {
				t.Fatal("root reference missing")
			}
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "child-call", Name: "run_agent", Arguments: contracts.JSON(map[string]string{"task": "child task"})}}, FinishReason: "tool_calls"}, nil
		}
		if last.Role == "tool" && !strings.Contains(string(last.Content), "child completed") {
			t.Fatalf("child result: %s", last.Content)
		}
		return contracts.ModelResult{Text: "parent completed", FinishReason: "stop"}, nil
	})
	source, err := Run(context.Background(), store, provider, RunOptions{Workspace: workspace, Task: "stopped source"})
	if err == nil {
		t.Fatal("source did not stop")
	}
	result, err := Run(context.Background(), store, provider, RunOptions{Workspace: workspace, SessionID: source.SessionID, Task: "parent task", Recall: &contracts.RecallReference{CoreID: "test-core", OperationID: "test-recall", SourceExecutionID: source.ExecutionID, Generation: 1}})
	if err != nil || !childCalled || result.Result.FinalText != "parent completed" {
		t.Fatalf("child called=%v result=%+v error=%v", childCalled, result, err)
	}
}

func TestRecallKeepsActualStoppedRunJSActivityAsMetadata(t *testing.T) {
	store, workspace := delegationStore(t)
	const privateBody = "INNER_BODY_DO_NOT_DUPLICATE"
	if err := os.WriteFile(filepath.Join(workspace, "inner.txt"), []byte(privateBody), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := delegationProvider(func(ctx context.Context, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		if request.Messages[len(request.Messages)-1].VisibleText() == "stopped JS" {
			return scriptedJS(`await tools.read_file({path:"inner.txt"}); await tools.command({program:"sleep",args:["30"]}); return "done";`), nil
		}
		reference := request.Messages[0].VisibleText()
		if strings.Contains(reference, privateBody) {
			t.Fatal("inner body expanded alongside unfinished outer call")
		}
		start := strings.Index(reference, "{")
		var projection recall.Projection
		if start < 0 {
			t.Fatal("reference JSON missing")
		}
		if err := json.Unmarshal([]byte(reference[start:]), &projection); err != nil {
			t.Fatal(err)
		}
		var outer int
		for _, observation := range projection.Observations {
			if observation.Kind != "tool_exchange" {
				continue
			}
			outer++
			if observation.Call.Name != "run_js" {
				t.Fatalf("inner call expanded as outer: %+v", observation)
			}
			if observation.Result == nil || len(observation.Activity) != 0 {
				t.Fatalf("saved outer result must be authoritative: %+v", observation)
			}
		}
		if outer != 1 {
			t.Fatalf("actual saved outer exchanges: %+v", projection.Observations)
		}

		return contracts.ModelResult{Text: "activity verified", FinishReason: "stop"}, nil
	})
	source, err := Run(ctx, store, provider, RunOptions{Workspace: workspace, Task: "stopped JS", Progress: func(o contracts.Observation) error {
		if o.Name == "command" && o.Phase == "started" {
			cancel()
		}
		return nil
	}})
	if err == nil {
		t.Fatal("source should stop")
	}
	// A missing outer completion must still use the exact Observation JSON
	// written by the VM/journal, rather than a fixture with renamed keys.
	saved, e := store.RecallEvidence(context.Background(), source.ExecutionID)
	if e != nil {
		t.Fatal(e)
	}
	evidence := recall.Evidence{Execution: recall.Execution{ExecutionID: source.ExecutionID, SessionID: source.SessionID, Lifecycle: "settled", Outcome: "interrupted", Task: "stopped JS"}}
	for _, message := range saved.Messages {
		var value contracts.Message
		if e = json.Unmarshal(message.JSON, &value); e != nil {
			t.Fatal(e)
		}
		if value.Role != "tool" {
			evidence.Messages = append(evidence.Messages, recall.Message{ExecutionID: message.ExecutionID, Ordinal: message.Ordinal, JSON: message.JSON})
		}
	}
	sawReadResult := false
	for _, record := range saved.Records {
		var observation contracts.Observation
		_ = json.Unmarshal(record.Payload, &observation)
		if record.Kind == "tool_result" && observation.Name == "read_file" {
			if record.Content != nil || !strings.Contains(string(observation.Result), privateBody) {
				t.Fatalf("RecallEvidence did not restore the actual read_file result once: payload=%s content=%d", record.Payload, len(record.Content))
			}
			sawReadResult = true
		}
		if record.Kind == "tool_result" && observation.ID == "script-call" {
			continue
		}
		evidence.Records = append(evidence.Records, recall.Record{ID: record.ID, ExecutionID: record.ExecutionID, Ordinal: record.Ordinal, Kind: record.Kind, Payload: record.Payload, Conversation: record.Conversation})
	}
	if !sawReadResult {
		t.Fatal("RecallEvidence omitted the completed nested read_file result")
	}
	projected, e := recall.Project(evidence)
	if e != nil {
		t.Fatal(e)
	}
	var outer int
	var readDone, commandCancelled bool
	for _, observation := range projected.Observations {
		if observation.Kind != "tool_exchange" {
			continue
		}
		outer++
		if observation.Call.Name != "run_js" {
			t.Fatalf("inner call expanded as outer: %+v", observation)
		}
		for _, activity := range observation.Activity {
			if activity.Name == "read_file" && activity.Status == "completed" {
				readDone = true
			}
			if activity.Name == "command" && activity.Status == "cancelled" {
				commandCancelled = true
			}
		}
	}
	if outer != 1 || !readDone || !commandCancelled {
		t.Fatalf("unfinished outer actual-journal activity: %+v", projected.Observations)
	}
	encoded, _ := json.Marshal(projected)
	if strings.Contains(string(encoded), privateBody) {
		t.Fatal("inner result body duplicated into reference")
	}
	_, err = Run(context.Background(), store, provider, RunOptions{Workspace: workspace, SessionID: source.SessionID, Task: "read recalled activity", Recall: &contracts.RecallReference{CoreID: "activity-core", OperationID: "activity-recall", SourceExecutionID: source.ExecutionID, Generation: 1}})
	if err != nil {
		t.Fatal(err)
	}
}
