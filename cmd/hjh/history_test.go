package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

func TestRunHistoryMissingDatabaseDoesNotCreateIt(t *testing.T) {
	workspace := t.TempDir()
	dbPath := filepath.Join(workspace, ".hjh", "history.sqlite3")
	var stdout, stderr bytes.Buffer
	code := runHistory(context.Background(), []string{"--workspace", workspace, "--db", dbPath}, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || strings.TrimSpace(stderr.String()) != "# no history" {
		t.Fatalf("runHistory = code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("history database exists after read: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = runHistory(context.Background(), []string{"--workspace", workspace, "--db", dbPath}, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || strings.TrimSpace(stderr.String()) != "# no history" {
		t.Fatalf("empty history = code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestRunHistorySelectsLatestInWorkspaceAndExplicitPrefixGlobally(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	otherWorkspace := filepath.Join(root, "other")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(otherWorkspace, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "history.sqlite3")
	store, err := history.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	createHistorySession(t, store, "local-old", workspace, "2026-10-10T00:00:00.000Z")
	createHistorySession(t, store, "local-new-a", workspace, "2026-10-11T00:00:00.000Z")
	createHistorySession(t, store, "local-new-z", workspace, "2026-10-11T00:00:00.000Z")
	createHistorySession(t, store, "external-newest", otherWorkspace, "2026-10-12T00:00:00.000Z")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runHistory(context.Background(), []string{"--db", dbPath, "--workspace", workspace, "--view", "canonical"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("latest history exit = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "# session local-new-a") || strings.Contains(stderr.String(), "external-newest") {
		t.Fatalf("latest session selection = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runHistory(context.Background(), []string{"--db", dbPath, "--workspace", workspace,
		"--session", "external", "--view", "canonical"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "# session external") {
		t.Fatalf("explicit prefix exit = %d, stderr %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runHistory(context.Background(), []string{"--db", dbPath, "--workspace", workspace,
		"--session", "local-new", "--view", "canonical"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "ambiguous") {
		t.Fatalf("ambiguous Session prefix exit = %d, stderr %q", code, stderr.String())
	}
}

func TestRunHistoryCanonicalAndJSONUseAdoptedMessagesAndTurnRange(t *testing.T) {
	workspace := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "history.sqlite3")
	sessionID, executionID := persistAdoptedTurn(t, dbPath, workspace)

	var stdout, stderr bytes.Buffer
	code := runHistory(context.Background(), []string{"--db", dbPath, "--workspace", workspace,
		"--session", sessionID, "--view", "canonical", "--from-turn", "1", "--to-turn", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("canonical history exit = %d: %s", code, stderr.String())
	}
	for _, expected := range []string{"# hjh Session History", "## Turn 1", "the saved question", "the saved answer"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("canonical history does not contain %q: %s", expected, stdout.String())
		}
	}
	if !strings.Contains(stdout.String(), "- Session: `saved-se`") || strings.Contains(stdout.String(), "- Session: `saved-session`") {
		t.Fatalf("canonical Session identity was not shortened: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = runHistory(context.Background(), []string{"--db", dbPath, "--workspace", workspace,
		"--session", sessionID, "--json", "--from-turn", "1", "--to-turn", "1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("JSON history exit = %d: %s", code, stderr.String())
	}
	var messages []struct {
		ExecutionID string `json:"executionId"`
		TurnNumber  int64  `json:"turnNumber"`
		JSON        struct {
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"json"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &messages); err != nil {
		t.Fatalf("decode adopted-message JSON: %v: %s", err, stdout.String())
	}
	if len(messages) != 2 || messages[0].ExecutionID != executionID || messages[1].TurnNumber != 1 ||
		messages[0].JSON.Content.Text != "the saved question" || messages[1].JSON.Content.Text != "the saved answer" {
		t.Fatalf("adopted JSON messages = %+v", messages)
	}
}

func TestRunHistoryDetailPreservesPersistedToolDataAndPartialThinking(t *testing.T) {
	workspace := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "history.sqlite3")
	store, sessionID, executionID := openHistorySession(t, dbPath, workspace, "detail-session", "2026-10-11T00:00:00.000Z")
	configurationID := "detail-configuration"
	if err := store.SaveConfiguration(context.Background(), configurationID, json.RawMessage(`{"kind":"test"}`)); err != nil {
		t.Fatal(err)
	}
	model, err := json.Marshal(contracts.OpenRouterModel("test/model"))
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(context.Background(), history.BeginExecutionInput{ExecutionID: executionID,
		Task: "inspect a file", SessionID: sessionID, AgentName: "default", Model: model,
		Build: json.RawMessage(`{}`), ConfigurationID: configurationID, CreatedAt: "2026-10-11T00:00:01.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", RequestOrdinal: 2, ModelStep: 2}
	if _, err := store.SaveAssistantProgress(context.Background(), history.AssistantProgressInput{
		RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 1, Request: request,
		Text: "unfinished answer", Thinking: []contracts.ThinkingItem{{Key: "reasoning-2", Text: "unfinished public thought", Kind: "reasoning"}},
		ObservedAt: "2026-10-11T00:00:02.000Z"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetContext(context.Background(), execution.ID, json.RawMessage(`{"turn":1,"summary":"captured context"}`)); err != nil {
		t.Fatal(err)
	}
	call := contracts.ToolCall{CallID: "call-17", Name: "read_file", Arguments: json.RawMessage(`{"path":"full/path.txt","include":"complete"}`)}
	callMessage := contracts.JSON(contracts.AssistantCalls([]contracts.ToolCall{call}, "reading", nil))
	_, _, err = store.AppendConversationMessage(context.Background(), history.ConversationMessageInput{
		RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 2, Message: callMessage,
		Facts: []contracts.ExecutionFact{{Kind: "tool_call", Phase: "started", Name: call.Name, CallID: call.CallID,
			Conversation: contracts.ConversationMetadata{EntityKind: "tool", LocalKey: call.CallID}}},
		ObservedAt: "2026-10-11T00:00:03.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	result := contracts.ToolResult{CallID: call.CallID, Name: call.Name, Text: "full tool result body", Outcome: "success"}
	resultMessage := contracts.JSON(contracts.ToolResults([]contracts.ToolResult{result}))
	_, _, err = store.AppendConversationMessage(context.Background(), history.ConversationMessageInput{
		RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 3, Message: resultMessage,
		Facts: []contracts.ExecutionFact{{Kind: "tool_result", Phase: "completed", Outcome: "success", Name: result.Name,
			CallID: result.CallID, Conversation: contracts.ConversationMetadata{EntityKind: "tool", LocalKey: result.CallID}}},
		ObservedAt: "2026-10-11T00:00:04.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(context.Background(), execution.ID, history.OccurrenceInput{
		Kind: "resource_revision", Payload: json.RawMessage(`{"path":"extension/image.bin"}`),
		Content: []byte{0xff, 0x00, 0x80, 0x42}, ObservedAt: "2026-10-11T00:00:05.000Z",
	}); err != nil {
		t.Fatal(err)
	}
	persistedMessages, err := store.Messages(context.Background(), execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	persistedRecords, err := store.Records(context.Background(), execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runHistory(context.Background(), []string{"--db", dbPath, "--workspace", workspace,
		"--session", sessionID, "--view", "detail"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("detail history exit = %d: %s", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, `"identity":"detail-session"`) {
		t.Fatal("detail output did not preserve the complete Session ID")
	}
	for _, expected := range []string{"\"executionId\":\"detail-session-execution\"", "full/path.txt", "full tool result body",
		"unfinished answer", "unfinished public thought", "\"callId\":\"call-17\"", "captured context",
		"\"snapshot\":{\"kind\":\"test\"}"} {
		if !strings.Contains(text, expected) {
			t.Errorf("detail history does not contain %q: %s", expected, text)
		}
	}

	kindCounts := make(map[string]int)
	recordIDs := make(map[string]bool)
	recordContents := make(map[string][]byte)
	for _, record := range persistedRecords {
		recordContents[record.ID] = record.Content
	}
	for index, line := range bytes.Split(bytes.TrimSpace(stdout.Bytes()), []byte("\n")) {
		var item struct {
			Kind     string                     `json:"kind"`
			Identity string                     `json:"identity"`
			Value    map[string]json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(line, &item); err != nil {
			t.Fatalf("decode detail line %d: %v: %s", index+1, err, line)
		}
		kindCounts[item.Kind]++
		switch item.Kind {
		case "message":
			var raw json.RawMessage
			if err := json.Unmarshal(item.Value["json"], &raw); err != nil || !json.Valid(raw) {
				t.Errorf("detail message %q omitted full persisted JSON: %s", item.Identity, line)
			}
		case "record":
			var id string
			if err := json.Unmarshal(item.Value["id"], &id); err != nil || id != item.Identity {
				t.Errorf("detail record identity %q does not match persisted record ID %q: %s", item.Identity, id, line)
			}
			for _, field := range []string{"payload", "content", "relations"} {
				if _, ok := item.Value[field]; !ok {
					t.Errorf("detail record %q omitted %s", item.Identity, field)
				}
			}
			var content []byte
			if err := json.Unmarshal(item.Value["content"], &content); err != nil {
				t.Errorf("decode detail record %q content: %v", id, err)
			} else if !bytes.Equal(content, recordContents[id]) {
				t.Errorf("detail record %q content differs from saved bytes", id)
			}
			recordIDs[id] = true
		}
	}
	if kindCounts["message"] != len(persistedMessages) || kindCounts["record"] != len(persistedRecords) {
		t.Errorf("detail message/record counts = %d/%d, persisted counts = %d/%d",
			kindCounts["message"], kindCounts["record"], len(persistedMessages), len(persistedRecords))
	}
	for _, record := range persistedRecords {
		if !recordIDs[record.ID] {
			t.Errorf("detail output omitted persisted record %q (%s)", record.ID, record.Kind)
		}
	}
	if kindCounts["configuration"] != 1 || kindCounts["context"] != 1 {
		t.Errorf("detail configuration/context count = %d/%d", kindCounts["configuration"], kindCounts["context"])
	}
}

func createHistorySession(t *testing.T, store *history.Store, id, workspace, createdAt string) {
	t.Helper()
	activeModel, err := json.Marshal(contracts.OpenRouterModel("test/model"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(context.Background(), history.SessionInput{ID: id, WorkspaceRoot: workspace,
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: activeModel,
		CreatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
}

func openHistorySession(t *testing.T, dbPath, workspace, sessionID, createdAt string) (*history.Store, string, string) {
	t.Helper()
	store, err := history.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	createHistorySession(t, store, sessionID, workspace, createdAt)
	return store, sessionID, sessionID + "-execution"
}

func persistAdoptedTurn(t *testing.T, dbPath, workspace string) (string, string) {
	t.Helper()
	store, sessionID, executionID := openHistorySession(t, dbPath, workspace, "saved-session", "2026-10-11T00:00:00.000Z")
	model, err := json.Marshal(contracts.OpenRouterModel("test/model"))
	if err != nil {
		t.Fatal(err)
	}
	configurationID := "saved-configuration"
	if err := store.SaveConfiguration(context.Background(), configurationID, json.RawMessage(`{"kind":"test"}`)); err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(context.Background(), history.BeginExecutionInput{ExecutionID: executionID,
		Task: "the saved question", SessionID: sessionID, AgentName: "default", Model: model,
		Build: json.RawMessage(`{}`), ConfigurationID: configurationID, CreatedAt: "2026-10-11T00:00:01.000Z"})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []json.RawMessage{
		json.RawMessage(`{"role":"user","content":{"kind":"text","text":"the saved question"}}`),
		json.RawMessage(`{"role":"assistant","content":{"kind":"text","text":"the saved answer"}}`),
	} {
		if _, err := store.AppendMessage(context.Background(), execution.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.EndExecution(context.Background(), execution.ID, history.OutcomeCompleted,
		json.RawMessage(`{"outcome":"completed"}`), "2026-10-11T00:00:02.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(context.Background(), execution.ID, "2026-10-11T00:00:03.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return sessionID, execution.ID
}
