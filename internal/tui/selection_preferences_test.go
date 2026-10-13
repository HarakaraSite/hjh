package tui

import (
	"context"
	"testing"

	"hjh/internal/api"
)

func TestHumanSelectionsSaveInOrderBeforeNewSessionRunAndSurviveUIClose(t *testing.T) {
	calls := make(chan api.RememberSelectionRequest, 4)
	releases := make(chan struct{}, 4)
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1),
		rememberSelectionFunc: func(ctx context.Context, request api.RememberSelectionRequest) (api.SelectionPreview, error) {
			calls <- request
			<-releases
			if err := ctx.Err(); err != nil {
				t.Error("closing the UI canceled an accepted selection save")
			}
			return api.SelectionPreview{}, nil
		},
	}
	m, _, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work", SessionID: "previous"})
	defer func() { cancel(); m.shutdown() }()
	// Choosing a provider clears model/effort. Each later choice must save
	// after the preceding one, and /new remembers the previous Session route.
	m.setProvider("chosen-provider")
	m.setModel("chosen-model")
	m.setEffort("high")
	m.applySessionAction(sessionAction{newSession: true})
	m.startRun("first task in next Session")
	first := <-calls
	if first.CoreID != "core-1" || first.Workspace != "/work" || first.SessionID != "previous" || first.ProviderID != "chosen-provider" || first.ModelID != "" {
		t.Fatalf("provider save=%+v", first)
	}
	select {
	case call := <-backend.runCalls:
		call.handle.finish(api.RunState{}, nil)
		t.Fatal("new run overtook the pending selection saves")
	default:
	}
	select {
	case <-calls:
		t.Fatal("later model selection overtook the provider save")
	default:
	}
	cancel()
	for i := range 3 {
		releases <- struct{}{}
		request := <-calls
		if request.ModelID != "chosen-model" || (i > 0 && request.Effort != "high") || request.SessionID != "previous" {
			t.Fatalf("queued save %d=%+v", i, request)
		}
	}
	releases <- struct{}{}
	call := <-backend.runCalls
	call.handle.finish(api.RunState{RunID: call.request.RunID}, nil)
	if call.request.SessionID != "" || call.request.ProviderID != "chosen-provider" || call.request.ModelID != "chosen-model" || call.request.Effort != "high" {
		t.Fatalf("next Session run inputs=%+v", call.request)
	}
}
