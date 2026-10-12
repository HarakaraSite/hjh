package corerun

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
)

type childControlProvider struct {
	parentStep      atomic.Int32
	parentContinued chan struct{}
	childrenStarted chan string
	continueParent  chan struct{}
	releaseChild    chan struct{}
}

func (p *childControlProvider) Prepare(req contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(req.Messages)
}
func (p *childControlProvider) Generate(ctx context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	task := req.Messages[0].VisibleText()
	if task != "Core async parent" {
		p.childrenStarted <- task
		if task == "child A" {
			select {
			case <-p.releaseChild:
			case <-ctx.Done():
				return contracts.ModelResult{}, ctx.Err()
			}
		} else {
			<-ctx.Done()
			return contracts.ModelResult{}, ctx.Err()
		}
		return contracts.ModelResult{Text: "child A finished"}, nil
	}
	var code string
	switch p.parentStep.Add(1) {
	case 1:
		code = `const children=await Promise.all([api.agents.spawn({task:"child A",tools:[]}),api.agents.spawn({task:"child B",tools:[]})]); globalThis.coreChildren=children; return children;`
	case 2:
		close(p.parentContinued)
		select {
		case <-p.continueParent:
		case <-ctx.Done():
			return contracts.ModelResult{}, ctx.Err()
		}
		code = `const a=await api.agents.collect({executionId:globalThis.coreChildren[0].executionId}); const b=await api.agents.cancel({executionId:globalThis.coreChildren[1].executionId}); return {a,b};`
	default:
		return contracts.ModelResult{Text: "Core parent finished"}, nil
	}
	return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: fmt.Sprintf("core-step-%d", p.parentStep.Load()), Name: "run_js", Arguments: contracts.JSON(map[string]string{"code": code})}}}, nil
}

func TestCoreAsyncChildrenSurviveDetachAndJoinOnParentSettlement(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel-parent-%t", cancelParent), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			provider := &childControlProvider{parentContinued: make(chan struct{}), childrenStarted: make(chan string, 2), continueParent: make(chan struct{}), releaseChild: make(chan struct{})}
			manager, service := productManager(t, provider, app.RunOptions{ModelID: "test/parent"})
			request := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: uuid.NewString(), Task: "Core async parent"}
			if _, created, err := manager.Start(request); err != nil || !created {
				t.Fatalf("start created=%t err=%v", created, err)
			}
			first, err := manager.Subscribe(request.CoreID, request.RunID)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			select {
			case <-provider.parentContinued:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			for range 2 {
				select {
				case <-provider.childrenStarted:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			initial := awaitRun(t, manager, request.RunID, func(s api.RunState) bool { return s.Phase == "running" && s.ExecutionID != "" })
			first.Close()
			inspection, err := service.Inspect(ctx, initial.ExecutionID, 0, -1)
			if err != nil || len(inspection.Children) != 2 {
				t.Fatalf("detached inspection children=%+v err=%v", inspection.Children, err)
			}
			for _, child := range inspection.Children {
				if child.Lifecycle != "active" {
					t.Fatalf("detach stopped child %+v", child)
				}
			}
			second, err := manager.SubscribeContext(ctx, request.CoreID, request.RunID)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			owner := managedRunForTest(manager, request.RunID).owner
			if err := second.view.WaitThrough(ctx, owner.ThroughSequence()); err != nil {
				t.Fatal(err)
			}
			restored, err := second.view.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"child A", "child B"} {
				if findEntity(restored, "task", text) == nil {
					t.Fatalf("reconnected view missing %s", text)
				}
			}
			if cancelParent {
				if _, err := manager.CancelContext(ctx, request.CoreID, request.RunID); err != nil {
					t.Fatal(err)
				}
			} else {
				close(provider.releaseChild)
				close(provider.continueParent)
			}
			terminal := awaitRun(t, manager, request.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
			if terminal.Result == nil || terminal.Result.OK == cancelParent {
				t.Fatalf("terminal result %+v", terminal.Result)
			}
			final, err := service.Inspect(ctx, initial.ExecutionID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			outcomes := map[string]string{}
			for _, child := range final.Children {
				if child.Lifecycle != "settled" || child.TerminalRecordID == "" || child.Adoption != "non_canonical" {
					t.Fatalf("parent terminal before child saved %+v", child)
				}
				outcomes[child.Task] = string(child.Outcome)
			}
			wantA := "completed"
			if cancelParent {
				wantA = "cancelled"
			}
			if outcomes["child A"] != wantA || outcomes["child B"] != "cancelled" {
				t.Fatalf("child outcomes %+v", outcomes)
			}
		})
	}
}
