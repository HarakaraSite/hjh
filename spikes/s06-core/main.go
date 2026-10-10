// S-06 uses the production Core, HTTP handlers, QuickJS and SQLite with a
// deterministic provider. Only the SSE response writer is deliberately stalled.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/corerun"
	"hjh/internal/history"
	"hjh/internal/httpapi"
)

type report struct {
	Updates               int     `json:"updates"`
	Cancelled             bool    `json:"cancelled"`
	FinalTextBytes        int     `json:"finalTextBytes"`
	ObserverTotalMS       float64 `json:"observerTotalMs"`
	ObserverP95MS         float64 `json:"observerP95Ms"`
	ObserverMaxMS         float64 `json:"observerMaxMs"`
	IndependentStartMS    float64 `json:"independentStartMs"`
	CancelRequestMS       float64 `json:"cancelRequestMs,omitempty"`
	SettleMS              float64 `json:"settleMs"`
	RestoreMS             float64 `json:"restoreMs"`
	CoreCloseMS           float64 `json:"coreCloseMs"`
	PersistedObservations int     `json:"persistedObservations"`
	ObservationBytes      int     `json:"observationPayloadBytes"`
	SSEBlockedAtTerminal  bool    `json:"sseBlockedAtTerminal"`
	TerminalAfterRelease  bool    `json:"terminalReceivedAfterRelease"`
	Cost                  *cost   `json:"cost,omitempty"`
}

// Allocation counters cover this isolated experiment process, not RSS or a
// production memory limit. GC checkpoints measure retained heap separately.
type cost struct {
	DatabaseBytes        int64   `json:"databaseBytes"`
	StreamAllocBytes     uint64  `json:"streamAllocBytes"`
	RestoreAllocBytes    uint64  `json:"restoreAllocBytes"`
	RepeatedRestoreBytes uint64  `json:"repeatedRestoreAllocBytes"`
	RepeatedRestoreMS    float64 `json:"repeatedRestoreMs"`
	HeapBeforeStream     uint64  `json:"heapBeforeStream"`
	HeapAfterTerminal    uint64  `json:"heapAfterTerminal"`
	HeapAfterRestore     uint64  `json:"heapAfterRestore"`
	HeapAfterCleanup     uint64  `json:"heapAfterCleanup"`
	ProcessPeakRSSBytes  uint64  `json:"processPeakRssBytes"`
}

func memory(gc bool) runtime.MemStats {
	if gc {
		runtime.GC()
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

func peakRSS() (uint64, error) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "VmHWM:" && fields[2] == "kB" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			return kb * 1024, err
		}
	}
	return 0, errors.New("Linux VmHWM is not available")
}

type provider struct {
	updates int
	ready   chan struct{}
	start   chan struct{}
	emitted chan struct{}
	finish  chan struct{}
	samples []time.Duration // written before emitted closes; read afterward
	text    string
}

func (p *provider) Prepare(r contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(r.Messages)
}

func (p *provider) Generate(ctx context.Context, r contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
	for _, message := range r.Messages {
		if message.Role == "user" && message.VisibleText() == "independent" {
			return contracts.ModelResult{Text: "別Sessionの依頼完了", FinishReason: "stop"}, nil
		}
	}
	close(p.ready)
	select {
	case <-p.start:
	case <-ctx.Done():
		return contracts.ModelResult{}, ctx.Err()
	}
	for i := 0; i < p.updates; i++ {
		p.text += strings.Repeat("x", 128)
		begin := time.Now()
		err := observe(contracts.Observation{ID: "stream", Kind: "assistant_text", Phase: "delta", Result: contracts.JSON(map[string]string{"text": p.text})})
		p.samples = append(p.samples, time.Since(begin))
		if err != nil {
			return contracts.ModelResult{}, err
		}
	}
	close(p.emitted)
	select {
	case <-p.finish:
		return contracts.ModelResult{Text: p.text, FinishReason: "stop"}, nil
	case <-ctx.Done():
		return contracts.ModelResult{}, ctx.Err()
	}
}

// This represents a terminal/network consumer that never completes a write.
// The Core's actual SSE handler stays inside Write until release is closed.
type stalledWriter struct {
	header  http.Header
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	body    bytes.Buffer // only read after handlerDone
}

func (w *stalledWriter) Header() http.Header { return w.header }
func (w *stalledWriter) WriteHeader(int)     {}
func (w *stalledWriter) Flush()              {}
func (w *stalledWriter) Write(b []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return w.body.Write(b)
}

func wait(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func terminal(ctx context.Context, m *corerun.Manager, id string) (api.RunState, error) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := m.Get(m.Core().CoreID, id)
		if err != nil || state.Phase == "terminal" {
			return state, err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return state, ctx.Err()
		}
	}
}

func experiment(updates int, cancelRun, measureCost bool) (out report, err error) {
	out.Updates, out.Cancelled = updates, cancelRun
	workspace, err := os.MkdirTemp("", "hjh-s06-core-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(workspace)
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		return out, err
	}
	defer store.Close()
	p := &provider{updates: updates, ready: make(chan struct{}), start: make(chan struct{}), emitted: make(chan struct{}), finish: make(chan struct{})}
	service := app.NewService(store, p, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	server := httpapi.New(service, manager)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: "stream", Task: "stream"}
	if _, _, err = manager.Start(request); err != nil {
		return out, err
	}
	if err = wait(ctx, p.ready); err != nil {
		return out, err
	}
	w := &stalledWriter{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(w.release) }) }
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		url := "/api/v1/runs/stream/events?coreId=" + request.CoreID
		server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx))
	}()
	defer func() { release(); _ = wait(ctx, handlerDone) }()
	if err = wait(ctx, w.entered); err != nil {
		return out, err
	}
	var streamMemory runtime.MemStats
	if measureCost {
		out.Cost = &cost{}
		streamMemory = memory(true)
		out.Cost.HeapBeforeStream = streamMemory.HeapAlloc
	}
	close(p.start)
	// Starting a second Session uses the actual JSON admission handler while
	// the first Session streams and its SSE writer is blocked.
	independent := api.StartRunRequest{CoreID: request.CoreID, RunID: "independent", Task: "independent"}
	body, _ := json.Marshal(independent)
	response := httptest.NewRecorder()
	begin := time.Now()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/runs", bytes.NewReader(body)))
	out.IndependentStartMS = milliseconds(time.Since(begin))
	if response.Code != http.StatusAccepted {
		return out, fmt.Errorf("independent admission: %s", response.Body.String())
	}
	second, err := terminal(ctx, manager, independent.RunID)
	if err != nil || second.Result == nil || !second.Result.OK {
		return out, fmt.Errorf("independent run failed: %v %+v", err, second.Result)
	}
	if err = wait(ctx, p.emitted); err != nil {
		return out, err
	}
	for _, sample := range p.samples {
		out.ObserverTotalMS += milliseconds(sample)
	}
	sort.Slice(p.samples, func(i, j int) bool { return p.samples[i] < p.samples[j] })
	if len(p.samples) != 0 {
		out.ObserverP95MS = milliseconds(p.samples[(len(p.samples)-1)*95/100])
		out.ObserverMaxMS = milliseconds(p.samples[len(p.samples)-1])
	}
	begin = time.Now()
	if cancelRun {
		cancelResponse := httptest.NewRecorder()
		server.ServeHTTP(cancelResponse, httptest.NewRequest(http.MethodPost, "/api/v1/runs/stream/cancel?coreId="+request.CoreID, nil))
		out.CancelRequestMS = milliseconds(time.Since(begin))
		if cancelResponse.Code != http.StatusAccepted {
			return out, fmt.Errorf("cancel: %s", cancelResponse.Body.String())
		}
	} else {
		close(p.finish)
	}
	state, err := terminal(ctx, manager, request.RunID)
	out.SettleMS = milliseconds(time.Since(begin))
	if err != nil || state.Result == nil || state.Result.AgentResult == nil {
		return out, fmt.Errorf("missing terminal result: %v", err)
	}
	if cancelRun && state.Result.AgentResult.Outcome != "cancelled" || !cancelRun && !state.Result.OK {
		return out, fmt.Errorf("wrong outcome: %+v", state.Result)
	}
	if measureCost {
		stats := memory(true)
		out.Cost.StreamAllocBytes = stats.TotalAlloc - streamMemory.TotalAlloc
		out.Cost.HeapAfterTerminal = stats.HeapAlloc
	}
	select {
	case <-handlerDone:
		return out, errors.New("SSE handler unexpectedly returned before writer release")
	default:
		out.SSEBlockedAtTerminal = true
	}
	inspection, err := service.Inspect(ctx, state.ExecutionID, 0, -1)
	if err != nil || inspection.Execution.Lifecycle == "active" {
		return out, fmt.Errorf("terminal was not persisted: %v", err)
	}
	for _, record := range inspection.Records {
		var o contracts.Observation
		if json.Unmarshal(record.Payload, &o) == nil && o.Kind == "assistant_text" && o.ExecutionID == state.ExecutionID {
			out.PersistedObservations++
			out.ObservationBytes += len(record.Payload)
		}
	}
	if out.PersistedObservations != updates {
		return out, fmt.Errorf("persisted %d of %d text observations", out.PersistedObservations, updates)
	}
	late, err := manager.Subscribe(request.CoreID, request.RunID)
	if err != nil {
		return out, err
	}
	defer late.Close()
	var restoreMemory runtime.MemStats
	if measureCost {
		restoreMemory = memory(true)
	}
	begin = time.Now()
	snapshot, err := late.Snapshot()
	out.RestoreMS = milliseconds(time.Since(begin))
	if err != nil || len(snapshot.Texts) != 1 || snapshot.Texts[0].Text != p.text {
		return out, fmt.Errorf("snapshot did not restore latest text: %v", err)
	}
	out.FinalTextBytes = len(snapshot.Texts[0].Text)
	if measureCost {
		stats := memory(false)
		out.Cost.RestoreAllocBytes = stats.TotalAlloc - restoreMemory.TotalAlloc
		// Re-read the unchanged terminal state: this exposes repeated replay
		// work that a reconnecting/multiple-client path currently performs.
		begin = time.Now()
		repeated, repeatErr := late.Snapshot()
		out.Cost.RepeatedRestoreMS = milliseconds(time.Since(begin))
		out.Cost.RepeatedRestoreBytes = memory(false).TotalAlloc - stats.TotalAlloc
		if repeatErr != nil || len(repeated.Texts) != 1 || repeated.Texts[0].Text != p.text {
			return out, fmt.Errorf("repeated snapshot lost text: %v", repeatErr)
		}
		out.Cost.HeapAfterRestore = memory(true).HeapAlloc
		file, statErr := os.Stat(filepath.Join(workspace, "history.sqlite3"))
		if statErr != nil {
			return out, statErr
		}
		out.Cost.DatabaseBytes = file.Size()
	}
	// Core resource shutdown must also be independent of a display write/ACK.
	// The test caller still owns Store, so late terminal restoration can read it.
	begin = time.Now()
	closeDone := make(chan struct{})
	var closeErr error
	go func() { closeErr = server.Close(); close(closeDone) }()
	if err = wait(ctx, closeDone); err != nil {
		return out, fmt.Errorf("Core shutdown waited for stalled SSE: %w", err)
	}
	if closeErr != nil {
		return out, closeErr
	}
	out.CoreCloseMS = milliseconds(time.Since(begin))
	// Release the client after persistence and cancellation/completion finished.
	release()
	if err = wait(ctx, handlerDone); err != nil {
		return out, err
	}
	out.TerminalAfterRelease = strings.Contains(w.body.String(), "event: run.terminal\n")
	if !out.TerminalAfterRelease {
		return out, errors.New("terminal missing after writer release")
	}
	return out, nil
}

func milliseconds(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func main() {
	updates := flag.Int("updates", 128, "simulated cumulative text updates (128 bytes each)")
	cancelRun := flag.Bool("cancel", false, "cancel after streaming instead of completing")
	measureCost := flag.Bool("measure-cost", false, "measure DB size, allocations, retained heap and repeated snapshot replay")
	flag.Parse()
	if *updates < 1 {
		fmt.Fprintln(os.Stderr, "updates must be positive for this experiment")
		os.Exit(1)
	}
	r, err := experiment(*updates, *cancelRun, *measureCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if r.Cost != nil {
		r.Cost.HeapAfterCleanup = memory(true).HeapAlloc
		r.Cost.ProcessPeakRSSBytes, err = peakRSS()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(r)
}
