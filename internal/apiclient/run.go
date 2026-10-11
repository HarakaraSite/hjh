package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
)

// RunEvent is a client-side notification. State is a complete display
// projection; snapshot and update consumers can coalesce by the DTO identities.
type RunEvent struct {
	Kind         string
	State        api.RunState
	Conversation *api.ConversationSnapshot
	Update       *api.ConversationUpdate
	Err          error
}

// RunHandle is the client-side control surface for one Core-owned run.
type RunHandle interface {
	RunID() string
	Cancel() error
	Wait() (api.RunState, error)
}

type cancelAttempt struct {
	done chan struct{}
	err  error
}

// runHandle owns the client's communication with one Core-owned run. Cancel
// is always an explicit API operation. Closing the client or losing the SSE
// stream only ends communication and never turns into cancellation.
type runHandle struct {
	client   *Client
	request  api.StartRunRequest
	observer func(RunEvent)
	attach   bool

	mu                    sync.Mutex
	state                 api.RunState
	hasState              bool
	conversationViewID    string
	conversationRevision  uint64
	initialSnapshotSeen   bool
	initialWasTerminal    bool
	terminalSnapshotCount int
	streamTerminal        *api.RunState
	accepted              chan struct{}
	acceptOnce            sync.Once
	acceptErr             error
	done                  chan struct{}
	finishOnce            sync.Once
	result                api.RunState
	err                   error
	cancelAttempt         *cancelAttempt
}

// StartRun creates a client run identity before starting its POST and returns
// immediately. A caller may call Cancel before the first response; the intent
// is held until the Core has registered the request, then sent explicitly.
func (c *Client) StartRun(request api.StartRunRequest, observer func(RunEvent)) RunHandle {
	if request.RunID == "" {
		request.RunID = uuid.NewString()
	}
	handle := &runHandle{
		client: c, request: request, observer: observer,
		accepted: make(chan struct{}), done: make(chan struct{}),
	}
	go handle.run()
	return handle
}

// AttachRun subscribes to an existing Core-owned run. Registration is a GET;
// it never replays the task or sends a start request.
func (c *Client) AttachRun(coreID, runID string, observer func(RunEvent)) RunHandle {
	handle := &runHandle{
		client: c, request: api.StartRunRequest{CoreID: coreID, RunID: runID},
		observer: observer, attach: true,
		accepted: make(chan struct{}), done: make(chan struct{}),
	}
	go handle.run()
	return handle
}

func (h *runHandle) RunID() string  { return h.request.RunID }
func (h *runHandle) CoreID() string { return h.request.CoreID }

func (h *runHandle) Wait() (api.RunState, error) {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.result, h.err
}

// Cancel records an explicit stop request even if POST is still in flight.
// It returns when the Core accepts the cancel request or reports terminal.
func (h *runHandle) Cancel() error {
	h.mu.Lock()
	if h.isDoneLocked() {
		err := h.err
		h.mu.Unlock()
		return err
	}
	attempt := h.cancelAttempt
	if attempt == nil {
		attempt = &cancelAttempt{done: make(chan struct{})}
		h.cancelAttempt = attempt
		go h.sendCancel(attempt)
	}
	h.mu.Unlock()

	select {
	case <-attempt.done:
		return attempt.err
	case <-h.client.ctx.Done():
		return h.client.ctx.Err()
	}
}

func (h *runHandle) sendCancel(attempt *cancelAttempt) {
	select {
	case <-h.accepted:
	case <-h.done:
		h.mu.Lock()
		err := h.err
		h.mu.Unlock()
		h.setCancelResult(attempt, err)
		return
	case <-h.client.ctx.Done():
		h.setCancelResult(attempt, h.client.ctx.Err())
		return
	}

	h.mu.Lock()
	acceptErr := h.acceptErr
	state, hasState := h.state, h.hasState
	h.mu.Unlock()
	if acceptErr != nil {
		h.setCancelResult(attempt, acceptErr)
		return
	}
	if hasState && state.Phase == "terminal" {
		h.setCancelResult(attempt, nil)
		return
	}
	_, err := h.client.CancelRun(h.client.ctx, h.request.CoreID, h.request.RunID)
	if err != nil {
		// A lost cancellation response is resolved through run state; the run
		// identifier makes the read safe to repeat against the same Core.
		latest, getErr := h.client.GetRun(h.client.ctx, h.request.CoreID, h.request.RunID)
		if getErr == nil && (latest.Phase == "cancelling" || latest.Phase == "terminal") {
			err = nil
		} else if getErr != nil {
			err = errors.Join(err, getErr)
		}
	}
	h.setCancelResult(attempt, err)
}

func (h *runHandle) setCancelResult(attempt *cancelAttempt, err error) {
	h.mu.Lock()
	attempt.err = err
	if err != nil && h.cancelAttempt == attempt {
		// Let a later explicit user action retry after the Core still reports a
		// running run. Concurrent callers already joined this attempt and retain
		// its result through their attempt pointer.
		h.cancelAttempt = nil
	}
	close(attempt.done)
	h.mu.Unlock()
}

func (h *runHandle) isDoneLocked() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

func (h *runHandle) run() {
	defer h.finishOnce.Do(func() { close(h.done) })
	state := api.RunState{
		CoreID: h.request.CoreID, RunID: h.request.RunID,
	}
	announcedDisconnect := false
	for {
		registered, registerErr, retryable := h.register()
		if registerErr == nil {
			state = registered
			h.setAccepted(state, nil)
			break
		}
		if !retryable {
			h.setAccepted(state, registerErr)
			h.finish(state, registerErr)
			return
		}
		if !announcedDisconnect {
			h.publish(RunEvent{Kind: "run.disconnected", State: state, Err: registerErr})
			announcedDisconnect = true
		}
		if !waitClient(h.client.ctx, 250*time.Millisecond) {
			h.finish(state, h.client.ctx.Err())
			return
		}
	}

	for {
		if h.client.ctx.Err() != nil {
			h.finish(state, h.client.ctx.Err())
			return
		}
		streamErr := h.client.readRunEvents(h.client.ctx, h.request.CoreID, h.request.RunID, h.publish)
		if h.client.ctx.Err() != nil {
			h.finish(state, h.client.ctx.Err())
			return
		}
		h.mu.Lock()
		streamTerminal := h.streamTerminal
		if streamTerminal != nil {
			terminalState := *streamTerminal
			h.mu.Unlock()
			h.finish(terminalState, nil)
			return
		}
		h.mu.Unlock()
		_, getErr := h.client.GetRun(h.client.ctx, h.request.CoreID, h.request.RunID)
		if getErr == nil {
			// GET reports run completion. A conversation subscriber completes
			// only after the final snapshot and run.terminal arrive over SSE.
		} else if isRunNotFound(getErr) || isCoreChanged(getErr) {
			h.finish(state, errors.Join(streamErr, getErr))
			return
		} else {
			h.publish(RunEvent{Kind: "run.disconnected", State: state, Err: errors.Join(streamErr, getErr)})
		}
		if !waitClient(h.client.ctx, 250*time.Millisecond) {
			h.finish(state, h.client.ctx.Err())
			return
		}
	}
}

func (h *runHandle) setAccepted(state api.RunState, err error) {
	h.mu.Lock()
	h.acceptErr = err
	if err == nil {
		h.state, h.hasState = state, true
	}
	h.mu.Unlock()
	h.acceptOnce.Do(func() { close(h.accepted) })
}

// register reports retryable when transport failures left acceptance
// unconfirmed. Retrying uses the original request and RunID, so the Core can
// return the existing run if the lost POST was already accepted.
func (h *runHandle) register() (api.RunState, error, bool) {
	if h.attach {
		state, err := h.client.GetRun(h.client.ctx, h.request.CoreID, h.request.RunID)
		return state, err, err != nil && !isAPIError(err) && h.client.ctx.Err() == nil
	}
	state, err := h.client.postRun(h.client.ctx, h.request)
	if err == nil {
		return state, nil, false
	}
	if isAPIError(err) {
		return api.RunState{}, err, false
	}
	// The POST may have committed before its response was lost. Look up the
	// client-generated ID first, then retry the identical POST only if absent.
	state, lookupErr := h.client.GetRun(h.client.ctx, h.request.CoreID, h.request.RunID)
	if lookupErr == nil {
		return state, nil, false
	}
	if isCoreChanged(lookupErr) {
		return api.RunState{}, errors.Join(err, lookupErr), false
	}
	if isRunNotFound(lookupErr) || !isAPIError(lookupErr) {
		retryState, retryErr := h.client.postRun(h.client.ctx, h.request)
		if retryErr == nil {
			return retryState, nil, false
		}
		if isAPIError(retryErr) {
			return api.RunState{}, retryErr, false
		}
		state, lookupErr = h.client.GetRun(h.client.ctx, h.request.CoreID, h.request.RunID)
		if lookupErr == nil {
			return state, nil, false
		}
		if isCoreChanged(lookupErr) {
			return api.RunState{}, errors.Join(err, retryErr, lookupErr), false
		}
		return api.RunState{}, errors.Join(err, retryErr, lookupErr), true
	}
	return api.RunState{}, errors.Join(err, lookupErr), true
}

func (c *Client) postRun(ctx context.Context, request api.StartRunRequest) (api.RunState, error) {
	var state api.RunState
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/runs", request, &state)
	return state, err
}

func (h *runHandle) publish(event RunEvent) bool {
	state := event.State
	if state.CoreID != "" && state.CoreID != h.request.CoreID || state.RunID != "" && state.RunID != h.request.RunID {
		return false
	}
	h.mu.Lock()
	updateState := !h.hasState || state.Seq >= h.state.Seq
	conversationEvent := event
	reconnect := false
	snapshotAccepted := true
	if event.Kind == "run.snapshot" || event.Kind == "run.terminal" {
		if event.Conversation != nil {
			if h.conversationViewID == event.Conversation.ViewID {
				if event.Conversation.Revision < h.conversationRevision {
					conversationEvent.Conversation = nil
					snapshotAccepted = false
				} else {
					h.conversationRevision = event.Conversation.Revision
				}
			} else {
				h.conversationViewID = event.Conversation.ViewID
				h.conversationRevision = event.Conversation.Revision
			}
			if event.Kind == "run.snapshot" && event.Conversation != nil && snapshotAccepted {
				if !h.initialSnapshotSeen {
					h.initialSnapshotSeen = true
					h.initialWasTerminal = state.Phase == "terminal"
				}
				if state.Phase == "terminal" {
					h.terminalSnapshotCount++
				}
			}
		}
	} else if event.Kind == "run.update" && event.Update != nil {
		if event.Update.ViewID != h.conversationViewID {
			reconnect = true
			conversationEvent.Update = nil
		} else if event.Update.Revision > h.conversationRevision {
			if event.Update.BaseRevision != h.conversationRevision {
				reconnect = true
				conversationEvent.Update = nil
			} else {
				h.conversationRevision = event.Update.Revision
			}
		} else {
			conversationEvent.Update = nil
		}
	}
	if updateState {
		h.state, h.hasState = state, true
	}
	if event.Kind == "run.terminal" && state.Phase == "terminal" && state.Result != nil &&
		h.initialSnapshotSeen && (!h.initialWasTerminal || h.terminalSnapshotCount >= 2) {
		terminalCopy := state
		h.streamTerminal = &terminalCopy
	} else if event.Kind == "run.terminal" {
		reconnect = true
	}
	h.mu.Unlock()
	if event.Kind == "run.terminal" && reconnect {
		return false
	}
	if h.observer != nil {
		h.observer(conversationEvent)
	}
	return !reconnect && event.Kind != "run.terminal"
}

func (h *runHandle) finish(state api.RunState, err error) {
	h.finishOnce.Do(func() {
		h.mu.Lock()
		h.result, h.err = state, err
		h.mu.Unlock()
		close(h.done)
	})
}

func (c *Client) readRunEvents(ctx context.Context, coreID, runID string, receive func(RunEvent) bool) error {
	query := url.Values{}
	query.Set("coreId", coreID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+apiPrefix+"/runs/"+url.PathEscape(runID)+"/events?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readAPIError(response)
	}
	return readSSE(response.Body, func(event, data string) bool {
		if data == "" {
			return true
		}
		kind := event
		if kind == "" || kind == "message" {
			kind = "run.update"
		}
		received := RunEvent{Kind: kind}
		switch kind {
		case "run.snapshot":
			var snapshot api.RunSnapshot
			if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
				return false
			}
			received.State = snapshot.RunState
			received.Conversation = &snapshot.Conversation
		case "run.terminal":
			if err := json.Unmarshal([]byte(data), &received.State); err != nil {
				return false
			}
		case "run.update":
			var update api.RunUpdate
			if err := json.Unmarshal([]byte(data), &update); err != nil {
				return false
			}
			received.State = update.RunState
			received.Update = update.Conversation
		default:
			if err := json.Unmarshal([]byte(data), &received.State); err != nil {
				return false
			}
		}
		return receive(received)
	})
}

func readSSE(body io.Reader, receive func(event, data string) bool) error {
	reader := bufio.NewReader(body)
	var event string
	var data []string
	dispatch := func() bool {
		if event == "" && len(data) == 0 {
			return true
		}
		keepReading := receive(event, strings.Join(data, "\n"))
		event = ""
		data = nil
		return keepReading
	}
	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if !dispatch() {
				return nil
			}
		} else if !strings.HasPrefix(line, ":") {
			field, value, found := strings.Cut(line, ":")
			if found && strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			switch field {
			case "event":
				event = value
			case "data":
				data = append(data, value)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			if len(data) > 0 || event != "" {
				dispatch()
			}
			return nil
		}
	}
}

func waitClient(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func isAPIError(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr)
}

func isCoreChanged(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.Code == "core_changed"
}
