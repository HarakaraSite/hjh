package sessionexperiment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"hjh/spikes/s01/internal/configsnapshot"
	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/session"
)

type Config struct {
	Tools, Out, Work, Exe string
	HistoryTurns          int
}
type Case struct {
	Name        string `json:"name"`
	Passed      bool   `json:"passed"`
	Observation any    `json:"observation,omitempty"`
	Error       string `json:"error,omitempty"`
}
type Summary struct {
	Time        time.Time `json:"time"`
	Environment string    `json:"environment"`
	Session     string    `json:"session"`
	Database    string    `json:"database"`
	Cases       []Case    `json:"cases"`
}
type suite struct {
	Config
	prefix, db, id, settings, editable string
	initial                            session.Prepared
	server                             *httptest.Server
	arrived                            chan struct{}
}

func Run(config Config) (Summary, error) {
	summary := Summary{Time: time.Now().UTC(), Environment: runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version(), Database: "history.sqlite"}
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return summary, err
	}
	s := &suite{Config: config, prefix: hex.EncodeToString(token[:]), arrived: make(chan struct{}, 1)}
	s.Work = filepath.Join(config.Work, s.prefix)
	for _, path := range []string{s.Work, s.Out} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return summary, err
		}
	}
	s.db = filepath.Join(s.Out, "history.sqlite")
	s.id = s.prefix + "-main"
	summary.Session = s.id
	s.editable = filepath.Join(s.Work, "editable")
	if err := copyTree(s.Tools, s.editable); err != nil {
		return summary, err
	}
	if err := os.WriteFile(filepath.Join(s.editable, "hook-config.yaml"), []byte("note: prepared\n"), 0644); err != nil {
		return summary, err
	}
	s.settings = filepath.Join(s.Work, "settings.json")
	settings := configsnapshot.Settings{Model: "spike-model-a", Instruction: "initial instruction", Extensions: s.editable, Hooks: "hooks.js"}
	if err := writeJSON(s.settings, settings); err != nil {
		return summary, err
	}
	store, err := open(s.db)
	if err != nil {
		return summary, err
	}
	if err = store.CreateSession(s.id, "S-04 saved session"); err == nil {
		s.initial, err = session.Prepare(store, s.settings, s.Exe, s.Work)
	}
	err = errors.Join(err, store.Close())
	if err != nil {
		return summary, err
	}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			s.arrived <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, "project: hjh-library-spike\nobservation: "+strings.Repeat("x", 8192)+"\ndependencies:\n  - name: accepted\n    range: '^1.2.0'\n    candidate: 1.4.2\n")
	}))
	defer s.server.Close()
	for _, c := range []struct {
		name string
		run  func() (any, error)
	}{
		{"execution_then_conversation_adoption", s.adoption},
		{"failed_cancelled_and_unadopted_attempts", s.mixed},
		{"restart_current_config_and_archived_sources", s.restart},
		{"kill_before_adoption_commit", s.crash},
		{"growing_history_selected_windows_and_pages", s.growing},
	} {
		o, e := c.run()
		item := Case{Name: c.name, Passed: e == nil, Observation: o}
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
	store, err = open(s.db)
	if err == nil {
		err = errors.Join(store.Integrity(), store.Close())
	}
	if err != nil {
		summary.Cases = append(summary.Cases, Case{Name: "database_integrity", Error: err.Error()})
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

func (s *suite) execute(ctx context.Context, name, sessionID, body, url string, p session.Prepared, adopt bool) (session.Result, error) {
	store, err := open(s.db)
	if err != nil {
		return session.Result{}, err
	}
	args := map[string]any{"url": url, "path": filepath.Join(s.Work, name+".report.json")}
	r, runErr := session.Execute(ctx, store, p, session.Request{SessionID: sessionID, TurnID: s.prefix + "-" + name, Text: name, Body: body, Args: args, ContextPairs: 3}, &host.Host{})
	if runErr == nil && adopt {
		err = session.Adopt(store, sessionID, &r, nil)
	}
	err = errors.Join(err, store.Close())
	if err != nil {
		return r, err
	}
	if err = writeJSON(filepath.Join(s.Out, name+"-execution.json"), r); err != nil {
		return r, err
	}
	return r, runErr
}

func (s *suite) normal(name, sessionID string, p session.Prepared, adopt bool) (session.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return s.execute(ctx, name, sessionID, session.GeneratedJS, s.server.URL, p, adopt)
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
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if e.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0644)
	})
}
func replace(path, old, new string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), old) {
		return fmt.Errorf("source edit target absent: %s", path)
	}
	return os.WriteFile(path, []byte(strings.ReplaceAll(string(b), old, new)), 0644)
}
