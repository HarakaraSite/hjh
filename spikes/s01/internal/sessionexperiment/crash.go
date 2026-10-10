package sessionexperiment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (s *suite) crash() (any, error) {
	baseline, err := s.inspect("crash_baseline", s.id, 0, 0, 3)
	if err != nil {
		return nil, err
	}
	gatePath := filepath.Join(s.Out, s.prefix+"-adoption.gate.json")
	cmd := s.workerCommand("crash_adoption", gatePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
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
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	var gate WorkerOutput
	for {
		select {
		case exit := <-done:
			waited = true
			return nil, fmt.Errorf("writer stopped before adoption gate: %v: %s", exit, stderr.String())
		case <-deadline.C:
			return nil, errors.New("adoption gate was not reached")
		case <-ticker.C:
			b, e := os.ReadFile(gatePath)
			if e != nil || json.Unmarshal(b, &gate) != nil {
				continue
			}
			goto ready
		}
	}
ready:
	live, err := s.inspect("crash_live", s.id, 0, baseline.Info.Turns, 3)
	if err != nil {
		return nil, err
	}
	if err = cmd.Process.Kill(); err != nil {
		return nil, err
	}
	exit := <-done
	waited = true
	if exit == nil || stderr.Len() != 0 {
		return nil, fmt.Errorf("unexpected crash writer result: %v: %s", exit, stderr.String())
	}
	stopped, err := s.inspect("crash_stopped", s.id, 0, baseline.Info.Turns, 3)
	if err != nil {
		return nil, err
	}
	store, err := open(s.db)
	if err != nil {
		return nil, err
	}
	evidence, readErr := store.Read(gate.Result.Run)
	err = errors.Join(readErr, store.Close())
	if err != nil {
		return nil, err
	}
	if err = writeJSON(filepath.Join(s.Out, "crash_execution_evidence.json"), evidence); err != nil {
		return nil, err
	}
	return map[string]any{"writerPID": gate.PID, "exit": exit.Error(), "turn": gate.Result.Turn, "gateFile": filepath.Base(gatePath), "live": "crash_live-inspection.json", "stopped": "crash_stopped-inspection.json", "evidence": "crash_execution_evidence.json", "adoptedBefore": baseline.Info.Adopted, "adoptedWhileTransactionOpen": live.Info.Adopted, "adoptedAfterStop": stopped.Info.Adopted, "executionStatus": evidence.Status}, check(live.Info.Adopted == baseline.Info.Adopted && stopped.Info.Adopted == baseline.Info.Adopted && len(live.Turns) == 1 && len(stopped.Turns) == 1 && stopped.Turns[0].ID == gate.Result.Turn && stopped.Turns[0].Outcome == "completed" && stopped.Turns[0].Response != "" && !stopped.Turns[0].Adopted && evidence.Status == "completed" && stopped.Conversation.RowsDecoded == 3, "uncommitted adoption became conversation, or completed execution/draft was lost")
}
