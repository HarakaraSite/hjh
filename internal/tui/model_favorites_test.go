package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
)

func updateUntil(t *testing.T, m *model, messages <-chan tea.Msg, match func(tea.Msg) bool) tea.Msg {
	t.Helper()
	for {
		msg := receive(t, messages)
		update(m, msg)
		if match(msg) {
			return msg
		}
	}
}

func optionIDs(options []connectionOption) []string {
	ids := make([]string, len(options))
	for i, option := range options {
		ids[i] = option.id
	}
	return ids
}

func receiveFavoriteReadCall(t *testing.T, calls <-chan modelsCall) modelsCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for favorite read")
		return modelsCall{}
	}
}

func TestModelFavoritesReorderRetainsSelectionAndSearchesSavedIDs(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	backend := &fakeBackend{
		core:      api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-b"}},
		models:    []api.ModelInfo{{ID: "model-a"}, {ID: "model-b"}, {ID: "model-c"}},
		favoritesFunc: func(ctx context.Context, scope apiclient.Scope, providerID string) (api.ModelFavorites, error) {
			started <- struct{}{}
			select {
			case <-release:
				return api.ModelFavorites{ScopeID: "scope-a", ProviderID: providerID, Revision: 1,
					ModelIDs: []string{"model-c", "offline-z", "offline-a"}}, nil
			case <-ctx.Done():
				return api.ModelFavorites{}, ctx.Err()
			}
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("draft stays here")
	m.openConnectionPicker(modelPicker)
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(connectionPickerLoadedMsg)
		return ok
	})
	<-started
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(modelCatalogLoadedMsg)
		return ok
	})
	if got := optionIDs(m.connectionPickerOptions()); !reflect.DeepEqual(got, []string{"model-a", "model-b", "model-c"}) {
		t.Fatalf("catalog order before favorite metadata = %v", got)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if selected := m.connectionPickerOptions()[m.connectionPicker.index].id; selected != "model-c" {
		t.Fatalf("selected model before favorite response = %q", selected)
	}
	close(release)
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(modelFavoritesLoadedMsg)
		return ok
	})
	if got, want := optionIDs(m.connectionPickerOptions()), []string{"model-c", "offline-a", "offline-z", "model-a", "model-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("favorite-first candidates = %v, want %v", got, want)
	}
	if selected := m.connectionPickerOptions()[m.connectionPicker.index].id; selected != "model-c" {
		t.Fatalf("favorite response moved selection to %q", selected)
	}
	if !m.connectionPickerOptions()[m.connectionPicker.index].favorite {
		t.Fatal("confirmed favorite was not marked")
	}
	update(m, tea.PasteMsg{Content: "offline"})
	if got, want := optionIDs(m.connectionPickerOptions()), []string{"offline-a", "offline-z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("search did not include both saved-only favorites: %v", got)
	}
	if got := m.textarea.Value(); got != "draft stays here" {
		t.Fatalf("search or favorite metadata changed draft: %q", got)
	}
}

func TestModelFavoriteTabWaitsForSavedResultAndPreservesSelection(t *testing.T) {
	putStarted := make(chan api.SetModelFavoriteRequest, 1)
	putRelease := make(chan struct{})
	backend := &fakeBackend{
		core:      api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-b"}},
		models:    []api.ModelInfo{{ID: "model-a"}, {ID: "model-b"}},
		favoritesFunc: func(_ context.Context, _ apiclient.Scope, providerID string) (api.ModelFavorites, error) {
			return api.ModelFavorites{ScopeID: "scope-a", ProviderID: providerID, Revision: 4}, nil
		},
		setFavoriteFunc: func(ctx context.Context, request api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error) {
			putStarted <- request
			select {
			case <-putRelease:
				return api.ModelFavoriteResult{
					ModelFavorites: api.ModelFavorites{ScopeID: "scope-a", ProviderID: request.ProviderID, Revision: 5, ModelIDs: []string{"model-b"}},
					ModelID:        request.ModelID, Favorite: *request.Favorite,
				}, nil
			case <-ctx.Done():
				return api.ModelFavoriteResult{}, ctx.Err()
			}
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("keep this draft")
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)
	selectedID := m.connectionPickerOptions()[m.connectionPicker.index].id
	beforeGeneration := m.selectionGeneration
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	request := <-putStarted
	if request.CoreID != "core-1" || request.Workspace != "/work" || request.ProviderID != "provider-a" || request.ModelID != selectedID || request.Favorite == nil || !*request.Favorite {
		t.Fatalf("Tab did not send the captured desired favorite value: %+v", request)
	}
	if m.connectionPickerOptions()[m.connectionPicker.index].favorite {
		t.Fatal("favorite was shown before save confirmation")
	}
	close(putRelease)
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(modelFavoriteSavedMsg)
		return ok
	})
	options := m.connectionPickerOptions()
	if options[m.connectionPicker.index].id != selectedID || !options[m.connectionPicker.index].favorite {
		t.Fatalf("save response did not retain and mark selected ID %q: index=%d options=%+v", selectedID, m.connectionPicker.index, options)
	}
	if m.selectionGeneration != beforeGeneration || m.modelSet || m.modelID != "" {
		t.Fatalf("favorite save changed next-run selection: generation=%d modelSet=%v model=%q", m.selectionGeneration, m.modelSet, m.modelID)
	}
	if got := m.textarea.Value(); got != "keep this draft" {
		t.Fatalf("favorite save changed draft: %q", got)
	}
}

func TestModelFavoriteTabsDuringPutDoNotRecoverAndPickerKeysRemainResponsive(t *testing.T) {
	for _, key := range []struct {
		name          string
		message       tea.KeyPressMsg
		selectedModel bool
	}{
		{name: "enter", message: tea.KeyPressMsg{Code: tea.KeyEnter}, selectedModel: true},
		{name: "escape", message: tea.KeyPressMsg{Code: tea.KeyEsc}},
	} {
		t.Run(key.name, func(t *testing.T) {
			reads := make(chan modelsCall, 4)
			puts := make(chan api.SetModelFavoriteRequest, 4)
			putRelease := make(chan struct{})
			backend := &fakeBackend{
				core:             api.Core{CoreID: "core-1"},
				selection:        api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-a"}},
				models:           []api.ModelInfo{{ID: "model-a"}},
				favoritesCalls:   reads,
				setFavoriteCalls: puts,
				favoritesFunc: func(_ context.Context, _ apiclient.Scope, providerID string) (api.ModelFavorites, error) {
					return api.ModelFavorites{ScopeID: "scope-a", ProviderID: providerID}, nil
				},
				setFavoriteFunc: func(ctx context.Context, request api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error) {
					select {
					case <-putRelease:
						return api.ModelFavoriteResult{
							ModelFavorites: api.ModelFavorites{ScopeID: "scope-a", ProviderID: request.ProviderID, Revision: 2, ModelIDs: []string{request.ModelID}},
							ModelID:        request.ModelID, Favorite: *request.Favorite,
						}, nil
					case <-ctx.Done():
						return api.ModelFavoriteResult{}, ctx.Err()
					}
				},
			}
			m, messages, cancel := testModel(backend, Options{Core: backend.core})
			defer func() { cancel(); m.shutdown() }()
			m.openConnectionPicker(modelPicker)
			loadedConnectionPicker(t, backend, m, messages)
			if call := receiveFavoriteReadCall(t, reads); call.providerID != "provider-a" {
				t.Fatalf("initial read used provider %q", call.providerID)
			}

			update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			request := <-puts
			if request.Favorite == nil || !*request.Favorite {
				t.Fatalf("initial desired value = %+v", request)
			}
			for range 3 {
				update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			}
			op := m.pendingModelFavorite(m.connectionPicker, "model-a")
			if op == nil || !op.putInFlight || op.recovering {
				t.Fatalf("Tab while PUT was in flight changed operation state: %+v", op)
			}
			if !strings.Contains(m.status, "saving favorite") {
				t.Fatalf("Tab while PUT was in flight changed status to %q", m.status)
			}
			rows, _ := m.connectionPickerRows()
			if !strings.Contains(strings.Join(rows, "\n"), "saving favorite…") {
				t.Fatalf("in-flight save was not shown: %v", rows)
			}
			select {
			case call := <-reads:
				t.Fatalf("Tab started an unexpected GET while PUT was in flight: %+v", call)
			case <-time.After(20 * time.Millisecond):
			}
			select {
			case duplicate := <-puts:
				t.Fatalf("Tab started a duplicate PUT: %+v", duplicate)
			default:
			}

			update(m, key.message)
			if m.connectionPicker != nil {
				t.Fatalf("%s did not close the picker while PUT was pending", key.name)
			}
			if m.modelSet != key.selectedModel {
				t.Fatalf("%s changed selection state: modelSet=%v", key.name, m.modelSet)
			}
			if m.modelFavoriteOperationByID(op.id) != op {
				t.Fatalf("%s canceled the independent pending favorite operation", key.name)
			}
			close(putRelease)
			updateUntil(t, m, messages, func(msg tea.Msg) bool {
				_, ok := msg.(modelFavoriteSavedMsg)
				return ok
			})
			if len(m.modelFavoriteOps) != 0 {
				t.Fatalf("completed write left %d pending operations", len(m.modelFavoriteOps))
			}
		})
	}
}

func TestUnknownFavoriteTabOnlyReloadsBeforeChanging(t *testing.T) {
	reads := 0
	puts := make(chan api.SetModelFavoriteRequest, 1)
	backend := &fakeBackend{
		core:      api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-a"}},
		models:    []api.ModelInfo{{ID: "model-a"}},
		favoritesFunc: func(_ context.Context, _ apiclient.Scope, providerID string) (api.ModelFavorites, error) {
			reads++
			if reads == 1 {
				return api.ModelFavorites{}, errors.New("temporary read failure")
			}
			return api.ModelFavorites{ScopeID: "scope-a", ProviderID: providerID, Revision: 1}, nil
		},
		setFavoriteCalls: puts,
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)
	if m.connectionPicker.favoritesKnown || m.connectionPicker.favoritesErr == nil {
		t.Fatalf("failed read was treated as confirmed favorite state: %+v", m.connectionPicker)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(modelFavoritesLoadedMsg)
		return ok
	})
	if !m.connectionPicker.favoritesKnown || len(puts) != 0 {
		t.Fatalf("Tab with unknown state did more than reload: known=%v writes=%d", m.connectionPicker.favoritesKnown, len(puts))
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	request := <-puts
	if request.Favorite == nil || !*request.Favorite {
		t.Fatalf("second Tab did not invert the confirmed false state: %+v", request)
	}
}

func TestUnconfirmedFavoriteTabChecksAndResendsSameDesiredValue(t *testing.T) {
	reads := 0
	puts := make(chan api.SetModelFavoriteRequest, 2)
	writeCount := 0
	backend := &fakeBackend{
		core:      api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-a"}},
		models:    []api.ModelInfo{{ID: "model-a"}},
		favoritesFunc: func(_ context.Context, _ apiclient.Scope, providerID string) (api.ModelFavorites, error) {
			reads++
			return api.ModelFavorites{ScopeID: "scope-a", ProviderID: providerID, Revision: uint64(reads)}, nil
		},
		setFavoriteFunc: func(_ context.Context, request api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error) {
			writeCount++
			puts <- request
			if writeCount == 1 {
				return api.ModelFavoriteResult{}, errors.New("response lost after submission")
			}
			return api.ModelFavoriteResult{
				ModelFavorites: api.ModelFavorites{ScopeID: "scope-a", ProviderID: request.ProviderID, Revision: 3, ModelIDs: []string{request.ModelID}},
				ModelID:        request.ModelID, Favorite: *request.Favorite,
			}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	first := <-puts
	if first.Favorite == nil || !*first.Favorite {
		t.Fatalf("first requested value = %+v", first)
	}
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(modelFavoriteSavedMsg)
		return ok
	})
	op := m.pendingModelFavorite(m.connectionPicker, "model-a")
	if op == nil || op.putInFlight || op.recovering {
		t.Fatalf("failed write was not settled before recovery: %+v", op)
	}
	if m.connectionPickerOptions()[m.connectionPicker.index].favorite {
		t.Fatal("unknown PUT result was shown as confirmed")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		loaded, ok := msg.(modelFavoritesLoadedMsg)
		return ok && loaded.purpose == modelFavoritesReadRecovery
	})
	second := <-puts
	if second.Favorite == nil || !*second.Favorite {
		t.Fatalf("recovery reversed the original desired value: %+v", second)
	}
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(modelFavoriteSavedMsg)
		return ok
	})
	if !m.connectionPickerOptions()[m.connectionPicker.index].favorite {
		t.Fatal("confirmed retry did not mark the favorite")
	}
}

func TestUnknownFavoriteWriteSurvivesCloseAndDoesNotUpdateReopenedPicker(t *testing.T) {
	putStarted := make(chan api.SetModelFavoriteRequest, 1)
	putRelease := make(chan struct{})
	reads := 0
	backend := &fakeBackend{
		core:      api.Core{CoreID: "core-1"},
		selection: api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-a"}},
		models:    []api.ModelInfo{{ID: "model-a"}, {ID: "model-b"}},
		favoritesFunc: func(_ context.Context, _ apiclient.Scope, providerID string) (api.ModelFavorites, error) {
			reads++
			favorites := api.ModelFavorites{ScopeID: "scope-a", ProviderID: providerID, Revision: uint64(reads)}
			if reads >= 3 {
				favorites.ModelIDs = []string{"model-a"}
			}
			return favorites, nil
		},
		setFavoriteFunc: func(ctx context.Context, request api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error) {
			putStarted <- request
			select {
			case <-putRelease:
				return api.ModelFavoriteResult{
					ModelFavorites: api.ModelFavorites{ScopeID: "scope-a", ProviderID: request.ProviderID, Revision: 5, ModelIDs: []string{request.ModelID}},
					ModelID:        request.ModelID, Favorite: *request.Favorite,
				}, nil
			case <-ctx.Done():
				return api.ModelFavoriteResult{}, ctx.Err()
			}
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	request := <-putStarted
	if request.Favorite == nil || !*request.Favorite {
		t.Fatalf("initial desired value = %+v", request)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.connectionPicker != nil || m.modelFavoriteOperationByID(m.modelFavoriteOpSeq) == nil {
		t.Fatal("closing the picker discarded the pending favorite operation")
	}
	m.openConnectionPicker(modelPicker)
	loadedConnectionPicker(t, backend, m, messages)
	if m.connectionPickerOptions()[m.connectionPicker.index].favorite {
		t.Fatal("new picker inherited an old operation's unconfirmed result")
	}
	close(putRelease)
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		_, ok := msg.(modelFavoriteSavedMsg)
		return ok
	})
	if len(m.modelFavoriteOps) != 0 || m.connectionPickerOptions()[m.connectionPicker.index].favorite {
		t.Fatalf("stale save directly changed the reopened picker: ops=%d options=%+v", len(m.modelFavoriteOps), m.connectionPickerOptions())
	}
	// A new read initiated for the current picker applies the saved state.
	updateUntil(t, m, messages, func(msg tea.Msg) bool {
		loaded, ok := msg.(modelFavoritesLoadedMsg)
		return ok && loaded.purpose == modelFavoritesReadOpen
	})
	options := m.connectionPickerOptions()
	rows, _ := m.connectionPickerRows()
	if len(options) < 1 || !options[m.connectionPicker.index].favorite || !strings.Contains(strings.Join(rows, "\n"), "★") {
		t.Fatalf("reopened picker did not show the confirmed favorite: %+v", options)
	}
}

func TestOldFavoriteRecoveryRefreshesCurrentPickerScope(t *testing.T) {
	for _, scenario := range []struct {
		name           string
		currentSession string
		currentWork    string
		failRefresh    bool
	}{
		{name: "reopen", currentSession: "session-1", currentWork: "/work"},
		{name: "shared-config-new-session", currentSession: "session-2", currentWork: "/other"},
		{name: "shared-config-refresh-error", currentSession: "session-2", currentWork: "/other", failRefresh: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			reads := make(chan modelsCall, 8)
			puts := make(chan api.SetModelFavoriteRequest, 4)
			readCount := 0
			writeCount := 0
			backend := &fakeBackend{
				core:             api.Core{CoreID: "core-1"},
				selection:        api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-a"}},
				models:           []api.ModelInfo{{ID: "model-a"}},
				favoritesCalls:   reads,
				setFavoriteCalls: puts,
				favoritesFunc: func(_ context.Context, scope apiclient.Scope, providerID string) (api.ModelFavorites, error) {
					readCount++
					if scenario.failRefresh && readCount == 4 {
						return api.ModelFavorites{}, errors.New("current picker refresh response lost")
					}
					favorites := api.ModelFavorites{ScopeID: "shared-scope", ProviderID: providerID, Revision: uint64(readCount)}
					if readCount >= 3 {
						favorites.ModelIDs = []string{"model-a"}
					}
					_ = scope
					return favorites, nil
				},
				setFavoriteFunc: func(_ context.Context, request api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error) {
					writeCount++
					if writeCount == 1 {
						return api.ModelFavoriteResult{}, errors.New("response lost after submission")
					}
					return api.ModelFavoriteResult{
						ModelFavorites: api.ModelFavorites{ScopeID: "shared-scope", ProviderID: request.ProviderID, Revision: uint64(readCount + 1)},
						ModelID:        request.ModelID, Favorite: *request.Favorite,
					}, nil
				},
			}
			m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1", Workspace: "/work"})
			defer func() { cancel(); m.shutdown() }()
			m.openConnectionPicker(modelPicker)
			loadedConnectionPicker(t, backend, m, messages)
			if call := receiveFavoriteReadCall(t, reads); call.scope.SessionID != "session-1" || call.scope.Workspace != "/work" {
				t.Fatalf("initial read scope = %+v", call.scope)
			}

			update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			first := <-puts
			if first.Favorite == nil || !*first.Favorite {
				t.Fatalf("initial favorite write = %+v", first)
			}
			updateUntil(t, m, messages, func(msg tea.Msg) bool {
				_, ok := msg.(modelFavoriteSavedMsg)
				return ok
			})
			if op := m.modelFavoriteOperationByID(m.modelFavoriteOpSeq); op == nil || op.putInFlight || op.recovering {
				t.Fatalf("failed write was not retained for recovery: %+v", op)
			}

			update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
			if scenario.currentSession != "session-1" {
				m.sessionID = scenario.currentSession
				m.workspace = scenario.currentWork
				m.selectionGeneration++
			}
			m.openConnectionPicker(modelPicker)
			loadedConnectionPicker(t, backend, m, messages)
			if call := receiveFavoriteReadCall(t, reads); call.scope.SessionID != scenario.currentSession || call.scope.Workspace != scenario.currentWork {
				t.Fatalf("current picker read scope = %+v", call.scope)
			}
			if m.connectionPickerOptions()[m.connectionPicker.index].favorite {
				t.Fatal("new picker applied the old operation's unconfirmed state")
			}

			update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			recoveryCall := receiveFavoriteReadCall(t, reads)
			if recoveryCall.scope.SessionID != "session-1" || recoveryCall.scope.Workspace != "/work" {
				t.Fatalf("recovery GET did not use the original operation scope: %+v", recoveryCall.scope)
			}
			updateUntil(t, m, messages, func(msg tea.Msg) bool {
				loaded, ok := msg.(modelFavoritesLoadedMsg)
				return ok && loaded.purpose == modelFavoritesReadRecovery
			})
			if m.modelFavoriteOperationByID(m.modelFavoriteOpSeq) != nil {
				t.Fatal("matching recovery GET left the old operation pending")
			}
			if m.connectionPickerOptions()[m.connectionPicker.index].favorite || !m.connectionPicker.favoritesLoading {
				t.Fatalf("old recovery response was applied directly or fresh read did not start: favorites=%+v", m.connectionPicker.favorites)
			}
			refreshCall := receiveFavoriteReadCall(t, reads)
			if refreshCall.scope.SessionID != scenario.currentSession || refreshCall.scope.Workspace != scenario.currentWork {
				t.Fatalf("current picker refresh used the old operation scope: %+v", refreshCall.scope)
			}

			update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			if !strings.Contains(m.status, "checking favorite status") {
				t.Fatalf("Tab during fresh read did not wait: status=%q", m.status)
			}
			select {
			case duplicate := <-puts:
				t.Fatalf("Tab during fresh read started a write from stale state: %+v", duplicate)
			default:
			}
			updateUntil(t, m, messages, func(msg tea.Msg) bool {
				loaded, ok := msg.(modelFavoritesLoadedMsg)
				return ok && loaded.purpose == modelFavoritesReadOpen
			})
			if scenario.failRefresh {
				if m.connectionPicker.favoritesErr == nil {
					t.Fatal("failed current-scope refresh did not expose an error")
				}
				update(m, tea.KeyPressMsg{Code: tea.KeyTab})
				retry := receiveFavoriteReadCall(t, reads)
				if retry.scope.SessionID != scenario.currentSession || retry.scope.Workspace != scenario.currentWork {
					t.Fatalf("refresh retry used old scope: %+v", retry.scope)
				}
				select {
				case staleWrite := <-puts:
					t.Fatalf("failed-refresh Tab wrote from the stale flag: %+v", staleWrite)
				default:
				}
				updateUntil(t, m, messages, func(msg tea.Msg) bool {
					loaded, ok := msg.(modelFavoritesLoadedMsg)
					return ok && loaded.purpose == modelFavoritesReadUnknownTab
				})
			}
			if !m.connectionPickerOptions()[m.connectionPicker.index].favorite {
				t.Fatal("fresh current-scope read did not show the confirmed favorite")
			}
			update(m, tea.KeyPressMsg{Code: tea.KeyTab})
			second := <-puts
			if second.Favorite == nil || *second.Favorite {
				t.Fatalf("Tab after fresh GET did not request removal: %+v", second)
			}
			updateUntil(t, m, messages, func(msg tea.Msg) bool {
				_, ok := msg.(modelFavoriteSavedMsg)
				return ok
			})
			if m.connectionPickerOptions()[m.connectionPicker.index].favorite {
				t.Fatal("confirmed removal remained marked as favorite")
			}
		})
	}
}
