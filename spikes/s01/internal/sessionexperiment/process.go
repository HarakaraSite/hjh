package sessionexperiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/session"
)

type WorkerConfig struct {
	DB, Session, Turn, Settings, Exe, Work, URL, Path, Gate string
	ContextPairs                                            int
}
type WorkerOutput struct {
	PID    int            `json:"pid"`
	Result session.Result `json:"result"`
}

func Worker(config WorkerConfig) (WorkerOutput, error) {
	output := WorkerOutput{PID: os.Getpid()}
	store, err := open(config.DB)
	if err != nil {
		return output, err
	}
	defer store.Close()
	prepared, err := session.Prepare(store, config.Settings, config.Exe, config.Work)
	if err != nil {
		return output, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	output.Result, err = session.Execute(ctx, store, prepared, session.Request{SessionID: config.Session, TurnID: config.Turn, Text: filepath.Base(config.Path), Body: session.GeneratedJS, Args: map[string]any{"url": config.URL, "path": config.Path}, ContextPairs: config.ContextPairs}, &host.Host{})
	if err != nil {
		return output, err
	}
	var gate func()
	if config.Gate != "" {
		gate = func() {
			if err := writeJSON(config.Gate, output); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			select {}
		}
	}
	err = session.Adopt(store, config.Session, &output.Result, gate)
	return output, err
}

func (s *suite) inspect(name, sessionID string, before, after, count int) (Inspection, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Exe, "-inspect", "-db", s.db, "-session", sessionID, "-before", fmt.Sprint(before), "-after", fmt.Sprint(after), "-count", fmt.Sprint(count))
	b, err := cmd.Output()
	if err != nil {
		return Inspection{}, commandError(err)
	}
	var v Inspection
	if err = json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	return v, os.WriteFile(filepath.Join(s.Out, name+"-inspection.json"), b, 0644)
}

func (s *suite) workerCommand(name, gate string) *exec.Cmd {
	return exec.Command(s.Exe, "-worker", "-db", s.db, "-session", s.id, "-turn", s.prefix+"-"+name, "-settings", s.settings, "-work", s.Work, "-url", s.server.URL, "-path", filepath.Join(s.Work, name+".report.json"), "-gate", gate, "-count", "3")
}
func (s *suite) runWorker(name string) (WorkerOutput, error) {
	cmd := s.workerCommand(name, "")
	b, err := cmd.Output()
	if err != nil {
		return WorkerOutput{}, commandError(err)
	}
	var output WorkerOutput
	if err = json.Unmarshal(b, &output); err != nil {
		return output, err
	}
	return output, os.WriteFile(filepath.Join(s.Out, name+"-worker.json"), b, 0644)
}
func commandError(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("child process: %w: %s", err, exit.Stderr)
	}
	return err
}
