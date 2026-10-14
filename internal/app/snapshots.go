package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

// SnapshotOwner retains only the latest prepared wire and budget Fact for
// each execution until those details are saved to history.
type SnapshotOwner struct {
	mu      sync.Mutex
	entries map[string]*snapshotEntry
}

type snapshotEntry struct {
	sessionID       string
	rootExecutionID string
	executionID     string
	request         *ownedRequestSnapshot
	budget          *ownedBudgetSnapshot
	frozen          bool
	saving          bool
	saveDone        chan struct{}
	lastErr         error
}

type ownedRequestSnapshot struct {
	recordID string
	wireBody []byte
	dirty    bool
}

type ownedBudgetSnapshot struct {
	recordID string
	fact     contextbudget.Fact
	dirty    bool
}

// SnapshotStatus is the current status of the latest request/budget detail.
// DetailAvailable is independent of State: memory and failed retain details,
// while saved means the owner released them after the database commit.
type SnapshotStatus = contracts.SnapshotStatus

// SnapshotCapture is an independent copy of one execution's current snapshot
// state. Mutating it cannot change data retained by the owner.
type SnapshotCapture struct {
	SessionID       string
	RootExecutionID string
	ExecutionID     string
	Request         *RequestSnapshotCapture
	Budget          *BudgetSnapshotCapture
	Status          SnapshotStatus
	RequestStatus   SnapshotStatus
	BudgetStatus    SnapshotStatus
}

type RequestSnapshotCapture struct {
	RecordID string
	WireBody []byte
}

type BudgetSnapshotCapture struct {
	RecordID string
	Fact     contextbudget.Fact
}

type snapshotPersister interface {
	SaveLatestSnapshots(context.Context, string, *history.LatestRequestSnapshot, *history.LatestBudgetSnapshot) error
}

func NewSnapshotOwner() *SnapshotOwner {
	return &SnapshotOwner{entries: make(map[string]*snapshotEntry)}
}

func (o *SnapshotOwner) recordRequest(sessionID, rootExecutionID, executionID, recordID string, wireBody []byte) error {
	return o.update(sessionID, rootExecutionID, executionID, func(entry *snapshotEntry) {
		entry.request = &ownedRequestSnapshot{recordID: recordID, wireBody: cloneBytes(wireBody), dirty: true}
		entry.lastErr = nil
	})
}

// recordBudget takes ownership of the projector's Fact value. Projection
// creates its slices for this result and its consumers only read them; capture
// clones the value before exposing it to another caller.
func (o *SnapshotOwner) recordBudget(sessionID, rootExecutionID, executionID, recordID string, fact contextbudget.Fact) error {
	return o.update(sessionID, rootExecutionID, executionID, func(entry *snapshotEntry) {
		entry.budget = &ownedBudgetSnapshot{recordID: recordID, fact: fact, dirty: true}
		entry.lastErr = nil
	})
}

func (o *SnapshotOwner) update(sessionID, rootExecutionID, executionID string, apply func(*snapshotEntry)) error {
	if o == nil {
		return errors.New("app snapshot owner is unavailable")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.entries == nil {
		o.entries = make(map[string]*snapshotEntry)
	}
	entry := o.entries[executionID]
	if entry == nil {
		entry = &snapshotEntry{sessionID: sessionID, rootExecutionID: rootExecutionID, executionID: executionID}
		o.entries[executionID] = entry
	}
	if entry.frozen {
		return errors.New("app execution snapshot is frozen")
	}
	apply(entry)
	return nil
}

func (o *SnapshotOwner) status(executionID string) SnapshotStatus {
	status, _ := o.statusWithPresence(executionID)
	return status
}

func (o *SnapshotOwner) statusWithPresence(executionID string) (SnapshotStatus, bool) {
	if o == nil {
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[executionID]
	if entry == nil {
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, false
	}
	return snapshotStatus(entry), true
}

func (o *SnapshotOwner) sessionID(executionID string) (string, bool) {
	if o == nil {
		return "", false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[executionID]
	if entry == nil {
		return "", false
	}
	return entry.sessionID, true
}

func (o *SnapshotOwner) retry(ctx context.Context, store snapshotPersister, executionID string) (SnapshotStatus, error) {
	if o == nil {
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, nil
	}
	o.mu.Lock()
	entry := o.entries[executionID]
	if entry == nil {
		o.mu.Unlock()
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, nil
	}
	status := snapshotStatus(entry)
	if !entry.frozen || status.State != contracts.SnapshotSaveFailed {
		o.mu.Unlock()
		return status, nil
	}
	o.mu.Unlock()
	return o.save(ctx, store, executionID)
}

func (o *SnapshotOwner) retryFailed(ctx context.Context, store snapshotPersister) []error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	var executionIDs []string
	for executionID, entry := range o.entries {
		if entry.frozen && snapshotStatus(entry).State == contracts.SnapshotSaveFailed {
			executionIDs = append(executionIDs, executionID)
		}
	}
	o.mu.Unlock()
	sort.Strings(executionIDs)
	var failures []error
	for _, executionID := range executionIDs {
		_, err := o.save(ctx, store, executionID)
		if err != nil {
			failures = append(failures, fmt.Errorf("snapshot save failed for execution %s: %w", executionID, err))
		}
	}
	return failures
}

func (o *SnapshotOwner) deleteSession(sessionID string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for executionID, entry := range o.entries {
		if entry.sessionID == sessionID {
			delete(o.entries, executionID)
		}
	}
}

func (o *SnapshotOwner) failedExecutionIDs(rootExecutionID string) []string {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var result []string
	for executionID, entry := range o.entries {
		if entry.rootExecutionID == rootExecutionID && executionID != rootExecutionID && snapshotStatus(entry).State == contracts.SnapshotSaveFailed {
			result = append(result, executionID)
		}
	}
	sort.Strings(result)
	return result
}

func (o *SnapshotOwner) capture(executionID string) (SnapshotCapture, bool) {
	if o == nil {
		return SnapshotCapture{}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[executionID]
	if entry == nil {
		return SnapshotCapture{}, false
	}
	result := SnapshotCapture{SessionID: entry.sessionID, RootExecutionID: entry.rootExecutionID,
		ExecutionID: entry.executionID, Status: snapshotStatus(entry),
		RequestStatus: snapshotDetailStatus(entry, true), BudgetStatus: snapshotDetailStatus(entry, false)}
	if entry.request != nil {
		result.Request = &RequestSnapshotCapture{RecordID: entry.request.recordID, WireBody: cloneBytes(entry.request.wireBody)}
	}
	if entry.budget != nil {
		result.Budget = &BudgetSnapshotCapture{RecordID: entry.budget.recordID, Fact: cloneFact(entry.budget.fact)}
	}
	return result, true
}

func (o *SnapshotOwner) freeze(executionID string) (SnapshotCapture, bool) {
	if o == nil {
		return SnapshotCapture{}, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[executionID]
	if entry == nil {
		return SnapshotCapture{}, false
	}
	entry.frozen = true
	return captureEntry(entry), true
}

func (o *SnapshotOwner) save(ctx context.Context, store snapshotPersister, executionID string) (SnapshotStatus, error) {
	if o == nil {
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, nil
	}
	o.mu.Lock()
	entry := o.entries[executionID]
	if entry == nil {
		o.mu.Unlock()
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, nil
	}
	if entry.saving {
		done := entry.saveDone
		o.mu.Unlock()
		select {
		case <-ctx.Done():
			return o.status(executionID), ctx.Err()
		case <-done:
			return o.statusAndError(executionID)
		}
	}
	status := snapshotStatus(entry)
	if status.State == contracts.SnapshotSaveSaved || status.State == contracts.SnapshotSaveUnavailable {
		lastErr := entry.lastErr
		o.mu.Unlock()
		return status, lastErr
	}
	entry.frozen = true
	entry.saving = true
	entry.saveDone = make(chan struct{})
	done := entry.saveDone
	requestID, requestBody := "", []byte(nil)
	if entry.request != nil && entry.request.dirty {
		requestID, requestBody = entry.request.recordID, cloneBytes(entry.request.wireBody)
	}
	budgetID := ""
	var fact contextbudget.Fact
	if entry.budget != nil && entry.budget.dirty {
		budgetID, fact = entry.budget.recordID, cloneFact(entry.budget.fact)
	}
	o.mu.Unlock()

	var request *history.LatestRequestSnapshot
	if requestID != "" {
		request = &history.LatestRequestSnapshot{RecordID: requestID, WireBody: requestBody}
	}
	var budget *history.LatestBudgetSnapshot
	if budgetID != "" {
		factJSON, err := json.Marshal(fact)
		if err != nil {
			o.finishSave(executionID, done, err)
			return o.statusAndError(executionID)
		}
		budget = &history.LatestBudgetSnapshot{RecordID: budgetID, Fact: factJSON}
	}
	err := store.SaveLatestSnapshots(ctx, executionID, request, budget)
	o.finishSave(executionID, done, err)
	return o.statusAndError(executionID)
}

func (o *SnapshotOwner) finishSave(executionID string, done chan struct{}, saveErr error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[executionID]
	if entry == nil || entry.saveDone != done {
		return
	}
	if saveErr == nil {
		if entry.request != nil && entry.request.dirty {
			entry.request.wireBody = nil
			entry.request.dirty = false
		}
		if entry.budget != nil && entry.budget.dirty {
			entry.budget.fact = contextbudget.Fact{}
			entry.budget.dirty = false
		}
	}
	entry.lastErr = saveErr
	entry.saving = false
	close(done)
}

func (o *SnapshotOwner) statusAndError(executionID string) (SnapshotStatus, error) {
	if o == nil {
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	entry := o.entries[executionID]
	if entry == nil {
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}, nil
	}
	return snapshotStatus(entry), entry.lastErr
}

func snapshotStatus(entry *snapshotEntry) SnapshotStatus {
	status := SnapshotStatus{ExecutionID: entry.executionID, Frozen: entry.frozen}
	status.DetailAvailable = entry.request != nil || entry.budget != nil
	switch {
	case !status.DetailAvailable:
		status.State = contracts.SnapshotSaveUnavailable
	case entry.lastErr != nil:
		status.State = contracts.SnapshotSaveFailed
		status.Error = entry.lastErr.Error()
	case (entry.request != nil && entry.request.dirty) || (entry.budget != nil && entry.budget.dirty):
		status.State = contracts.SnapshotSaveMemory
	default:
		status.State = contracts.SnapshotSaveSaved
	}
	return status
}

func snapshotDetailStatus(entry *snapshotEntry, request bool) SnapshotStatus {
	status := SnapshotStatus{ExecutionID: entry.executionID, Frozen: entry.frozen}
	dirty := false
	if request && entry.request != nil {
		status.DetailAvailable = true
		dirty = entry.request.dirty
	} else if !request && entry.budget != nil {
		status.DetailAvailable = true
		dirty = entry.budget.dirty
	}
	switch {
	case !status.DetailAvailable:
		status.State = contracts.SnapshotSaveUnavailable
	case entry.lastErr != nil:
		status.State = contracts.SnapshotSaveFailed
		status.Error = entry.lastErr.Error()
	case dirty:
		status.State = contracts.SnapshotSaveMemory
	default:
		status.State = contracts.SnapshotSaveSaved
	}
	return status
}

func captureEntry(entry *snapshotEntry) SnapshotCapture {
	result := SnapshotCapture{SessionID: entry.sessionID, RootExecutionID: entry.rootExecutionID,
		ExecutionID: entry.executionID, Status: snapshotStatus(entry),
		RequestStatus: snapshotDetailStatus(entry, true), BudgetStatus: snapshotDetailStatus(entry, false)}
	if entry.request != nil {
		result.Request = &RequestSnapshotCapture{RecordID: entry.request.recordID, WireBody: cloneBytes(entry.request.wireBody)}
	}
	if entry.budget != nil {
		result.Budget = &BudgetSnapshotCapture{RecordID: entry.budget.recordID, Fact: cloneFact(entry.budget.fact)}
	}
	return result
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

func cloneFact(value contextbudget.Fact) contextbudget.Fact {
	value.Budget.ContextTokens = cloneInt64(value.Budget.ContextTokens)
	value.Budget.InputTokens = cloneInt64(value.Budget.InputTokens)
	value.Budget.OutputReserve = cloneInt64(value.Budget.OutputReserve)
	value.Budget.InputRatio = cloneFloat64(value.Budget.InputRatio)
	value.Budget.HistoryTokens = cloneInt64(value.Budget.HistoryTokens)
	value.Budget.Limit = cloneInt64(value.Budget.Limit)
	value.HistoryWireBytes = cloneInt64(value.HistoryWireBytes)
	value.HistoryEstimatedTokens = cloneInt64(value.HistoryEstimatedTokens)
	value.Sources = append([]contextbudget.Source(nil), value.Sources...)
	value.OmittedCurrent = append([]int(nil), value.OmittedCurrent...)
	value.OmittedCurrentSources = append([]contextbudget.Source(nil), value.OmittedCurrentSources...)
	value.History.Turns = append([]contextbudget.HistoryTurnFact(nil), value.History.Turns...)
	for index := range value.History.Turns {
		value.History.Turns[index].SelectedSources = append([]contextbudget.Source(nil), value.History.Turns[index].SelectedSources...)
		value.History.Turns[index].OmittedSources = append([]contextbudget.Source(nil), value.History.Turns[index].OmittedSources...)
	}
	return value
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
