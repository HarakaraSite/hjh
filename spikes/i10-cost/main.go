// Measure recall through production admission, request preparation and storage
// in new private workspaces using an in-process local provider.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

type requestObservation struct {
	Task       string
	BodyCopies int
	Reference  bool
}
type heldProvider struct {
	body    string
	entered chan requestObservation
}

func (p *heldProvider) Prepare(r contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(struct {
		Instruction string
		Messages    []contracts.Message
		Tools       []contracts.ToolDefinition
	}{r.Instruction, r.Messages, r.Tools})
}
func (p *heldProvider) Generate(ctx context.Context, r contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
	task := r.Messages[len(r.Messages)-1].VisibleText()
	if task == "source" && len(r.Messages) == 1 {
		return contracts.ModelResult{FinishReason: "tool_calls", Calls: []contracts.ToolCall{{CallID: "read-body", Name: "read_file", Arguments: contracts.JSON(map[string]any{"path": "body.txt"})}}}, nil
	}
	if observe != nil {
		if err := observe(contracts.Observation{Kind: "assistant_text", Phase: "delta", ID: "partial", Text: "unfinished source answer"}); err != nil {
			return contracts.ModelResult{}, err
		}
	}
	entry := requestObservation{Task: task}
	for _, m := range r.Messages {
		text := m.VisibleText()
		entry.BodyCopies += strings.Count(text, p.body)
		entry.Reference = entry.Reference || strings.Contains(text, `"sourceExecutionId"`)
	}
	p.entered <- entry
	<-ctx.Done()
	return contracts.ModelResult{}, ctx.Err()
}

type checkpoint struct {
	HeapBytes  uint64 `json:"heapBytesAfterGC"`
	TotalAlloc uint64 `json:"totalAllocatedBytes"`
	DBBytes    int64  `json:"databaseBytes"`
}

func sample(database string) checkpoint {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	info, err := os.Stat(database)
	if err != nil {
		panic(err)
	}
	return checkpoint{stats.HeapAlloc, stats.TotalAlloc, info.Size()}
}
func waitTerminal(manager *corerun.Manager, runID string) (api.RunState, error) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		state, err := manager.Get(manager.Core().CoreID, runID)
		if err != nil {
			return state, err
		}
		if state.Phase == "terminal" {
			return state, nil
		}
		time.Sleep(time.Millisecond)
	}
	return api.RunState{}, fmt.Errorf("run %s did not settle", runID)
}
func entered(provider *heldProvider) (requestObservation, error) {
	select {
	case entry := <-provider.entered:
		return entry, nil
	case <-time.After(15 * time.Second):
		return requestObservation{}, fmt.Errorf("request not observed")
	}
}
func measure(out string, count, bodyBytes int) (any, error) {
	workspace, err := os.MkdirTemp(out, fmt.Sprintf("recall-%d-", count))
	if err != nil {
		return nil, err
	}
	database := filepath.Join(workspace, "history.sqlite3")
	store, err := history.Open(database)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	body := "I10_BODY_" + strings.Repeat("x", bodyBytes-len("I10_BODY_"))
	if err := os.WriteFile(filepath.Join(workspace, "body.txt"), []byte(body), 0644); err != nil {
		return nil, err
	}
	provider := &heldProvider{body: body, entered: make(chan requestObservation, 1)}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	defer service.Close()
	manager := corerun.New(service, workspace)
	defer manager.Close()
	coreID := manager.Core().CoreID
	if _, _, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: "source", Task: "source"}); err != nil {
		return nil, err
	}
	if _, err := entered(provider); err != nil {
		return nil, err
	}
	if _, err := manager.Cancel(coreID, "source"); err != nil {
		return nil, err
	}
	source, err := waitTerminal(manager, "source")
	if err != nil {
		return nil, err
	}
	before := sample(database)
	var firstPrepared, firstRequested checkpoint
	var allocationBytes uint64
	for i := 0; i < count; i++ {
		op, err := manager.Recall(context.Background(), source.SessionID, api.RecallOperationRequest{CoreID: coreID, OperationID: fmt.Sprintf("recall-%d", i), Action: "prepare", Selector: source.ExecutionID})
		if err != nil {
			return nil, err
		}
		if op.Receipt.SourceExecutionID != source.ExecutionID {
			return nil, fmt.Errorf("wrong source")
		}
		if i == 0 {
			firstPrepared = sample(database)
		}
		start := sample(database)
		runID := fmt.Sprintf("target-%d", i)
		if _, _, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: runID, SessionID: source.SessionID, Task: "consume recall"}); err != nil {
			return nil, err
		}
		observed, err := entered(provider)
		if err != nil {
			return nil, err
		}
		if !observed.Reference || observed.BodyCopies != 1 {
			return nil, fmt.Errorf("reference=%t body copies=%d", observed.Reference, observed.BodyCopies)
		}
		request := sample(database)
		allocationBytes += request.TotalAlloc - start.TotalAlloc
		if i == 0 {
			firstRequested = request
		}
		state, err := manager.SessionContext(context.Background(), coreID, source.SessionID)
		if err != nil {
			return nil, err
		}
		if state.PendingRecall != nil || state.LatestRecallAdmission == nil || state.LatestRecallAdmission.RequestRecordOrdinal == nil {
			return nil, fmt.Errorf("recall admission/request not visible")
		}
		if _, err := manager.Cancel(coreID, runID); err != nil {
			return nil, err
		}
		if _, err := waitTerminal(manager, runID); err != nil {
			return nil, err
		}
	}
	settled := sample(database)
	// All attribution queries happen after heap checkpoints, on this private DB.
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var referenceDigests, referenceContents, referenceBytes, mirrorBytes, relations, receiptBytes, wireDigests, wireBytes int64
	queries := []struct {
		query string
		args  []any
		dest  []any
	}{
		{`SELECT count(DISTINCT m.content_digest),count(DISTINCT c.content_digest),COALESCE(sum(length(c.content_bytes)),0) FROM messages m JOIN contents c ON c.content_digest=m.content_digest JOIN executions e ON e.execution_id=m.execution_id WHERE e.execution_id<>? AND m.message_ordinal=0`, []any{source.ExecutionID}, []any{&referenceDigests, &referenceContents, &referenceBytes}},
		{`SELECT COALESCE(sum(length(payload_json)),0) FROM semantic_records WHERE json_extract(payload_json,'$.conversation.entityKind')='recall'`, nil, []any{&mirrorBytes}},
		{`SELECT count(*) FROM recall_relations`, nil, []any{&relations}},
		{`SELECT COALESCE(sum(length(result_json)+length(target_json)),0) FROM command_receipts WHERE operation LIKE 'recall.%'`, nil, []any{&receiptBytes}},
		{`SELECT count(DISTINCT r.content_digest),COALESCE(sum(length(c.content_bytes)),0) FROM semantic_records r JOIN contents c ON c.content_digest=r.content_digest WHERE r.kind='model_request' AND json_extract(r.payload_json,'$.schemaVersion')=2 AND r.execution_id<>?`, []any{source.ExecutionID}, []any{&wireDigests, &wireBytes}},
	}
	for _, q := range queries {
		if err := db.QueryRow(q.query, q.args...).Scan(q.dest...); err != nil {
			return nil, err
		}
	}
	if relations != int64(count) || referenceDigests != 1 {
		return nil, fmt.Errorf("relations=%d reference digests=%d", relations, referenceDigests)
	}
	var storedReferenceBytes, storedWireBytes int64
	if err := db.QueryRow(`SELECT COALESCE(sum(length(content_bytes)),0) FROM contents WHERE content_digest IN(SELECT m.content_digest FROM messages m WHERE m.execution_id<>? AND m.message_ordinal=0)`, source.ExecutionID).Scan(&storedReferenceBytes); err != nil {
		return nil, err
	}
	if err := db.QueryRow(`SELECT COALESCE(sum(length(content_bytes)),0) FROM contents WHERE content_digest IN(SELECT content_digest FROM semantic_records WHERE kind='model_request' AND json_extract(payload_json,'$.schemaVersion')=2 AND execution_id<>?)`, source.ExecutionID).Scan(&storedWireBytes); err != nil {
		return nil, err
	}
	metadataBefore := sample(database)
	for i := 0; i < 64; i++ {
		if _, err := manager.SessionContext(context.Background(), coreID, source.SessionID); err != nil {
			return nil, err
		}
	}
	metadataAfter := sample(database)
	runtime.KeepAlive(manager)
	runtime.KeepAlive(provider)
	return map[string]any{"count": count, "bodyBytes": bodyBytes, "database": database, "before": before, "firstPrepared": firstPrepared, "firstRequest": firstRequested, "settled": settled,
		"admissionThroughRequestAllocatedBytes": allocationBytes, "referenceDigestCount": referenceDigests, "referenceReadBytesAcrossTargets": referenceBytes, "storedUniqueReferenceBytes": storedReferenceBytes,
		"recallMirrorMetadataBytes": mirrorBytes, "relationCount": relations, "operationMetadataBytes": receiptBytes, "wireDigestCount": wireDigests, "wireBytesAcrossTargets": wireBytes, "storedUniqueWireBytes": storedWireBytes,
		"metadataRead64AllocatedBytes": metadataAfter.TotalAlloc - metadataBefore.TotalAlloc}, nil
}
func main() {
	out := flag.String("out", "dist/i10-live/cost", "new private workspaces directory")
	repeat := flag.Int("repeat", 16, "repeated actual recall request count")
	bodyBytes := flag.Int("body-bytes", 128*1024, "source read_file result bytes")
	flag.Parse()
	if err := os.MkdirAll(*out, 0755); err != nil {
		panic(err)
	}
	var results []any
	for _, n := range []int{1, *repeat} {
		r, err := measure(*out, n, *bodyBytes)
		if err != nil {
			panic(err)
		}
		results = append(results, r)
	}
	encoded, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "summary.json"), append(encoded, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))
}
