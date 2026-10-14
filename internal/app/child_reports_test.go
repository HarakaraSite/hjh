package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestChildReportsReachParentRequestAndSavedStatusResults(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("source evidence"), 0644); err != nil {
		t.Fatal(err)
	}
	ready, release := make(chan struct{}), make(chan struct{})
	var parentStep, childStep atomic.Int32
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := req.Messages[0].VisibleText()
		if task == "report child" {
			if childStep.Add(1) == 1 {
				return contracts.ModelResult{Text: "first finding", Calls: []contracts.ToolCall{{CallID: "child-read", Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": "source.txt"})}}}, nil
			}
			close(ready)
			select {
			case <-release:
			case <-ctx.Done():
				return contracts.ModelResult{}, ctx.Err()
			}
			return contracts.ModelResult{Text: "final finding"}, nil
		}
		switch parentStep.Add(1) {
		case 1:
			return scriptedJS(`const child = await api.agents.spawn({task:"report child",tools:["read_file"]}); globalThis.reportChildID=child.executionId; return child;`), nil
		case 2:
			select {
			case <-ready:
			case <-ctx.Done():
				return contracts.ModelResult{}, ctx.Err()
			}
			return scriptedJS(`const first = await api.agents.status({executionId:globalThis.reportChildID});
const again = await api.agents.status({executionId:globalThis.reportChildID});
const empty = await api.agents.status({executionId:globalThis.reportChildID,after:first.nextCursor});
if (first.reports.length!==1 || JSON.stringify(first.reports)!==JSON.stringify(again.reports) || empty.reports.length!==0) throw new Error("report cursor mismatch");
globalThis.reportCursor=first.nextCursor;
return {first,again,empty};`), nil
		case 3:
			body, _ := json.Marshal(req.Messages)
			if !strings.Contains(string(body), "first finding") {
				t.Error("parent model input missing returned status report")
			}
			close(release)
			return scriptedJS(`const result = await api.agents.collect({executionId:globalThis.reportChildID});
const delta = await api.agents.status({executionId:globalThis.reportChildID,after:globalThis.reportCursor});
if (delta.reports.length!==1 || delta.reports[0].text!=="final finding") throw new Error("final report missing from delta");
return {result,delta};`), nil
		default:
			body, _ := json.Marshal(req.Messages)
			if !strings.Contains(string(body), "final finding") {
				t.Error("parent model input missing final delta")
			}
			return contracts.ModelResult{Text: "parent report complete"}, nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "report parent", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("run=%+v err=%v", result, err)
	}
	children, err := store.Children(ctx, result.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children=%+v err=%v", children, err)
	}
	reports, cursor, err := store.AssistantReports(ctx, children[0].ID, -1)
	if err != nil || len(reports) != 2 || cursor != 3 {
		t.Fatalf("source reports=%+v cursor=%d err=%v", reports, cursor, err)
	}
	records, err := store.Records(ctx, result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	observedReports := map[int64]string{}
	for _, record := range records {
		var observation contracts.Observation
		if json.Unmarshal(record.Payload, &observation) != nil || observation.Name != "agents.status" || observation.Phase != "completed" {
			continue
		}
		if record.Content != nil {
			t.Fatalf("agents.status detail duplicated its result in Content: payload=%s content=%d", record.Payload, len(record.Content))
		}
		var status contracts.AgentStatus
		if err := json.Unmarshal(observation.Result, &status); err != nil {
			t.Fatal(err)
		}
		if len(status.Reports) > 0 && !strings.Contains(string(observation.Result), status.Reports[0].Text) {
			t.Fatalf("agents.status detail did not restore its reports: payload=%s", record.Payload)
		}
		for _, report := range status.Reports {
			if report.ExecutionID != children[0].ID {
				t.Fatalf("wrong status report source %+v", report)
			}
			observedReports[report.MessageOrdinal] = report.Text
		}
	}
	for _, report := range reports {
		if observedReports[report.MessageOrdinal] != report.Text {
			t.Fatalf("saved status lost report %+v", report)
		}
	}
	conversation, err := store.Conversation(ctx, result.SessionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var historyText strings.Builder
	for _, message := range conversation {
		historyText.Write(message.JSON)
	}
	if !strings.Contains(historyText.String(), "first finding") || !strings.Contains(historyText.String(), "final finding") {
		t.Fatal("adopted parent tool history missing returned reports")
	}
}
