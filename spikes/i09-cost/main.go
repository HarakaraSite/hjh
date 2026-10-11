// Measure accepted runtime-input storage and retained Go heap through Core.
// Uses a held local provider and new private workspaces, without credentials.
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

type heldProvider struct{ entered chan struct{} }

func (p heldProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}

func (p heldProvider) Generate(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	close(p.entered)
	<-ctx.Done()
	return contracts.ModelResult{}, ctx.Err()
}

type checkpoint struct {
	HeapBytes uint64 `json:"heapBytesAfterGC"`
	DBBytes   int64  `json:"databaseBytes"`
}

func sample(database string) checkpoint {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	info, err := os.Stat(database)
	if err != nil {
		panic(err)
	}
	return checkpoint{stats.HeapAlloc, info.Size()}
}

func measure(out, kind string, count, bodyBytes int) (any, error) {
	workspace, err := os.MkdirTemp(out, kind+"-")
	if err != nil {
		return nil, err
	}
	database := filepath.Join(workspace, "history.sqlite3")
	store, err := history.Open(database)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	provider := heldProvider{entered: make(chan struct{})}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	defer service.Close()
	manager := corerun.New(service, workspace)
	defer manager.Close()
	coreID, runID := manager.Core().CoreID, "root"
	if _, _, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: runID, Task: "held root"}); err != nil {
		return nil, err
	}
	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("provider did not start")
	}
	state, err := manager.Get(coreID, runID)
	if err != nil {
		return nil, err
	}
	body := strings.Repeat("x", bodyBytes)
	before := sample(database)
	var digest string
	for i := 0; i < count; i++ {
		receipt, created, err := manager.SubmitInput(context.Background(), runID, api.SubmitInputRequest{
			CoreID: coreID, SessionID: state.SessionID, InputID: fmt.Sprintf("input-%d", i), Kind: kind, Text: body})
		if err != nil || !created {
			return nil, fmt.Errorf("admit input %d: created=%t: %w", i, created, err)
		}
		if digest != "" && digest != receipt.ContentDigest {
			return nil, fmt.Errorf("same body produced different digests")
		}
		digest = receipt.ContentDigest
	}
	admitted := sample(database)
	if _, err := manager.CancelInputChain(coreID, runID); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		chain, err := manager.InputChain(coreID, runID)
		if err != nil {
			return nil, err
		}
		if chain.Phase == "settled" {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("input chain did not settle")
		}
		time.Sleep(time.Millisecond)
	}
	settled := sample(database)
	// Attribution after heap checkpoints; the product Store policy is unchanged.
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var copies, contentBytes, receiptBytes, controlBytes int64
	if err := db.QueryRow("SELECT count(*),sum(length(content_bytes)) FROM contents WHERE content_digest=?", digest).Scan(&copies, &contentBytes); err != nil {
		return nil, err
	}
	if err := db.QueryRow("SELECT sum(length(result_json)+length(target_json)) FROM command_receipts").Scan(&receiptBytes); err != nil {
		return nil, err
	}
	if err := db.QueryRow("SELECT sum(length(control_json)) FROM execution_control_facts").Scan(&controlBytes); err != nil {
		return nil, err
	}
	var notApplied int
	if err := db.QueryRow("SELECT count(*) FROM command_receipts WHERE json_extract(result_json,'$.reason')='cancelled'").Scan(&notApplied); err != nil {
		return nil, err
	}
	if copies != 1 || notApplied != count {
		return nil, fmt.Errorf("content copies=%d, stopped inputs=%d, count=%d", copies, notApplied, count)
	}
	runtime.KeepAlive(manager)
	runtime.KeepAlive(body)
	return map[string]any{"kind": kind, "count": count, "bodyBytes": bodyBytes, "database": database,
		"before": before, "admitted": admitted, "settled": settled, "contentCopies": copies,
		"sharedContentBytes": contentBytes, "receiptMetadataBytes": receiptBytes, "controlBytes": controlBytes}, nil
}

func main() {
	out := flag.String("out", "dist/i09-live/cost", "directory for new private measurement workspaces")
	count := flag.Int("repeat", 64, "input count for the repeated case")
	bodyBytes := flag.Int("body-bytes", 128*1024, "identical input text size")
	flag.Parse()
	if err := os.MkdirAll(*out, 0755); err != nil {
		panic(err)
	}
	var results []any
	for _, kind := range []string{"steering", "follow-up"} {
		for _, n := range []int{1, *count} {
			result, err := measure(*out, kind, n, *bodyBytes)
			if err != nil {
				panic(err)
			}
			results = append(results, result)
		}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(*out, "summary.json"), append(data, '\n'), 0644); err != nil {
		panic(err)
	}
	fmt.Println(string(data))
}
