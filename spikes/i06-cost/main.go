// Manual same-Session lifetime measurement through the production Core path.
// No external provider, credentials, user workspace, or process is accessed.
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
	"strconv"
	"strings"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

type fixedProvider struct{ text string }

func (p fixedProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}
func (p fixedProvider) Generate(_ context.Context, _ contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
	if err := observe(contracts.Observation{ID: "request", Kind: "model_request", Phase: "start", PhysicalRequestID: "request"}); err != nil {
		return contracts.ModelResult{}, err
	}
	if err := observe(contracts.Observation{ID: "result", Parent: "request", Kind: "model_result", Phase: "end", Text: p.text,
		Result: contracts.JSON(map[string]any{"text": p.text, "calls": []contracts.ToolCall{}})}); err != nil {
		return contracts.ModelResult{}, err
	}
	return contracts.ModelResult{Text: p.text, FinishReason: "stop"}, nil
}

type checkpoint struct {
	Turns           int    `json:"turns"`
	HeapBytes       uint64 `json:"heapBytesAfterGC"`
	TotalAllocBytes uint64 `json:"totalAllocBytes"`
	RSSBytes        uint64 `json:"rssBytes"`
	Goroutines      int    `json:"goroutines"`
	DatabaseBytes   int64  `json:"databaseBytes"`
}

func sample(turns int, database string) checkpoint {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	s := checkpoint{Turns: turns, HeapBytes: stats.HeapAlloc, TotalAllocBytes: stats.TotalAlloc, Goroutines: runtime.NumGoroutine()}
	if info, err := os.Stat(database); err == nil {
		s.DatabaseBytes = info.Size()
	}
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "VmRSS:" {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				s.RSSBytes = kb * 1024
			}
		}
	}
	return s
}

func measure(turns, bodyBytes int) (any, error) {
	workspace, err := os.MkdirTemp("", "hjh-i06-lifetime-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(workspace)
	database := filepath.Join(workspace, "history.sqlite3")
	store, err := history.Open(database)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	provider := fixedProvider{text: strings.Repeat("x", bodyBytes)}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	defer service.Close()
	defer manager.Close()
	checkpoints := []checkpoint{sample(0, database)}
	started := time.Now()
	var session string
	for i := 1; i <= turns; i++ {
		id := fmt.Sprintf("turn-%d", i)
		if _, _, err := manager.Start(api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: id,
			SessionID: session, Task: fmt.Sprintf("turn %d", i)}); err != nil {
			return nil, err
		}
		for {
			state, err := manager.Get(manager.Core().CoreID, id)
			if err != nil {
				return nil, err
			}
			if state.Phase == "terminal" {
				if state.Result == nil || !state.Result.OK || state.Result.AgentResult.FinalText != provider.text {
					return nil, fmt.Errorf("wrong terminal result at turn %d: %+v", i, state.Result)
				}
				session = state.SessionID
				break
			}
			time.Sleep(time.Millisecond)
		}
		if i == 1 || i%25 == 0 || i == turns {
			checkpoints = append(checkpoints, sample(i, database))
		}
	}
	// Unchanged terminal reads must remain result-only. They do not request a
	// conversation materialization or preserve a full inspection in this runner.
	for i := 0; i < 100; i++ {
		if _, err := manager.Get(manager.Core().CoreID, fmt.Sprintf("turn-%d", turns)); err != nil {
			return nil, err
		}
	}
	checkpoints = append(checkpoints, sample(turns, database))
	elapsed := float64(time.Since(started)) / float64(time.Millisecond)
	beforeView := sample(turns, database)
	viewStarted := time.Now()
	view, err := manager.SessionView(context.Background(), session)
	if err != nil {
		return nil, err
	}
	viewMS := float64(time.Since(viewStarted)) / float64(time.Millisecond)
	var viewStats runtime.MemStats
	runtime.ReadMemStats(&viewStats)
	viewAlloc := viewStats.TotalAlloc - beforeView.TotalAllocBytes
	answers := 0
	for _, entity := range view.Entities {
		if entity.Kind == "assistant" {
			answers++
		}
	}
	if answers != turns {
		return nil, fmt.Errorf("Session view has %d answers for %d turns", answers, turns)
	}
	// Attribution runs after all lifetime/latency checkpoints, on a measurement
	// connection. It does not change the production Store connection policy.
	db, err := sql.Open("sqlite", database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query("SELECT kind, count(*), sum(length(payload_json)) FROM semantic_records GROUP BY kind ORDER BY sum(length(payload_json)) DESC")
	if err != nil {
		return nil, err
	}
	var payloads []map[string]any
	for rows.Next() {
		var kind string
		var count, bytes int64
		if err := rows.Scan(&kind, &count, &bytes); err != nil {
			rows.Close()
			return nil, err
		}
		payloads = append(payloads, map[string]any{"kind": kind, "records": count, "payloadBytes": bytes})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var contentBytes int64
	if err := db.QueryRow("SELECT coalesce(sum(length(content_bytes)),0) FROM contents").Scan(&contentBytes); err != nil {
		return nil, err
	}
	runtime.KeepAlive(manager)
	return map[string]any{"turns": turns, "bodyBytesPerTurn": bodyBytes, "sameSession": true,
		"terminalReads": 100, "elapsedMs": elapsed, "checkpoints": checkpoints, "semanticPayloadsByKind": payloads, "contentBytes": contentBytes, "coldSessionViewMs": viewMS, "coldSessionViewAllocBytes": viewAlloc, "coldSessionViewEntities": len(view.Entities)}, nil
}

func main() {
	turns := flag.Int("turns", 200, "same-Session turns to measure")
	body := flag.Int("body-bytes", 256, "answer bytes per turn")
	flag.Parse()
	result, err := measure(*turns, *body)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
