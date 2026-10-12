package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/history"
)

func TestSteerChildDuringToolAndFinalResponsePersistsOrderedInstructions(t *testing.T) {
	store, workspace := delegationStore(t)
	commandRelease := filepath.Join(workspace, "release-command")
	finalObserved := filepath.Join(workspace, "child-final-observed")
	finalRelease := make(chan struct{})
	commandStarted := make(chan struct{})
	var commandStartedOnce, commandReleaseOnce, finalReleaseOnce sync.Once
	var steerCompletions atomic.Int32
	var parentStep, childStep atomic.Int32
	var sawBothInstructions, sawAllInstructions atomic.Bool

	finalPathJSON, err := json.Marshal(finalObserved)
	if err != nil {
		t.Fatal(err)
	}
	parentScript := `// Steer the active child and check its saved instructions
const childId = globalThis.childExecutionId;
const first = await tools.steer_agent({executionId:childId,text:"First: include the deadline."});
const second = await tools.steer_agent({executionId:childId,text:"Second: identify the owner."});
await tools.command({program:"sh",args:["-c","while [ ! -f \"$1\" ]; do sleep 0.02; done; printf ready","sh",` + string(finalPathJSON) + `]});
const last = await tools.steer_agent({executionId:childId,text:"Third: state the next step."});
const result = await tools.collect_agent({executionId:childId});
const status = await tools.agent_status({executionId:childId});
return {first,second,last,result,status};`

	provider := delegationProvider(func(ctx context.Context, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		task := request.Messages[0].VisibleText()
		switch task {
		case "steer parent":
			switch parentStep.Add(1) {
			case 1:
				return scriptedJS(`const child=await tools.spawn_agent({task:"steer child",tools:["command"]}); globalThis.childExecutionId=child.executionId; return child;`), nil
			case 2:
				select {
				case <-commandStarted:
				case <-ctx.Done():
					return contracts.ModelResult{}, ctx.Err()
				}
				return scriptedJS(parentScript), nil
			default:
				body, _ := json.Marshal(request.Messages)
				if !strings.Contains(string(body), "child followed all three instructions") {
					t.Error("parent's next request omitted the collected child result")
				}
				if !strings.Contains(string(body), `\"status\":\"applied\"`) {
					t.Errorf("parent's next request omitted applied instruction receipts: %s", body)
				}
				return contracts.ModelResult{Text: "parent verified child continuation"}, nil
			}
		case "steer child":
			switch childStep.Add(1) {
			case 1:
				return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "child-command", Name: "command",
					Arguments: contracts.JSON(map[string]any{"program": "sh", "args": []string{"-c",
						`while [ ! -f "$1" ]; do sleep 0.02; done; printf child-tool-completed`, "sh", commandRelease}})}}}, nil
			case 2:
				texts := steeringTexts(request.Messages)
				if strings.Join(texts, "|") == "First: include the deadline.|Second: identify the owner." {
					sawBothInstructions.Store(true)
				} else {
					t.Errorf("next child request received instructions out of order: %q", texts)
				}
				if observe != nil {
					if err := observe(contracts.Observation{Kind: "model_result", Name: "child-pre-final", Text: "child preliminary final", ModelStep: 2}); err != nil {
						return contracts.ModelResult{}, err
					}
				}
				select {
				case <-finalRelease:
				case <-ctx.Done():
					return contracts.ModelResult{}, ctx.Err()
				}
				return contracts.ModelResult{Text: "child preliminary final"}, nil
			default:
				texts := steeringTexts(request.Messages)
				if strings.Join(texts, "|") == "First: include the deadline.|Second: identify the owner.|Third: state the next step." {
					sawAllInstructions.Store(true)
				} else {
					t.Errorf("final-boundary instruction was not appended in order: %q", texts)
				}
				return contracts.ModelResult{Text: "child followed all three instructions"}, nil
			}
		default:
			return contracts.ModelResult{}, context.Canceled
		}
	})

	owner := executiondata.NewOwner()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	defer func() { _ = os.WriteFile(commandRelease, []byte("release"), 0644) }()
	result, err := Run(ctx, store, provider, RunOptions{Task: "steer parent", Workspace: workspace, ModelID: "test/model",
		ExecutionDataOwner: owner,
		Progress: func(observation contracts.Observation) error {
			if observation.Kind == "model_tool" && observation.Name == "command" && observation.Phase == "started" {
				commandStartedOnce.Do(func() { close(commandStarted) })
			}
			if observation.Kind == "tool" && observation.Name == "steer_agent" && observation.Phase == "completed" {
				switch steerCompletions.Add(1) {
				case 2:
					commandReleaseOnce.Do(func() { _ = os.WriteFile(commandRelease, []byte("release"), 0644) })
				case 3:
					finalReleaseOnce.Do(func() { close(finalRelease) })
				}
			}
			if observation.Kind == "model_result" && observation.Name == "child-pre-final" {
				_ = os.WriteFile(finalObserved, []byte("ready"), 0644)
			}
			return nil
		}})
	if err != nil || !result.Result.OK {
		t.Fatalf("run=%+v err=%v", result, err)
	}
	if !sawBothInstructions.Load() || !sawAllInstructions.Load() {
		t.Fatalf("child did not receive steering at both boundaries: duringTool=%v finalBoundary=%v", sawBothInstructions.Load(), sawAllInstructions.Load())
	}
	children, err := store.Children(ctx, result.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children=%+v err=%v", children, err)
	}
	child := children[0]
	if child.Outcome != "completed" {
		t.Fatalf("child outcome=%s, want completed", child.Outcome)
	}

	messages, err := store.Messages(ctx, child.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var childMessageTexts []string
	var completedCommand bool
	for _, saved := range messages {
		var message contracts.Message
		if err := json.Unmarshal(saved.JSON, &message); err != nil {
			t.Fatal(err)
		}
		if message.Steering != nil && *message.Steering {
			childMessageTexts = append(childMessageTexts, message.VisibleText())
		}
		if message.Role == "tool" && strings.Contains(string(saved.JSON), "child-tool-completed") {
			completedCommand = true
		}
	}
	if strings.Join(childMessageTexts, "|") != "First: include the deadline.|Second: identify the owner.|Third: state the next step." || !completedCommand {
		t.Fatalf("child saved messages omit ordered guidance or the completed active tool: steering=%q command=%v", childMessageTexts, completedCommand)
	}

	records, err := store.Records(ctx, child.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	accepted, applied := map[uint64]contracts.ChildInstructionReceipt{}, map[uint64]contracts.ChildInstructionReceipt{}
	for _, record := range records {
		if record.Kind != "control_decision" {
			continue
		}
		var decision struct {
			Operation string                            `json:"operation"`
			Decision  string                            `json:"decision"`
			Receipt   contracts.ChildInstructionReceipt `json:"receipt"`
		}
		if err := json.Unmarshal(record.Payload, &decision); err != nil || decision.Operation != "agent_instruction" {
			continue
		}
		switch decision.Decision {
		case "accepted":
			var message contracts.Message
			if err := json.Unmarshal(record.Content, &message); err != nil || message.VisibleText() == "" {
				t.Fatalf("accepted instruction omitted its source body: content=%s err=%v", record.Content, err)
			}
			accepted[decision.Receipt.Sequence] = decision.Receipt
		case "applied":
			applied[decision.Receipt.Sequence] = decision.Receipt
		}
	}
	if len(accepted) != 3 || len(applied) != 3 {
		t.Fatalf("saved instruction decisions accepted=%+v applied=%+v", accepted, applied)
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		acceptedReceipt, hasAccepted := accepted[sequence]
		appliedReceipt, hasApplied := applied[sequence]
		if !hasAccepted || !hasApplied || acceptedReceipt.InstructionID != appliedReceipt.InstructionID ||
			acceptedReceipt.ContentDigest != appliedReceipt.ContentDigest || appliedReceipt.AppliedMessageOrdinal == nil {
			t.Fatalf("sequence %d receipt transition does not match: accepted=%+v applied=%+v", sequence, acceptedReceipt, appliedReceipt)
		}
	}
}

func TestSteerCancelledChildPersistsUnappliedReasonBeforeSettlement(t *testing.T) {
	store, workspace := delegationStore(t)
	commandStarted := make(chan struct{})
	var commandStartedOnce sync.Once
	var parentStep atomic.Int32
	provider := delegationProvider(func(ctx context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		switch request.Messages[0].VisibleText() {
		case "cancel steer parent":
			switch parentStep.Add(1) {
			case 1:
				return scriptedJS(`const child=await tools.spawn_agent({task:"cancel steer child",tools:["command"]}); globalThis.childExecutionId=child.executionId; return child;`), nil
			case 2:
				select {
				case <-commandStarted:
				case <-ctx.Done():
					return contracts.ModelResult{}, ctx.Err()
				}
				return scriptedJS(`const childId=globalThis.childExecutionId;
const accepted=await tools.steer_agent({executionId:childId,text:"Save this before cancellation."});
const cancelled=await tools.cancel_agent({executionId:childId});
const status=await tools.agent_status({executionId:childId});
return {accepted,cancelled,status};`), nil
			default:
				body, _ := json.Marshal(request.Messages)
				if !strings.Contains(string(body), `\"status\":\"unapplied\"`) || !strings.Contains(string(body), `\"reason\":\"cancelled\"`) {
					t.Errorf("parent's next request omitted the saved unapplied cancellation receipt: %s", body)
				}
				return contracts.ModelResult{Text: "parent observed unapplied instruction"}, nil
			}
		case "cancel steer child":
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "blocking-command", Name: "command",
				Arguments: contracts.JSON(map[string]any{"program": "sh", "args": []string{"-c",
					`while :; do sleep 1; done`, "sh"}})}}}, nil
		default:
			return contracts.ModelResult{}, context.Canceled
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "cancel steer parent", Workspace: workspace, ModelID: "test/model",
		Progress: func(observation contracts.Observation) error {
			if observation.Kind == "model_tool" && observation.Name == "command" && observation.Phase == "started" {
				commandStartedOnce.Do(func() { close(commandStarted) })
			}
			return nil
		}})
	if err != nil || !result.Result.OK {
		t.Fatalf("run=%+v err=%v", result, err)
	}
	children, err := store.Children(ctx, result.ExecutionID)
	if err != nil || len(children) != 1 || children[0].Outcome != "cancelled" {
		t.Fatalf("children=%+v err=%v", children, err)
	}
	statusRecords, err := store.Records(ctx, result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var savedStatus contracts.AgentStatus
	for _, record := range statusRecords {
		var observation contracts.Observation
		if json.Unmarshal(record.Payload, &observation) != nil || observation.Kind != "tool" || observation.Name != "agent_status" || observation.Phase != "completed" {
			continue
		}
		_ = json.Unmarshal(observation.Result, &savedStatus)
	}
	if len(savedStatus.Instructions) != 1 || savedStatus.Instructions[0].Status != "unapplied" ||
		savedStatus.Instructions[0].Reason != "cancelled" || savedStatus.Instructions[0].ContentDigest == "" {
		t.Fatalf("status did not return saved unapplied receipt: %+v", savedStatus.Instructions)
	}
	childRecords, err := store.Records(ctx, children[0].ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var foundUnapplied bool
	for _, record := range childRecords {
		var decision struct {
			Operation string                            `json:"operation"`
			Decision  string                            `json:"decision"`
			Receipt   contracts.ChildInstructionReceipt `json:"receipt"`
		}
		if record.Kind == "control_decision" && json.Unmarshal(record.Payload, &decision) == nil &&
			decision.Operation == "agent_instruction" && decision.Decision == "unapplied" && decision.Receipt.Reason == "cancelled" {
			foundUnapplied = true
		}
	}
	if !foundUnapplied {
		t.Fatal("child history did not persist the cancelled instruction decision before settlement")
	}
}

func TestFinalInstructionBoundaryWaitsForInFlightAcceptanceSave(t *testing.T) {
	scope, store, parentID := childScopeTest(t, delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		return contracts.ModelResult{}, context.Canceled
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prepared, err := scope.delegation.prepareChild(ctx, contracts.AgentRunRequest{Task: "final boundary"})
	if err != nil {
		t.Fatal(err)
	}
	ordinal, err := store.ExecutionDisplayOrdinal(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}
	journal := newJournal(context.WithoutCancel(ctx), store, prepared.input.execution.ID, parentID, "final boundary",
		scope.delegation.input.executionDataOwner, nil)
	journal.parentExecutionID = parentID
	journal.rootExecutionOrdinal = ordinal
	journal.control = scope.delegation.control
	instructions := newChildInstructionOwner(prepared.input.execution.ID, parentID)
	instructions.attachJournal(journal, nil)
	defer func() {
		_ = instructions.Close("completed")
		if err := journal.end(history.OutcomeCompleted, map[string]any{"ok": true}); err != nil {
			t.Errorf("settle child: %v", err)
		}
	}()

	db, err := sql.Open("sqlite", filepath.Join(prepared.input.workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err = blocker.ExecContext(ctx, "UPDATE executions SET task_id=task_id WHERE execution_id=?", prepared.input.execution.ID); err != nil {
		t.Fatal(err)
	}

	type acceptResult struct {
		receipt contracts.ChildInstructionReceipt
		err     error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		receipt, acceptErr := instructions.Accept(ctx, contracts.AgentSteerRequest{ExecutionID: prepared.input.execution.ID,
			Text: "Finish the saved instruction."})
		accepted <- acceptResult{receipt: receipt, err: acceptErr}
	}()
	deadline := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		instructions.mu.Lock()
		registered := len(instructions.items) == 1
		var item *childInstruction
		if registered {
			item = instructions.items[0]
		}
		instructions.mu.Unlock()
		if registered {
			select {
			case <-item.done:
				t.Fatal("acceptance save completed while the SQLite writer was held")
			default:
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("steering instruction was not registered")
		case <-ticker.C:
		}
	}

	type applyResult struct {
		messages []contracts.Message
		err      error
	}
	applying := make(chan applyResult, 1)
	go func() {
		messages, applyErr := instructions.Apply(ctx, true)
		applying <- applyResult{messages: messages, err: applyErr}
	}()
	var appliedTooEarly *applyResult
	var apply applyResult
	select {
	case result := <-applying:
		appliedTooEarly = &result
		apply = result
	case <-time.After(100 * time.Millisecond):
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	accept := <-accepted
	if appliedTooEarly == nil {
		apply = <-applying
	}
	if appliedTooEarly != nil {
		t.Fatalf("final boundary returned before the registered acceptance was saved: messages=%+v err=%v",
			appliedTooEarly.messages, appliedTooEarly.err)
	}
	if accept.err != nil || accept.receipt.Status != "accepted" {
		t.Fatalf("acceptance receipt=%+v err=%v", accept.receipt, accept.err)
	}
	if apply.err != nil || len(apply.messages) != 1 || apply.messages[0].VisibleText() != "Finish the saved instruction." {
		t.Fatalf("final boundary messages=%+v err=%v", apply.messages, apply.err)
	}
	receipts := instructions.Receipts()
	if len(receipts) != 1 || receipts[0].Status != "applied" || receipts[0].AppliedMessageOrdinal == nil {
		t.Fatalf("final boundary did not persist the applied receipt: %+v", receipts)
	}
}

func steeringTexts(messages []contracts.Message) []string {
	var texts []string
	for _, message := range messages {
		if message.Steering != nil && *message.Steering {
			texts = append(texts, message.VisibleText())
		}
	}
	return texts
}
