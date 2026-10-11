package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
)

func authProfilesForTest(provider, profile, kind string) api.AuthProfiles {
	return api.AuthProfiles{
		Target: api.AuthTarget{ProviderID: provider, AuthProfile: profile, Kind: kind},
		Profiles: []api.AuthProfile{{
			AuthProfile: profile,
			Methods:     []string{kind},
			Providers:   []api.AuthProvider{{ID: provider, DisplayName: provider, Kind: kind}},
		}},
	}
}

func TestStaleAuthProfileReadCannotReplaceNewLoginTarget(t *testing.T) {
	oldResult := make(chan struct{})
	newResult := make(chan struct{})
	started := make(chan api.SelectionRequest, 2)
	backend := &fakeBackend{
		core:              api.Core{CoreID: "core-1"},
		authProfilesCalls: make(chan api.SelectionRequest, 2),
		authProfilesFunc: func(ctx context.Context, request api.SelectionRequest) (api.AuthProfiles, error) {
			started <- request
			release := oldResult
			profiles := authProfilesForTest("openai-chatgpt", "chatgpt-account", "chatgpt-oauth")
			if request.ProviderID == "openrouter" {
				release = newResult
				profiles = authProfilesForTest("openrouter", "router-key", "api-key")
			}
			select {
			case <-release:
				return profiles, nil
			case <-ctx.Done():
				return api.AuthProfiles{}, ctx.Err()
			}
		},
	}
	m, messages, cancel := testModel(backend, Options{
		Core: backend.core, Workspace: "/work", ProviderID: "openai-chatgpt",
	})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/login")
	oldRequest := <-started
	m.handleCommand("/login openrouter")
	newRequest := <-started
	if oldRequest.ProviderID != "openai-chatgpt" || newRequest.ProviderID != "openrouter" {
		t.Fatalf("auth profile requests = %+v / %+v", oldRequest, newRequest)
	}
	<-backend.authProfilesCalls
	<-backend.authProfilesCalls
	close(newResult)
	update(m, receive(t, messages))
	if m.authUI == nil || m.authUI.screen != authAPIKeyScreen || m.authUI.providerID != "openrouter" {
		t.Fatalf("new targeted login did not open its API key editor: %+v", m.authUI)
	}
	close(oldResult)
	update(m, receive(t, messages))
	if m.authUI == nil || m.authUI.screen != authAPIKeyScreen || m.authUI.providerID != "openrouter" || m.authUI.selectedProfile != "router-key" {
		t.Fatalf("stale profile response replaced the current login target: %+v", m.authUI)
	}
}

func TestAuthProfileReadRebasesAfterSessionLoadStartedBeforeLogin(t *testing.T) {
	releaseOld := make(chan struct{})
	started := make(chan api.SelectionRequest, 2)
	oldProfiles := authProfilesForTest("provider-a", "profile-a", "api-key")
	currentProfiles := authProfilesForTest("provider-b", "profile-b", "api-key")
	backend := &fakeBackend{
		core:              api.Core{CoreID: "core-1"},
		authProfilesCalls: make(chan api.SelectionRequest, 2),
		authProfilesFunc: func(ctx context.Context, request api.SelectionRequest) (api.AuthProfiles, error) {
			started <- request
			if request.SessionID == "session-a" {
				select {
				case <-releaseOld:
					return oldProfiles, nil
				case <-ctx.Done():
					return api.AuthProfiles{}, ctx.Err()
				}
			}
			return currentProfiles, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work", SessionID: "session-a"})
	defer func() { cancel(); m.shutdown() }()
	m.lastSelection = contracts.ModelSelection{Provider: "provider-a", AuthProfile: "profile-a"}
	m.sessionGeneration = 9
	// /login is accepted while /resume is still loading. It initially captures A.
	m.sessionLoading = true
	m.sessionGeneration++
	m.handleCommand("/login")
	oldRequest := <-started
	if oldRequest.SessionID != "session-a" {
		t.Fatalf("login during resume did not capture the pre-load route: %+v", oldRequest)
	}
	// The resumed Session has a different effective provider even though workspace is shared.
	update(m, sessionLoadedMsg{
		generation: m.sessionGeneration,
		session: api.Session{
			ID: "session-b", WorkspaceRoot: "/work",
			ActiveModel: json.RawMessage(`{"provider":"provider-b","authProfile":"profile-b"}`),
		},
	})
	var currentRequest api.SelectionRequest
	for currentRequest.SessionID == "" {
		message := receive(t, messages)
		if loaded, ok := message.(authProfilesLoadedMsg); ok && loaded.request.SessionID == "session-b" {
			update(m, loaded)
			currentRequest = loaded.request
			continue
		}
		update(m, message)
	}
	if m.authUI.selectionRequest.SessionID != "session-b" || m.authUI.profiles.Target.ProviderID != "provider-b" {
		t.Fatalf("authentication profile picker did not rebase to the loaded Session: request=%+v state=%+v", currentRequest, m.authUI)
	}
	close(releaseOld)
	for {
		message := receive(t, messages)
		if loaded, ok := message.(authProfilesLoadedMsg); ok && loaded.request.SessionID == "session-a" {
			update(m, loaded)
			break
		}
		update(m, message)
	}
	if m.authUI.profiles.Target.ProviderID != "provider-b" || m.authUI.selectionRequest.SessionID != "session-b" {
		t.Fatalf("late response from the pre-resume Session replaced the current target: %+v", m.authUI)
	}
}

func TestAPIKeyInputIsMaskedAndSavedWithMutationID(t *testing.T) {
	backend := &fakeBackend{
		core:                api.Core{CoreID: "core-1"},
		authProfilesCalls:   make(chan api.SelectionRequest, 1),
		authProfiles:        authProfilesForTest("openrouter", "router-key", "api-key"),
		registerAPIKeyCalls: make(chan api.AuthRequest, 1),
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("draft stays outside auth input")
	m.handleCommand("/login openrouter")
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	if m.authUI == nil || m.authUI.screen != authAPIKeyScreen {
		t.Fatalf("targeted provider did not open its API key editor: %+v", m.authUI)
	}
	output := attachWriter(m)
	secret := "sk-live-value-that-must-stay-masked"
	update(m, tea.PasteMsg{Content: secret})
	m.View()
	if strings.Contains(output.String(), secret) || !strings.Contains(output.String(), "•") {
		t.Fatalf("API key editor exposed input instead of masking it: %q", output.String())
	}
	frame, cursor, _, _, _, bodyRows := m.screenFrame([]string{"model: test", "status: ready"})
	_, _, _, cursorLine, _ := m.authRows()
	if cursor == nil || cursor.Position.Y != cursorLine-m.viewport.YOffset() || frame.Cursor.Row != cursor.Position.Y || cursor.Position.Y >= len(bodyRows) {
		t.Fatalf("masked API key cursor did not stay on its editor row: cursor=%+v frame=%+v line=%d body=%v", cursor, frame.Cursor, cursorLine, bodyRows)
	}
	if m.textarea.Value() != "draft stays outside auth input" || strings.Contains(m.renderTranscript(), secret) {
		t.Fatalf("API key entered the conversation draft or transcript: draft=%q transcript=%q", m.textarea.Value(), m.renderTranscript())
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	request := <-backend.registerAPIKeyCalls
	if request.Value != secret || request.MutationID == "" || request.ProviderID != "openrouter" || request.AuthProfile != "router-key" {
		t.Fatalf("API key registration request = %+v", request)
	}
	if m.authUI.secret != "" || strings.Contains(m.status, secret) {
		t.Fatalf("submitted key remained visible in UI state: secret=%q status=%q", m.authUI.secret, m.status)
	}
	update(m, receive(t, messages))
	if !strings.Contains(m.status, "saved") || strings.Contains(m.status, secret) || strings.Contains(m.renderTranscript(), secret) {
		t.Fatalf("API key result was not safely confirmed: status=%q transcript=%q", m.status, m.renderTranscript())
	}
}

func TestAPIKeyUnknownSaveReadsMutationWithoutResending(t *testing.T) {
	var profileReads atomic.Int32
	backend := &fakeBackend{
		core:                api.Core{CoreID: "core-1"},
		authProfilesCalls:   make(chan api.SelectionRequest, 2),
		registerAPIKeyCalls: make(chan api.AuthRequest, 2),
		registerAPIKeyFunc: func(_ context.Context, request api.AuthRequest) (api.APIKeyState, error) {
			return api.APIKeyState{}, errors.New("response lost after Core save")
		},
		authProfilesFunc: func(_ context.Context, _ api.SelectionRequest) (api.AuthProfiles, error) {
			if profileReads.Add(1) == 1 {
				return authProfilesForTest("openrouter", "router-key", "api-key"), nil
			}
			profiles := authProfilesForTest("openrouter", "router-key", "api-key")
			profiles.Profiles[0].APIKey = &api.APIKeyState{Registered: true, Source: "registered", Revision: 4, MutationID: "another-client"}
			return profiles, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/login openrouter")
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	update(m, tea.PasteMsg{Content: "sk-never-retry-automatically"})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	firstRequest := <-backend.registerAPIKeyCalls
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	if profileReads.Load() != 2 || m.authUI.apiKey == nil || m.authUI.apiKey.MutationID != "another-client" ||
		!strings.Contains(m.status, "result unknown") {
		t.Fatalf("unknown save was not reconciled from current state: reads=%d key=%+v status=%q", profileReads.Load(), m.authUI.apiKey, m.status)
	}
	select {
	case request := <-backend.registerAPIKeyCalls:
		t.Fatalf("TUI automatically resent API key mutation %s as %s", firstRequest.MutationID, request.MutationID)
	default:
	}
}

func TestChatGPTCallbackUsesMaskedEditorAndDedicatedRequest(t *testing.T) {
	accountReads := make(chan authAccountsCall, 2)
	backend := &fakeBackend{
		core:                     api.Core{CoreID: "core-1"},
		authProfilesCalls:        make(chan api.SelectionRequest, 1),
		authProfiles:             authProfilesForTest("openai-chatgpt", "chatgpt-account", "chatgpt-oauth"),
		authAccountsCalls:        accountReads,
		beginAuthAttemptCalls:    make(chan api.AuthRequest, 1),
		completeAuthAttemptCalls: make(chan api.AuthRequest, 1),
		authAccountsFunc: func(context.Context, apiclient.Scope, string, string) (api.AuthAccounts, error) {
			return api.AuthAccounts{
				SelectedAccountID: "primary",
				Accounts:          []api.AuthAccount{{AccountID: "primary", Label: "Standard account"}},
			}, nil
		},
		beginAuthAttemptFunc: func(_ context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
			return api.AuthAttempt{
				CoreID: request.CoreID, AttemptID: request.AttemptID, ProviderID: request.ProviderID,
				AuthProfile: request.AuthProfile, AccountID: "new-slot", Phase: "ready",
				AuthorizationURL: "https://auth.example/authorize?state=opaque",
			}, nil
		},
		completeAuthAttemptFunc: func(_ context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
			return api.AuthAttempt{CoreID: request.CoreID, AttemptID: request.AttemptID, Phase: "exchanging"}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("conversation draft")
	m.handleCommand("/login openai-chatgpt")
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	<-accountReads
	update(m, receive(t, messages))
	if m.authUI == nil || m.authUI.screen != authAccountsScreen {
		t.Fatalf("targeted ChatGPT provider did not open account screen: %+v", m.authUI)
	}
	update(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	begin := <-backend.beginAuthAttemptCalls
	if begin.AccountID != "" || begin.AttemptID == "" || begin.CoreID != backend.core.CoreID {
		t.Fatalf("adding account did not start a new Core-owned slot: %+v", begin)
	}
	update(m, receive(t, messages))
	if m.authUI.screen != authOAuthScreen || m.authUI.attempt == nil || m.authUI.attempt.AuthorizationURL == "" {
		t.Fatalf("authorization URL screen was not shown: %+v", m.authUI)
	}
	output := attachWriter(m)
	m.View()
	if !strings.Contains(output.String(), "https://auth.example/authorize") {
		t.Fatalf("authorization URL was not displayed: %q", output.String())
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	update(m, tea.PasteMsg{Content: "http://127.0.0.1:1455/auth/callback?code=callback-secret&state=callback-state"})
	m.View()
	callback := "http://127.0.0.1:1455/auth/callback?code=callback-secret&state=callback-state"
	if strings.Contains(output.String(), callback) || strings.Contains(m.renderTranscript(), callback) || m.textarea.Value() != "conversation draft" {
		t.Fatalf("callback URL escaped the masked editor: output=%q transcript=%q draft=%q", output.String(), m.renderTranscript(), m.textarea.Value())
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	request := <-backend.completeAuthAttemptCalls
	if request.CallbackURL != callback || request.AttemptID != begin.AttemptID || strings.Contains(m.status, callback) {
		t.Fatalf("callback request/status = %+v / %q", request, m.status)
	}
	update(m, receive(t, messages))
	if strings.Contains(m.renderTranscript(), callback) || strings.Contains(m.status, callback) {
		t.Fatalf("callback URL appeared after submission: transcript=%q status=%q", m.renderTranscript(), m.status)
	}
}

func TestSelectingChatGPTAccountRefreshesThePicker(t *testing.T) {
	accountReads := make(chan authAccountsCall, 2)
	selectCalls := make(chan api.AuthRequest, 1)
	accounts := api.AuthAccounts{
		SelectedAccountID: "primary",
		Accounts: []api.AuthAccount{
			{AccountID: "primary", Label: "Primary", Connected: true},
			{AccountID: "secondary", Label: "Secondary", Connected: true},
		},
	}
	backend := &fakeBackend{
		core:                   api.Core{CoreID: "core-1"},
		authProfilesCalls:      make(chan api.SelectionRequest, 1),
		authProfiles:           authProfilesForTest("openai-chatgpt", "chatgpt-account", "chatgpt-oauth"),
		authAccountsCalls:      accountReads,
		selectAuthAccountCalls: selectCalls,
		authAccountsFunc: func(context.Context, apiclient.Scope, string, string) (api.AuthAccounts, error) {
			return accounts, nil
		},
		selectAuthAccountFunc: func(_ context.Context, request api.AuthRequest) (api.AuthAccounts, error) {
			accounts.SelectedAccountID = request.AccountID
			return accounts, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/login openai-chatgpt")
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	<-accountReads
	update(m, receive(t, messages))
	selectionGeneration := m.selectionGeneration
	update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	request := <-selectCalls
	if request.AccountID != "secondary" || request.ProviderID != "openai-chatgpt" {
		t.Fatalf("account selection request = %+v", request)
	}
	update(m, receive(t, messages))
	<-accountReads
	update(m, receive(t, messages))
	if m.authUI.accounts.SelectedAccountID != "secondary" || m.selectionGeneration != selectionGeneration+1 {
		t.Fatalf("saved account was not refreshed for the current picker/catalog generation: accounts=%+v generation=%d", m.authUI.accounts, m.selectionGeneration)
	}
}

func TestAuthBeginEscapeCancelsReturnedAttemptID(t *testing.T) {
	beginStarted := make(chan api.AuthRequest, 1)
	releaseBegin := make(chan api.AuthAttempt, 1)
	cancelCalls := make(chan api.AuthRequest, 1)
	deleteCalls := make(chan authAttemptCall, 1)
	backend := &fakeBackend{
		core:                   api.Core{CoreID: "core-1"},
		authProfilesCalls:      make(chan api.SelectionRequest, 1),
		authProfiles:           authProfilesForTest("openai-chatgpt", "chatgpt-account", "chatgpt-oauth"),
		authAccountsCalls:      make(chan authAccountsCall, 1),
		beginAuthAttemptCalls:  beginStarted,
		cancelAuthAttemptCalls: cancelCalls,
		deleteAuthAttemptCalls: deleteCalls,
		authAccounts: api.AuthAccounts{
			OwnerID: "owner-1", Accounts: []api.AuthAccount{{AccountID: "primary", Label: "Standard account"}},
		},
		beginAuthAttemptFunc: func(ctx context.Context, _ api.AuthRequest) (api.AuthAttempt, error) {
			select {
			case attempt := <-releaseBegin:
				return attempt, nil
			case <-ctx.Done():
				return api.AuthAttempt{}, ctx.Err()
			}
		},
		cancelAuthAttemptFunc: func(_ context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
			return api.AuthAttempt{CoreID: request.CoreID, AttemptID: request.AttemptID, Phase: "cancelled"}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/login openai-chatgpt")
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	<-backend.authAccountsCalls
	update(m, receive(t, messages))
	update(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	request := <-beginStarted
	if m.authUI == nil || !m.authUI.beginPending || m.authUI.attemptID != request.AttemptID || m.authUI.screen != authOAuthScreen {
		t.Fatalf("starting attempt was not tracked in its UI: state=%+v request=%+v", m.authUI, request)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.authUI.cancelRequested || m.authUI.screen != authAccountsScreen {
		t.Fatalf("Esc did not retain the cancellation intent while returning to accounts: %+v", m.authUI)
	}
	actual := api.AuthAttempt{
		CoreID: "core-1", AttemptID: "actual-existing-attempt", ProviderID: "openai-chatgpt",
		AuthProfile: "chatgpt-account", OwnerID: "owner-1", AccountID: "primary", Phase: "ready",
	}
	releaseBegin <- actual
	update(m, receive(t, messages))
	cancelRequest := <-cancelCalls
	if cancelRequest.CoreID != "core-1" || cancelRequest.AttemptID != actual.AttemptID {
		t.Fatalf("delayed begin was not cancelled by its returned attempt ID: %+v", cancelRequest)
	}
	update(m, receive(t, messages))
	if m.authUI.screen != authAccountsScreen || m.authUI.attemptID != actual.AttemptID || m.authUI.attempt.Phase != "cancelled" {
		t.Fatalf("cancellation reopened or lost the actual attempt: %+v", m.authUI)
	}
	if deleted := <-deleteCalls; deleted.attemptID != actual.AttemptID {
		t.Fatalf("terminal attempt cleanup used the provisional ID: %+v", deleted)
	}
	update(m, receive(t, messages))
}

func TestAuthBeginDetachCancellationSurvivesRootAndBackendClose(t *testing.T) {
	beginStarted := make(chan api.AuthRequest, 1)
	releaseBegin := make(chan api.AuthAttempt, 1)
	cancelEntered := make(chan api.AuthRequest, 1)
	releaseCancel := make(chan struct{})
	backendClosed := make(chan struct{})
	backend := &fakeBackend{
		core:                   api.Core{CoreID: "core-1"},
		authProfilesCalls:      make(chan api.SelectionRequest, 1),
		authProfiles:           authProfilesForTest("openai-chatgpt", "chatgpt-account", "chatgpt-oauth"),
		authAccountsCalls:      make(chan authAccountsCall, 1),
		beginAuthAttemptCalls:  beginStarted,
		cancelAuthAttemptCalls: cancelEntered,
		closeFunc: func() error {
			select {
			case <-backendClosed:
			default:
				close(backendClosed)
			}
			return nil
		},
		beginAuthAttemptFunc: func(ctx context.Context, _ api.AuthRequest) (api.AuthAttempt, error) {
			select {
			case attempt := <-releaseBegin:
				return attempt, nil
			case <-ctx.Done():
				return api.AuthAttempt{}, ctx.Err()
			}
		},
		cancelAuthAttemptFunc: func(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
			cancelEntered <- request
			select {
			case <-ctx.Done():
				return api.AuthAttempt{}, ctx.Err()
			case <-releaseCancel:
				return api.AuthAttempt{CoreID: request.CoreID, AttemptID: request.AttemptID, Phase: "cancelled"}, nil
			}
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/login openai-chatgpt")
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	<-backend.authAccountsCalls
	update(m, receive(t, messages))
	update(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	request := <-beginStarted
	update(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if !m.detached || !m.quitting || !m.authUI.cancelRequested {
		t.Fatalf("Ctrl+D did not detach while retaining the pending cancellation: detached=%v quitting=%v state=%+v", m.detached, m.quitting, m.authUI)
	}
	shutdownDone := make(chan struct{})
	go func() { m.shutdown(); close(shutdownDone) }()
	actual := api.AuthAttempt{
		CoreID: "core-1", AttemptID: "actual-returned-attempt", ProviderID: "openai-chatgpt",
		AuthProfile: "chatgpt-account", OwnerID: "owner-1", Phase: "ready",
	}
	releaseBegin <- actual
	select {
	case cancelRequest := <-cancelEntered:
		if cancelRequest.AttemptID != actual.AttemptID || cancelRequest.CoreID != request.CoreID {
			t.Fatalf("detach cancellation did not use the accepted attempt: %+v", cancelRequest)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("detached begin did not issue cancellation")
	}
	select {
	case <-backendClosed:
		t.Fatal("backend closed before detached cancellation communication completed")
	default:
	}
	close(releaseCancel)
	select {
	case <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after the cancellation response")
	}
	select {
	case <-backendClosed:
	default:
		t.Fatal("backend was not closed after cancellation communication completed")
	}
}

func TestAuthResponsesRemainCurrentWhenRunFinishes(t *testing.T) {
	t.Run("API key save", func(t *testing.T) {
		releaseSave := make(chan struct{})
		backend := &fakeBackend{
			core:                api.Core{CoreID: "core-1"},
			authProfilesCalls:   make(chan api.SelectionRequest, 1),
			authProfiles:        authProfilesForTest("openrouter", "router-key", "api-key"),
			registerAPIKeyCalls: make(chan api.AuthRequest, 1),
			registerAPIKeyFunc: func(ctx context.Context, request api.AuthRequest) (api.APIKeyState, error) {
				select {
				case <-releaseSave:
					return api.APIKeyState{Registered: true, Source: "registered", MutationID: request.MutationID}, nil
				case <-ctx.Done():
					return api.APIKeyState{}, ctx.Err()
				}
			},
		}
		m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
		defer func() { cancel(); m.shutdown() }()
		m.handleCommand("/login openrouter")
		<-backend.authProfilesCalls
		update(m, receive(t, messages))
		update(m, tea.PasteMsg{Content: "sk-api-key"})
		update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
		<-backend.registerAPIKeyCalls
		finishRunDuringAuthTest(m)
		close(releaseSave)
		update(m, receive(t, messages))
		if !strings.Contains(m.authUI.status, "saved") || m.authUI.apiKey == nil || !m.authUI.apiKey.Registered {
			t.Fatalf("run completion discarded the API-key save result: state=%+v status=%q", m.authUI, m.status)
		}
	})

	t.Run("account list", func(t *testing.T) {
		releaseAccounts := make(chan struct{})
		accounts := api.AuthAccounts{
			OwnerID: "owner-1", SelectedAccountID: "primary",
			Accounts: []api.AuthAccount{
				{AccountID: "primary", Label: "Primary", Connected: true},
				{AccountID: "secondary", Label: "Secondary", Connected: true},
			},
		}
		accountReads := make(chan authAccountsCall, 1)
		backend := &fakeBackend{
			core:              api.Core{CoreID: "core-1"},
			authProfilesCalls: make(chan api.SelectionRequest, 1),
			authProfiles:      authProfilesForTest("openai-chatgpt", "chatgpt-account", "chatgpt-oauth"),
			authAccountsCalls: accountReads,
			authAccountsFunc: func(ctx context.Context, _ apiclient.Scope, _ string, _ string) (api.AuthAccounts, error) {
				select {
				case <-releaseAccounts:
					return accounts, nil
				case <-ctx.Done():
					return api.AuthAccounts{}, ctx.Err()
				}
			},
		}
		m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
		defer func() { cancel(); m.shutdown() }()
		m.handleCommand("/login openai-chatgpt")
		<-backend.authProfilesCalls
		update(m, receive(t, messages))
		<-accountReads
		finishRunDuringAuthTest(m)
		close(releaseAccounts)
		update(m, receive(t, messages))
		if m.authUI.accounts.SelectedAccountID != "primary" || m.authUI.ownerID != "owner-1" {
			t.Fatalf("run completion discarded account list: accounts=%+v owner=%q status=%q", m.authUI.accounts, m.authUI.ownerID, m.status)
		}
	})

	t.Run("attempt poll", func(t *testing.T) {
		releasePoll := make(chan struct{})
		pollCalls := make(chan authAttemptCall, 1)
		attempt := api.AuthAttempt{CoreID: "core-1", AttemptID: "attempt-1", ProviderID: "openai-chatgpt", AuthProfile: "chatgpt-account", Phase: "ready"}
		backend := &fakeBackend{
			core:             api.Core{CoreID: "core-1"},
			authAttemptCalls: pollCalls,
			authAttemptFunc: func(ctx context.Context, coreID, attemptID string) (api.AuthAttempt, error) {
				select {
				case <-releasePoll:
					return attempt, nil
				case <-ctx.Done():
					return api.AuthAttempt{}, ctx.Err()
				}
			},
		}
		m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
		defer func() { cancel(); m.shutdown() }()
		state := &authUIState{
			open: true, generation: 1, coreID: "core-1", scope: apiclient.Scope{Workspace: "/work"},
			screen: authOAuthScreen, attemptID: "attempt-1", attemptCoreID: "core-1", attempt: &attempt,
		}
		m.authUI = state
		m.readAuthAttempt(state)
		<-pollCalls
		finishRunDuringAuthTest(m)
		close(releasePoll)
		update(m, receive(t, messages))
		if state.pollJobID != 0 || state.attempt == nil || state.attempt.Phase != "ready" {
			t.Fatalf("run completion discarded attempt poll: state=%+v", state)
		}
	})
}

func finishRunDuringAuthTest(m *model) {
	const jobID = 887766
	m.activeRun = &activeRun{jobID: jobID, coreID: m.core.CoreID, id: "run-1", entities: map[string]api.ConversationEntity{}}
	m.finishRun(runDoneMsg{
		jobID: jobID,
		value: api.RunState{
			CoreID: m.core.CoreID, RunID: "run-1", SessionID: "session-finished", Phase: "terminal",
			Result: &api.RunResult{OK: true},
		},
	})
}

func TestAuthAccountsRestoreAttemptBySharedOwner(t *testing.T) {
	accountReads := make(chan authAccountsCall, 2)
	attemptReads := make(chan authAttemptCall, 1)
	sharedAttempt := api.AuthAttempt{
		CoreID: "core-1", AttemptID: "attempt-from-alias-a", ProviderID: "provider-a",
		AuthProfile: "profile-a", OwnerID: "shared-owner", Phase: "exchanging",
	}
	backend := &fakeBackend{
		core:              api.Core{CoreID: "core-1"},
		authProfilesCalls: make(chan api.SelectionRequest, 1),
		authProfiles:      authProfilesForTest("provider-b", "profile-b", "chatgpt-oauth"),
		authAccountsCalls: accountReads,
		authAttemptCalls:  attemptReads,
		authAccountsFunc: func(context.Context, apiclient.Scope, string, string) (api.AuthAccounts, error) {
			return api.AuthAccounts{
				OwnerID: "shared-owner", Accounts: []api.AuthAccount{{AccountID: "slot-1", Connected: true}},
				Attempts: []api.AuthAttempt{sharedAttempt},
			}, nil
		},
		authAttemptFunc: func(context.Context, string, string) (api.AuthAttempt, error) {
			return sharedAttempt, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/login provider-b")
	<-backend.authProfilesCalls
	update(m, receive(t, messages))
	<-accountReads
	update(m, receive(t, messages))
	call := <-attemptReads
	if call.coreID != "core-1" || call.attemptID != sharedAttempt.AttemptID {
		t.Fatalf("shared-owner attempt was not fetched by its original identity: %+v", call)
	}
	update(m, receive(t, messages))
	if m.authUI.screen != authOAuthScreen || m.authUI.providerID != "provider-b" ||
		m.authUI.attemptProvider != "provider-a" || m.authUI.attemptProfile != "profile-a" || m.authUI.attemptID != sharedAttempt.AttemptID {
		t.Fatalf("alias picker lost the original shared-owner attempt identity: state=%+v", m.authUI)
	}
}

func TestAuthPartialAccountSaveGuidesSelectionRecovery(t *testing.T) {
	accountReads := make(chan authAccountsCall, 2)
	deleteCalls := make(chan authAttemptCall, 1)
	accounts := api.AuthAccounts{
		OwnerID: "owner-1", SelectedAccountID: "primary",
		Accounts: []api.AuthAccount{
			{AccountID: "primary", Label: "Primary", Connected: true},
			{AccountID: "saved-slot", Label: "Saved account", Connected: true},
		},
	}
	backend := &fakeBackend{
		core:                   api.Core{CoreID: "core-1"},
		authAccountsCalls:      accountReads,
		deleteAuthAttemptCalls: deleteCalls,
		authAccountsFunc: func(context.Context, apiclient.Scope, string, string) (api.AuthAccounts, error) {
			return accounts, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	state := &authUIState{
		open: true, generation: 4, coreID: "core-1", scope: apiclient.Scope{Workspace: "/work"},
		providerID: "openai-chatgpt", selectedProfile: "chatgpt-account", screen: authOAuthScreen,
		attemptID: "attempt-1", attemptCoreID: "core-1", attemptProvider: "openai-chatgpt", attemptProfile: "chatgpt-account",
	}
	m.authUI = state
	attempt := api.AuthAttempt{
		CoreID: "core-1", AttemptID: "attempt-1", ProviderID: "openai-chatgpt", AuthProfile: "chatgpt-account",
		OwnerID: "owner-1", AccountID: "saved-slot", Phase: "failed", AccountSaved: true, SelectionSaved: false,
	}
	m.finishAuthAttempt(state, attempt)
	if !strings.Contains(state.status, "Account saved") || !strings.Contains(state.status, "selection was not completed") ||
		!strings.Contains(state.status, "press Enter") || state.screen != authAccountsScreen || state.selectionRecoveryAccountID != "saved-slot" {
		t.Fatalf("partial save was presented as a retryable login failure: state=%+v", state)
	}
	deleted := <-deleteCalls
	if deleted.attemptID != attempt.AttemptID {
		t.Fatalf("terminal attempt metadata cleanup ID = %+v", deleted)
	}
	update(m, receive(t, messages))
	call := <-accountReads
	if call.scope.SessionID != "" || call.providerID != "openai-chatgpt" || call.authProfile != "chatgpt-account" {
		t.Fatalf("initial recovery account read used an unexpected target: %+v", call)
	}
	update(m, receive(t, messages))
	if state.index != 1 || state.accounts.Accounts[state.index].AccountID != "saved-slot" || !strings.Contains(state.status, "press Enter") {
		t.Fatalf("saved account was not highlighted with selection recovery guidance: index=%d state=%+v", state.index, state)
	}
	m.sessionLoading = true
	m.sessionGeneration++
	update(m, sessionLoadedMsg{
		generation: m.sessionGeneration,
		session:    api.Session{ID: "session-b", WorkspaceRoot: "/work"},
	})
	call = <-accountReads
	if call.scope.SessionID != "session-b" || call.providerID != "openai-chatgpt" || call.authProfile != "chatgpt-account" {
		t.Fatalf("selection recovery did not read the original account target through the current Session: %+v", call)
	}
	for {
		message := receive(t, messages)
		if loaded, ok := message.(authAccountsLoadedMsg); ok && loaded.scope.SessionID == "session-b" {
			update(m, loaded)
			break
		}
		update(m, message)
	}
	if state.sessionGeneration != m.sessionGeneration || state.scope.SessionID != "session-b" || state.index != 1 ||
		state.accounts.Accounts[state.index].AccountID != "saved-slot" || !strings.Contains(state.status, "press Enter") {
		t.Fatalf("selection recovery was lost after Session change: state=%+v", state)
	}
	select {
	case <-backend.beginAuthAttemptCalls:
		t.Fatal("partial account save incorrectly started another browser attempt")
	default:
	}
}
