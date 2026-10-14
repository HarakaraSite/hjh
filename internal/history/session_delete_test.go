package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"hjh/internal/contracts"
)

func TestDeleteSessionRemovesConversationExecutionTreeAndRelatedReceipts(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	model := json.RawMessage(`{"provider":"test","modelId":"test/model"}`)
	if err := store.SaveConfiguration(ctx, "shared-config", json.RawMessage(`{"agent":{"name":"default"}}`)); err != nil {
		t.Fatal(err)
	}
	deletedSnapshot := json.RawMessage(`{"agent":{"name":"reviewer","instruction":"review this change"}}`)
	if err := store.SaveConfiguration(ctx, "deleted-config", deletedSnapshot); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"deleted", "kept"} {
		if _, err := store.CreateSession(ctx, SessionInput{ID: id, WorkspaceRoot: "/work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model}); err != nil {
			t.Fatal(err)
		}
	}
	root, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "root", SessionID: "deleted", Task: "shared task", Model: model, Build: json.RawMessage(`{}`), ConfigurationID: "shared-config"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{ExecutionID: "child", ParentExecutionID: root.ID, Task: "child task", Model: model, Build: json.RawMessage(`{}`), ConfigurationID: "deleted-config"})
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{ExecutionID: "grandchild", ParentExecutionID: child.ID, Task: "nested task", Model: model, Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "kept-root", SessionID: "kept", Task: "shared task", Model: model, Build: json.RawMessage(`{}`), ConfigurationID: "shared-config"})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "failed-root", SessionID: "deleted", Task: "failed task", Model: model, Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	removedBodies := [][]byte{deletedSnapshot, []byte("child task"), []byte("nested task"), []byte("failed task"), []byte("queued text")}
	var keptToolResult []byte
	var keptToolMessageBody []byte
	for _, execution := range []Execution{root, child, grandchild, kept} {
		if _, err := store.AppendMessage(ctx, execution.ID, json.RawMessage(`{"role":"assistant","text":"saved response"}`)); err != nil {
			t.Fatal(err)
		}
		toolMessageBody := []byte("tool message failure body for " + execution.ID)
		toolMessage := contracts.JSON(contracts.ToolResults([]contracts.ToolResult{{CallID: "call-missing",
			Name: "read_file", Text: string(toolMessageBody), Outcome: "error", Failure: contracts.JSON(map[string]string{"code": "not_found"}),
			Terminal: "tool_error"}}))
		if _, err := store.AppendMessage(ctx, execution.ID, toolMessage); err != nil {
			t.Fatal(err)
		}
		if execution.ID == kept.ID {
			keptToolMessageBody = toolMessageBody
		} else {
			removedBodies = append(removedBodies, toolMessageBody)
		}
		if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "tool_call", Payload: json.RawMessage(`{}`), Content: []byte("saved tool result")}); err != nil {
			t.Fatal(err)
		}
		resultBody := contracts.JSON(map[string]string{"stdout": "tool result for " + execution.ID})
		if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "tool_result", Payload: contracts.JSON(map[string]any{
			"callId": "result-" + execution.ID, "name": "command", "phase": "completed", "result": resultBody,
		})}); err != nil {
			t.Fatal(err)
		}
		if execution.ID == kept.ID {
			keptToolResult = resultBody
		} else {
			removedBodies = append(removedBodies, resultBody)
		}
		if err := store.SetContext(ctx, execution.ID, json.RawMessage(`{"items":[]}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, execution := range []Execution{root, child, grandchild, failed} {
		message := contracts.JSON(contracts.User("request for " + execution.ID))
		attachment := []byte("model input for " + execution.ID)
		if _, err := store.AppendMessage(ctx, execution.ID, message); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "model_request", Payload: json.RawMessage(`{}`), Content: attachment}); err != nil {
			t.Fatal(err)
		}
		removedBodies = append(removedBodies, message, attachment)
	}
	if err := store.EndExecution(ctx, failed.ID, OutcomeFailed, json.RawMessage(`{"outcome":"failed"}`), ""); err != nil {
		t.Fatal(err)
	}
	for _, input := range []contracts.InputReceipt{
		{CoreID: "old-core", SessionID: "deleted", RunID: "root-run", ExecutionID: root.ID, InputID: "steering", Kind: "steering"},
		{CoreID: "old-core", SessionID: "deleted", RunID: "root-run", InputID: "queued-followup", Kind: "follow-up"},
	} {
		body := []byte("queued text")
		if input.Kind == "steering" {
			body = contracts.JSON(contracts.User("queued text"))
		}
		if _, err := store.SaveRuntimeInput(ctx, input, "signature", body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO data_session_read_cursors VALUES ('reader','deleted',1,1,1,'{}',?)`, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO recall_relations VALUES (?,?,?)`, root.ID, kept.ID, semanticRecordID(root.ID, 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO recall_relations VALUES (?,?,?)`, kept.ID, root.ID, semanticRecordID(kept.ID, 2)); err != nil {
		t.Fatal(err)
	}
	for _, execution := range []Execution{grandchild, child, root, kept} {
		if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted, json.RawMessage(`{"outcome":"completed"}`), ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, execution := range []Execution{root, kept} {
		if err := store.AdoptCanonical(ctx, execution.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteSession(ctx, "deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session(ctx, "deleted"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted Session still readable: %v", err)
	}
	for _, id := range []string{root.ID, child.ID, grandchild.ID, failed.ID} {
		if _, err := store.Execution(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted execution %s still readable: %v", id, err)
		}
	}
	if messages, err := store.Conversation(ctx, "kept", 0, -1); err != nil || len(messages) != 2 {
		t.Fatalf("other Session conversation affected: %+v, %v", messages, err)
	} else {
		assertPublicToolMessageFailure(t, messages[1].JSON, string(keptToolMessageBody))
	}
	if _, err := store.Configuration(ctx, "shared-config"); err != nil {
		t.Fatalf("shared configuration was removed: %v", err)
	}
	if _, err := store.Configuration(ctx, "deleted-config"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unused configuration was not removed: %v", err)
	}
	for _, body := range removedBodies {
		if _, err := store.Content(ctx, contentDigest(body)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted Session retained body %q: %v", body, err)
		}
	}
	var sharedTaskDigest string
	if err := store.db.QueryRowContext(ctx, "SELECT task_content_digest FROM executions WHERE execution_id=?", kept.ID).Scan(&sharedTaskDigest); err != nil {
		t.Fatal(err)
	}
	if content, err := store.Content(ctx, sharedTaskDigest); err != nil || string(content) != "shared task" {
		t.Fatalf("shared task content was removed: %v", err)
	}
	if content, err := store.Content(ctx, contentDigest(keptToolResult)); err != nil || !bytes.Equal(content, keptToolResult) {
		t.Fatalf("remaining Session tool result was removed: %q, %v", content, err)
	}
	if content, err := store.Content(ctx, contentDigest(keptToolMessageBody)); err != nil || !bytes.Equal(content, keptToolMessageBody) {
		t.Fatalf("remaining Session tool Message body was removed: %q, %v", content, err)
	}
	for _, table := range []string{"command_receipts", "follow_up_receipts", "core_session_cursors", "data_session_read_cursors", "recall_relations"} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("deleted Session retained %s: count=%d, %v", table, count, err)
		}
	}
	rows, err := store.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("Session deletion left broken database references")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, "kept"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"configurations", "contents"} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("last Session deletion retained %s: count=%d, %v", table, count, err)
		}
	}
}

func TestDeleteSessionPreservesRemainingInputBodiesAndLeavesUnrelatedData(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	model := contracts.JSON(contracts.OpenRouterModel("test/model"))
	for _, id := range []string{"deleted", "kept"} {
		if _, err := store.CreateSession(ctx, SessionInput{ID: id, WorkspaceRoot: "/work", AgentChoice: contracts.JSON(map[string]string{"name": "default"}), ActiveModel: model}); err != nil {
			t.Fatal(err)
		}
	}
	root, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "deleted-root", SessionID: "deleted", Task: "queued task", Model: model, Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, inputID := range []string{"old-steering", "new-steering"} {
		marker := true
		body := contracts.JSON(contracts.Message{Role: "user", Content: contracts.JSON(contracts.TextContent{Kind: "text", Text: inputID}), Steering: &marker})
		if _, err := store.AppendMessage(ctx, root.ID, body); err != nil {
			t.Fatal(err)
		}
		receipt, err := store.SaveRuntimeInput(ctx, contracts.InputReceipt{CoreID: "core", SessionID: "kept", RunID: "kept-run", InputID: inputID, Kind: "steering", Status: "accepted"}, inputID, body)
		if err != nil {
			t.Fatal(err)
		}
		if inputID == "old-steering" {
			// The existing receipt format referenced contents, before inline
			// acceptedMessage storage. It must remain valid as a body reference.
			if _, err := store.db.ExecContext(ctx, `UPDATE command_receipts SET result_json=? WHERE core_epoch=? AND command_id=?`, string(contracts.JSON(receipt)), receipt.CoreID, receipt.InputID); err != nil {
				t.Fatal(err)
			}
		}
	}
	follow, err := store.SaveRuntimeInput(ctx, contracts.InputReceipt{CoreID: "core", SessionID: "kept", RunID: "kept-run", InputID: "follow", Kind: "follow-up", Status: "queued"}, "follow", []byte("queued task"))
	if err != nil {
		t.Fatal(err)
	}
	unrelated := contracts.JSON(map[string]string{"instruction": "not owned by the deleted Session"})
	if err := store.SaveConfiguration(ctx, "unrelated", unrelated); err != nil {
		t.Fatal(err)
	}
	if err := store.EndExecution(ctx, root.ID, OutcomeCancelled, json.RawMessage(`{"outcome":"cancelled"}`), ""); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, "deleted"); err != nil {
		t.Fatal(err)
	}
	if content, err := store.Content(ctx, follow.ContentDigest); err != nil || string(content) != "queued task" {
		t.Fatalf("remaining follow-up lost its body: %s, %v", content, err)
	}
	var oldDigest string
	if err := store.db.QueryRowContext(ctx, `SELECT json_extract(result_json,'$.contentDigest') FROM command_receipts WHERE command_id='old-steering'`).Scan(&oldDigest); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Content(ctx, oldDigest); err != nil {
		t.Fatalf("remaining legacy input lost its body: %v", err)
	}
	newReceipt, err := store.RuntimeInput(ctx, "core", "new-steering", true)
	if err != nil || newReceipt.Text != "new-steering" {
		t.Fatalf("remaining inline input lost its body: %+v, %v", newReceipt, err)
	}
	if _, err := store.Content(ctx, newReceipt.ContentDigest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inline receipt checksum retained an unused contents row: %v", err)
	}
	if snapshot, err := store.Configuration(ctx, "unrelated"); err != nil || string(snapshot.Snapshot) != string(unrelated) {
		t.Fatalf("unrelated configuration affected: %+v, %v", snapshot, err)
	}
	if err := store.DeleteSession(ctx, "kept"); err != nil {
		t.Fatal(err)
	}
	for _, digest := range []string{oldDigest, follow.ContentDigest} {
		if _, err := store.Content(ctx, digest); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted receipt retained body %q: %v", digest, err)
		}
	}
}
