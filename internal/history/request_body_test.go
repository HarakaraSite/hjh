package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"hjh/internal/contracts"
)

func TestMetadataAndSnapshotSaveReplaceExecutionOwnedBody(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "latest-body-owner", "task")
	if err := store.SetContext(ctx, execution.ID, json.RawMessage(`{"items":[{"digest":"sha256:generic-context"}]}`)); err != nil {
		t.Fatal(err)
	}
	appendRequest := func(step int, body string) Record {
		t.Helper()
		payload := contracts.JSON(map[string]any{
			"schemaVersion": 1, "requestOrdinal": step, "modelStep": step,
			"modelSelection": contracts.ModelSelection{Provider: "fixture", ModelID: "fixture/model"},
			"itemCount":      2, "messageCount": 1, "toolContractCount": 0, "instructionCount": 0, "wireBytes": len(body),
		})
		record, err := appendRequestSnapshotForTest(ctx, store, execution.ID,
			OccurrenceInput{Kind: "model_request", Payload: payload, Content: []byte(body)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	firstBody := strings.Repeat("first wire body ", 8)
	first := appendRequest(1, firstBody)
	shared, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "resource_revision",
		Payload: json.RawMessage(`{"kind":"other-owner"}`), Content: []byte(firstBody)})
	if err != nil {
		t.Fatal(err)
	}
	second := appendRequest(2, "second wire body")
	records, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var oldRequest, newRequest, sharedBody bool
	for _, record := range records {
		switch record.ID {
		case first.ID:
			oldRequest = record.Content == nil
		case second.ID:
			newRequest = string(record.Content) == "second wire body"
		case shared.ID:
			sharedBody = string(record.Content) == firstBody
		}
	}
	if !oldRequest || !newRequest || !sharedBody {
		t.Fatalf("request replacement did not retain only the newest request body: old=%t new=%t shared=%t", oldRequest, newRequest, sharedBody)
	}
	var retained int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contents WHERE content_digest=?`, digestBytes([]byte(firstBody))).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != 1 {
		t.Fatalf("generic semantic content was not retained: count=%d", retained)
	}

	third := appendRequest(3, "third wire body")
	latest, err := store.Records(ctx, execution.ID, third.Ordinal, third.Ordinal)
	if err != nil || len(latest) != 1 || latest[0].ID != third.ID || string(latest[0].Content) != "third wire body" {
		t.Fatalf("latest request after second replacement = %+v, %v", latest, err)
	}
	var priorWireCopies int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contents WHERE content_digest=?`, digestBytes([]byte("second wire body"))).Scan(&priorWireCopies); err != nil {
		t.Fatal(err)
	}
	if priorWireCopies != 0 {
		t.Fatalf("prior WireBody was inserted into contents: count=%d", priorWireCopies)
	}
	var latestID string
	var latestBody []byte
	if err := store.db.QueryRowContext(ctx, `SELECT record_id,wire_body FROM execution_latest_requests WHERE execution_id=?`, execution.ID).Scan(&latestID, &latestBody); err != nil {
		t.Fatal(err)
	}
	if latestID != third.ID || string(latestBody) != "third wire body" {
		t.Fatalf("execution latest request row = %q/%q", latestID, latestBody)
	}
	var latestDigest sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT content_digest FROM semantic_records WHERE record_id=?`, third.ID).Scan(&latestDigest); err != nil {
		t.Fatal(err)
	}
	if latestDigest.Valid {
		t.Fatalf("latest request semantic record owns shared contents digest %q", latestDigest.String)
	}
	var latestWireCopies int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contents WHERE content_digest=?`, digestBytes([]byte("third wire body"))).Scan(&latestWireCopies); err != nil {
		t.Fatal(err)
	}
	if latestWireCopies != 0 {
		t.Fatalf("latest WireBody was inserted into contents: count=%d", latestWireCopies)
	}
	manifest, err := store.Context(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var locator struct {
		SchemaVersion int `json:"schemaVersion"`
		LatestRequest struct {
			RecordID string `json:"recordId"`
		} `json:"latestRequest"`
	}
	if err := json.Unmarshal(manifest, &locator); err != nil || locator.SchemaVersion != 1 || locator.LatestRequest.RecordID != third.ID {
		t.Fatalf("reconstructed latest request locator = %s, %v", manifest, err)
	}
	var contextView struct {
		Items []struct {
			Digest string `json:"digest"`
		} `json:"items"`
	}
	if err := json.Unmarshal(manifest, &contextView); err != nil || len(contextView.Items) != 1 || contextView.Items[0].Digest != "sha256:generic-context" {
		t.Fatalf("generic execution context was not retained: %s, %v", manifest, err)
	}
	var storedManifest string
	if err := store.db.QueryRowContext(ctx, `SELECT manifest_json FROM execution_contexts WHERE execution_id=?`, execution.ID).Scan(&storedManifest); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(storedManifest, `"latestRequest"`) {
		t.Fatalf("stored context duplicated latest request locator: %s", storedManifest)
	}
}

func TestLatestRequestBodiesAreSeparateForParentAndChildAndCascadeOnDelete(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	parent := beginRecallTestExecution(t, store, sessionID, "latest-parent", "parent task")
	child, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{
		ExecutionID: "latest-child", ParentExecutionID: parent.ID, Task: "child task",
		Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	appendRequest := func(executionID, body string) Record {
		t.Helper()
		payload := contracts.JSON(map[string]any{
			"schemaVersion": 1, "requestOrdinal": 1, "modelStep": 1, "wireBytes": len(body),
		})
		record, err := appendRequestSnapshotForTest(ctx, store, executionID, OccurrenceInput{
			Kind: "model_request", Payload: payload, Content: []byte(body),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	parentRecord := appendRequest(parent.ID, "parent-wire")
	childRecord := appendRequest(child.ID, "child-wire")
	for _, want := range []struct {
		executionID string
		recordID    string
		body        string
	}{{parent.ID, parentRecord.ID, "parent-wire"}, {child.ID, childRecord.ID, "child-wire"}} {
		records, err := store.Records(ctx, want.executionID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		var matched bool
		for _, record := range records {
			if record.ID == want.recordID && string(record.Content) == want.body {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("execution %s did not return its own latest wire %q: %+v", want.executionID, want.body, records)
		}
	}
	var requestRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id IN (?,?)`, parent.ID, child.ID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 2 {
		t.Fatalf("parent and child own %d latest request rows, want 2", requestRows)
	}
	if err := store.DeleteSession(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id IN (?,?)`, parent.ID, child.ID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 {
		t.Fatalf("Session deletion retained %d execution latest request rows", requestRows)
	}
}

func digestBytes(body []byte) string {
	return contentDigest(body)
}

func TestRecordsConcurrentLatestRequestReplacementKeepsCapturedBody(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Multiple connections let the replacement compete with Records between
	// database statements, as it can when callers use the store concurrently.
	store.db.SetMaxOpenConns(4)
	session, err := store.CreateSession(ctx, SessionInput{
		ID: "request-read-race-session", WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`),
		ActiveModel: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	execution := beginRecallTestExecution(t, store, session.ID, "request-read-race", "task")
	appendRequest := func(requestOrdinal int) error {
		body := []byte(fmt.Sprintf("wire-%03d:", requestOrdinal) + strings.Repeat("x", 32*1024))
		payload := contracts.JSON(map[string]any{
			"schemaVersion": 1, "requestOrdinal": requestOrdinal, "modelStep": requestOrdinal,
			"modelSelection": contracts.ModelSelection{Provider: "fixture", ModelID: "fixture/model"},
			"itemCount":      2, "messageCount": 1, "toolContractCount": 0,
			"instructionCount": 0, "wireBytes": len(body),
		})
		_, err := appendRequestSnapshotForTest(ctx, store, execution.ID, OccurrenceInput{
			Kind: "model_request", Payload: payload, Content: body,
		}, nil)
		return err
	}
	if err := appendRequest(1); err != nil {
		t.Fatal(err)
	}

	const readers = 2
	const readsPerReader = 120
	const replacements = 60
	start := make(chan struct{})
	var ready sync.WaitGroup
	var work sync.WaitGroup
	ready.Add(readers + 1)
	work.Add(readers + 1)
	failures := make(chan error, readers+1)
	for i := 0; i < readers; i++ {
		go func() {
			defer work.Done()
			ready.Done()
			<-start
			for attempt := 0; attempt < readsPerReader; attempt++ {
				records, err := store.Records(ctx, execution.ID, 0, -1)
				if err != nil {
					failures <- fmt.Errorf("Records while replacing latest request: %w", err)
					return
				}
				bodyCount := 0
				for _, record := range records {
					if record.Kind != "model_request" || record.Content == nil {
						continue
					}
					bodyCount++
					var metadata struct {
						RequestOrdinal int `json:"requestOrdinal"`
						WireBytes      int `json:"wireBytes"`
					}
					if err := json.Unmarshal(record.Payload, &metadata); err != nil {
						failures <- fmt.Errorf("decode captured request metadata: %w", err)
						return
					}
					wantPrefix := fmt.Sprintf("wire-%03d:", metadata.RequestOrdinal)
					if !strings.HasPrefix(string(record.Content), wantPrefix) || len(record.Content) != metadata.WireBytes {
						failures <- fmt.Errorf("request %d body does not match its captured metadata", metadata.RequestOrdinal)
						return
					}
				}
				if bodyCount != 1 {
					failures <- fmt.Errorf("Records returned %d retained latest request bodies, want 1", bodyCount)
					return
				}
			}
		}()
	}
	go func() {
		defer work.Done()
		ready.Done()
		<-start
		for ordinal := 2; ordinal <= replacements+1; ordinal++ {
			if err := appendRequest(ordinal); err != nil {
				failures <- fmt.Errorf("replace latest request %d: %w", ordinal, err)
				return
			}
		}
	}()
	ready.Wait()
	close(start)
	work.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
