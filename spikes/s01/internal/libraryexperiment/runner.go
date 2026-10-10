// Package libraryexperiment observes external libraries, hooks and prepared distribution.
package libraryexperiment

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
	"path/filepath"
	"runtime"
	"time"

	"hjh/spikes/s01/internal/history"
	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/jsruntime"
	"hjh/spikes/s01/internal/trace"
)

const manifestYAML = `project: hjh-library-spike
baseline: &baseline "^1.2.0"
dependencies:
  - name: accepted
    range: *baseline
    candidate: 1.4.2
  - name: excluded
    range: *baseline
    candidate: 2.0.0
  - name: prerelease
    range: ">=2.1.0-beta.1 <2.1.0"
    candidate: 2.1.0-beta.2
`

const generatedJS = `const w = await tools.workflow(input); return {report:w.report, savedBytes:w.savedBytes, names:w.report.compatible.map(dep => dep.name)};`

type Config struct{ Tools, Out, Packages, Exe string }
type Case struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Observation any    `json:"observation,omitempty"`
	Error       string `json:"error,omitempty"`
}
type Summary struct {
	Time         time.Time       `json:"time"`
	Environment  string          `json:"environment"`
	Dependencies json.RawMessage `json:"dependencies"`
	Cases        []Case          `json:"cases"`
}
type Observation struct {
	Worker  jsruntime.Result `json:"worker"`
	History history.View     `json:"history"`
}
type Report struct {
	Project    string `json:"project"`
	Compatible []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"compatible"`
	Note         string `json:"note"`
	ToolRevision string `json:"toolRevision"`
	HookRevision string `json:"hookRevision"`
}
type Value struct {
	Report     Report   `json:"report"`
	SavedBytes int      `json:"savedBytes"`
	Names      []string `json:"names"`
}
type suite struct {
	Config
	prefix, db, hookConfig string
	server                 *httptest.Server
}

func Run(config Config) (Summary, error) {
	summary := Summary{Time: time.Now().UTC(), Environment: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version()}
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return summary, err
	}
	s := &suite{Config: config, prefix: hex.EncodeToString(token[:])}
	if err := os.MkdirAll(s.Out, 0755); err != nil {
		return summary, err
	}
	s.db = filepath.Join(s.Out, "history.sqlite")
	s.hookConfig = filepath.Join(s.Out, s.prefix+"-hook-config.yaml")
	if err := os.WriteFile(s.hookConfig, []byte("note: prepared\n"), 0644); err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(s.Out, "manifest.yaml"), []byte(manifestYAML), 0644); err != nil {
		return summary, err
	}
	data, err := os.ReadFile(filepath.Join(s.Tools, "vendor/manifest.json"))
	if err != nil {
		return summary, fmt.Errorf("prepare dependencies first: %w", err)
	}
	summary.Dependencies = data
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		if r.URL.Path == "/invalid" {
			fmt.Fprint(w, "project: [unterminated")
			return
		}
		fmt.Fprint(w, manifestYAML)
	}))
	defer s.server.Close()
	for _, c := range []struct {
		name string
		run  func() (any, error)
	}{
		{"libraries_http_file_nested_hooks", s.normal},
		{"tool_and_hook_edit_next_worker", s.edits},
		{"yaml_library_error", s.yamlError},
		{"hook_error", s.hookError},
		{"runtime_and_module_capabilities", s.capabilities},
		{"prepared_distribution_without_node", s.distribution},
	} {
		observation, e := c.run()
		item := Case{Name: c.name, Passed: e == nil, Observation: observation}
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
	if err = writeJSON(filepath.Join(s.Out, "summary.json"), summary); err != nil {
		return summary, err
	}
	for _, c := range summary.Cases {
		if !c.Passed {
			err = errors.Join(err, fmt.Errorf("%s: %s", c.Name, c.Error))
		}
	}
	return summary, err
}

func (s *suite) input(name string) map[string]any {
	return map[string]any{"url": s.server.URL, "path": filepath.Join(s.Out, s.prefix+"-"+name+".report.json"), "hookConfigPath": s.hookConfig}
}

func Execute(db, run, modules, hooks, body string, input any) (Observation, error) {
	var o Observation
	store, err := history.Open(db)
	if err != nil {
		return o, err
	}
	if err = store.Register(run); err != nil {
		store.Close()
		return o, err
	}
	rec := trace.NewWithSink(store.Sink(run, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	o.Worker, err = jsruntime.RunWithOptions(ctx, modules, body, input, &host.Host{}, rec, jsruntime.Options{Hooks: hooks})
	if saveErr := errors.Join(rec.Close(), store.Close()); saveErr != nil {
		return o, fmt.Errorf("persist: %w", saveErr)
	}
	reader, readErr := history.OpenReadOnly(db)
	if readErr != nil {
		return o, readErr
	}
	o.History, readErr = reader.Read(run)
	readErr = errors.Join(readErr, reader.Close())
	if readErr != nil {
		return o, readErr
	}
	return o, err
}

func (s *suite) execute(name, modules, hooks, body string, input any) (Observation, error) {
	o, runErr := Execute(s.db, s.prefix+"-"+name, modules, hooks, body, input)
	if err := writeJSON(filepath.Join(s.Out, name+"-view.json"), o); err != nil {
		return o, err
	}
	return o, runErr
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
func check(ok bool, message string) error {
	if !ok {
		return errors.New(message)
	}
	return nil
}
func find(o Observation, kind, name string) (history.Call, bool) {
	for _, c := range o.History.Calls {
		if c.Kind == kind && c.Name == name {
			return c, true
		}
	}
	return history.Call{}, false
}
