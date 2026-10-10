package historyexperiment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hjh/spikes/s01/internal/history"
	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/jsruntime"
	"hjh/spikes/s01/internal/trace"
)

type Gate struct {
	Stage string      `json:"stage"`
	Event trace.Event `json:"attemptedEvent"`
}

// Child is a real JS/Go/SQLite writer. Fault injection blocks at the selected save boundary.
func Child(db, run, modules, stage, effect, ready string) error {
	store, err := history.Open(db)
	if err != nil {
		return err
	}
	defer store.Close()
	if err = store.Register(run); err != nil {
		return err
	}
	gate := func(e trace.Event) {
		if err := writeJSON(ready, Gate{Stage: stage, Event: e}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		select {} // Parent waits for this gate and SIGKILLs this owned process.
	}
	var beforeCommit func(trace.Event)
	if stage == "uncommitted" {
		beforeCommit = func(e trace.Event) {
			if e.Kind == "host" && e.Name == "command" && e.Phase == "completed" {
				gate(e)
			}
		}
	}
	rec := trace.NewWithSink(store.Sink(run, beforeCommit))
	if stage == "committed" {
		rec.Observe = func(e trace.Event) {
			if e.Kind == "tool" && e.Name == "command" && e.Phase == "completed" {
				gate(e)
			}
		}
	}
	_, runErr := jsruntime.Run(context.Background(), modules, `await tools.command({program:"sh",args:["-c",'printf crash-effect > "$1"; printf command-finished',"effect",input.path]}); return "parent-finished";`, map[string]any{"path": effect}, &host.Host{}, rec)
	return errors.Join(runErr, rec.Close(), errors.New("writer did not reach selected termination gate"))
}

func (s *suite) crash(stage string) (map[string]any, error) {
	name := "crash_" + stage
	run := s.runID(name)
	effectName := run + ".effect.txt"
	effect := filepath.Join(s.out, effectName)
	ready := filepath.Join(s.out, run+".gate.json")
	cmd := exec.Command(s.exe, "-child", stage, "-db", s.db, "-run", run, "-tools", s.modules, "-effect", effect, "-ready", ready)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	defer func() {
		if !waited {
			cmd.Process.Kill()
			<-done
		}
	}()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	var gate Gate
	for {
		select {
		case exit := <-done:
			waited = true
			return nil, fmt.Errorf("writer exited before gate: %v: %s", exit, stderr.String())
		case <-deadline.C:
			return nil, errors.New("writer gate was not observed")
		case <-ticker.C:
			b, err := os.ReadFile(ready)
			if err != nil {
				continue
			}
			if json.Unmarshal(b, &gate) != nil {
				continue
			}
			goto readyObserved
		}
	}
readyObserved:
	before, err := s.inspect(run, name+"-live-view.json")
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(effect)
	if err != nil {
		return nil, err
	}
	if err = cmd.Process.Kill(); err != nil {
		return nil, err
	}
	exit := <-done
	waited = true
	if exit == nil {
		return nil, errors.New("expected SIGKILL writer exit")
	}
	if stderr.Len() != 0 {
		return nil, fmt.Errorf("writer stderr before termination: %s", stderr.String())
	}
	afterStop, err := s.inspect(run, name+"-stopped-view.json")
	if err != nil {
		return nil, err
	}
	store, err := history.Open(s.db)
	if err != nil {
		return nil, err
	}
	reason := fmt.Sprintf("experiment observed writer PID %d exit (%v); Wait completed", cmd.Process.Pid, exit)
	err = errors.Join(store.ReconcileStopped(run, reason), store.Close())
	if err != nil {
		return nil, err
	}
	resolved, err := s.inspect(run, name+"-resolved-view.json")
	if err != nil {
		return nil, err
	}
	o := map[string]any{"run": run, "stage": gate.Stage, "writerPID": cmd.Process.Pid, "writerExit": exit.Error(), "sideEffectReadback": string(content), "sideEffectFile": effectName, "liveStatus": before.Status, "stoppedBeforeReconcileStatus": afterStop.Status, "resolvedStatus": resolved.Status, "liveEventCount": len(before.Events), "stoppedEventCount": len(afterStop.Events), "resolvedEventCount": len(resolved.Events), "readbackFiles": []string{name + "-live-view.json", name + "-stopped-view.json", name + "-resolved-view.json"}}
	if err = check(string(content) == "crash-effect" && before.Status == "pending" && afterStop.Status == "pending" && resolved.Status == "interrupted" && len(before.Events) == len(afterStop.Events), "side effect, read-only behavior, or interruption readback mismatch"); err != nil {
		return o, err
	}
	goBefore, ok := find(afterStop, "host", "command")
	goAfter, okAfter := find(resolved, "host", "command")
	toolAfter, okTool := find(resolved, "tool", "command")
	if !ok || !okAfter || !okTool {
		return o, errors.New("internal command record missing")
	}
	if stage == "committed" {
		detail, err := decode[host.CommandResult](goAfter.Result)
		if err != nil {
			return o, err
		}
		return o, check(goBefore.Status == "completed" && goAfter.Status == "completed" && toolAfter.Status == "completed" && detail.Stdout == "command-finished" && detail.ExitCode == 0 && len(resolved.Events) == len(afterStop.Events)+1, "committed command evidence was lost or overwritten during reconciliation")
	}
	// INSERT succeeded in the child, but was never committed. The real side effect
	// exists; readers must not invent a stored successful command result.
	for _, e := range afterStop.Events {
		if e.Kind == "host" && e.Phase == "completed" {
			return o, errors.New("uncommitted result became visible")
		}
	}
	return o, check(gate.Event.Phase == "completed" && goBefore.Status == "pending" && goAfter.Status == "outcome_unknown" && toolAfter.Status == "outcome_unknown" && len(goAfter.Result) == 0 && len(resolved.Events) == len(afterStop.Events)+3 && strings.Contains(goAfter.Error, "Wait completed"), "unknown operation outcome was incorrectly inferred after killed transaction")
}
