// Package historyexperiment observes S-02 against real SQLite and separate processes.
package historyexperiment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"hjh/spikes/s01/internal/history"
	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/jsruntime"
	"hjh/spikes/s01/internal/trace"
)

type Case struct {
	Name        string         `json:"name"`
	Passed      bool           `json:"passed"`
	Observation map[string]any `json:"observation,omitempty"`
	Error       string         `json:"error,omitempty"`
}
type Summary struct {
	Time        time.Time        `json:"time"`
	Environment string           `json:"environment"`
	Driver      string           `json:"driver"`
	Settings    history.Settings `json:"settings"`
	Database    string           `json:"database"`
	Cases       []Case           `json:"cases"`
}

type suite struct {
	modules, out, db, exe, prefix string
	server                        *httptest.Server
}

func Run(modules, out, exe string) (Summary, error) {
	summary := Summary{Time: time.Now().UTC(), Environment: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(), Driver: "modernc.org/sqlite v1.60.1", Database: "history.sqlite"}
	if err := os.MkdirAll(out, 0755); err != nil {
		return summary, err
	}
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return summary, err
	}
	s := &suite{modules: modules, out: out, db: filepath.Join(out, summary.Database), exe: exe, prefix: hex.EncodeToString(token[:])}
	store, err := history.Open(s.db)
	if err != nil {
		return summary, err
	}
	summary.Settings, err = store.Settings()
	if closeErr := store.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	if err != nil {
		return summary, err
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"values":["red","blue"]}`)
	}))
	defer s.server.Close()
	for _, c := range []struct {
		name string
		run  func() (map[string]any, error)
	}{
		{"success_nested_aggregate", s.success},
		{"failure_after_effect", s.failure},
		{"command_failure_details", s.commandFailure},
		{"cancel_after_effect", s.cancel},
		{"parallel_workers_same_database", s.parallel},
		{"kill_after_committed_tool", func() (map[string]any, error) { return s.crash("committed") }},
		{"kill_before_host_commit", func() (map[string]any, error) { return s.crash("uncommitted") }},
	} {
		obs, e := c.run()
		item := Case{Name: c.name, Passed: e == nil, Observation: obs}
		if e != nil {
			item.Error = e.Error()
		}
		summary.Cases = append(summary.Cases, item)
		fmt.Printf("%s: %t", c.name, item.Passed)
		if e != nil {
			fmt.Printf(" (%v)", e)
		}
		fmt.Println()
	}
	store, err = history.Open(s.db)
	if err == nil {
		err = errors.Join(store.Integrity(), store.Close())
	}
	if err != nil {
		summary.Cases = append(summary.Cases, Case{Name: "database_integrity", Error: err.Error()})
	}
	if err = writeJSON(filepath.Join(out, "summary.json"), summary); err != nil {
		return summary, err
	}
	for _, c := range summary.Cases {
		if !c.Passed {
			err = errors.Join(err, fmt.Errorf("%s: %s", c.Name, c.Error))
		}
	}
	return summary, err
}

func (s *suite) runID(name string) string { return s.prefix + "-" + name }

func (s *suite) execute(ctx context.Context, name, body string, input any, h *host.Host, observe func(trace.Event)) (jsruntime.Result, history.View, error) {
	run := s.runID(name)
	store, err := history.Open(s.db)
	if err != nil {
		return jsruntime.Result{}, history.View{}, err
	}
	if err = store.Register(run); err != nil {
		store.Close()
		return jsruntime.Result{}, history.View{}, err
	}
	rec := trace.NewWithSink(store.Sink(run, nil))
	rec.Observe = observe
	result, runErr := jsruntime.Run(ctx, s.modules, body, input, h, rec)
	if err = errors.Join(rec.Close(), store.Close()); err != nil {
		return result, history.View{}, fmt.Errorf("persist: %w", err)
	}
	v, err := s.inspect(run, name+"-view.json")
	if err != nil {
		return result, v, fmt.Errorf("readback: %w", err)
	}
	return result, v, runErr
}

// inspect uses the same executable in a new process with a read-only connection.
func (s *suite) inspect(run, file string) (history.View, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.exe, "-inspect", s.db, "-run", run)
	b, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return history.View{}, fmt.Errorf("reader: %w: %s", err, exit.Stderr)
		}
		return history.View{}, err
	}
	var v history.View
	if err = json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	if v.Run != run {
		return v, fmt.Errorf("reader returned wrong run %s", v.Run)
	}
	return v, os.WriteFile(filepath.Join(s.out, file), b, 0644)
}

func Inspect(path, run string) (history.View, error) {
	store, err := history.OpenReadOnly(path)
	if err != nil {
		return history.View{}, err
	}
	v, readErr := store.Read(run)
	return v, errors.Join(readErr, store.Close())
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

func check(condition bool, message string) error {
	if !condition {
		return errors.New(message)
	}
	return nil
}
func find(v history.View, kind, name string) (history.Call, bool) {
	for _, c := range v.Calls {
		if c.Kind == kind && c.Name == name {
			return c, true
		}
	}
	return history.Call{}, false
}
func obs(r jsruntime.Result, v history.View, file string) map[string]any {
	return map[string]any{"run": v.Run, "worker": r, "status": v.Status, "eventCount": len(v.Events), "readbackFile": file, "reader": "separate process, read-only"}
}

func decode[T any](raw json.RawMessage) (T, error) {
	var value T
	err := json.Unmarshal(raw, &value)
	return value, err
}
