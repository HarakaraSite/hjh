package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

func TestUsageSummaryStorageLocalJSAndReopen(t *testing.T) {
	store, workspace := delegationStore(t)
	// Synthetic values in the Responses shape observed in the approved plan.
	usages := []json.RawMessage{
		json.RawMessage(`{"input_tokens":140,"output_tokens":35,"total_tokens":175,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":15},"attribution":{"items":{"first":{"tokens":140}},"request_fields":{"instructions":20}}}`),
		json.RawMessage(`{"input_tokens":240,"output_tokens":45,"total_tokens":285,"input_tokens_details":{"cached_tokens":200,"cache_write_tokens":10},"output_tokens_details":{"reasoning_tokens":25},"attribution":{"items":{"second":{"tokens":240}},"request_fields":{"instructions":30}}}`),
	}
	texts := []string{"要約 first", "要約 second"}
	var wires [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		wires = append(wires, body)
		call := len(wires)
		text := "normal answer"
		usage := json.RawMessage(`{"input_tokens":70,"output_tokens":5,"total_tokens":75}`)
		if call <= 2 {
			if !bytes.Contains(body, []byte(fmt.Sprintf("SUMMARY_INPUT_%d", call))) {
				t.Errorf("summary request %d not sent: %s", call, body)
			}
			text, usage = texts[call-1], usages[call-1]
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": fmt.Sprintf("resp_%d", call), "status": "completed", "usage": usage,
			"output": []any{map[string]any{"type": "message", "id": fmt.Sprintf("msg_%d", call), "role": "assistant",
				"content": []any{map[string]string{"type": "output_text", "text": text}}}},
		}}))
	}))
	defer server.Close()
	configPath, modelID := standardLocalProviderConfig(t, workspace, "openai-chatgpt", server.URL)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  const expected=`+string(contracts.JSON(usages))+`;
  const texts=`+string(contracts.JSON(texts))+`;
  const before=await api.context.measure();
  for(let i=0;i<2;i++) {
    const result=await api.model.generate({instruction:'SUMMARY_INSTRUCTION_'+(i+1),messages:[{role:'user',content:{kind:'text',text:'SUMMARY_INPUT_'+(i+1)}}]});
    if(result.text!==texts[i] || result.finishReason!=='completed' || JSON.stringify(result.usage)!==JSON.stringify(expected[i])) throw new Error('complete model result lost');
  }
  const after=await api.context.measure();
  if(JSON.stringify(before)!==JSON.stringify(after)) throw new Error('summary changed root context estimate');
}`)
	var live []contracts.Observation
	var notified []contracts.ContextBudgetStatus
	result, err := Run(t.Context(), store, nil, RunOptions{Workspace: workspace, ConfigPath: configPath,
		Task: "normal root task", ProviderID: "openai-chatgpt", ModelID: modelID,
		Progress: func(o contracts.Observation) error {
			if o.Kind == "host" && o.Name == "context.model.generate" && o.Phase == "completed" {
				live = append(live, o)
			}
			return nil
		}, OnContextBudget: func(status contracts.ContextBudgetStatus) { notified = append(notified, status) }})
	if err != nil || !result.Result.OK || result.Result.FinalText != "normal answer" || len(wires) != 3 {
		t.Fatalf("JS/local HTTP run: %+v calls=%d err=%v", result, len(wires), err)
	}
	if len(live) != 2 || live[0].ID == "" || live[0].ID == live[1].ID || live[0].Parent == "" || live[0].Parent != live[1].Parent {
		t.Fatalf("Host calls were not independent within one hook: %+v", live)
	}
	wantResults := make(map[string]json.RawMessage)
	for i, o := range live {
		if o.ExecutionID != result.ExecutionID || o.ParentExecutionID != "" {
			t.Fatalf("Host execution changed: %+v", o)
		}
		metadata := usageStorageObject(t, o.Result)
		requireUsageStorageJSON(t, metadata["usage"], usages[i])
		value := contracts.JSON(contextModelResult{Text: texts[i], FinishReason: "completed", Usage: usages[i]})
		requireUsageStorageJSON(t, metadata["outputBytes"], contracts.JSON(len(value)))
		requireUsageStorageJSON(t, metadata["finishReason"], contracts.JSON("completed"))
		aggregate := usageStorageObject(t, usages[i])
		delete(aggregate, "attribution")
		metadata["usage"] = contracts.JSON(aggregate)
		wantResults[o.ID] = contracts.JSON(metadata)
	}
	if len(notified) != 1 || notified[0].ExecutionID != result.ExecutionID || notified[0].ModelStep != 1 {
		t.Fatalf("summary entered root budget notifications: %+v", notified)
	}
	children, err := store.Children(t.Context(), result.ExecutionID)
	if err != nil || len(children) != 0 {
		t.Fatalf("summary created child execution: %+v err=%v", children, err)
	}

	checkSaved := func(reader *history.Store) {
		t.Helper()
		service := &Service{store: reader, snapshotOwner: NewSnapshotOwner()}
		records, err := reader.Records(t.Context(), result.ExecutionID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		starts := map[string]string{}
		hookCompleted, hostCount, usageCount, wireCount, budgetCount := 0, 0, 0, 0, 0
		for _, record := range records {
			var o contracts.Observation
			if err := json.Unmarshal(record.Payload, &o); err != nil {
				t.Fatal(err)
			}
			if o.Phase == "started" {
				starts[o.ID] = record.ID
			}
		}
		// Read only this test's disposable DB to inspect physical contents too.
		db, err := sql.Open("sqlite", "file:"+filepath.Join(workspace, "history.sqlite3")+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, record := range records {
			var o contracts.Observation
			if err := json.Unmarshal(record.Payload, &o); err != nil {
				t.Fatal(err)
			}
			if record.Kind == "context_budget" {
				budgetCount++
			}
			if o.Kind == "usage" {
				usageCount++
			}
			if record.Kind == "model_request" && len(record.Content) > 0 {
				wireCount++
				if !bytes.Equal(record.Content, wires[2]) || bytes.Contains(record.Content, []byte("SUMMARY_")) {
					t.Fatalf("summary replaced root wire: %s", record.Content)
				}
			}
			if o.Kind == "hook" && o.Name == "projectRequest" && o.Phase == "completed" {
				hookCompleted++
				if o.ID != live[0].Parent {
					t.Fatalf("parent hook ID changed: %+v", o)
				}
				requireUsageSummaryRelation(t, record, "call_start", starts[o.ID])
			}
			if o.Kind != "host" || o.Name != "context.model.generate" || o.Phase != "completed" {
				continue
			}
			hostCount++
			want, ok := wantResults[o.ID]
			if !ok || record.Kind != "effect_observation" || o.Parent != live[0].Parent || o.ExecutionID != result.ExecutionID {
				t.Fatalf("Host ID/scope changed: %+v", o)
			}
			requireUsageStorageJSON(t, o.Result, want)
			requireUsageSummaryRelation(t, record, "parent_call", starts[o.Parent])
			requireUsageSummaryRelation(t, record, "call_start", starts[o.ID])
			one, err := service.Record(t.Context(), result.ExecutionID, record.Ordinal)
			if err != nil || !reflect.DeepEqual(one, record) {
				t.Fatalf("Record differs from Records: %s err=%v", record.ID, err)
			}
			var storedPayload string
			var content []byte
			if err := db.QueryRowContext(t.Context(), `SELECT r.payload_json,c.content_bytes FROM semantic_records r JOIN contents c ON c.content_digest=r.content_digest WHERE r.record_id=?`, record.ID).Scan(&storedPayload, &content); err != nil {
				t.Fatal(err)
			}
			stored := usageStorageObject(t, json.RawMessage(storedPayload))
			if _, duplicate := stored["result"]; duplicate || len(stored["resultRef"]) == 0 || record.Content != nil {
				t.Fatalf("Host result not stored once through resultRef: %s", storedPayload)
			}
			requireUsageStorageJSON(t, content, want)
			if bytes.Contains(content, []byte(`"attribution"`)) || strings.Contains(storedPayload, `"attribution"`) {
				t.Fatalf("raw usage re-entered storage: %s %s", storedPayload, content)
			}
		}
		if hostCount != 2 || hookCompleted != 1 || usageCount != 1 || wireCount != 1 || budgetCount != 1 {
			t.Fatalf("saved Host/hook/root usage/wire/budget=%d/%d/%d/%d/%d", hostCount, hookCompleted, usageCount, wireCount, budgetCount)
		}
		state, _ := budgetFact(t, reader, result.SessionID)
		if state.LatestRequest == nil || state.LatestRequest.ExecutionID != result.ExecutionID || state.LatestRequest.ModelStep != 1 || state.LatestRequest.WireBytes != int64(len(wires[2])) {
			t.Fatalf("summary changed latest root request: %+v", state.LatestRequest)
		}
		if state.LatestBudget.ExecutionID != result.ExecutionID || state.LatestBudget.ModelStep != 1 || !reflect.DeepEqual(state.LatestBudget.Summary, notified[0].Summary) || state.LatestBudget.Summary.WireBytes != int64(len(wires[2])) {
			t.Fatalf("summary changed latest root budget: %+v", state.LatestBudget)
		}
	}
	checkSaved(store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	checkSaved(reader)
}

func requireUsageSummaryRelation(t *testing.T, record history.Record, name, target string) {
	t.Helper()
	for _, relation := range record.Relations {
		if target != "" && relation.Relation == name && relation.TargetRecordID == target && relation.Mandatory && relation.Resolved {
			return
		}
	}
	t.Fatalf("missing %s relation to %q: %+v", name, target, record.Relations)
}

func TestUsageSummaryStorageLeavesOtherResultsUntouched(t *testing.T) {
	raw := json.RawMessage(` {"usage":{"attribution":{"keep":true}},"text":"<p>&日本語","number":1.2500} `)
	for _, o := range []contracts.Observation{
		{Kind: "host", Name: "readFile", Result: raw},
		{Kind: "model_tool", Name: "context.model.generate", Result: raw},
		{Kind: "host", Name: "context.model.generate", Result: json.RawMessage(` {"usage":{"total_tokens":0},"outputBytes":80} `)},
	} {
		saved, err := observationForStorage(o)
		if err != nil || !bytes.Equal(saved.Result, o.Result) {
			t.Fatalf("unrelated result bytes changed: %s err=%v", saved.Result, err)
		}
	}
}
