package history

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"hjh/internal/contracts"
)

func TestAppendLatestRequestReclaimsOnlyUnreferencedPreviousBody(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "latest-body-owner", "task")
	appendRequest := func(step int, body string) Record {
		t.Helper()
		payload := contracts.JSON(map[string]any{
			"schemaVersion": 1, "requestOrdinal": step, "modelStep": step,
			"modelSelection": contracts.ModelSelection{Provider: "fixture", ModelID: "fixture/model"},
			"itemCount":      2, "messageCount": 1, "toolContractCount": 0, "instructionCount": 0, "wireBytes": len(body),
		})
		record, err := store.AppendLatestRequest(ctx, execution.ID, OccurrenceInput{Kind: "model_request", Payload: payload, Content: []byte(body)}, nil)
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
		t.Fatalf("body still referenced by another semantic record was reclaimed: count=%d", retained)
	}

	third := appendRequest(3, "third wire body")
	latest, err := store.Records(ctx, execution.ID, third.Ordinal, third.Ordinal)
	if err != nil || len(latest) != 1 || latest[0].ID != third.ID || string(latest[0].Content) != "third wire body" {
		t.Fatalf("latest request after second replacement = %+v, %v", latest, err)
	}
	var reclaimed int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contents WHERE content_digest=?`, digestBytes([]byte("second wire body"))).Scan(&reclaimed); err != nil {
		t.Fatal(err)
	}
	if reclaimed != 0 {
		t.Fatalf("unreferenced prior WireBody remains in contents: count=%d", reclaimed)
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
		t.Fatalf("latest request locator = %s, %v", manifest, err)
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
		_, err := store.AppendLatestRequest(ctx, execution.ID, OccurrenceInput{
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
