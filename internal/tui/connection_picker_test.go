package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/contracts"
)

func loadedConnectionPicker(t *testing.T, backend *fakeBackend, m *model, messages <-chan tea.Msg) {
	t.Helper()
	loaded := false
	for {
		msg := receive(t, messages)
		if _, ok := msg.(connectionPickerLoadedMsg); ok {
			loaded = true
		}
		update(m, msg)
		if loaded && m.connectionPicker != nil &&
			(m.connectionPicker.kind != modelPicker || (!m.connectionPicker.modelsLoading && !m.connectionPicker.favoritesLoading)) {
			return
		}
	}
}

func TestModelPickerSearchSelectionKeepsDraftAndUsesResolvedAuthProfile(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{
			Role: "reviewer",
			Selection: contracts.ModelSelection{
				Provider: "chatgpt", ModelID: "gpt-6.1-sol", Effort: "medium", AuthProfile: "saved-account",
			},
		},
		models: []api.ModelInfo{
			{ID: "gpt-6.1-sol", Name: "Workhorse"},
			{ID: "local/日本語", Name: "日本語モデル"},
		},
		modelsCalls: make(chan modelsCall, 1),
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("draft must stay untouched")
	update(m, tea.WindowSizeMsg{Width: 38, Height: 14})
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)

	call := <-backend.modelsCalls
	if call.providerID != "chatgpt" || call.authProfile != "saved-account" || call.scope.Workspace != "/work" {
		t.Fatalf("catalog did not use resolved next-run route/profile: %+v", call)
	}
	if m.connectionPicker.index != 0 || m.connectionPickerOptions()[m.connectionPicker.index].id != "gpt-6.1-sol" {
		t.Fatalf("current model was not initially selected: index=%d options=%+v", m.connectionPicker.index, m.connectionPickerOptions())
	}

	update(m, tea.PasteMsg{Content: "日本語"})
	options := m.connectionPickerOptions()
	if len(options) != 1 || options[0].id != "local/日本語" {
		t.Fatalf("pasted Unicode search did not filter model ID/name: %+v", options)
	}
	if got := m.textarea.Value(); got != "draft must stay untouched" {
		t.Fatalf("picker search changed draft: %q", got)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.connectionPicker.query != "日本" {
		t.Fatalf("backspace did not remove the final UTF-8 rune: %q", m.connectionPicker.query)
	}
	update(m, tea.KeyPressMsg{Code: '語', Text: "語"})
	if m.connectionPicker.query != "日本語" {
		t.Fatalf("keyboard text was not added to model search: %q", m.connectionPicker.query)
	}
	m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if m.textarea.Value() != "" || m.connectionPicker.query != "日本語" {
		t.Fatalf("Ctrl+C did not clear only the draft: draft=%q query=%q", m.textarea.Value(), m.connectionPicker.query)
	}
	m.textarea.SetValue("draft after selection")
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.connectionPicker != nil || m.pickerOpen || m.modelID != "local/日本語" || !m.modelSet || m.textarea.Value() != "draft after selection" {
		t.Fatalf("model selection altered the wrong state: picker=%v open=%v model=%q set=%v draft=%q", m.connectionPicker != nil, m.pickerOpen, m.modelID, m.modelSet, m.textarea.Value())
	}
}

func TestPickerConfirmsInheritedModelAndEffortBeforeChangingRole(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1),
		selection: api.SelectionPreview{Role: "reviewer", Selection: contracts.ModelSelection{
			Provider: "provider", ModelID: "inherited-model", Effort: "low",
		}},
		models: []api.ModelInfo{{ID: "inherited-model", Efforts: []string{"low", "medium"}, EffortSource: "config"}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.openConnectionPicker(effortPicker)
	loadedConnectionPicker(t, backend, m, messages)
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.handleCommand("/role writer")
	m.startRun("continue with the confirmed model and effort")
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{RunID: call.request.RunID}, nil)
	if call.request.Role != "writer" || call.request.ModelID != "inherited-model" || call.request.Effort != "low" {
		t.Fatalf("confirming inherited values did not pin them for the next role: %+v", call.request)
	}
}

func TestProviderAndEffortPickerShowResolvedDetailsAndCatalogValues(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{Role: "default", Selection: contracts.ModelSelection{
			Provider: "openai-chatgpt", ModelID: "gpt-6.1-sol", Effort: "custom", AuthProfile: "account-2",
		}},
		providers: []api.ProviderDefinition{
			{ID: "openai-chatgpt", DisplayName: "OpenAI ChatGPT", API: "responses", Auth: api.ProviderAuth{Kind: "chatgpt", Profile: "account-2"}, DefaultModel: "gpt-6.1-sol"},
			{ID: "opencode-go", DisplayName: "OpenCode Go", API: "chat_completions", Auth: api.ProviderAuth{Kind: "api_key"}, DefaultModel: "kimi-k2.5"},
		},
		models: []api.ModelInfo{{ID: "gpt-6.1-sol", Efforts: []string{"low", "ultra"}, EffortSource: "catalog"}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("existing draft")
	m.openConnectionPicker(providerPicker)
	loadedConnectionPicker(t, backend, m, messages)
	rows, _ := m.connectionPickerRows()
	providerView := strings.Join(rows, "\n")
	for _, value := range []string{"OpenAI ChatGPT", "openai-chatgpt", "responses", "chatgpt / account-2", "gpt-6.1-sol"} {
		if !strings.Contains(providerView, value) {
			t.Fatalf("provider option omitted %q: %s", value, providerView)
		}
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.connectionPicker != nil || m.providerID != "opencode-go" || m.modelSet || m.modelID != "" || m.effort != "" || m.authProfile != "" {
		t.Fatalf("provider selection did not apply the shared reset behavior: picker=%v provider=%q model=%q modelSet=%v effort=%q auth=%q", m.connectionPicker != nil, m.providerID, m.modelID, m.modelSet, m.effort, m.authProfile)
	}

	backend.selection.Selection.Provider = "opencode-go"
	backend.selection.Selection.AuthProfile = "go-account"
	backend.selection.Selection.Effort = ""
	m.openConnectionPicker(effortPicker)
	loadedConnectionPicker(t, backend, m, messages)
	options := m.connectionPickerOptions()
	var ids []string
	for _, option := range options {
		ids = append(ids, option.id)
	}
	if strings.Join(ids, ",") != "auto,low,ultra" {
		t.Fatalf("effort picker did not preserve auto and catalog values: %v", ids)
	}
	rows, _ = m.connectionPickerRows()
	if !strings.Contains(strings.Join(rows, "\n"), "catalog: catalog") {
		t.Fatalf("effort source is not visible: %v", rows)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.connectionPicker != nil || m.effort != "auto" {
		t.Fatalf("auto did not replace an inherited/default effort: picker=%v effort=%q", m.connectionPicker != nil, m.effort)
	}
	m.openConnectionPicker(effortPicker)
	loadedConnectionPicker(t, backend, m, messages)
	update(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.connectionPicker != nil || m.effort != "ultra" {
		t.Fatalf("catalog effort selection did not apply to next run: picker=%v effort=%q", m.connectionPicker != nil, m.effort)
	}
	if m.textarea.Value() != "existing draft" {
		t.Fatalf("opening and closing connection pickers changed draft: %q", m.textarea.Value())
	}
}

func TestPickerEscapeIgnoresLatePreviewAndDirectUnknownModelRemainsAllowed(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	backend := &fakeBackend{
		core:           api.Core{CoreID: "core-1"},
		selectionCalls: make(chan api.SelectionRequest, 1),
		selectionFunc: func(context.Context, api.SelectionRequest) (api.SelectionPreview, error) {
			started <- struct{}{}
			<-release
			return api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-a"}}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("draft")
	m.openConnectionPicker(modelPicker)
	<-started
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	close(release)
	late := receive(t, messages)
	update(m, late)
	if m.pickerOpen || m.connectionPicker != nil {
		t.Fatal("late preview reopened a dismissed picker")
	}

	m.handleCommand("/model user-defined-model")
	if !m.modelSet || m.modelID != "user-defined-model" {
		t.Fatalf("direct unknown model ID was rejected: set=%v model=%q", m.modelSet, m.modelID)
	}
	if m.textarea.Value() != "draft" {
		t.Fatalf("direct model selection changed draft: %q", m.textarea.Value())
	}
}

func TestProviderPickerCanRecoverFromUndefinedSavedProvider(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"},
		selectionFunc: func(context.Context, api.SelectionRequest) (api.SelectionPreview, error) {
			return api.SelectionPreview{}, errors.New("saved provider is no longer defined")
		},
		providers: []api.ProviderDefinition{{
			ID: "available-provider", DisplayName: "Available", API: "responses",
			Auth: api.ProviderAuth{Kind: "api_key"}, DefaultModel: "model-default",
		}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, ProviderID: "removed-provider"})
	defer func() { cancel(); m.shutdown() }()
	m.openConnectionPicker(providerPicker)
	loadedConnectionPicker(t, backend, m, messages)
	if m.connectionPicker.loading || m.connectionPicker.err != nil || m.connectionPicker.previewErr == nil {
		t.Fatalf("preview failure blocked provider list: %+v", m.connectionPicker)
	}
	rows, _ := m.connectionPickerRows()
	view := strings.Join(rows, "\n")
	if !strings.Contains(view, "saved provider is no longer defined") || !strings.Contains(view, "available-provider") {
		t.Fatalf("provider picker did not show recovery path: %s", view)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.connectionPicker != nil || m.providerID != "available-provider" || m.modelSet {
		t.Fatalf("selecting a valid provider did not replace the stale route: picker=%v provider=%q modelSet=%v", m.connectionPicker != nil, m.providerID, m.modelSet)
	}
}

func TestModelsListingUsesResolvedAuthProfileForRequestedProvider(t *testing.T) {
	backend := &fakeBackend{
		core:           api.Core{CoreID: "core-1"},
		selectionCalls: make(chan api.SelectionRequest, 2),
		modelsCalls:    make(chan modelsCall, 1),
		selectionFunc: func(_ context.Context, request api.SelectionRequest) (api.SelectionPreview, error) {
			preview := api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "current", AuthProfile: "current-account"}}
			if request.ProviderID == "other" {
				preview.Selection.Provider = "other"
				preview.Selection.AuthProfile = "other-default"
			}
			return preview, nil
		},
		models: []api.ModelInfo{{ID: "other/model", Name: "Other Model"}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/models other")
	first := <-backend.selectionCalls
	if first.ProviderID != "" {
		t.Fatalf("initial preview should describe current route: %+v", first)
	}
	second := <-backend.selectionCalls
	if second.ProviderID != "other" || second.AuthProfile != "" {
		t.Fatalf("explicit alternate route preview did not clear unrelated route overrides: %+v", second)
	}
	call := <-backend.modelsCalls
	if call.providerID != "other" || call.authProfile != "other-default" {
		t.Fatalf("models listing did not use resolved route/profile: %+v", call)
	}
	msg := receive(t, messages)
	update(m, msg)
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].text, "other/model") {
		t.Fatalf("models listing omitted catalog output: %+v", m.entries)
	}
}

func TestModelsExplicitProviderCanRecoverFromUndefinedCurrentProvider(t *testing.T) {
	requests := make(chan api.SelectionRequest, 2)
	backend := &fakeBackend{
		core:           api.Core{CoreID: "core-1"},
		selectionCalls: requests,
		modelsCalls:    make(chan modelsCall, 1),
		selectionFunc: func(_ context.Context, request api.SelectionRequest) (api.SelectionPreview, error) {
			if request.ProviderID == "available-provider" {
				return api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "available-provider", AuthProfile: "available-account"}}, nil
			}
			return api.SelectionPreview{}, errors.New("saved provider is no longer defined")
		},
		models: []api.ModelInfo{{ID: "available/model", Name: "Available Model"}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, ProviderID: "removed-provider"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/models available-provider")
	if request := <-requests; request.ProviderID != "removed-provider" {
		t.Fatalf("listing did not first preview the current route: %+v", request)
	}
	if request := <-requests; request.ProviderID != "available-provider" || request.ModelID != "" || request.Effort != "" || request.AuthProfile != "" {
		t.Fatalf("explicit route retry retained unrelated overrides: %+v", request)
	}
	call := <-backend.modelsCalls
	if call.providerID != "available-provider" || call.authProfile != "available-account" {
		t.Fatalf("explicit provider listing did not resolve its route/profile: %+v", call)
	}
	update(m, receive(t, messages))
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].text, "available/model") {
		t.Fatalf("explicit route listing did not complete: %+v", m.entries)
	}
}

func TestModelsWithoutProviderArgumentUsesPreviewRouteInsteadOfLastRun(t *testing.T) {
	requests := make(chan api.SelectionRequest, 1)
	backend := &fakeBackend{
		core:           api.Core{CoreID: "core-1"},
		selectionCalls: requests,
		modelsCalls:    make(chan modelsCall, 1),
		selectionFunc: func(_ context.Context, request api.SelectionRequest) (api.SelectionPreview, error) {
			return api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "effective-provider", AuthProfile: "effective-account"}}, nil
		},
		models: []api.ModelInfo{{ID: "effective/model", Name: "Effective Model"}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.lastSelection = contracts.ModelSelection{Provider: "last-run-provider", AuthProfile: "last-run-account"}
	m.handleCommand("/models")
	if request := <-requests; request.ProviderID != "" {
		t.Fatalf("/models without an ID pinned the prior provider instead of asking for the next-run preview: %+v", request)
	}
	call := <-backend.modelsCalls
	if call.providerID != "effective-provider" || call.authProfile != "effective-account" {
		t.Fatalf("/models did not use current preview route/profile: %+v", call)
	}
	update(m, receive(t, messages))
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].text, "effective/model") {
		t.Fatalf("effective route listing did not complete: %+v", m.entries)
	}
}

func TestPickerSelectionWhileRunActiveDoesNotChangeCapturedRequest(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, _, cancel := testModel(backend, Options{
		Core: backend.core, SessionID: "session-1", ModelID: "original-model", ProviderID: "original-provider", Effort: "low",
	})
	defer func() { cancel(); m.shutdown() }()
	m.modelSet = true
	m.startRun("task")
	call := <-backend.runCalls
	m.setProvider("next-provider")
	m.setModel("next-model")
	m.setEffort("ultra")
	if call.request.ProviderID != "original-provider" || call.request.ModelID != "original-model" || call.request.Effort != "low" {
		t.Fatalf("next-run selection mutated active StartRun request: %+v", call.request)
	}
	if m.providerID != "next-provider" || m.modelID != "next-model" || m.effort != "ultra" {
		t.Fatalf("next-run choices were not retained: provider=%q model=%q effort=%q", m.providerID, m.modelID, m.effort)
	}
	m.activeRun.actualSelection = contracts.ModelSelection{Provider: "original-provider", ModelID: "original-model", Effort: "low"}
	m.activeRun.hasActualSelection = true
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "next-provider", ModelID: "next-model", Effort: "ultra"}
	connection := m.footerModel(160)
	if !strings.Contains(connection, "original-provider / original-model / low") || !strings.Contains(connection, "next: next-provider / next-model / ultra") {
		t.Fatalf("footer does not distinguish the captured run from next selection: %q", connection)
	}
	call.handle.finish(api.RunState{RunID: call.request.RunID}, nil)
}

func TestPickerKeepsSelectedRowVisibleAtSmallHeight(t *testing.T) {
	models := make([]api.ModelInfo, 24)
	for i := range models {
		models[i] = api.ModelInfo{ID: "provider/model-" + strings.Repeat("x", i+1)}
	}
	backend := &fakeBackend{
		core:      api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider", ModelID: models[len(models)-1].ID}},
		models:    models,
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	update(m, tea.WindowSizeMsg{Width: 32, Height: 12})
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)
	if m.connectionPicker.index != len(models)-1 {
		t.Fatalf("current model not initially selected: %d", m.connectionPicker.index)
	}
	view := m.viewport.View()
	compact := strings.NewReplacer(" ", "", "\n", "").Replace(view)
	want := "▶provider/model-" + strings.Repeat("x", len(models))
	if !strings.Contains(compact, want) {
		t.Fatalf("selected wrapped row is outside viewport: %q", view)
	}
}

func TestPickerRoleChangeReloadsForLatestSelectionGeneration(t *testing.T) {
	requests := make(chan api.SelectionRequest, 4)
	backend := &fakeBackend{
		core:           api.Core{CoreID: "core-1"},
		selectionCalls: requests,
		selectionFunc: func(_ context.Context, request api.SelectionRequest) (api.SelectionPreview, error) {
			return api.SelectionPreview{Role: request.Role, Selection: contracts.ModelSelection{Provider: "provider", ModelID: "model", Effort: request.Effort}}, nil
		},
		models: []api.ModelInfo{{ID: "model"}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.openConnectionPicker(modelPicker)
	firstRequest := <-requests
	m.roleID, m.roleSet = "reviewer", true
	m.nextSelectionChanged()
	secondRequest := <-requests
	if firstRequest.Role != "" || secondRequest.Role != "reviewer" || m.selectionGeneration < 2 {
		t.Fatalf("role change did not capture a new picker generation: first=%+v second=%+v generation=%d", firstRequest, secondRequest, m.selectionGeneration)
	}
	var loaded []connectionPickerLoadedMsg
	for len(loaded) < 2 {
		msg := receive(t, messages)
		if result, ok := msg.(connectionPickerLoadedMsg); ok {
			loaded = append(loaded, result)
		}
	}
	for _, result := range loaded {
		update(m, result)
	}
	if m.connectionPicker == nil || m.connectionPicker.loading || m.connectionPicker.request.Role != "reviewer" || m.connectionPicker.preview.Role != "reviewer" {
		t.Fatalf("latest role preview was not retained: %+v", m.connectionPicker)
	}
}
