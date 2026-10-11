package corerun

import (
	"context"
	"errors"
	"fmt"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/conversation"
	"hjh/internal/executiondata"
	"hjh/internal/history"
)

type runConversationCut struct {
	cut     history.ConversationCut
	through *uint64
}

func (m *Manager) ensureRunView(ctx context.Context, runID string, run *managedRun) (*conversation.View, error) {
	for {
		run.viewMu.Lock()
		if run.view != nil {
			view := run.view.view
			run.viewMu.Unlock()
			return view, nil
		}
		if creating := run.viewCreating; creating != nil {
			run.viewMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-creating:
				continue
			}
		}
		creating := make(chan struct{})
		run.viewCreating = creating
		run.viewMu.Unlock()

		view := conversation.NewView(runID)
		capture, dataSubscription, err := executiondata.CaptureAndSubscribe(run.owner,
			func(rootID string, through uint64) (runConversationCut, error) {
				captured := runConversationCut{}
				if rootID == "" {
					return captured, nil
				}
				cut, err := m.service.CaptureConversationCut(ctx, rootID)
				if err != nil {
					return runConversationCut{}, err
				}
				captured.cut = cut
				if through != 0 {
					captured.through = &through
				}
				return captured, nil
			}, view.Enqueue)
		if err == nil {
			rootCuts := []api.ConversationCut(nil)
			var walk func(func(contracts.ExecutionFact) error) error
			if capture.cut.RootExecutionID != "" {
				rootCuts = []api.ConversationCut{conversationCutDTO(capture.cut, capture.through)}
				walk = func(visit func(contracts.ExecutionFact) error) error {
					return m.service.WalkConversationCut(ctx, capture.cut, visit)
				}
			}
			err = view.LoadCut(ctx, capture.through, rootCuts, walk)
		}
		if err != nil {
			if dataSubscription != nil {
				dataSubscription.Close()
			}
			view.Release()
		}

		m.mu.Lock()
		run.viewMu.Lock()
		if err == nil && run.viewConsumers > 0 {
			run.view = &conversationView{view: view, data: dataSubscription}
		} else if err == nil {
			err = conversation.ErrClosed
		}
		run.viewCreating = nil
		close(creating)
		run.viewMu.Unlock()
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return view, nil
	}
}

func conversationCutDTO(cut history.ConversationCut, throughOverride *uint64) api.ConversationCut {
	through := cut.ThroughSequence
	if throughOverride != nil {
		through = throughOverride
	}
	head := make([]api.ConversationExecutionHead, len(cut.Executions))
	for i, execution := range cut.Executions {
		head[i] = api.ConversationExecutionHead{ExecutionID: execution.ExecutionID,
			ParentExecutionID: execution.ParentExecutionID, SemanticOrdinal: execution.LatestRecordOrdinal,
			MessageOrdinal: execution.LatestMessageOrdinal}
	}
	return api.ConversationCut{RootExecutionID: cut.RootExecutionID, RootExecutionOrdinal: cut.RootExecutionOrdinal,
		ThroughSequence: copySequence(through), Executions: head}
}

func sessionConversationCutsDTO(cut history.SessionConversationCut) []api.ConversationCut {
	roots := make([]api.ConversationCut, len(cut.Roots))
	for i, root := range cut.Roots {
		roots[i] = conversationCutDTO(root, nil)
	}
	return roots
}

func (m *Manager) releaseView(run *managedRun) {
	run.viewMu.Lock()
	materialized := run.view
	run.view = nil
	run.viewMu.Unlock()
	if materialized == nil {
		return
	}
	// Owner gate removal and view teardown happen after releasing viewMu.
	if materialized.data != nil {
		materialized.data.Close()
	}
	materialized.view.Release()
}

func (m *Manager) releaseViewIfUnused(run *managedRun) {
	m.mu.Lock()
	if run.viewConsumers != 0 {
		m.mu.Unlock()
		return
	}
	run.viewMu.Lock()
	materialized := run.view
	run.view = nil
	run.viewMu.Unlock()
	m.mu.Unlock()
	if materialized == nil {
		return
	}
	if materialized.data != nil {
		materialized.data.Close()
	}
	materialized.view.Release()
}

// SessionView materializes one fixed Session cut for the duration of this
// read. Overlapping reads share the same restoration and entity map until the
// last read has copied its snapshot. When a root is active, cut capture and
// commit registration share its execution-data gate; snapshots stay bounded
// by that fixed cut.
func (m *Manager) SessionView(ctx context.Context, sessionID string) (api.ConversationSnapshot, error) {
	return m.sessionViewSnapshot(ctx, sessionID, m.restoreSessionView)
}

func (m *Manager) sessionViewSnapshot(ctx context.Context, sessionID string,
	restore func(context.Context, string) (*conversation.View, *executiondata.Subscription, error)) (api.ConversationSnapshot, error) {
	m.mu.Lock()
	flight := m.sessionViews[sessionID]
	creator := flight == nil
	if creator {
		flight = &sessionViewFlight{ready: make(chan struct{}), refs: 1}
		m.sessionViews[sessionID] = flight
	} else {
		flight.refs++
	}
	m.mu.Unlock()

	if creator {
		flight.view, flight.data, flight.err = restore(ctx, sessionID)
		m.mu.Lock()
		close(flight.ready)
		m.mu.Unlock()
	} else {
		select {
		case <-ctx.Done():
			m.releaseSessionView(sessionID, flight)
			return api.ConversationSnapshot{}, ctx.Err()
		case <-flight.ready:
		}
	}
	if flight.err != nil {
		m.releaseSessionView(sessionID, flight)
		return api.ConversationSnapshot{}, flight.err
	}
	snapshot, err := flight.view.Snapshot(ctx)
	m.releaseSessionView(sessionID, flight)
	return snapshot, err
}

func (m *Manager) restoreSessionView(ctx context.Context, sessionID string) (*conversation.View, *executiondata.Subscription, error) {
	for {
		m.mu.Lock()
		var active *managedRun
		if runID := m.sessions[sessionID]; runID != "" {
			active = m.runs[runID]
		}
		version := m.sessionVersions[sessionID]
		m.mu.Unlock()

		view := conversation.NewSessionView(sessionID)
		var cut history.SessionConversationCut
		var dataSubscription *executiondata.Subscription
		var err error
		if active != nil {
			cut, dataSubscription, err = executiondata.CaptureAndSubscribe(active.owner,
				func(_ string, _ uint64) (history.SessionConversationCut, error) {
					return m.service.CaptureSessionConversationCut(ctx, sessionID)
				}, view.Enqueue)
		} else {
			cut, err = m.service.CaptureSessionConversationCut(ctx, sessionID)
		}
		if err != nil {
			if dataSubscription != nil {
				dataSubscription.Close()
			}
			view.Release()
			return nil, nil, err
		}

		// If a run was admitted while a non-gated Session cut was being read,
		// discard that cut and repeat through the new root's write gate.
		m.mu.Lock()
		changed := m.sessionVersions[sessionID] != version
		m.mu.Unlock()
		if changed {
			if dataSubscription != nil {
				dataSubscription.Close()
			}
			view.Release()
			continue
		}

		rootCuts := sessionConversationCutsDTO(cut)
		err = view.LoadCut(ctx, nil, rootCuts, func(visit func(contracts.ExecutionFact) error) error {
			return m.service.WalkSessionConversationCut(ctx, cut, visit)
		})
		if err == nil {
			return view, dataSubscription, nil
		}
		if dataSubscription != nil {
			dataSubscription.Close()
		}
		view.Release()
		return nil, nil, err
	}
}

func (m *Manager) releaseSessionView(sessionID string, flight *sessionViewFlight) {
	m.mu.Lock()
	flight.refs--
	if flight.refs != 0 {
		m.mu.Unlock()
		return
	}
	if m.sessionViews[sessionID] == flight {
		delete(m.sessionViews, sessionID)
	}
	view, data := flight.view, flight.data
	flight.view, flight.data = nil, nil
	m.mu.Unlock()
	if data != nil {
		data.Close()
	}
	if view != nil {
		view.Release()
	}
}

func (m *Manager) waitForFinalConversation(ctx context.Context, run *managedRun, view *conversation.View) error {
	through := run.owner.ThroughSequence()
	if err := view.WaitThrough(ctx, through); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("wait for final conversation prefix: %w", err)
	}
	return nil
}

func copySequence(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
