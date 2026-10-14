package history

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestEmbeddedSchemaV4AddsLatestSnapshotTables(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 4 {
		t.Fatalf("user_version = %d, want 4", version)
	}
	var metadataVersion int
	if err := store.db.QueryRow("SELECT schema_version FROM store_metadata WHERE singleton=1").Scan(&metadataVersion); err != nil {
		t.Fatal(err)
	}
	if metadataVersion != 4 {
		t.Fatalf("store_metadata.schema_version = %d, want 4", metadataVersion)
	}
	var tables, indexes int
	if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND sql IS NOT NULL").Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if tables != 23 || indexes != 9 {
		t.Fatalf("catalog has %d tables and %d explicit indexes, want 23 and 9", tables, indexes)
	}
	for table, columns := range map[string][]string{
		"execution_latest_requests": {"execution_id", "record_id", "wire_body"},
		"execution_latest_budgets":  {"execution_id", "record_id", "fact_json"},
	} {
		var ddl string
		if err := store.db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		for _, column := range columns {
			if !strings.Contains(ddl, column) {
				t.Fatalf("%s schema does not contain column %q: %s", table, column, ddl)
			}
		}
	}
	rows, err := store.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("foreign_key_check returned a violation")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := store.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q, want ok", integrity)
	}
	memoryStore, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	var foreignKeys int
	if err := memoryStore.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := memoryStore.Close(); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("in-memory foreign_keys = %d, want 1", foreignKeys)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	preexistingPath := filepath.Join(t.TempDir(), "preexisting-empty.db")
	if err := os.WriteFile(preexistingPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(preexistingPath); err == nil {
		t.Fatal("Open initialized a preexisting empty database")
	}
	preexistingBytes, err := os.ReadFile(preexistingPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(preexistingBytes) != 0 {
		t.Fatal("Open wrote the schema into a preexisting empty database")
	}

	missingPath := filepath.Join(t.TempDir(), "absent.db")
	if _, err := OpenReadOnly(missingPath); err == nil {
		t.Fatal("OpenReadOnly accepted a missing database")
	}
	if _, err := os.Stat(missingPath); !os.IsNotExist(err) {
		t.Fatalf("OpenReadOnly created the database: stat error = %v", err)
	}
}

func TestRelativeDatabasePathCanBeReopenedReadOnly(t *testing.T) {
	ctx := context.Background()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeDirectory, err := filepath.Rel(workingDirectory, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(relativeDirectory, "history.db")
	if filepath.IsAbs(path) {
		t.Fatalf("test path is unexpectedly absolute: %q", path)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	storedSession, err := readOnly.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedSession.ID != session.ID {
		t.Fatalf("relative path read Session %q, want %q", storedSession.ID, session.ID)
	}
}

func TestHistoryPersistsFullMessagesEvidenceAndCanonicalAdoption(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	choice := json.RawMessage(`{"name":"default","file":"agents/default.md"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: choice, ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	configuration := json.RawMessage(`{"agent":{"name":"default"},"tools":[],"hooks":[]}`)
	if err := store.SaveConfiguration(ctx, "config-1", configuration); err != nil {
		t.Fatal(err)
	}
	configurationRead, err := store.Configuration(ctx, "config-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configurationRead.Snapshot, configuration) {
		t.Fatalf("configuration snapshot changed: %s", configurationRead.Snapshot)
	}
	contentRead, err := store.Content(ctx, configurationRead.ContentDigest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contentRead, configuration) {
		t.Fatalf("configuration content changed: %q", contentRead)
	}

	execution, err := store.BeginExecution(ctx, BeginExecutionInput{
		Task: "read, transform, and write a file", SessionID: session.ID,
		Model: model, Build: json.RawMessage(`{"goVersion":"go1.27.2"}`),
		ConfigurationID: "config-1", MaxSteps: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	messageJSON := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":{"kind":"text","text":"Update the file."}}`),
		json.RawMessage(`{"role":"assistant","content":[{"kind":"tool_call","callId":"call-1","name":"read","arguments":{"path":"input.txt"}}],"text":"Reading the input.","providerState":{"provider":"openrouter","model":"model/example","reasoning":{"field":"reasoning","text":"I should inspect it."},"reasoningDetails":[{"type":"future_detail","value":{"opaque":[1,true,"x"]}}]}}`),
		json.RawMessage(`{"role":"tool","content":[{"kind":"tool_result","callId":"call-1","name":"read","text":"source bytes","outcome":"success"}]}`),
		json.RawMessage(`{"role":"assistant","content":{"kind":"text","text":"Done."},"providerState":{"provider":"openrouter","model":"model/example","reasoningDetails":[{"type":"opaque","token":"kept"}]}}`),
	}
	for _, message := range messageJSON {
		if _, err := store.AppendMessage(ctx, execution.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	firstRecord, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind:    "tool_call",
		Payload: json.RawMessage(`{"callId":"call-1","operation":"read","arguments":{"path":"input.txt"}}`),
		Content: []byte{0x00, 0xff, 0x10, 0x7f},
		// The earlier tool Message has a tool_body owner at ordinal 1, so the
		// following result occurrence is ordinal 3.
		Relations: []Relation{{Relation: "result", TargetRecordID: semanticRecordID(execution.ID, 3), Mandatory: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind:    "tool_result",
		Payload: json.RawMessage(`{"callId":"call-1","outcome":"success"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if secondRecord.Ordinal != firstRecord.Ordinal+1 {
		t.Fatalf("semantic ordinals = %d, %d", firstRecord.Ordinal, secondRecord.Ordinal)
	}
	contextManifest := json.RawMessage(`{"items":[{"digest":"sha256:abc","kind":"source"}],"schemaVersion":1}`)
	if err := store.SetContext(ctx, execution.ID, contextManifest); err != nil {
		t.Fatal(err)
	}
	readContext, err := store.Context(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(readContext) {
		t.Fatalf("stored context is invalid JSON: %s", readContext)
	}
	if err := store.SetAssistantText(ctx, execution.ID, "parent", 1, 1, 8,
		json.RawMessage(`{"kind":"assistant_thinking","observedAt":"2026-10-10T00:00:01.000Z","payload":{"text":"working"}}`),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted,
		json.RawMessage(`{"outcome":"completed","stopReason":"final_response"}`),
		"2026-10-10T00:00:02.000Z",
	); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-10T00:00:03.000Z"); err != nil {
		t.Fatal(err)
	}

	conversation, err := store.Conversation(ctx, session.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) != len(messageJSON) {
		t.Fatalf("conversation contains %d messages, want %d", len(conversation), len(messageJSON))
	}
	for i, message := range conversation {
		got, err := contracts.EncodeCanonicalJSON(message.JSON)
		if err != nil {
			t.Fatal(err)
		}
		want, err := contracts.EncodeCanonicalJSON(messageJSON[i])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("conversation message %d changed: %s", i, message.JSON)
		}
	}
	if !bytes.Contains(conversation[1].JSON, []byte(`"reasoningDetails":[{"type":"future_detail"`)) {
		t.Fatalf("assistant provider state was not retained: %s", conversation[1].JSON)
	}
	messages, err := store.Messages(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 {
		t.Fatalf("execution has %d messages, want 4", len(messages))
	}
	records, err := store.Records(ctx, execution.ID, firstRecord.Ordinal, secondRecord.Ordinal)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].ID != firstRecord.ID || records[1].ID != secondRecord.ID {
		t.Fatalf("semantic range returned records %#v", records)
	}
	if !bytes.Equal(records[0].Content, []byte{0x00, 0xff, 0x10, 0x7f}) {
		t.Fatalf("semantic content bytes changed: %v", records[0].Content)
	}
	if len(records[0].Relations) != 1 || !records[0].Relations[0].Resolved {
		t.Fatalf("relation was not resolved by its target record: %#v", records[0].Relations)
	}
	allRecords, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(allRecords) != 5 || allRecords[0].Kind != "tool_body" || allRecords[3].Kind != "assistant_message" || allRecords[4].Kind != "host_decision" {
		t.Fatalf("assistant text and terminal evidence not persisted: %#v", allRecords)
	}
	updatedSession, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedSession.StateRevision != 2 || updatedSession.NextTurn != 2 || updatedSession.MessageCount != 4 || updatedSession.TurnCount != 1 {
		t.Fatalf("adopted Session state = revision %d, next turn %d, %d messages, %d turns",
			updatedSession.StateRevision, updatedSession.NextTurn, updatedSession.MessageCount, updatedSession.TurnCount)
	}
	if updatedSession.ModelChangeCount != 1 {
		t.Fatalf("same model created an unnecessary model change row: count %d", updatedSession.ModelChangeCount)
	}
	executions, err := store.Executions(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 1 || executions[0].Adoption != "canonical" || !strings.Contains(string(executions[0].Build), `"goVersion"`) {
		t.Fatalf("execution readback lost status or build JSON: %#v", executions)
	}
	byID, err := store.Execution(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if byID.ID != execution.ID || byID.Adoption != "canonical" {
		t.Fatalf("single execution readback = %#v", byID)
	}
}

func TestOccurrenceRelationResolutionUsesStoredTargetRows(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	newSession := func() Session {
		t.Helper()
		session, err := store.CreateSession(ctx, SessionInput{
			WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
		})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	startExecution := func(sessionID string) Execution {
		t.Helper()
		execution, err := store.BeginExecution(ctx, BeginExecutionInput{
			Task: "record tool work", SessionID: sessionID, Model: model,
			Build: json.RawMessage(`{"fixture":"test-only"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return execution
	}

	session := newSession()
	execution := startExecution(session.ID)
	start, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "tool_call", Payload: json.RawMessage(`{"phase":"start","callId":"c1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	end, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "tool_result", Payload: json.RawMessage(`{"phase":"end","callId":"c1"}`),
		Relations: []Relation{{Relation: "call_start", TargetRecordID: start.ID, Mandatory: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(end.Relations) != 1 || !end.Relations[0].Resolved {
		t.Fatalf("existing start record was not recognized from storage: %#v", end.Relations)
	}
	if _, err := store.AppendMessage(ctx, execution.ID,
		json.RawMessage(`{"role":"user","content":{"kind":"text","text":"record tool work"}}`),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted,
		json.RawMessage(`{"outcome":"completed"}`), "2026-10-10T00:00:14.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-10T00:00:15.000Z"); err != nil {
		t.Fatalf("execution with a relation to its stored start record did not adopt: %v", err)
	}

	missingSession := newSession()
	missingExecution := startExecution(missingSession.ID)
	missing, err := store.AppendOccurrence(ctx, missingExecution.ID, OccurrenceInput{
		Kind: "tool_result", Payload: json.RawMessage(`{"phase":"end","callId":"missing"}`),
		Relations: []Relation{{Relation: "call_start", TargetRecordID: "not-yet-stored", Mandatory: true, Resolved: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing.Relations) != 1 || missing.Relations[0].Resolved {
		t.Fatalf("caller-supplied resolved state bypassed target lookup: %#v", missing.Relations)
	}
	if err := store.EndExecution(ctx, missingExecution.ID, OutcomeCompleted,
		json.RawMessage(`{"outcome":"completed"}`), "2026-10-10T00:00:16.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, missingExecution.ID, "2026-10-10T00:00:17.000Z"); err == nil {
		t.Fatal("execution with a missing mandatory target was adopted")
	}
}

func TestFailedAndCancelledEvidenceIsNotAdopted(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	for _, test := range []struct {
		name    string
		outcome Outcome
	}{
		{name: "failed", outcome: OutcomeFailed},
		{name: "cancelled", outcome: OutcomeCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := store.CreateSession(ctx, SessionInput{
				WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
			})
			if err != nil {
				t.Fatal(err)
			}
			execution, err := store.BeginExecution(ctx, BeginExecutionInput{
				Task: "perform work", SessionID: session.ID, Model: model,
				Build: json.RawMessage(`{"fixture":"test-only"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.AppendMessage(ctx, execution.ID,
				json.RawMessage(`{"role":"user","content":{"kind":"text","text":"perform work"}}`),
			); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
				Kind: "effect_observation", Payload: json.RawMessage(`{"phase":"failed","reason":"observed"}`),
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.EndExecution(ctx, execution.ID, test.outcome,
				json.RawMessage(`{"outcome":"`+string(test.outcome)+`","detail":"kept"}`), "2026-10-10T00:00:04.000Z",
			); err != nil {
				t.Fatal(err)
			}
			if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-10T00:00:05.000Z"); err == nil {
				t.Fatal("non-completed execution was adopted")
			}
			conversation, err := store.Conversation(ctx, session.ID, 1, -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(conversation) != 0 {
				t.Fatalf("non-completed evidence entered conversation: %#v", conversation)
			}
			records, err := store.Records(ctx, execution.ID, 1, -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 2 || records[0].Kind != "effect_observation" || records[1].Kind != "host_decision" {
				t.Fatalf("non-completed evidence was lost: %#v", records)
			}
		})
	}
}

func TestCanonicalAdoptionChecksTheCapturedSessionRevision(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	var executions []Execution
	for _, text := range []string{"first turn", "stale turn"} {
		execution, err := store.BeginExecution(ctx, BeginExecutionInput{
			Task: text, SessionID: session.ID, Model: model,
			Build: json.RawMessage(`{"fixture":"test-only"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendMessage(ctx, execution.ID,
			json.RawMessage(`{"role":"user","content":{"kind":"text","text":"`+text+`"}}`),
		); err != nil {
			t.Fatal(err)
		}
		if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted,
			json.RawMessage(`{"outcome":"completed"}`), "2026-10-10T00:00:08.000Z"); err != nil {
			t.Fatal(err)
		}
		executions = append(executions, execution)
	}
	if executions[0].BaseRevision != executions[1].BaseRevision || executions[0].BaseMessageCount != executions[1].BaseMessageCount {
		t.Fatal("concurrent executions did not capture the same starting Session state")
	}
	if err := store.AdoptCanonical(ctx, executions[0].ID, "2026-10-10T00:00:09.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, executions[1].ID, "2026-10-10T00:00:10.000Z"); err == nil {
		t.Fatal("stale execution overwrote the adopted Session revision")
	}
	conversation, err := store.Conversation(ctx, session.ID, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) != 1 || !bytes.Contains(conversation[0].JSON, []byte("first turn")) {
		t.Fatalf("stale execution changed canonical conversation: %#v", conversation)
	}
}

func TestSessionModelChangeUpdatesMetadataAndTurnAttribution(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	initialModel := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/first","effort":"auto"}`)
	nextModel := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/second","effort":"auto"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: initialModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.UpdateSessionModel(ctx, session.ID, nextModel, "2026-10-10T00:00:11.000Z")
	if err != nil {
		t.Fatal(err)
	}
	if updated.StateRevision != 2 || updated.ModelChangeCount != 2 || updated.PrivateStateFromTurn != 1 || !bytes.Equal(updated.ActiveModel, nextModel) {
		t.Fatalf("Session metadata after model change = %#v", updated)
	}
	var changes int
	if err := store.db.QueryRowContext(ctx,
		"SELECT count(*) FROM session_model_changes WHERE session_id=? AND effective_from_turn=1", session.ID,
	).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 2 {
		t.Fatalf("turn 1 has %d model attribution rows, want initial and changed selections", changes)
	}
	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != session.ID || !bytes.Equal(sessions[0].ActiveModel, nextModel) {
		t.Fatalf("Session list did not return updated metadata: %#v", sessions)
	}
}

func TestAdoptionMakesTheExecutionModelTheSessionDefault(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	previousModel := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/first","effort":"auto"}`)
	usedModel := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/second","effort":"high"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: previousModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, BeginExecutionInput{
		Task: "work with selected model", SessionID: session.ID, Model: usedModel,
		Build: json.RawMessage(`{"fixture":"test-only"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, execution.ID,
		json.RawMessage(`{"role":"user","content":{"kind":"text","text":"work"}}`),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted,
		json.RawMessage(`{"outcome":"completed"}`), "2026-10-10T00:00:12.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-10T00:00:13.000Z"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(updated.ActiveModel, usedModel) || updated.ModelChangeCount != 2 || updated.PrivateStateFromTurn != 1 {
		t.Fatalf("adopted model not stored as Session default: %#v", updated)
	}
	var effectiveTurn int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT effective_from_turn FROM session_model_changes
		WHERE session_id=? ORDER BY change_ordinal DESC LIMIT 1`, session.ID,
	).Scan(&effectiveTurn); err != nil {
		t.Fatal(err)
	}
	if effectiveTurn != 1 {
		t.Fatalf("adopted model effective turn = %d, want 1", effectiveTurn)
	}
}

func TestChildExecutionAdmissionAndReadbackKeepSessionUnchanged(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveConfiguration(ctx, "child-config", json.RawMessage(`{"agent":{"name":"default"}}`)); err != nil {
		t.Fatal(err)
	}
	parent, err := store.BeginExecution(ctx, BeginExecutionInput{
		ExecutionID: "parent-execution", Task: "delegate repository investigation", SessionID: session.ID,
		AgentName: "default", Model: model, Build: json.RawMessage(`{"goVersion":"go1.27.2"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{
		ExecutionID: "child-execution", TaskID: "child-task", Task: "inspect src",
		ParentExecutionID: parent.ID, SpawnCallID: "spawn-call-1", AgentName: "default",
		Model: model, Build: json.RawMessage(`{"goVersion":"go1.27.2"}`),
		ConfigurationID: "child-config", MaxSteps: 4, CreatedAt: "2026-10-10T00:00:01.000Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ID != "child-execution" || child.TaskID != "child-task" || child.Task != "inspect src" {
		t.Fatalf("child identity or task readback = %#v", child)
	}
	if child.SessionID != "" || child.SessionCorrelation != session.ID || child.Turn != parent.Turn ||
		child.BaseRevision != 0 || child.BaseMessageCount != 0 || child.Adoption != "non_canonical" {
		t.Fatalf("child Session attribution = %#v", child)
	}
	if child.ParentExecutionID != parent.ID || child.SpawnCallID != "spawn-call-1" ||
		child.AgentName != "default" || child.ConfigurationID != "child-config" || child.MaxSteps != 4 ||
		!bytes.Equal(child.Model, model) || !bytes.Equal(child.Build, json.RawMessage(`{"goVersion":"go1.27.2"}`)) {
		t.Fatalf("child input readback = %#v", child)
	}
	var canonicalSession sql.NullString
	if err := store.db.QueryRowContext(ctx,
		"SELECT canonical_session_id FROM executions WHERE execution_id=?", child.ID,
	).Scan(&canonicalSession); err != nil {
		t.Fatal(err)
	}
	if canonicalSession.Valid {
		t.Fatalf("child canonical_session_id = %q, want NULL", canonicalSession.String)
	}

	nested, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{
		ExecutionID: "nested-child", Task: "inspect docs", ParentExecutionID: child.ID,
		SpawnCallID: "spawn-call-2", AgentName: "default", Model: model,
		Build: json.RawMessage(`{"goVersion":"go1.27.2"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if nested.ParentExecutionID != child.ID || nested.SessionCorrelation != session.ID || nested.Turn != parent.Turn {
		t.Fatalf("nested child attribution = %#v", nested)
	}

	if _, err := store.AppendMessage(ctx, child.ID,
		json.RawMessage(`{"role":"assistant","content":{"kind":"text","text":"src inspected"}}`),
	); err != nil {
		t.Fatal(err)
	}
	if err := store.EndExecution(ctx, child.ID, OutcomeCompleted,
		json.RawMessage(`{"outcome":"completed"}`), "2026-10-10T00:00:02.000Z"); err != nil {
		t.Fatal(err)
	}
	storedChild, err := store.Execution(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedChild.TerminalRecordID == "" {
		t.Fatal("settled child has no terminal record reference")
	}
	records, err := store.Records(ctx, child.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 || records[len(records)-1].ID != storedChild.TerminalRecordID || records[len(records)-1].Kind != "host_decision" {
		t.Fatalf("terminal record readback = %#v, terminal ID %q", records, storedChild.TerminalRecordID)
	}
	if err := store.AdoptCanonical(ctx, child.ID, "2026-10-10T00:00:03.000Z"); err == nil {
		t.Fatal("child execution was adopted into the parent Session")
	}
	after, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.StateRevision != before.StateRevision || after.MessageCount != before.MessageCount ||
		after.NextTurn != before.NextTurn || after.TurnCount != before.TurnCount {
		t.Fatalf("child execution changed parent Session: before %#v, after %#v", before, after)
	}
	conversation, err := store.Conversation(ctx, session.ID, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) != 0 {
		t.Fatalf("child message entered canonical Session conversation: %#v", conversation)
	}
	children, err := store.Children(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].ID != child.ID {
		t.Fatalf("parent direct children = %#v", children)
	}
	nestedChildren, err := store.Children(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nestedChildren) != 1 || nestedChildren[0].ID != nested.ID {
		t.Fatalf("child direct children = %#v", nestedChildren)
	}
	executions, err := store.Executions(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 3 {
		t.Fatalf("Session-correlated execution count = %d, want root and both children", len(executions))
	}
}

func TestHistoryReadbackHelper(t *testing.T) {
	path := os.Getenv("HJH_HISTORY_READBACK_DB")
	if path == "" {
		t.Skip("separate-process helper")
	}
	store, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conversation, err := store.Conversation(context.Background(), os.Getenv("HJH_HISTORY_READBACK_SESSION"), 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) != 2 || string(conversation[1].JSON) != `{"role":"assistant","content":{"kind":"text","text":"saved"}}` {
		t.Fatalf("separate process read unexpected conversation: %#v", conversation)
	}
}

func TestSeparateProcessCanReadAdoptedConversation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, BeginExecutionInput{
		Task: "work", SessionID: session.ID, Model: model, Build: json.RawMessage(`{"fixture":"test-only"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{
		`{"role":"user","content":{"kind":"text","text":"save"}}`,
		`{"role":"assistant","content":{"kind":"text","text":"saved"}}`,
	} {
		if _, err := store.AppendMessage(ctx, execution.ID, json.RawMessage(message)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted,
		json.RawMessage(`{"outcome":"completed"}`), "2026-10-10T00:00:06.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-10T00:00:07.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestHistoryReadbackHelper$")
	command.Env = append(os.Environ(),
		"HJH_HISTORY_READBACK_DB="+path,
		"HJH_HISTORY_READBACK_SESSION="+session.ID,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("separate process readback failed: %v\n%s", err, output)
	}
}
