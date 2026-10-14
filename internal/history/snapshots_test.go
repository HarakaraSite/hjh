package history

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"hjh/internal/contracts"
)

func appendRequestSnapshotForTest(ctx context.Context, store *Store, executionID string, input OccurrenceInput,
	externalRelations json.RawMessage) (Record, error) {
	record, err := store.AppendLatestRequestMetadata(ctx, executionID, input, externalRelations)
	if err != nil {
		return Record{}, err
	}
	if err := store.SaveLatestSnapshots(ctx, executionID, &LatestRequestSnapshot{RecordID: record.ID, WireBody: input.Content}, nil); err != nil {
		return Record{}, err
	}
	return record, nil
}

func appendBudgetSnapshotForTest(ctx context.Context, store *Store, executionID string, summary, fact json.RawMessage) (Record, error) {
	record, err := store.AppendBudgetSummary(ctx, executionID, summary)
	if err != nil {
		return Record{}, err
	}
	if err := store.SaveLatestSnapshots(ctx, executionID, nil, &LatestBudgetSnapshot{RecordID: record.ID, Fact: fact}); err != nil {
		return Record{}, err
	}
	return record, nil
}

func TestMetadataAndSnapshotSaveSplitAcrossActiveAndSettledExecution(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "snapshot-split", "task")
	relations := contracts.JSON([]map[string]any{{"relation": "recall_source", "targetRecordId": "source-record"}})
	request, err := store.AppendLatestRequestMetadata(ctx, execution.ID, OccurrenceInput{
		Kind: "model_request", Payload: contracts.JSON(map[string]any{"requestOrdinal": 1, "modelStep": 1,
			"wireBytes": len("active wire")}), Content: []byte("active wire"),
	}, relations)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := store.AppendBudgetSummary(ctx, execution.ID, budgetTestPayload(1, false))
	if err != nil {
		t.Fatal(err)
	}
	if request.Content != nil {
		t.Fatalf("metadata-only request returned body %q", request.Content)
	}
	var requestRows, budgetRows int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", execution.ID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_latest_budgets WHERE execution_id=?", execution.ID).Scan(&budgetRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 || budgetRows != 0 {
		t.Fatalf("metadata append wrote snapshot rows: request=%d budget=%d", requestRows, budgetRows)
	}
	var manifest string
	if err := store.db.QueryRowContext(ctx, "SELECT manifest_json FROM execution_contexts WHERE execution_id=?", execution.ID).Scan(&manifest); err != nil {
		t.Fatal(err)
	}
	var manifestFields struct {
		ExternalRelations []map[string]string `json:"externalRelations"`
	}
	if err := json.Unmarshal([]byte(manifest), &manifestFields); err != nil || len(manifestFields.ExternalRelations) != 1 ||
		manifestFields.ExternalRelations[0]["targetRecordId"] != "source-record" {
		t.Fatalf("request external relations = %s, %v", manifest, err)
	}
	wrongKind, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "resource_revision", Payload: json.RawMessage(`{"kind":"other"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID,
		&LatestRequestSnapshot{RecordID: request.ID, WireBody: []byte("active wire")},
		&LatestBudgetSnapshot{RecordID: wrongKind.ID, Fact: budgetTestFact(1)}); err == nil {
		t.Fatal("snapshot save accepted a non-budget record locator")
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", execution.ID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 {
		t.Fatalf("failed multi-snapshot save left a request row: %d", requestRows)
	}
	otherExecution := beginRecallTestExecution(t, store, sessionID, "snapshot-other-execution", "other task")
	otherBudget, err := store.AppendBudgetSummary(ctx, otherExecution.ID, budgetTestPayload(1, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID, nil,
		&LatestBudgetSnapshot{RecordID: otherBudget.ID, Fact: budgetTestFact(1)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot save for another execution error = %v, want ErrNotFound", err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_latest_budget_snapshot
		BEFORE INSERT ON execution_latest_budgets BEGIN SELECT RAISE(ABORT, 'snapshot fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID,
		&LatestRequestSnapshot{RecordID: request.ID, WireBody: []byte("active wire")},
		&LatestBudgetSnapshot{RecordID: budget.ID, Fact: budgetTestFact(1)}); err == nil {
		t.Fatal("snapshot save succeeded despite budget table write failure")
	}
	if _, err := store.db.ExecContext(ctx, "DROP TRIGGER fail_latest_budget_snapshot"); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", execution.ID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 {
		t.Fatalf("failed budget write committed request snapshot: %d", requestRows)
	}

	activeBefore := snapshotRecordCounters(t, ctx, store, execution.ID)
	if err := store.SaveLatestSnapshots(ctx, execution.ID,
		&LatestRequestSnapshot{RecordID: request.ID, WireBody: []byte("active wire")},
		&LatestBudgetSnapshot{RecordID: budget.ID, Fact: budgetTestFact(1)}); err != nil {
		t.Fatalf("save active snapshots: %v", err)
	}
	activeAfter := snapshotRecordCounters(t, ctx, store, execution.ID)
	if activeAfter != activeBefore {
		t.Fatalf("active snapshot save changed execution state: before=%+v after=%+v", activeBefore, activeAfter)
	}

	secondRequest, err := store.AppendLatestRequestMetadata(ctx, execution.ID, OccurrenceInput{
		Kind: "model_request", Payload: contracts.JSON(map[string]any{"requestOrdinal": 2, "modelStep": 2,
			"wireBytes": len("settled wire")}),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	secondBudget, err := store.AppendBudgetSummary(ctx, execution.ID, budgetTestPayload(2, true))
	if err != nil {
		t.Fatal(err)
	}
	settleRecallTestExecution(t, store, execution, OutcomeCompleted, "2026-10-14T00:00:00Z")
	settledBefore := executionSnapshot(t, ctx, store, execution.ID)
	if err := store.SaveLatestSnapshots(ctx, execution.ID,
		&LatestRequestSnapshot{RecordID: secondRequest.ID, WireBody: []byte("settled wire")},
		&LatestBudgetSnapshot{RecordID: secondBudget.ID, Fact: budgetTestFact(2)}); err != nil {
		t.Fatalf("save settled snapshots: %v", err)
	}
	settledAfter := executionSnapshot(t, ctx, store, execution.ID)
	if settledAfter != settledBefore {
		t.Fatalf("settled snapshot save changed execution state: before=%+v after=%+v", settledBefore, settledAfter)
	}
	if _, err := store.AppendLatestRequestMetadata(ctx, execution.ID, OccurrenceInput{
		Kind: "model_request", Payload: json.RawMessage(`{"requestOrdinal":3}`),
	}, nil); !errors.Is(err, ErrExecutionClosed) {
		t.Fatalf("settled request metadata append error = %v, want ErrExecutionClosed", err)
	}
	if _, err := store.AppendBudgetSummary(ctx, execution.ID, budgetTestPayload(3, false)); !errors.Is(err, ErrExecutionClosed) {
		t.Fatalf("settled budget summary append error = %v, want ErrExecutionClosed", err)
	}
	if unchanged := executionSnapshot(t, ctx, store, execution.ID); unchanged != settledAfter {
		t.Fatalf("settled metadata append changed execution state: before=%+v after=%+v", settledAfter, unchanged)
	}
	records, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var firstRequest, latestRequest, firstBudget, latestBudget *Record
	for index := range records {
		if records[index].ID == request.ID {
			firstRequest = &records[index]
		}
		if records[index].ID == secondRequest.ID {
			latestRequest = &records[index]
		}
		if records[index].ID == budget.ID {
			firstBudget = &records[index]
		}
		if records[index].ID == secondBudget.ID {
			latestBudget = &records[index]
		}
	}
	if firstRequest == nil || firstRequest.Content != nil || latestRequest == nil || string(latestRequest.Content) != "settled wire" {
		t.Fatalf("request snapshots after replacement: first=%+v latest=%+v", firstRequest, latestRequest)
	}
	if firstBudget == nil || latestBudget == nil || budgetDetailAvailable(t, firstBudget.Payload, false) || !budgetDetailAvailable(t, latestBudget.Payload, true) {
		t.Fatalf("budget snapshots after replacement: first=%+v latest=%+v", firstBudget, latestBudget)
	}
}

func TestSaveLatestBudgetSnapshotDoesNotRequireRequestSnapshot(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "budget-only-snapshot", "task")
	budget, err := store.AppendBudgetSummary(ctx, execution.ID, budgetTestPayload(1, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID, nil,
		&LatestBudgetSnapshot{RecordID: budget.ID, Fact: budgetTestFact(1)}); err != nil {
		t.Fatal(err)
	}
	var requestRows int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", execution.ID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 {
		t.Fatalf("budget-only save created request row: %d", requestRows)
	}
	records, err := store.Records(ctx, execution.ID, budget.Ordinal, budget.Ordinal)
	if err != nil || len(records) != 1 || !budgetDetailAvailable(t, records[0].Payload, true) {
		t.Fatalf("budget-only detail = %+v, %v", records, err)
	}
}

func TestSaveLatestRequestSnapshotPreservesPresentEmptyBody(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "empty-wire-snapshot", "task")
	request, err := store.AppendLatestRequestMetadata(ctx, execution.ID, OccurrenceInput{
		Kind: "model_request", Payload: json.RawMessage(`{"requestOrdinal":1,"wireBytes":0}`),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID, &LatestRequestSnapshot{RecordID: request.ID}, nil); err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(ctx, execution.ID, request.Ordinal, request.Ordinal)
	if err != nil || len(records) != 1 || records[0].Content == nil || len(records[0].Content) != 0 {
		t.Fatalf("present empty wire body = %+v, %v", records, err)
	}
}

func budgetDetailAvailable(t *testing.T, payload json.RawMessage, want bool) bool {
	t.Helper()
	var decoded struct {
		DetailAvailable bool `json:"detailAvailable"`
		Fact            any  `json:"fact"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.DetailAvailable != want {
		t.Fatalf("budget detailAvailable = %t, want %t in %s", decoded.DetailAvailable, want, payload)
	}
	if decoded.DetailAvailable != (decoded.Fact != nil) {
		t.Fatalf("budget Fact/detail mismatch in %s", payload)
	}
	return decoded.DetailAvailable
}

func TestRecordsAnnotateOnlyLatestLogicalSnapshotRecords(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "snapshot-record-status", "task")
	appendLogicalRequest := func(requestOrdinal int) Record {
		t.Helper()
		record, err := store.AppendLatestRequestMetadata(ctx, execution.ID, OccurrenceInput{
			Kind: "model_request", Payload: contracts.JSON(map[string]any{
				"schemaVersion": 1, "requestOrdinal": requestOrdinal, "modelStep": requestOrdinal,
			}),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	oldRequest := appendLogicalRequest(1)
	latestRequest := appendLogicalRequest(2)
	if err := store.SaveLatestSnapshots(ctx, execution.ID,
		&LatestRequestSnapshot{RecordID: latestRequest.ID, WireBody: []byte("latest wire")}, nil); err != nil {
		t.Fatal(err)
	}
	physicalRequest, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "model_request", Payload: json.RawMessage(`{"requestOrdinal":99,"modelStep":99}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	latestBudget, err := store.AppendBudgetSummary(ctx, execution.ID, budgetTestPayload(1, false))
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "resource_revision", Payload: json.RawMessage(`{"kind":"other"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Record, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	request := byID[latestRequest.ID]
	if request.SnapshotSave == nil || request.SnapshotSave.State != contracts.SnapshotSaveSaved || !request.SnapshotSave.DetailAvailable ||
		string(request.Content) != "latest wire" {
		t.Fatalf("latest logical request status/body = %+v", request)
	}
	var requestPayload struct {
		DetailAvailable *bool                    `json:"detailAvailable"`
		SnapshotSave    contracts.SnapshotStatus `json:"snapshotSave"`
	}
	if err := json.Unmarshal(request.Payload, &requestPayload); err != nil || requestPayload.DetailAvailable == nil ||
		!*requestPayload.DetailAvailable || requestPayload.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("latest logical request payload=%s err=%v", request.Payload, err)
	}
	budget := byID[latestBudget.ID]
	if budget.SnapshotSave == nil || budget.SnapshotSave.State != contracts.SnapshotSaveUnavailable || budget.SnapshotSave.DetailAvailable {
		t.Fatalf("latest budget status = %+v", budget)
	}
	for _, recordID := range []string{oldRequest.ID, physicalRequest.ID, other.ID} {
		record := byID[recordID]
		if record.SnapshotSave != nil {
			t.Fatalf("non-latest or non-logical record %s received snapshot state: %+v", recordID, record)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(record.Payload, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["snapshotSave"]; ok {
			t.Fatalf("non-latest or non-logical record %s payload received snapshot state: %s", recordID, record.Payload)
		}
	}
}

func TestContextLatestRequestLocatorUsesLogicalMetadataWithoutSnapshotRow(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "context-request-locator", "task")
	appendLogicalRequest := func(requestOrdinal int, relations json.RawMessage) Record {
		t.Helper()
		record, err := store.AppendLatestRequestMetadata(ctx, execution.ID, OccurrenceInput{
			Kind: "model_request", Payload: contracts.JSON(map[string]any{
				"schemaVersion": 1, "requestOrdinal": requestOrdinal, "modelStep": requestOrdinal,
			}),
		}, relations)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	appendLogicalRequest(1, nil)
	relations := contracts.JSON([]map[string]any{{"relation": "recall_source", "targetRecordId": "source-record"}})
	latestRequest := appendLogicalRequest(2, relations)
	if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "model_request", Payload: json.RawMessage(`{"requestOrdinal":99,"modelStep":99}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER fail_context_request_snapshot
		BEFORE INSERT ON execution_latest_requests BEGIN SELECT RAISE(ABORT, 'snapshot fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	readLocator := func() (latestRequestLocator, json.RawMessage) {
		t.Helper()
		manifest, err := store.Context(ctx, execution.ID)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			SchemaVersion int                  `json:"schemaVersion"`
			External      json.RawMessage      `json:"externalRelations"`
			Latest        latestRequestLocator `json:"latestRequest"`
		}
		if err := json.Unmarshal(manifest, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.SchemaVersion != 1 || decoded.Latest.RecordID != latestRequest.ID ||
			decoded.Latest.RecordOrdinal != latestRequest.Ordinal || decoded.Latest.RequestOrdinal != 2 {
			t.Fatalf("context locator = %+v from %s", decoded, manifest)
		}
		return decoded.Latest, decoded.External
	}
	activeLocator, external := readLocator()
	var relationFields []struct {
		Relation       string `json:"relation"`
		TargetRecordID string `json:"targetRecordId"`
	}
	if err := json.Unmarshal(external, &relationFields); err != nil || len(relationFields) != 1 ||
		relationFields[0].Relation != "recall_source" || relationFields[0].TargetRecordID != "source-record" {
		t.Fatalf("context recall relations = %s err=%v", external, err)
	}
	var requestRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?`, execution.ID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 {
		t.Fatalf("failed snapshot fixture unexpectedly wrote %d request rows", requestRows)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID,
		&LatestRequestSnapshot{RecordID: latestRequest.ID, WireBody: []byte("latest wire")}, nil); err == nil {
		t.Fatal("snapshot save succeeded despite fixture trigger")
	}
	failedLocator, _ := readLocator()
	if failedLocator != activeLocator {
		t.Fatalf("failed snapshot changed logical request locator: active=%+v failed=%+v", activeLocator, failedLocator)
	}
	if _, err := store.db.ExecContext(ctx, "DROP TRIGGER fail_context_request_snapshot"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID,
		&LatestRequestSnapshot{RecordID: latestRequest.ID, WireBody: []byte("latest wire")}, nil); err != nil {
		t.Fatal(err)
	}
	savedLocator, savedExternal := readLocator()
	if savedLocator != activeLocator || string(savedExternal) != string(external) {
		t.Fatalf("saved snapshot changed logical context metadata: locator=%+v external=%s", savedLocator, savedExternal)
	}
}

func TestLatestSnapshotStatusUsesOnlyCurrentLogicalMetadataRows(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "latest-snapshot-status", "task")
	appendRequest := func(ordinal int) Record {
		t.Helper()
		record, err := store.AppendLatestRequestMetadata(ctx, execution.ID, OccurrenceInput{
			Kind: "model_request", Payload: contracts.JSON(map[string]any{
				"schemaVersion": 1, "requestOrdinal": ordinal, "modelStep": ordinal,
			}),
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}

	firstRequest := appendRequest(1)
	status, err := store.LatestSnapshotStatus(ctx, execution.ID)
	if err != nil || status.State != contracts.SnapshotSaveUnavailable || status.DetailAvailable || status.Frozen {
		t.Fatalf("metadata without snapshot status=%+v err=%v", status, err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID, &LatestRequestSnapshot{
		RecordID: firstRequest.ID, WireBody: []byte("first wire"),
	}, nil); err != nil {
		t.Fatal(err)
	}
	status, err = store.LatestSnapshotStatus(ctx, execution.ID)
	if err != nil || status.State != contracts.SnapshotSaveSaved || !status.DetailAvailable {
		t.Fatalf("saved latest request status=%+v err=%v", status, err)
	}

	latestRequest := appendRequest(2)
	if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "model_request",
		Payload: json.RawMessage(`{"requestOrdinal":99,"modelStep":99}`)}); err != nil {
		t.Fatal(err)
	}
	status, err = store.LatestSnapshotStatus(ctx, execution.ID)
	if err != nil || status.State != contracts.SnapshotSaveUnavailable || status.DetailAvailable {
		t.Fatalf("older or physical request snapshot leaked into latest status=%+v err=%v", status, err)
	}

	budget, err := store.AppendBudgetSummary(ctx, execution.ID, budgetTestPayload(1, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLatestSnapshots(ctx, execution.ID, nil, &LatestBudgetSnapshot{
		RecordID: budget.ID, Fact: budgetTestFact(1),
	}); err != nil {
		t.Fatal(err)
	}
	status, err = store.LatestSnapshotStatus(ctx, execution.ID)
	if err != nil || status.State != contracts.SnapshotSaveSaved || !status.DetailAvailable {
		t.Fatalf("saved latest budget status=%+v err=%v", status, err)
	}
	if latestRequest.ID == firstRequest.ID {
		t.Fatal("request test did not add a new logical record")
	}
}

func snapshotRecordCounters(t *testing.T, ctx context.Context, store *Store, executionID string) [2]int64 {
	t.Helper()
	var latestOrdinal, occurrenceCount int64
	if err := store.db.QueryRowContext(ctx, `SELECT latest_ordinal,occurrence_count FROM executions WHERE execution_id=?`, executionID).
		Scan(&latestOrdinal, &occurrenceCount); err != nil {
		t.Fatal(err)
	}
	return [2]int64{latestOrdinal, occurrenceCount}
}
