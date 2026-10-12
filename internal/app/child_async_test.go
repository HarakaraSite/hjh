package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/history"
)

func TestAsyncChildrenThroughRunJSAndSavedHistory(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "parent-input.txt"), []byte("parent independent work"), 0644); err != nil {
		t.Fatal(err)
	}
	owner := executiondata.NewOwner()
	var liveMu sync.Mutex
	var liveFacts []contracts.ExecutionFact
	_, subscription, err := executiondata.CaptureAndSubscribe(owner,
		func(string, uint64) (struct{}, error) { return struct{}{}, nil },
		func(commit contracts.ExecutionCommit) {
			liveMu.Lock()
			liveFacts = append(liveFacts, commit.Facts...)
			liveMu.Unlock()
		})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	steadyStarted := make(chan struct{}, 1)
	var parentStep atomic.Int32
	var sawIndependentWork atomic.Bool
	provider := delegationProvider(func(ctx context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		first := ""
		if len(request.Messages) > 0 {
			first = request.Messages[0].VisibleText()
		}
		switch first {
		case "async parent":
			step := parentStep.Add(1)
			switch step {
			case 1:
				return scriptedJS(`const [quick, steady] = await Promise.all([
  api.agents.spawn({task:"quick child", tools:[]}),
  api.agents.spawn({task:"steady child", tools:[]})
]);
const independent = await tools.read_file({path:"parent-input.txt"});
globalThis.childIDs = {quick: quick.executionId, steady: steady.executionId};
return {quick, steady, independent};`), nil
			case 2:
				body, _ := json.Marshal(request.Messages)
				if strings.Contains(string(body), "parent independent work") {
					sawIndependentWork.Store(true)
				}
				select {
				case <-steadyStarted:
				case <-ctx.Done():
					return contracts.ModelResult{}, ctx.Err()
				}
				return scriptedJS(`const quick = await api.agents.status({executionId:globalThis.childIDs.quick});
const steady = await api.agents.status({executionId:globalThis.childIDs.steady});
const completed = await api.agents.collect({executionId:globalThis.childIDs.quick});
const cancelled = await api.agents.cancel({executionId:globalThis.childIDs.steady});
return {quick, steady, completed, cancelled};`), nil
			default:
				return contracts.ModelResult{Text: "finished parent"}, nil
			}
		case "quick child":
			return contracts.ModelResult{Text: "quick child result"}, nil
		case "steady child":
			select {
			case steadyStarted <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return contracts.ModelResult{}, ctx.Err()
		default:
			return contracts.ModelResult{}, context.Canceled
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "async parent", Workspace: workspace, ModelID: "test/model",
		ExecutionDataOwner: owner})
	if err != nil || !result.Result.OK || result.Result.FinalText != "finished parent" {
		t.Fatalf("run %+v: %v", result, err)
	}
	if !sawIndependentWork.Load() {
		t.Fatal("parent's next model step did not receive its independent read_file result")
	}

	children, err := store.Children(ctx, result.ExecutionID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children %+v: %v", children, err)
	}
	byTask := map[string]history.Execution{}
	for _, child := range children {
		byTask[child.Task] = child
	}
	quick, hasQuick := byTask["quick child"]
	steady, hasSteady := byTask["steady child"]
	if !hasQuick || !hasSteady || quick.Outcome != "completed" || steady.Outcome != "cancelled" ||
		quick.Adoption != "non_canonical" || steady.Adoption != "non_canonical" || quick.TerminalRecordID == "" || steady.TerminalRecordID == "" {
		t.Fatalf("children did not settle independently: %+v", children)
	}
	parentRecords, err := store.Records(ctx, result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	startedSpawns := map[string]bool{}
	terminalRelations := map[string]bool{}
	hadReadFile := false
	hadSecondStepStatus := 0
	for _, record := range parentRecords {
		var observation contracts.Observation
		if err := json.Unmarshal(record.Payload, &observation); err != nil {
			continue
		}
		if observation.Kind == "agent" && observation.Name == "agents.spawn" && observation.Phase == "started" {
			startedSpawns[observation.ID] = true
		}
		if observation.Kind == "tool" && observation.Name == "read_file" && observation.Phase == "completed" {
			hadReadFile = true
		}
		if observation.Kind == "agent" && observation.Name == "agents.status" && observation.Phase == "started" {
			hadSecondStepStatus++
		}
		if observation.Kind == "agent" && (observation.Name == "agents.collect" || observation.Name == "agents.cancel") && observation.Phase != "started" {
			for _, relation := range record.Relations {
				if relation.Relation == "child_terminal" && relation.Resolved {
					terminalRelations[relation.TargetRecordID] = true
				}
			}
		}
	}
	if !hadReadFile || hadSecondStepStatus != 2 {
		t.Fatalf("parent independent work or next-step statuses were not recorded: readFile=%v statuses=%d", hadReadFile, hadSecondStepStatus)
	}
	for _, child := range children {
		if !startedSpawns[child.SpawnCallID] {
			t.Fatalf("child %s spawn ID %q has no saved agents.spawn call", child.ID, child.SpawnCallID)
		}
		if !terminalRelations[child.TerminalRecordID] {
			t.Fatalf("child %s terminal record %q has no collect/cancel relation", child.ID, child.TerminalRecordID)
		}
	}

	cut, err := store.CaptureConversationCut(ctx, result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var replayFacts []contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		replayFacts = append(replayFacts, fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	liveMu.Lock()
	liveFacts = append([]contracts.ExecutionFact(nil), liveFacts...)
	liveMu.Unlock()
	type correlation struct {
		RootExecutionID, ExecutionID, ParentExecutionID        string
		Kind, Phase, Text, Name, CallID, ParentCallID, Outcome string
	}
	correlate := func(fact contracts.ExecutionFact) correlation {
		callID, outcome := fact.CallID, fact.Outcome
		if fact.Kind == "execution_admission" {
			// Replay represents this saved execution row without the synthetic
			// call identifier and outcome attached to the live admission event.
			callID, outcome = "", ""
		}
		return correlation{fact.RootExecutionID, fact.ExecutionID, fact.ParentExecutionID,
			fact.Kind, fact.Phase, fact.Text, fact.Name, callID, fact.ParentCallID, outcome}
	}
	for _, child := range children {
		liveChild, replayChild := map[correlation]int{}, map[correlation]int{}
		for _, fact := range liveFacts {
			if fact.ExecutionID == child.ID {
				liveChild[correlate(fact)]++
			}
		}
		for _, fact := range replayFacts {
			if fact.ExecutionID == child.ID {
				replayChild[correlate(fact)]++
			}
		}
		if len(liveChild) == 0 || !reflect.DeepEqual(liveChild, replayChild) {
			t.Fatalf("child %s live/replay facts differ:\n live=%+v\n replay=%+v", child.ID, liveChild, replayChild)
		}
	}
}
