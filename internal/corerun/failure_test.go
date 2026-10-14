package corerun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
)

func TestCoreSaveFailuresReturnClassifiedTerminalInsteadOfDecodeError(t *testing.T) {
	for _, tc := range []struct{ name, trigger string }{
		{"terminal", `CREATE TRIGGER fail_terminal BEFORE INSERT ON semantic_records WHEN NEW.kind='host_decision' BEGIN SELECT RAISE(ABORT, 'terminal save failed'); END`},
		{"input control", `CREATE TRIGGER fail_control BEFORE INSERT ON execution_control_facts BEGIN SELECT RAISE(ABORT, 'control save failed'); END`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := productManager(t, &observedProvider{}, app.RunOptions{})
			db, err := sql.Open("sqlite", filepath.Join(m.Core().Workspace, "history.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			state, _, err := m.Start(api.StartRunRequest{CoreID: m.Core().CoreID, RunID: uuid.NewString(), Task: "persist failure classification"})
			if err != nil {
				t.Fatal(err)
			}
			state = awaitRun(t, m, state.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
			if state.Result == nil || state.Result.OK || state.Result.Error == nil || state.Result.Error.Diagnostic == nil || state.Result.Error.Diagnostic.Code != contracts.FailureCommit ||
				state.Result.AgentResult == nil || state.Result.AgentResult.Diagnostic == nil || state.Result.AgentResult.Diagnostic.Code != contracts.FailureCommit || !strings.Contains(state.Result.Error.Message, "save failed") {
				t.Fatalf("save failure did not reach Core terminal: %+v", state)
			}
			state.Result.Error.Diagnostic.Code = "mutated"
			state.Result.AgentResult.Diagnostic.Code = "mutated"
			next, err := m.Get(m.Core().CoreID, state.RunID)
			if err != nil || next.Result.Error.Diagnostic.Code != contracts.FailureCommit || next.Result.AgentResult.Diagnostic.Code != contracts.FailureCommit {
				t.Fatalf("caller mutated terminal diagnostics: %+v %v", next, err)
			}
		})
	}
}

type gatedFailureProvider struct{ entered, release chan struct{} }

func (p *gatedFailureProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}
func (p *gatedFailureProvider) Generate(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	close(p.entered)
	select {
	case <-ctx.Done():
		return contracts.ModelResult{}, ctx.Err()
	case <-p.release:
	}
	return contracts.ModelResult{}, contracts.WithFailure(errors.New("provider response fixture failure"), contracts.FailureDiagnostic{Code: contracts.FailureResponse})
}

func TestQueuedReceiptSaveFailureOverridesProviderDiagnostic(t *testing.T) {
	provider := &gatedFailureProvider{entered: make(chan struct{}), release: make(chan struct{})}
	m, _ := productManager(t, provider, app.RunOptions{})
	coreID, runID := m.Core().CoreID, uuid.NewString()
	if _, _, err := m.Start(api.StartRunRequest{CoreID: coreID, RunID: runID, Task: "fail then save follow-up receipt"}); err != nil {
		t.Fatal(err)
	}
	<-provider.entered
	state := awaitRun(t, m, runID, func(s api.RunState) bool { return s.Phase == "running" })
	if _, _, err := m.SubmitInput(context.Background(), runID, api.SubmitInputRequest{CoreID: coreID, SessionID: state.SessionID, InputID: uuid.NewString(), Kind: "follow-up", Text: "queued task"}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(m.Core().Workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_receipt BEFORE UPDATE ON command_receipts WHEN NEW.operation='follow-up' BEGIN SELECT RAISE(ABORT, 'receipt save failed'); END`); err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	state = awaitRun(t, m, runID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if state.Result.Error.Diagnostic == nil || state.Result.Error.Diagnostic.Code != contracts.FailureCommit || !strings.Contains(state.Result.Error.Message, "provider response fixture failure") || !strings.Contains(state.Result.Error.Message, "receipt save failed") {
		t.Fatalf("receipt save failed behind provider error: %+v", state.Result)
	}
}
