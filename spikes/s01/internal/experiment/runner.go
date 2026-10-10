// Package experiment exercises the real spike path and writes reproducible observations.
package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/jsruntime"
	"hjh/spikes/s01/internal/trace"
	"modernc.org/quickjs"
)

type Case struct {
	Name        string         `json:"name"`
	Passed      bool           `json:"passed"`
	Observation map[string]any `json:"observation,omitempty"`
	Error       string         `json:"error,omitempty"`
}

type Summary struct {
	Time        time.Time `json:"time"`
	Environment string    `json:"environment"`
	Binding     string    `json:"binding"`
	Engine      string    `json:"engine"`
	Cases       []Case    `json:"cases"`
}

type suite struct {
	out, modules             string
	server                   *httptest.Server
	mu                       sync.Mutex
	waitStarted, waitStopped chan struct{}
	barriers                 map[string]*barrier
}

type barrier struct {
	started, active, max int
	ready                chan struct{}
}

func Run(moduleDir, out string) (Summary, error) {
	summary := Summary{Time: time.Now().UTC(), Environment: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(), Binding: "modernc.org/quickjs v0.25.0", Engine: quickjs.Version()}
	if err := os.MkdirAll(out, 0755); err != nil {
		return summary, err
	}
	s := &suite{out: out, modules: moduleDir, barriers: map[string]*barrier{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.serve))
	defer s.server.Close()
	for _, c := range []struct {
		name string
		run  func() (map[string]any, error)
	}{
		{"result_chain", s.chain},
		{"nested_tools", s.nested},
		{"parallel_http", s.parallel},
		{"js_exception", s.jsException},
		{"tool_exception", s.toolException},
		{"http_status_failure", s.httpStatus},
		{"http_transport_failure", s.httpTransport},
		{"command_failure", s.commandFailure},
		{"partial_failure_evidence", s.partialFailure},
		{"cancel_sync_js", func() (map[string]any, error) { return s.cancelJS(false) }},
		{"cancel_async_js", func() (map[string]any, error) { return s.cancelJS(true) }},
		{"cancel_http", s.cancelHTTP},
		{"cancel_process_group", s.cancelCommand},
		{"external_edit_next_worker", s.edit},
		{"vm_shared_state", s.shared},
		{"vm_parallel_isolation", s.isolation},
	} {
		observation, err := c.run()
		item := Case{Name: c.name, Passed: err == nil, Observation: observation}
		if err != nil {
			item.Error = err.Error()
		}
		summary.Cases = append(summary.Cases, item)
		fmt.Printf("%s: %t", c.name, item.Passed)
		if err != nil {
			fmt.Printf(" (%v)", err)
		}
		fmt.Println()
	}
	b, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return summary, err
	}
	if err = os.WriteFile(filepath.Join(out, "summary.json"), append(b, '\n'), 0644); err != nil {
		return summary, err
	}
	for _, c := range summary.Cases {
		if !c.Passed {
			err = errors.Join(err, fmt.Errorf("%s: %s", c.Name, c.Error))
		}
	}
	return summary, err
}

func (s *suite) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/data":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"values":["red","blue"]}`)
	case "/status":
		w.WriteHeader(503)
		fmt.Fprint(w, "service unavailable")
	case "/wait":
		s.mu.Lock()
		started, stopped := s.waitStarted, s.waitStopped
		s.mu.Unlock()
		close(started)
		<-r.Context().Done()
		close(stopped)
	case "/paired":
		group := r.URL.Query().Get("group")
		s.mu.Lock()
		b := s.barriers[group]
		if b == nil {
			b = &barrier{ready: make(chan struct{})}
			s.barriers[group] = b
		}
		b.started++
		b.active++
		if b.active > b.max {
			b.max = b.active
		}
		if b.started == 2 {
			close(b.ready)
		}
		s.mu.Unlock()
		select {
		case <-b.ready:
			fmt.Fprint(w, "paired")
		case <-r.Context().Done():
		}
		s.mu.Lock()
		b.active--
		s.mu.Unlock()
	default:
		w.WriteHeader(404)
	}
}

func (s *suite) maxParallel(group string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.barriers[group] == nil {
		return 0
	}
	return s.barriers[group].max
}

func (s *suite) worker(ctx context.Context, name, modules, body string, input any, h *host.Host, observe func(trace.Event)) (jsruntime.Result, []trace.Event, error) {
	path := filepath.Join(s.out, name+".jsonl")
	rec, err := trace.New(path)
	if err != nil {
		return jsruntime.Result{}, nil, err
	}
	rec.Observe = observe
	result, runErr := jsruntime.Run(ctx, modules, body, input, h, rec)
	closeErr := rec.Close()
	events, readErr := trace.Read(path)
	// Recording and readback failures must not be mistaken for an expected JS error.
	if err = errors.Join(closeErr, readErr, validateEvidence(events)); err != nil {
		return result, events, fmt.Errorf("evidence: %w", err)
	}
	return result, events, runErr
}

func (s *suite) ordinary(name, body string, input any) (jsruntime.Result, []trace.Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.worker(ctx, name, s.modules, body, input, &host.Host{}, nil)
}

func validateEvidence(events []trace.Event) error {
	started := map[string]trace.Event{}
	ended := map[string]bool{}
	for i, e := range events {
		if e.Sequence != i+1 {
			return fmt.Errorf("out-of-order sequence %d", e.Sequence)
		}
		if e.Phase == "started" {
			if e.Parent != "" {
				if _, ok := started[e.Parent]; !ok {
					return fmt.Errorf("missing parent %s", e.Parent)
				}
			}
			started[e.ID] = e
		} else {
			if _, ok := started[e.ID]; !ok {
				return fmt.Errorf("end without start: %s", e.ID)
			}
			ended[e.ID] = true
		}
	}
	if len(started) != len(ended) {
		return fmt.Errorf("unfinished calls: started %d, ended %d", len(started), len(ended))
	}
	return nil
}

func observed(result jsruntime.Result, events []trace.Event) map[string]any {
	return map[string]any{"worker": result, "readbackEvents": len(events)}
}

func require(condition bool, message string) error {
	if !condition {
		return errors.New(message)
	}
	return nil
}

func expectError(result jsruntime.Result, events []trace.Event, err error, contains string) (map[string]any, error) {
	obs := observed(result, events)
	if err == nil {
		return obs, errors.New("expected error, got success")
	}
	if strings.Contains(err.Error(), "evidence:") {
		return obs, err
	}
	return obs, require(strings.Contains(err.Error(), contains), fmt.Sprintf("error does not contain %q: %v", contains, err))
}

func decode[T any](result jsruntime.Result) (T, error) {
	var value T
	err := json.Unmarshal(result.Value, &value)
	return value, err
}
