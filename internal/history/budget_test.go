package history

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func budgetTestPayload(step int, exceeded bool) json.RawMessage {
	return contracts.JSON(map[string]any{
		"modelStep":      step,
		"modelSelection": contracts.ModelSelection{Provider: "fixture", API: "fixture-api", ModelID: "fixture/model"},
		"exceeded":       exceeded,
		"summary": map[string]any{"estimatedTokens": step * 10, "selectedMessageCount": step,
			"omittedCurrentMessageCount": step - 1, "historyStatus": "partial", "historyReason": "budget_limit"},
		"detailAvailable": false,
	})
}

func budgetTestFact(step int) json.RawMessage {
	return contracts.JSON(map[string]any{"step": step, "sources": []any{map[string]any{"kind": "current", "messageOrdinal": step}}})
}

func TestLatestBudgetDetailOverwritesIndependentlyFromWireAndMarksPastSummary(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	execution := beginRecallTestExecution(t, store, sessionID, "budget-wire-independent", "task")
	requestPayload := contracts.JSON(map[string]any{"schemaVersion": 1, "requestOrdinal": 1, "modelStep": 1,
		"modelSelection": contracts.ModelSelection{Provider: "fixture", API: "fixture-api", ModelID: "fixture/model"},
		"itemCount":      1, "messageCount": 1, "toolContractCount": 0, "instructionCount": 0, "wireBytes": 12})
	request, err := store.AppendLatestRequest(ctx, execution.ID, OccurrenceInput{Kind: "model_request", Payload: requestPayload, Content: []byte("first wire")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstBudget, err := store.AppendLatestBudget(ctx, execution.ID, budgetTestPayload(1, false), budgetTestFact(1))
	if err != nil {
		t.Fatal(err)
	}
	latestBudget, err := store.AppendLatestBudget(ctx, execution.ID, budgetTestPayload(2, true), budgetTestFact(2))
	if err != nil {
		t.Fatal(err)
	}

	past, err := store.Records(ctx, execution.ID, firstBudget.Ordinal, firstBudget.Ordinal)
	if err != nil || len(past) != 1 {
		t.Fatalf("past budget record: %+v %v", past, err)
	}
	var pastPayload map[string]json.RawMessage
	if err := json.Unmarshal(past[0].Payload, &pastPayload); err != nil {
		t.Fatal(err)
	}
	var pastAvailable bool
	if err := json.Unmarshal(pastPayload["detailAvailable"], &pastAvailable); err != nil || pastAvailable {
		t.Fatalf("past detail marker=%s err=%v", pastPayload["detailAvailable"], err)
	}
	if _, exists := pastPayload["fact"]; exists {
		t.Fatalf("past record retained full detail: %s", past[0].Payload)
	}

	current, err := store.Records(ctx, execution.ID, latestBudget.Ordinal, latestBudget.Ordinal)
	if err != nil || len(current) != 1 {
		t.Fatalf("latest budget record: %+v %v", current, err)
	}
	var currentPayload struct {
		ModelStep       int  `json:"modelStep"`
		Exceeded        bool `json:"exceeded"`
		DetailAvailable bool `json:"detailAvailable"`
		Summary         struct {
			EstimatedTokens int64 `json:"estimatedTokens"`
		} `json:"summary"`
		Fact struct {
			Step int `json:"step"`
		} `json:"fact"`
	}
	if err := json.Unmarshal(current[0].Payload, &currentPayload); err != nil {
		t.Fatal(err)
	}
	if !currentPayload.DetailAvailable || currentPayload.ModelStep != 2 || currentPayload.Fact.Step != 2 ||
		!currentPayload.Exceeded || currentPayload.Summary.EstimatedTokens != 20 {
		t.Fatalf("latest budget summary/detail mismatch: %+v", currentPayload)
	}
	requests, err := store.Records(ctx, execution.ID, request.Ordinal, request.Ordinal)
	if err != nil || len(requests) != 1 || string(requests[0].Content) != "first wire" {
		t.Fatalf("budget update changed latest wire: %+v %v", requests, err)
	}
}

func TestBudgetReadDuringOverwriteKeepsSummaryAndFactFromSameStep(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "history.db")
	writer, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	created, err := writer.CreateSession(ctx, SessionInput{ID: "budget-concurrency", WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`)})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := writer.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "budget-concurrency-root", SessionID: created.ID,
		Task: "task", Model: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var done atomic.Bool
	var writerErr error
	var readerErr error
	var reads atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for step := 1; step <= 80; step++ {
			payload := budgetTestPayload(step, step%2 == 0)
			fact := budgetTestFact(step)
			if _, saveErr := writer.AppendLatestBudget(ctx, execution.ID, payload, fact); saveErr != nil {
				writerErr = saveErr
				break
			}
			time.Sleep(time.Millisecond)
		}
		done.Store(true)
	}()
	go func() {
		defer wg.Done()
		for {
			records, readErr := reader.Records(ctx, execution.ID, 0, -1)
			if readErr != nil {
				readerErr = readErr
				return
			}
			var latest *Record
			for i := range records {
				if records[i].Kind == "context_budget" {
					latest = &records[i]
				}
			}
			if latest != nil {
				var payload struct {
					ModelStep       int  `json:"modelStep"`
					DetailAvailable bool `json:"detailAvailable"`
					Fact            struct {
						Step int `json:"step"`
					} `json:"fact"`
				}
				if err := json.Unmarshal(latest.Payload, &payload); err != nil {
					readerErr = err
					return
				}
				if !payload.DetailAvailable || payload.ModelStep != payload.Fact.Step {
					readerErr = fmt.Errorf("mixed budget snapshot: step=%d fact=%d payload=%s", payload.ModelStep, payload.Fact.Step, latest.Payload)
					return
				}
				reads.Add(1)
			}
			if done.Load() {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	wg.Wait()
	if writerErr != nil || readerErr != nil {
		t.Fatalf("writer=%v reader=%v", writerErr, readerErr)
	}
	if reads.Load() == 0 {
		t.Fatal("reader observed no budget snapshots during overwrite")
	}
}
