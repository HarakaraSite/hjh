package tui

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/tui/renderer"
)

type connectionOption struct {
	id       string
	label    string
	detail   string
	favorite bool
}

func (m *model) selectionRequest() api.SelectionRequest {
	request := api.SelectionRequest{SessionID: m.sessionID, Workspace: m.workspace}
	if m.roleSet {
		request.Role = m.roleID
	}
	if m.providerID != "" {
		request.ProviderID = m.providerID
	}
	if m.modelSet {
		request.ModelID = m.modelID
	}
	if m.effort != "" {
		request.Effort = m.effort
	}
	if m.authProfile != "" {
		request.AuthProfile = m.authProfile
	}
	return request
}

func (m *model) openConnectionPicker(kind connectionPickerKind) {
	m.closeSessionPicker()
	m.closeConnectionPicker()
	m.sealCurrentFrame()
	m.connectionPickerSeq++
	m.pickerOpen = true
	m.viewport.SetYOffset(0)
	picker := &connectionPickerState{
		kind: kind, request: m.selectionRequest(), generation: m.selectionGeneration,
		pickerID: m.connectionPickerSeq, loading: true, retainedModels: make(map[string]struct{}),
	}
	m.connectionPicker = picker
	m.status = "loading " + string(kind) + " selection and catalog"
	m.loadConnectionPicker(picker)
	m.refreshViewport(true)
}

func (m *model) closeConnectionPicker() {
	if m.connectionPicker != nil {
		m.cancelJob(m.connectionPicker.jobID)
		m.cancelJob(m.connectionPicker.catalogJobID)
		m.cancelJob(m.connectionPicker.favoritesJobID)
	}
	m.connectionPickerSeq++
	m.connectionPicker = nil
	if m.pickerJobID == 0 {
		m.pickerOpen = false
	}
}

func (m *model) loadConnectionPicker(picker *connectionPickerState) {
	if picker == nil || m.connectionPicker != picker {
		return
	}
	if picker.jobID != 0 {
		m.cancelJob(picker.jobID)
	}
	m.cancelJob(picker.catalogJobID)
	m.cancelJob(picker.favoritesJobID)
	request := picker.request
	kind := picker.kind
	generation := m.selectionGeneration
	picker.generation = generation
	picker.loading = true
	picker.err = nil
	picker.previewErr = nil
	picker.providers = nil
	picker.models = nil
	picker.favorites = api.ModelFavorites{}
	picker.favoritesKnown = false
	picker.favoritesLoading = picker.kind == modelPicker
	picker.favoritesErr = nil
	picker.retainedModels = make(map[string]struct{})
	picker.modelsLoading = picker.kind == modelPicker
	picker.catalogJobID = 0
	picker.favoritesJobID = 0
	picker.jobID = 0
	pickerID := picker.pickerID
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	picker.jobID = job.id
	m.status = "loading " + string(kind) + " selection and catalog"
	saveTail := m.selectionSaveTail
	m.launch(job, func() tea.Msg {
		loaded := connectionPickerLoadedMsg{jobID: job.id, generation: generation, pickerID: pickerID, request: request}
		if err := waitForSelectionSave(ctx, saveTail); err != nil {
			loaded.err = err
			return loaded
		}
		scope := apiclient.Scope{SessionID: request.SessionID, Workspace: request.Workspace}
		if kind == providerPicker {
			preview, previewErr := m.backend.Selection(ctx, request)
			loaded.previewErr = previewErr
			if previewErr == nil {
				loaded.preview = preview
			}
			loaded.providers, loaded.err = m.backend.Providers(ctx, scope)
			return loaded
		}
		preview, err := m.backend.Selection(ctx, request)
		if err != nil {
			loaded.err = err
			return loaded
		}
		loaded.preview = preview
		if kind == effortPicker && preview.Selection.Provider != "" {
			result, err := m.backend.ModelsWithAuth(ctx, scope, preview.Selection.Provider, preview.Selection.AuthProfile)
			loaded.models, loaded.err = result.Models, err
		}
		return loaded
	})
}

func (m *model) startModelCatalogRead(picker *connectionPickerState, scope apiclient.Scope) {
	if picker == nil || m.connectionPicker != picker {
		return
	}
	pickerID, generation := picker.pickerID, picker.generation
	coreID, providerID := m.core.CoreID, picker.preview.Selection.Provider
	authProfile := picker.preview.Selection.AuthProfile
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	picker.catalogJobID = job.id
	m.launch(job, func() tea.Msg {
		result, err := m.backend.ModelsWithAuth(ctx, scope, providerID, authProfile)
		return modelCatalogLoadedMsg{
			jobID: job.id, pickerID: pickerID, selectionGeneration: generation,
			coreID: coreID, sessionID: scope.SessionID, workspace: scope.Workspace,
			providerID: providerID, models: result.Models, authOwnerID: result.AuthOwnerID,
			authAccountID: result.AuthAccountID, err: err,
		}
	})
}

func (m *model) acceptModelCatalog(msg modelCatalogLoadedMsg) {
	picker := m.connectionPicker
	if picker == nil || !m.pickerOpen || picker.kind != modelPicker || picker.pickerID != msg.pickerID ||
		picker.catalogJobID != msg.jobID ||
		picker.generation != msg.selectionGeneration || m.selectionGeneration != msg.selectionGeneration ||
		m.core.CoreID != msg.coreID || m.sessionID != msg.sessionID || m.workspace != msg.workspace ||
		picker.request.SessionID != msg.sessionID || picker.request.Workspace != msg.workspace ||
		picker.preview.Selection.Provider != msg.providerID {
		return
	}
	picker.catalogJobID = 0
	selectedID := m.selectedPickerModelID(picker)
	picker.modelsLoading = false
	if msg.err != nil {
		picker.err = msg.err
		m.status = "model picker failed: " + msg.err.Error()
		m.refreshViewport(false)
		return
	}
	picker.err = nil
	picker.models = msg.models
	picker.authOwnerID = msg.authOwnerID
	picker.authAccountID = msg.authAccountID
	m.restorePickerModelID(picker, selectedID)
	m.refreshViewport(false)
}

func (m *model) nextSelectionChanged() {
	m.selectionGeneration++
	m.requestFooterSelection()
	if m.connectionPicker == nil {
		return
	}
	picker := m.connectionPicker
	picker.request = m.selectionRequest()
	picker.index = 0
	m.loadConnectionPicker(picker)
}

func (m *model) connectionPickerOptions() []connectionOption {
	picker := m.connectionPicker
	if picker == nil || picker.loading || picker.err != nil {
		return nil
	}
	options := make([]connectionOption, 0)
	switch picker.kind {
	case providerPicker:
		for _, provider := range picker.providers {
			detail := provider.API + " · auth: " + provider.Auth.Kind
			if provider.Auth.Profile != "" {
				detail += " / " + provider.Auth.Profile
			}
			if provider.DefaultModel != "" {
				detail += " · default model: " + provider.DefaultModel
			}
			if provider.DefaultEffort != "" {
				detail += " · default effort: " + provider.DefaultEffort
			}
			label := provider.DisplayName
			if label == "" {
				label = provider.ID
			}
			options = append(options, connectionOption{id: provider.ID, label: label + " · " + provider.ID, detail: detail})
		}
	case modelPicker:
		options = modelPickerOptions(picker)
	case effortPicker:
		modelInfo, found := findModel(picker.models, picker.preview.Selection.ModelID)
		options = append(options, connectionOption{id: "auto", label: "auto", detail: "automatic effort"})
		if found {
			for _, effort := range modelInfo.Efforts {
				if effort == "" || containsOption(options, effort) {
					continue
				}
				options = append(options, connectionOption{id: effort, label: effort, detail: modelInfo.EffortSource})
			}
		}
		current := picker.preview.Selection.Effort
		if current == "" {
			current = "auto"
		}
		if !containsOption(options, current) {
			options = append(options, connectionOption{id: current, label: current, detail: "current next-run value · not listed in catalog"})
		}
	}
	return options
}

func modelPickerOptions(picker *connectionPickerState) []connectionOption {
	if picker == nil {
		return nil
	}
	favoriteIDs := make(map[string]struct{}, len(picker.favorites.ModelIDs))
	if picker.favoritesKnown {
		for _, id := range picker.favorites.ModelIDs {
			favoriteIDs[id] = struct{}{}
		}
	}
	query := strings.ToLower(picker.query)
	candidates := make([]connectionOption, 0, len(picker.models)+len(favoriteIDs)+len(picker.retainedModels)+1)
	seen := make(map[string]struct{}, cap(candidates))
	appendCandidate := func(id, name, detail string, inCatalog bool) {
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		if query != "" && !strings.Contains(strings.ToLower(id+" "+name), query) {
			return
		}
		label := id
		if name != "" && name != id {
			label += " · " + name
		}
		_, favorite := favoriteIDs[id]
		if !picker.favoritesKnown {
			favorite = false
		}
		if !inCatalog {
			if favorite {
				detail = "favorite · not in catalog"
			} else if detail == "" {
				detail = "not in catalog"
			}
		}
		if id == picker.preview.Selection.ModelID {
			if detail == "" {
				detail = "current next-run model"
			} else if !strings.Contains(detail, "current") {
				detail += " · current next-run model"
			}
		}
		candidates = append(candidates, connectionOption{id: id, label: label, detail: detail, favorite: favorite})
	}
	for _, model := range picker.models {
		detail := ""
		if model.ID == picker.preview.Selection.ModelID {
			detail = "current next-run model"
		}
		appendCandidate(model.ID, model.Name, detail, true)
	}
	extraFavorites := make([]string, 0, len(favoriteIDs))
	for id := range favoriteIDs {
		if _, exists := seen[id]; !exists {
			extraFavorites = append(extraFavorites, id)
		}
	}
	sort.Strings(extraFavorites)
	for _, id := range extraFavorites {
		appendCandidate(id, "", "", false)
	}
	retained := make([]string, 0, len(picker.retainedModels))
	for id := range picker.retainedModels {
		if _, exists := seen[id]; !exists {
			retained = append(retained, id)
		}
	}
	sort.Strings(retained)
	for _, id := range retained {
		detail := "no longer a favorite · not in catalog"
		if id == picker.preview.Selection.ModelID {
			detail = "current picker candidate · not in catalog"
		}
		appendCandidate(id, "", detail, false)
	}
	currentID := picker.preview.Selection.ModelID
	if currentID != "" {
		if _, exists := seen[currentID]; !exists {
			appendCandidate(currentID, "", "current model · not in catalog", false)
		}
	}
	if !picker.favoritesKnown {
		return candidates
	}
	ordered := make([]connectionOption, 0, len(candidates))
	for _, option := range candidates {
		if option.favorite {
			ordered = append(ordered, option)
		}
	}
	for _, option := range candidates {
		if !option.favorite {
			ordered = append(ordered, option)
		}
	}
	return ordered
}

func (picker *connectionPickerState) queryMatches(value string) bool {
	return picker == nil || picker.query == "" || strings.Contains(strings.ToLower(value), strings.ToLower(picker.query))
}

func (picker *connectionPickerState) selectedIndex(options []connectionOption) int {
	if picker == nil {
		return 0
	}
	want := ""
	switch picker.kind {
	case providerPicker:
		want = picker.preview.Selection.Provider
	case modelPicker:
		want = picker.preview.Selection.ModelID
	case effortPicker:
		want = picker.preview.Selection.Effort
		if want == "" {
			want = "auto"
		}
	}
	for index, option := range options {
		if option.id == want {
			return index
		}
	}
	return 0
}

func containsOption(options []connectionOption, id string) bool {
	for _, option := range options {
		if option.id == id {
			return true
		}
	}
	return false
}

func findModel(models []api.ModelInfo, id string) (api.ModelInfo, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return api.ModelInfo{}, false
}

func (m *model) connectionPickerRows() ([]string, []int) {
	picker := m.connectionPicker
	width := max(1, m.width)
	if picker == nil {
		return nil, nil
	}
	header := string(picker.kind) + " for next run · arrows move · Enter selects · Esc closes"
	if picker.loading {
		return renderer.WrapText(header+"\nLoading…", width), nil
	}
	if picker.err != nil {
		return renderer.WrapText(header+"\n"+picker.err.Error(), width), nil
	}
	if picker.kind == modelPicker {
		header = "Models · type to search ID/name · ↑/↓, PgUp/PgDn, Home/End · Enter selects · Tab favorite · Esc closes"
		if picker.query != "" {
			header += "\nsearch: " + picker.query
		}
		if picker.modelsLoading {
			header += "\ncatalog: loading…"
		}
		if picker.favoritesLoading {
			header += "\nfavorites: loading…"
		} else if picker.favoritesErr != nil {
			header += "\nfavorites unavailable: " + picker.favoritesErr.Error() + " · Tab to retry"
		} else if !picker.favoritesKnown {
			header += "\nfavorites not loaded · Tab to load"
		}
	}
	if picker.kind == effortPicker {
		modelInfo, found := findModel(picker.models, picker.preview.Selection.ModelID)
		source := "no catalog information"
		if found && modelInfo.EffortSource != "" {
			source = "catalog: " + modelInfo.EffortSource
		} else if found && len(modelInfo.Efforts) > 0 {
			source = "catalog"
		}
		header = "Effort · " + picker.preview.Selection.ModelID + " · " + source + " · /effort VALUE is also available"
	}
	preview := picker.preview
	rows := renderer.WrapText(header, width)
	if picker.previewErr != nil {
		rows = append(rows, renderer.WrapText("Current selection preview unavailable: "+picker.previewErr.Error(), width)...)
		rows = append(rows, renderer.WrapText("Choose a defined provider to continue.", width)...)
	} else {
		selected := preview.Selection.Provider + " / " + preview.Selection.ModelID + " · effort: " + preview.Selection.Effort + " · role: " + preview.Role
		rows = append(rows, renderer.WrapText("resolved next run: "+strings.TrimSpace(selected), width)...)
	}
	options := m.connectionPickerOptions()
	starts := make([]int, len(options))
	for index, option := range options {
		rows = append(rows, "")
		starts[index] = len(rows)
		marker := "  "
		if index == picker.index {
			marker = "▶ "
		}
		favoriteMarker := "  "
		if option.favorite {
			favoriteMarker = "★ "
		}
		rows = append(rows, renderer.WrapText(marker+favoriteMarker+option.label, width)...)
		if option.detail != "" {
			rows = append(rows, renderer.WrapText("  "+option.detail, width)...)
		}
		if picker.kind == modelPicker {
			if op := m.pendingModelFavorite(picker, option.id); op != nil {
				message := "saving favorite…"
				if op.recovering {
					message = "checking favorite…"
				} else if !op.putInFlight {
					message = "favorite result unknown · Tab to check"
				}
				rows = append(rows, renderer.WrapText("  "+message, width)...)
			}
		}
	}
	if len(options) == 0 {
		message := "No catalog candidates. Direct IDs remain available."
		if picker.kind == modelPicker && picker.query != "" {
			message = "No models match the search. Direct /model MODEL_ID remains available."
		}
		rows = append(rows, "")
		rows = append(rows, renderer.WrapText(message, width)...)
	}
	return rows, starts
}

func (m *model) moveConnectionPicker(delta int) {
	if m.connectionPicker == nil {
		return
	}
	options := m.connectionPickerOptions()
	if len(options) == 0 {
		return
	}
	m.connectionPicker.index = min(len(options)-1, max(0, m.connectionPicker.index+delta))
	m.refreshViewport(false)
}

func (m *model) handleConnectionPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	picker := m.connectionPicker
	if picker == nil {
		return nil
	}
	switch msg.String() {
	case "esc":
		m.closeConnectionPicker()
		m.status = "ready"
	case "tab":
		if picker.kind == modelPicker {
			m.toggleModelFavorite()
		}
	case "enter":
		m.selectConnectionOption()
	case "up":
		m.moveConnectionPicker(-1)
	case "down":
		m.moveConnectionPicker(1)
	case "pgup", "alt+up":
		m.moveConnectionPicker(-max(1, m.viewport.Height()/4))
	case "pgdown", "alt+down":
		m.moveConnectionPicker(max(1, m.viewport.Height()/4))
	case "home":
		picker.index = 0
		m.refreshViewport(false)
	case "end":
		picker.index = max(0, len(m.connectionPickerOptions())-1)
		m.refreshViewport(false)
	case "backspace", "ctrl+h":
		if picker.kind == modelPicker && picker.query != "" {
			_, size := utf8.DecodeLastRuneInString(picker.query)
			picker.query = picker.query[:len(picker.query)-size]
			picker.index = picker.selectedIndex(m.connectionPickerOptions())
			m.refreshViewport(false)
		}
	default:
		key := msg.Key()
		if picker.kind == modelPicker && key.Mod == 0 && key.Text != "" {
			picker.query += key.Text
			picker.index = 0
			m.refreshViewport(false)
		}
	}
	return nil
}

func (m *model) appendModelQuery(text string) {
	if m.connectionPicker == nil || m.connectionPicker.kind != modelPicker || text == "" {
		return
	}
	m.connectionPicker.query += text
	m.connectionPicker.index = 0
	m.refreshViewport(false)
}

func (m *model) selectConnectionOption() {
	picker := m.connectionPicker
	if picker == nil || picker.loading {
		return
	}
	options := m.connectionPickerOptions()
	if picker.index < 0 || picker.index >= len(options) {
		m.status = "no catalog candidate selected · direct commands remain available"
		return
	}
	option := options[picker.index]
	current := ""
	switch picker.kind {
	case providerPicker:
		current = picker.preview.Selection.Provider
	case modelPicker:
		current = picker.preview.Selection.ModelID
	case effortPicker:
		current = picker.preview.Selection.Effort
		if current == "" {
			current = "auto"
		}
	}
	kind := picker.kind
	m.closeConnectionPicker()
	if kind == providerPicker && option.id == current {
		m.rememberSelection()
		m.status = "next-run " + string(kind) + " unchanged: " + option.id
		return
	}
	switch kind {
	case providerPicker:
		m.setProvider(option.id)
	case modelPicker:
		m.applyModelSelection(option.id, current)
	case effortPicker:
		m.setEffort(option.id)
	}
}

func (m *model) setProvider(providerID string) {
	before := m.selectionRequest()
	m.providerID = providerID
	m.modelID, m.effort, m.authProfile = "", "", ""
	m.modelSet = false
	m.rememberSelection()
	if before != m.selectionRequest() {
		m.nextSelectionChanged()
	}
	m.status = "next connection: " + m.providerID + " · destination defaults; use /model and /effort to override"
}

func (m *model) setModel(modelID string) {
	current := m.modelID
	if !m.modelSet {
		if m.footerSelectionSet {
			current = m.footerSelection.Selection.ModelID
		} else {
			current = m.lastSelection.ModelID
		}
	}
	m.applyModelSelection(modelID, current)
}

func (m *model) applyModelSelection(modelID, currentModelID string) {
	before := m.selectionRequest()
	if modelID != currentModelID {
		m.effort = "auto"
	}
	m.modelID = modelID
	m.modelSet = true
	m.rememberSelection()
	if before != m.selectionRequest() {
		m.nextSelectionChanged()
	}
	m.status = "model for next run: " + m.modelID
}

func (m *model) setEffort(effort string) {
	before := m.selectionRequest()
	m.effort = effort
	m.rememberSelection()
	if before != m.selectionRequest() {
		m.nextSelectionChanged()
	}
	m.status = "effort for next run: " + m.effort
}
