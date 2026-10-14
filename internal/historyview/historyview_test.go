package historyview

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

func TestRenderSessionsExtendsCollidingShortIDs(t *testing.T) {
	title := "same work"
	sessions := []history.Session{
		{ID: "session-alpha", WorkspaceRoot: "/work/a", Title: &title, TurnCount: 2, UpdatedAt: "2026-10-12T10:00:00Z"},
		{ID: "session-beta", WorkspaceRoot: "/work/b", Title: &title, TurnCount: 3, UpdatedAt: "2026-10-12T11:00:00Z"},
	}
	got := RenderSessions(sessions)
	for _, expected := range []string{"session-a", "session-b", "same work", "/work/a", "2 turns", "updated 2026-10-12T10:00:00Z"} {
		if !strings.Contains(got, expected) {
			t.Errorf("sessions output omits %q: %s", expected, got)
		}
	}
	if strings.Contains(got, "session-alpha") || strings.Contains(got, "session-beta") {
		t.Fatalf("sessions output did not shorten IDs: %s", got)
	}
}

func TestWriteDetailRecordPreservesBudgetSummaryAndDetailGranularity(t *testing.T) {
	ctx := context.Background()
	store, err := history.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession(ctx, history.SessionInput{ID: "detail-budget-session", WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`)})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, history.BeginExecutionInput{ExecutionID: "detail-budget-execution", SessionID: session.ID,
		Task: "task", Model: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for step := 1; step <= 2; step++ {
		payload := contracts.JSON(map[string]any{"modelStep": step,
			"summary":         map[string]any{"estimatedTokens": 100 + step*10, "selectedMessageCount": step},
			"detailAvailable": false})
		fact := contracts.JSON(map[string]any{"sources": []any{map[string]any{"kind": "current", "messageOrdinal": step}}})
		if _, err := store.AppendLatestBudget(ctx, execution.ID, payload, fact); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var budgetRecords []history.Record
	for _, record := range saved {
		if record.Kind == "context_budget" {
			budgetRecords = append(budgetRecords, record)
		}
	}
	if len(budgetRecords) != 2 {
		t.Fatalf("budget records=%d: %+v", len(budgetRecords), budgetRecords)
	}
	var output bytes.Buffer
	if err := WriteDetailRecord(&output, budgetRecords[0]); err != nil {
		t.Fatal(err)
	}
	if err := WriteDetailRecord(&output, budgetRecords[1]); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("detail lines=%d: %s", len(lines), output.String())
	}
	var records []struct {
		Kind  string `json:"kind"`
		Value struct {
			Payload map[string]json.RawMessage `json:"payload"`
		} `json:"value"`
	}
	for _, line := range lines {
		var record struct {
			Kind  string `json:"kind"`
			Value struct {
				Payload map[string]json.RawMessage `json:"payload"`
			} `json:"value"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if records[0].Kind != "record" || records[1].Kind != "record" {
		t.Fatalf("detail record kinds: %+v", records)
	}
	var olderAvailable, latestAvailable bool
	if err := json.Unmarshal(records[0].Value.Payload["detailAvailable"], &olderAvailable); err != nil {
		t.Fatal(err)
	}
	if _, hasFact := records[0].Value.Payload["fact"]; hasFact || olderAvailable {
		t.Fatalf("older budget detail was presented as full: %+v", records[0].Value.Payload)
	}
	if err := json.Unmarshal(records[1].Value.Payload["detailAvailable"], &latestAvailable); err != nil {
		t.Fatal(err)
	}
	if !latestAvailable || len(records[1].Value.Payload["fact"]) == 0 {
		t.Fatalf("latest budget detail missing: %+v", records[1].Value.Payload)
	}
}

func TestSelectSessionUsesSharedCaseInsensitivePrefixResolution(t *testing.T) {
	sessions := []history.Session{{ID: "Session-Alpha"}, {ID: "Session-Beta"}}
	selected, ok, err := SelectSession(sessions, "", "SESSION-B")
	if err != nil || !ok || selected.ID != "Session-Beta" {
		t.Fatalf("selected = %+v, %t, %v", selected, ok, err)
	}
	if _, _, err := SelectSession(sessions, "", "session-"); err == nil || !strings.Contains(err.Error(), "Session-A") || !strings.Contains(err.Error(), "Session-B") {
		t.Fatalf("ambiguous result omitted distinguishable candidates: %v", err)
	}
}
