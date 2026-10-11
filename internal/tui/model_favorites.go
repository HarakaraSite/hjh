package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

type modelFavoritesReadPurpose uint8

const (
	modelFavoritesReadOpen modelFavoritesReadPurpose = iota + 1
	modelFavoritesReadUnknownTab
	modelFavoritesReadRecovery
)

var errModelFavoritesProviderMismatch = errors.New("favorite response provider did not match request")

type modelFavoriteOperation struct {
	id                  uint64
	key                 string
	coreID              string
	sessionID           string
	workspace           string
	scopeID             string
	providerID          string
	modelID             string
	desired             bool
	pickerID            uint64
	selectionGeneration uint64
	putInFlight         bool
	recovering          bool
}

func modelFavoriteSnapshotKey(scopeID, providerID string) string {
	return scopeID + "\x00" + providerID
}

func modelFavoriteOperationKey(scopeID, providerID, modelID string) string {
	return scopeID + "\x00" + providerID + "\x00" + modelID
}

func modelFavoriteSet(favorites api.ModelFavorites) map[string]struct{} {
	ids := make(map[string]struct{}, len(favorites.ModelIDs))
	for _, id := range favorites.ModelIDs {
		ids[id] = struct{}{}
	}
	return ids
}

func (m *model) startModelFavoritesRead(
	pickerID, selectionGeneration uint64,
	coreID string,
	scope apiclient.Scope,
	providerID string,
	purpose modelFavoritesReadPurpose,
	operationID uint64,
) uint64 {
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.launch(job, func() tea.Msg {
		favorites, err := m.backend.ModelFavorites(ctx, scope, providerID)
		return modelFavoritesLoadedMsg{
			jobID: job.id, pickerID: pickerID, selectionGeneration: selectionGeneration,
			coreID: coreID, sessionID: scope.SessionID, workspace: scope.Workspace,
			providerID: providerID, operationID: operationID, purpose: purpose,
			favorites: favorites, err: err,
		}
	})
	return job.id
}

func (m *model) toggleModelFavorite() {
	picker := m.connectionPicker
	if picker == nil || picker.kind != modelPicker || picker.loading {
		return
	}
	options := m.connectionPickerOptions()
	if picker.index < 0 || picker.index >= len(options) {
		return
	}
	modelID := options[picker.index].id
	if picker.preview.Selection.Provider == "" {
		m.status = "resolve a provider before changing model favorites"
		return
	}
	if !picker.favoritesKnown || picker.favoritesErr != nil {
		if !picker.favoritesLoading {
			picker.favoritesLoading = true
			picker.favoritesErr = nil
			picker.favoritesJobID = m.startModelFavoritesRead(picker.pickerID, picker.generation, m.core.CoreID,
				apiclient.Scope{SessionID: picker.request.SessionID, Workspace: picker.request.Workspace},
				picker.preview.Selection.Provider, modelFavoritesReadUnknownTab, 0)
		}
		m.status = "loading favorite status for " + modelID
		m.refreshViewport(false)
		return
	}
	if picker.favoritesLoading {
		m.status = "checking favorite status for " + modelID
		m.refreshViewport(false)
		return
	}
	if op := m.pendingModelFavorite(picker, modelID); op != nil {
		if op.putInFlight {
			m.status = "saving favorite for " + modelID
			m.refreshViewport(false)
			return
		}
		if !op.recovering {
			op.recovering = true
			m.startModelFavoritesRead(op.pickerID, op.selectionGeneration, op.coreID,
				apiclient.Scope{SessionID: op.sessionID, Workspace: op.workspace},
				op.providerID, modelFavoritesReadRecovery, op.id)
		}
		m.status = "checking favorite status for " + modelID
		m.refreshViewport(false)
		return
	}
	_, isFavorite := modelFavoriteSet(picker.favorites)[modelID]
	m.modelFavoriteOpSeq++
	op := &modelFavoriteOperation{
		id:                  m.modelFavoriteOpSeq,
		coreID:              m.core.CoreID,
		sessionID:           picker.request.SessionID,
		workspace:           picker.request.Workspace,
		scopeID:             picker.favorites.ScopeID,
		providerID:          picker.preview.Selection.Provider,
		modelID:             modelID,
		desired:             !isFavorite,
		pickerID:            picker.pickerID,
		selectionGeneration: picker.generation,
	}
	op.key = modelFavoriteOperationKey(op.scopeID, op.providerID, op.modelID)
	if m.modelFavoriteOps == nil {
		m.modelFavoriteOps = make(map[string]*modelFavoriteOperation)
	}
	m.modelFavoriteOps[op.key] = op
	m.startModelFavoriteWrite(op)
	m.status = "saving favorite for " + modelID
	m.refreshViewport(false)
}

func (m *model) startModelFavoriteWrite(op *modelFavoriteOperation) {
	if op == nil {
		return
	}
	op.putInFlight = true
	op.recovering = false
	request := api.SetModelFavoriteRequest{
		CoreID: op.coreID, SessionID: op.sessionID, Workspace: op.workspace,
		ProviderID: op.providerID, ModelID: op.modelID,
	}
	desired := op.desired
	request.Favorite = &desired
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.launch(job, func() tea.Msg {
		result, err := m.backend.SetModelFavorite(ctx, request)
		return modelFavoriteSavedMsg{
			jobID: job.id, operationID: op.id, coreID: op.coreID,
			sessionID: op.sessionID, workspace: op.workspace,
			providerID: op.providerID, modelID: op.modelID, favorite: desired,
			result: result, err: err,
		}
	})
}

func (m *model) pendingModelFavorite(picker *connectionPickerState, modelID string) *modelFavoriteOperation {
	if picker == nil || picker.kind != modelPicker || !picker.favoritesKnown {
		return nil
	}
	key := modelFavoriteOperationKey(picker.favorites.ScopeID, picker.preview.Selection.Provider, modelID)
	return m.modelFavoriteOps[key]
}

func (m *model) acceptModelFavoritesRead(msg modelFavoritesLoadedMsg) {
	if msg.purpose == modelFavoritesReadRecovery {
		m.acceptModelFavoriteRecovery(msg)
		return
	}
	picker := m.connectionPicker
	if !m.matchesModelPickerRead(picker, msg) {
		return
	}
	if picker.favoritesJobID == msg.jobID {
		picker.favoritesJobID = 0
	}
	picker.favoritesLoading = false
	if msg.err != nil {
		picker.favoritesErr = msg.err
		if msg.purpose == modelFavoritesReadUnknownTab {
			m.status = "favorite status unavailable: " + msg.err.Error()
		}
		m.refreshViewport(false)
		return
	}
	if msg.favorites.ProviderID != msg.providerID {
		picker.favoritesErr = errModelFavoritesProviderMismatch
		m.refreshViewport(false)
		return
	}
	selectedID := m.selectedPickerModelID(picker)
	snapshot := m.rememberModelFavoriteSnapshot(msg.favorites)
	m.retainRemovedFavoriteCandidates(picker, snapshot)
	picker.favorites = snapshot
	picker.favoritesKnown = true
	picker.favoritesErr = nil
	m.restorePickerModelID(picker, selectedID)
	if msg.purpose == modelFavoritesReadUnknownTab {
		m.status = "favorite status loaded · press Tab again to change " + selectedID
	}
	m.refreshViewport(false)
}

func (m *model) acceptModelFavoriteRecovery(msg modelFavoritesLoadedMsg) {
	op := m.modelFavoriteOperationByID(msg.operationID)
	if op == nil || !op.recovering || op.coreID != msg.coreID || op.sessionID != msg.sessionID ||
		op.workspace != msg.workspace || op.providerID != msg.providerID || m.core.CoreID != op.coreID {
		return
	}
	if msg.err != nil {
		op.recovering = false
		if m.modelFavoriteOperationVisible(op) {
			m.status = "favorite result unknown · Tab to check · " + msg.err.Error()
			m.refreshViewport(false)
		}
		return
	}
	if msg.favorites.ProviderID != op.providerID || msg.favorites.ScopeID != op.scopeID {
		op.recovering = false
		if m.modelFavoriteOperationVisible(op) {
			m.status = "favorite result unknown · Tab to check"
			m.refreshViewport(false)
		}
		return
	}
	snapshot := m.rememberModelFavoriteSnapshot(msg.favorites)
	_, isFavorite := modelFavoriteSet(snapshot)[op.modelID]
	if m.modelFavoriteOperationVisible(op) {
		picker := m.connectionPicker
		selectedID := m.selectedPickerModelID(picker)
		m.retainRemovedFavoriteCandidates(picker, snapshot)
		picker.favorites = snapshot
		picker.favoritesKnown = true
		picker.favoritesLoading = false
		picker.favoritesErr = nil
		m.restorePickerModelID(picker, selectedID)
	}
	if isFavorite == op.desired {
		delete(m.modelFavoriteOps, op.key)
		if m.modelFavoriteOperationVisible(op) {
			m.status = favoriteSavedStatus(op)
			m.refreshViewport(false)
		} else {
			m.refreshCurrentModelFavorites(op, snapshot)
		}
		return
	}
	if current := m.modelFavoriteOps[op.key]; current == op {
		m.startModelFavoriteWrite(op)
		if m.modelFavoriteOperationVisible(op) {
			m.status = "saving favorite for " + op.modelID
			m.refreshViewport(false)
		}
	}
}

func (m *model) acceptModelFavoriteWrite(msg modelFavoriteSavedMsg) {
	op := m.modelFavoriteOperationByID(msg.operationID)
	if op == nil || op.coreID != msg.coreID || op.sessionID != msg.sessionID || op.workspace != msg.workspace ||
		op.providerID != msg.providerID || op.modelID != msg.modelID || op.desired != msg.favorite {
		return
	}
	op.putInFlight = false
	if msg.err != nil || msg.result.ProviderID != op.providerID || msg.result.ScopeID != op.scopeID ||
		msg.result.ModelID != op.modelID || msg.result.Favorite != op.desired {
		if m.modelFavoriteOperationVisible(op) {
			m.status = "favorite result unknown · Tab to check"
			m.refreshViewport(false)
		}
		return
	}
	if m.core.CoreID != op.coreID {
		delete(m.modelFavoriteOps, op.key)
		return
	}
	snapshot := m.rememberModelFavoriteSnapshot(msg.result.ModelFavorites)
	visible := m.modelFavoriteOperationVisible(op)
	if visible {
		picker := m.connectionPicker
		selectedID := m.selectedPickerModelID(picker)
		m.retainRemovedFavoriteCandidates(picker, snapshot)
		picker.favorites = snapshot
		picker.favoritesKnown = true
		picker.favoritesLoading = false
		picker.favoritesErr = nil
		m.restorePickerModelID(picker, selectedID)
	}
	delete(m.modelFavoriteOps, op.key)
	if visible {
		m.status = favoriteSavedStatus(op)
		m.refreshViewport(false)
		return
	}
	m.refreshCurrentModelFavorites(op, snapshot)
}

func (m *model) refreshCurrentModelFavorites(op *modelFavoriteOperation, snapshot api.ModelFavorites) {
	picker := m.connectionPicker
	if picker == nil || !m.pickerOpen || picker.kind != modelPicker || m.core.CoreID != op.coreID ||
		picker.generation != m.selectionGeneration || picker.request.SessionID != m.sessionID ||
		picker.request.Workspace != m.workspace || picker.preview.Selection.Provider != op.providerID ||
		!picker.favoritesKnown || picker.favorites.ScopeID != snapshot.ScopeID ||
		picker.favorites.ProviderID != snapshot.ProviderID || snapshot.ScopeID != op.scopeID ||
		snapshot.ProviderID != op.providerID {
		return
	}
	picker.favoritesLoading = true
	picker.favoritesErr = nil
	picker.favoritesJobID = m.startModelFavoritesRead(picker.pickerID, picker.generation, m.core.CoreID,
		apiclient.Scope{SessionID: picker.request.SessionID, Workspace: picker.request.Workspace},
		picker.preview.Selection.Provider, modelFavoritesReadOpen, 0)
	m.refreshViewport(false)
}

func (m *model) matchesModelPickerRead(picker *connectionPickerState, msg modelFavoritesLoadedMsg) bool {
	if picker == nil || !m.pickerOpen || picker.kind != modelPicker || picker.pickerID != msg.pickerID ||
		picker.favoritesJobID != msg.jobID ||
		picker.generation != msg.selectionGeneration || m.selectionGeneration != msg.selectionGeneration ||
		m.core.CoreID != msg.coreID || picker.request.SessionID != msg.sessionID || picker.request.Workspace != msg.workspace ||
		m.sessionID != msg.sessionID || m.workspace != msg.workspace {
		return false
	}
	if !picker.loading && picker.preview.Selection.Provider != msg.providerID {
		return false
	}
	return true
}

func (m *model) modelFavoriteOperationVisible(op *modelFavoriteOperation) bool {
	picker := m.connectionPicker
	return picker != nil && m.pickerOpen && picker.kind == modelPicker && picker.pickerID == op.pickerID &&
		picker.generation == op.selectionGeneration && m.selectionGeneration == op.selectionGeneration &&
		m.core.CoreID == op.coreID && m.sessionID == op.sessionID && m.workspace == op.workspace &&
		picker.request.SessionID == op.sessionID && picker.request.Workspace == op.workspace &&
		picker.preview.Selection.Provider == op.providerID && picker.favoritesKnown &&
		picker.favorites.ScopeID == op.scopeID && picker.favorites.ProviderID == op.providerID
}

func (m *model) modelFavoriteOperationByID(id uint64) *modelFavoriteOperation {
	for _, op := range m.modelFavoriteOps {
		if op.id == id {
			return op
		}
	}
	return nil
}

func (m *model) rememberModelFavoriteSnapshot(snapshot api.ModelFavorites) api.ModelFavorites {
	if m.modelFavoriteSnapshots == nil {
		m.modelFavoriteSnapshots = make(map[string]api.ModelFavorites)
	}
	key := modelFavoriteSnapshotKey(snapshot.ScopeID, snapshot.ProviderID)
	if current, ok := m.modelFavoriteSnapshots[key]; ok && current.Revision >= snapshot.Revision {
		return current
	}
	snapshot.ModelIDs = append([]string(nil), snapshot.ModelIDs...)
	m.modelFavoriteSnapshots[key] = snapshot
	return snapshot
}

func (m *model) retainRemovedFavoriteCandidates(picker *connectionPickerState, snapshot api.ModelFavorites) {
	if picker.retainedModels == nil {
		picker.retainedModels = make(map[string]struct{})
	}
	previous := modelFavoriteSet(picker.favorites)
	next := modelFavoriteSet(snapshot)
	for id := range previous {
		if _, stillFavorite := next[id]; stillFavorite {
			continue
		}
		if _, inCatalog := findModel(picker.models, id); !inCatalog {
			picker.retainedModels[id] = struct{}{}
		}
	}
}

func (m *model) selectedPickerModelID(picker *connectionPickerState) string {
	if picker == nil {
		return ""
	}
	options := m.connectionPickerOptions()
	if picker.index >= 0 && picker.index < len(options) {
		return options[picker.index].id
	}
	if picker.kind == modelPicker {
		return picker.preview.Selection.ModelID
	}
	return ""
}

func (m *model) restorePickerModelID(picker *connectionPickerState, selectedID string) {
	if picker == nil {
		return
	}
	options := m.connectionPickerOptions()
	for index, option := range options {
		if option.id == selectedID {
			picker.index = index
			return
		}
	}
	picker.index = picker.selectedIndex(options)
}

func favoriteSavedStatus(op *modelFavoriteOperation) string {
	if op.desired {
		return "favorite saved: " + op.modelID
	}
	return "favorite removed: " + op.modelID
}
