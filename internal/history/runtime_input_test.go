package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestRuntimeInputReceiptRevisionAndSteeringContentShareTransactions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession(ctx, SessionInput{ID: "runtime-session", WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`)})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "runtime-root", Task: "task", SessionID: session.ID,
		Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{"fixture":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	steering := true
	body, err := contracts.EncodeJSON(contracts.Message{Role: "user", Content: contracts.JSON(contracts.TextContent{Kind: "text", Text: "add a detail"}), Steering: &steering})
	if err != nil {
		t.Fatal(err)
	}
	acceptedAt := "2000-01-02T03:04:05Z"
	accepted, err := store.SaveRuntimeInput(ctx, contracts.InputReceipt{SchemaVersion: 1, CoreID: "core-a", SessionID: session.ID,
		RunID: "run-a", ExecutionID: execution.ID, InputID: "input-a", Kind: "steering", Sequence: 1, Status: "accepted",
		AcceptedAt: acceptedAt, UpdatedAt: acceptedAt}, "fingerprint-a", body)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Revision != 1 || accepted.ContentDigest == "" {
		t.Fatalf("accepted steering receipt = %+v", accepted)
	}
	duplicate, err := store.SaveRuntimeInput(ctx, accepted, "fingerprint-a", []byte("ignored on replay"))
	if err != nil || duplicate.Revision != accepted.Revision || duplicate.ContentDigest != accepted.ContentDigest {
		t.Fatalf("idempotent replay = %+v, %v", duplicate, err)
	}
	if _, err := store.SaveRuntimeInput(ctx, contracts.InputReceipt{CoreID: "core-a", SessionID: session.ID, RunID: "run-a", ExecutionID: execution.ID,
		InputID: "input-a", Kind: "steering"}, "other-fingerprint", body); !errors.Is(err, ErrInputIDConflict) {
		t.Fatalf("reused input ID error = %v, want conflict", err)
	}

	messageFacts := []contracts.ExecutionFact{{Kind: "user_message", Role: "user", Text: "add a detail",
		Conversation: contracts.ConversationMetadata{EntityKind: "steering", LocalKey: "input/input-a"}}}
	ordinal, facts, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: execution.ID, ExecutionID: execution.ID,
		Sequence: 9, Message: body, ContentDigest: accepted.ContentDigest, Facts: messageFacts, SteeringReceipt: &accepted})
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != 0 || len(facts) != 1 || facts[0].Conversation.EntityKind != "steering" {
		t.Fatalf("saved steering message = ordinal %d, facts %+v", ordinal, facts)
	}
	if accepted.Status != "applied" || accepted.Revision != 2 || accepted.AppliedSequence != 9 || accepted.AppliedMessageOrdinal == nil || *accepted.AppliedMessageOrdinal != ordinal {
		t.Fatalf("applied receipt = %+v", accepted)
	}
	acceptedTime, err := time.Parse(time.RFC3339Nano, accepted.AcceptedAt)
	if err != nil {
		t.Fatal(err)
	}
	updatedTime, err := time.Parse(time.RFC3339Nano, accepted.UpdatedAt)
	if err != nil || accepted.AcceptedAt != acceptedAt || !updatedTime.After(acceptedTime) {
		t.Fatalf("applied receipt timestamps = accepted %q, updated %q, err %v", accepted.AcceptedAt, accepted.UpdatedAt, err)
	}
	var messageDigest string
	if err := store.db.QueryRowContext(ctx, "SELECT content_digest FROM messages WHERE execution_id=? AND message_ordinal=?", execution.ID, ordinal).Scan(&messageDigest); err != nil {
		t.Fatal(err)
	}
	if messageDigest != accepted.ContentDigest {
		t.Fatalf("message digest = %q, accepted digest = %q", messageDigest, accepted.ContentDigest)
	}
	var mirrorPayload, mirrorDigest string
	if err := store.db.QueryRowContext(ctx, `SELECT payload_json,content_digest FROM semantic_records
		WHERE execution_id=? AND json_extract(payload_json,'$.conversation.entityKind')='steering'`, execution.ID).Scan(&mirrorPayload, &mirrorDigest); err != nil {
		t.Fatal(err)
	}
	if mirrorDigest != accepted.ContentDigest || bytes.Contains([]byte(mirrorPayload), []byte("add a detail")) {
		t.Fatalf("steering semantic mirror duplicated body or lost content reference: payload=%s digest=%q", mirrorPayload, mirrorDigest)
	}
	var mirror struct {
		Kind         string `json:"kind"`
		Phase        string `json:"phase"`
		Role         string `json:"role"`
		Steering     bool   `json:"steering"`
		Text         string `json:"text"`
		Conversation struct {
			EntityKind string `json:"entityKind"`
			LocalKey   string `json:"localKey"`
		} `json:"conversation"`
		Locator struct {
			MessageOrdinal int64 `json:"messageOrdinal"`
		} `json:"locator"`
	}
	if err := json.Unmarshal([]byte(mirrorPayload), &mirror); err != nil {
		t.Fatal(err)
	}
	if mirror.Kind != "user_message" || mirror.Phase != "completed" || mirror.Role != "user" || !mirror.Steering || mirror.Text != "" ||
		mirror.Conversation.EntityKind != "steering" || mirror.Conversation.LocalKey != "input/input-a" || mirror.Locator.MessageOrdinal != ordinal {
		t.Fatalf("compact steering mirror lost identity: %+v", mirror)
	}
	stored, err := store.RuntimeInput(ctx, "core-a", "input-a", true)
	if err != nil || stored.Text != "add a detail" || stored.Revision != 2 {
		t.Fatalf("stored applied receipt = %+v, %v", stored, err)
	}
	page, err := store.RuntimeInputs(ctx, session.ID, "core-a", "core-a", 1, false)
	if err != nil || page.Revision != 2 || len(page.Receipts) != 1 || page.Receipts[0].Status != "applied" {
		t.Fatalf("incremental input page = %+v, %v", page, err)
	}
	cut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var replayed *contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		if fact.Conversation.EntityKind == "steering" {
			copy := fact
			replayed = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if facts[0].Text != "add a detail" || replayed == nil || replayed.Role != facts[0].Role || replayed.Text != facts[0].Text ||
		replayed.Conversation != facts[0].Conversation || replayed.Locator.MessageOrdinal != facts[0].Locator.MessageOrdinal {
		t.Fatalf("live/cold steering facts diverged: live=%+v replay=%+v", facts[0], replayed)
	}
}

func TestRuntimeInputFollowUpStoresRawTaskAndUpdatesQueueState(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession(ctx, SessionInput{ID: "follow-session", WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`)})
	if err != nil {
		t.Fatal(err)
	}
	text := []byte("follow-up task")
	acceptedAt := "2000-01-02T03:04:05Z"
	queued, err := store.SaveRuntimeInput(ctx, contracts.InputReceipt{CoreID: "core-b", SessionID: session.ID, RunID: "run-b", ExecutionID: "exec-b",
		InputID: "input-b", Kind: "follow-up", Sequence: 1, Status: "queued", AcceptedAt: acceptedAt, UpdatedAt: acceptedAt,
		Next: &contracts.InputSelection{Role: "default", MaxSteps: 3}}, "fingerprint-b", text)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.RuntimeInput(ctx, "core-b", "input-b", true)
	if err != nil || detail.Text != string(text) || detail.ContentDigest != queued.ContentDigest {
		t.Fatalf("follow-up detail = %+v, %v", detail, err)
	}
	startedExecution, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "next-exec", Task: "ignored duplicate text",
		TaskContentDigest: queued.ContentDigest, SessionID: session.ID, Model: json.RawMessage(`{"provider":"fixture"}`),
		Build: json.RawMessage(`{"fixture":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	var taskDigest string
	if err := store.db.QueryRowContext(ctx, "SELECT task_content_digest FROM executions WHERE execution_id=?", startedExecution.ID).Scan(&taskDigest); err != nil {
		t.Fatal(err)
	}
	if startedExecution.Task != string(text) || taskDigest != queued.ContentDigest {
		t.Fatalf("follow-up execution task was not reused: %+v digest=%q", startedExecution, taskDigest)
	}
	queued.Status = "started"
	queued.StartedRunID = "next-run"
	queued.StartedExecutionID = "next-exec"
	updated, err := store.UpdateRuntimeInput(ctx, queued)
	if err != nil {
		t.Fatal(err)
	}
	var status, executionID string
	if err := store.db.QueryRowContext(ctx, `SELECT status,started_execution_id FROM follow_up_receipts WHERE core_epoch=? AND queue_id=?`, "core-b", "input-b").Scan(&status, &executionID); err != nil {
		t.Fatal(err)
	}
	updatedTime, err := time.Parse(time.RFC3339Nano, updated.UpdatedAt)
	acceptedTime, acceptedErr := time.Parse(time.RFC3339Nano, updated.AcceptedAt)
	if err != nil || acceptedErr != nil || !updatedTime.After(acceptedTime) || updated.AcceptedAt != acceptedAt ||
		updated.Revision != 2 || status != "started" || executionID != "next-exec" {
		t.Fatalf("follow-up state did not update atomically: receipt=%+v row=%s/%s", updated, status, executionID)
	}
	page, err := store.RuntimeInputs(ctx, session.ID, "", "core-b", 0, false)
	if err != nil || page.Revision != 2 || len(page.Receipts) != 1 || !page.Receipts[0].ManagedByThisCore {
		t.Fatalf("first-resume input page = %+v, %v", page, err)
	}
}
