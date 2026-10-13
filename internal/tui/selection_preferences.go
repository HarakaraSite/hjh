package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

type selectionSaveFailedMsg struct {
	coreID, workspace string
	err               error
}

func waitForSelectionSave(ctx context.Context, tail <-chan struct{}) error {
	if tail == nil {
		return nil
	}
	select {
	case <-tail:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *model) rememberSelection() {
	request := api.RememberSelectionRequest{CoreID: m.core.CoreID, SelectionRequest: m.selectionRequest()}
	previous := m.selectionSaveTail
	// Accepted human choices finish in order, including when the UI closes.
	ctx := context.WithoutCancel(m.ctx)
	job := m.newJob(func() {})
	job.closeBeforeBackend = true
	m.selectionSaveTail = job.done
	m.launch(job, func() tea.Msg {
		_ = waitForSelectionSave(ctx, previous)
		_, err := m.backend.RememberSelection(ctx, request)
		if err != nil {
			return selectionSaveFailedMsg{request.CoreID, request.Workspace, err}
		}
		return nil
	})
}

// deferredSelectionRun holds the usual run controls while preceding selection
// saves finish. Its channel publishes the backend handle to Wait and Cancel.
type deferredSelectionRun struct {
	id     string
	ready  chan struct{}
	handle RunHandle
}

func (r *deferredSelectionRun) RunID() string { return r.id }
func (r *deferredSelectionRun) Wait() (api.RunState, error) {
	<-r.ready
	return r.handle.Wait()
}
func (r *deferredSelectionRun) Cancel() error {
	<-r.ready
	return r.handle.Cancel()
}

func (m *model) startRunAfterSelectionSave(request api.StartRunRequest, observer func(apiclient.RunEvent)) RunHandle {
	tail := m.selectionSaveTail
	if tail == nil {
		return m.backend.StartRun(request, observer)
	}
	select {
	case <-tail:
		return m.backend.StartRun(request, observer)
	default:
	}
	run := &deferredSelectionRun{id: request.RunID, ready: make(chan struct{})}
	go func() {
		<-tail
		run.handle = m.backend.StartRun(request, observer)
		close(run.ready)
	}()
	return run
}
