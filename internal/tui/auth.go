package tui

import (
	"context"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

func (m *model) authRequestForProvider(providerID string) api.SelectionRequest {
	request := m.selectionRequest()
	if providerID != "" {
		request.ProviderID = providerID
		request.ModelID = ""
		request.Effort = ""
		request.Role = ""
	}
	return request
}

func (m *model) openAuthUI(providerID string) {
	if m.authUI != nil {
		m.cancelAuthAttempt(m.authUI)
	}
	m.closeSessionPicker()
	m.closeConnectionPicker()
	m.sealCurrentFrame()
	m.authGeneration++
	request := m.authRequestForProvider(providerID)
	state := &authUIState{
		open: true, generation: m.authGeneration, coreID: m.core.CoreID,
		sessionGeneration: m.sessionGeneration,
		scope:             apiclient.Scope{SessionID: request.SessionID, Workspace: request.Workspace},
		selectionRequest:  request, targetProvider: providerID, screen: authProfilesScreen,
	}
	m.authUI = state
	m.pickerOpen = true
	m.viewport.SetYOffset(0)
	m.status = "loading authentication profiles"
	m.loadAuthProfiles(state)
	m.refreshViewport(true)
}

func (m *model) authRouteCurrent(state *authUIState) bool {
	return m.authContextCurrent(state) && !m.sessionLoading && state.sessionGeneration == m.sessionGeneration
}

func (m *model) rebaseAuthUIForCurrentRoute() {
	state := m.authUI
	if state == nil || !state.open || m.sessionLoading {
		return
	}
	if state.attemptJobID != 0 || state.beginPending || state.attempt != nil && !state.attempt.Terminal() {
		state.routeRebasePending = true
		return
	}
	if state.selectionRecoveryAccountID != "" {
		// The attempt and saved slot belong to the original OAuth owner, while the
		// account list is read through the Session that is current now. Keep the
		// original provider/profile so Enter selects that same owner's slot.
		request := m.authRequestForProvider(state.providerID)
		state.sessionGeneration = m.sessionGeneration
		state.scope = apiclient.Scope{SessionID: request.SessionID, Workspace: request.Workspace}
		state.selectionRequest = request
		state.targetProvider = state.providerID
		state.profilesJobID = 0
		state.accountsReadJobID = 0
		state.routeRebasePending = false
		if state.screen == authAccountsScreen {
			m.loadAuthAccounts(state)
		}
		m.refreshViewport(false)
		return
	}
	request := m.authRequestForProvider(state.targetProvider)
	state.sessionGeneration = m.sessionGeneration
	state.scope = apiclient.Scope{SessionID: request.SessionID, Workspace: request.Workspace}
	state.selectionRequest = request
	state.profilesJobID = 0
	state.accountsReadJobID = 0
	state.preserveSecret = state.screen == authAPIKeyScreen || state.secret != ""
	state.routeRebasePending = false
	state.screen = authProfilesScreen
	state.status = "Session changed. Reloading authentication profiles for the current Session."
	m.status = state.status
	m.loadAuthProfiles(state)
	m.refreshViewport(false)
}

func (m *model) applyPendingAuthRouteRebase(state *authUIState) {
	if state != nil && state.routeRebasePending {
		state.routeRebasePending = false
		m.rebaseAuthUIForCurrentRoute()
	}
}

func (m *model) loadAuthProfiles(state *authUIState) {
	if state == nil || m.authUI != state {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	state.profilesJobID = job.id
	generation, coreID, request := state.generation, state.coreID, state.selectionRequest
	m.launch(job, func() tea.Msg {
		value, err := m.backend.AuthProfiles(ctx, request)
		return authProfilesLoadedMsg{jobID: job.id, generation: generation, coreID: coreID, request: request, value: value, err: err}
	})
}

func (m *model) openAuthStatus(providerID string) {
	request := m.authRequestForProvider(providerID)
	m.query("authentication", func(ctx context.Context) (string, error) {
		profiles, err := m.backend.AuthProfiles(ctx, request)
		if err != nil {
			return "", err
		}
		return formatAuthProfiles(profiles), nil
	})
}

func (m *model) acceptAuthProfiles(msg authProfilesLoadedMsg) {
	state := m.authUI
	if state == nil || !state.open || state.generation != msg.generation || state.coreID != msg.coreID ||
		state.profilesJobID != msg.jobID || !m.authRouteCurrent(state) ||
		state.selectionRequest != msg.request || state.screen != authProfilesScreen {
		return
	}
	state.profilesJobID = 0
	if msg.err != nil {
		state.status = "Could not load authentication profiles."
		m.status = state.status
		m.refreshViewport(false)
		m.applyPendingAuthRouteRebase(state)
		return
	}
	state.profiles = msg.value
	state.index = profileIndex(state.profiles.Profiles, state.profiles.Target.AuthProfile)
	if len(state.profiles.Profiles) == 0 {
		state.status = "No authentication profiles are used by the current providers or role."
		m.status = state.status
		m.refreshViewport(false)
		return
	}
	if state.index < 0 {
		state.index = 0
	}
	if state.targetProvider != "" {
		index := profileIndex(state.profiles.Profiles, state.profiles.Target.AuthProfile)
		if index < 0 {
			index = profileForProvider(state.profiles.Profiles, state.targetProvider)
		}
		if index < 0 {
			state.status = "The selected provider has no registered authentication profile."
			m.status = state.status
			m.refreshViewport(false)
			return
		}
		profile := state.profiles.Profiles[index]
		state.index = index
		state.selectedProfile = profile.AuthProfile
		kind := state.profiles.Target.Kind
		if state.profiles.Target.ProviderID != state.targetProvider || kind == "" {
			kind = providerKind(profile, state.targetProvider)
		}
		m.chooseAuthMethod(kind, state.targetProvider)
		return
	}
	state.status = "Choose a profile to manage its authentication."
	m.status = state.status
	m.refreshViewport(false)
}

func profileIndex(profiles []api.AuthProfile, name string) int {
	if name == "" {
		return -1
	}
	for index, profile := range profiles {
		if profile.AuthProfile == name {
			return index
		}
	}
	return -1
}

func profileForProvider(profiles []api.AuthProfile, providerID string) int {
	for index, profile := range profiles {
		for _, provider := range profile.Providers {
			if provider.ID == providerID {
				return index
			}
		}
	}
	return -1
}

func providerKind(profile api.AuthProfile, providerID string) string {
	for _, provider := range profile.Providers {
		if provider.ID == providerID {
			return provider.Kind
		}
	}
	return ""
}

func (m *model) chooseAuthProfile(profile api.AuthProfile) {
	state := m.authUI
	if state == nil {
		return
	}
	state.selectedProfile = profile.AuthProfile
	state.index = 0
	methods := orderedAuthMethods(profile.Methods)
	if len(methods) == 1 {
		m.chooseAuthMethod(methods[0], "")
		return
	}
	state.screen = authMethodsScreen
	state.status = "Choose an authentication method."
	m.status = state.status
	m.refreshViewport(false)
}

func (m *model) chooseAuthMethod(kind, preferredProvider string) {
	state := m.authUI
	if state == nil {
		return
	}
	profile, ok := m.authProfileValue(state, state.selectedProfile)
	if !ok || !containsString(profile.Methods, kind) {
		state.status = "The selected authentication method is unavailable for this profile."
		m.status = state.status
		m.refreshViewport(false)
		return
	}
	state.method = kind
	providers := authProviders(profile, kind)
	if preferredProvider == "" && state.profiles.Target.AuthProfile == profile.AuthProfile && state.profiles.Target.Kind == kind {
		preferredProvider = state.profiles.Target.ProviderID
	}
	if preferredProvider != "" {
		for _, provider := range providers {
			if provider.ID == preferredProvider {
				m.openAuthProvider(provider.ID)
				return
			}
		}
	}
	if len(providers) == 1 {
		m.openAuthProvider(providers[0].ID)
		return
	}
	if len(providers) == 0 {
		state.status = "No provider uses this authentication method in the selected profile."
		m.status = state.status
		m.refreshViewport(false)
		return
	}
	state.index = 0
	state.screen = authProvidersScreen
	state.status = "Choose a provider to manage; this does not change the next run."
	m.status = state.status
	m.refreshViewport(false)
}

func authProviders(profile api.AuthProfile, kind string) []api.AuthProvider {
	providers := make([]api.AuthProvider, 0)
	for _, provider := range profile.Providers {
		if provider.Kind == kind {
			providers = append(providers, provider)
		}
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	return providers
}

func (m *model) authProfileValue(state *authUIState, name string) (api.AuthProfile, bool) {
	if state == nil {
		return api.AuthProfile{}, false
	}
	for _, profile := range state.profiles.Profiles {
		if profile.AuthProfile == name {
			return profile, true
		}
	}
	return api.AuthProfile{}, false
}

func (m *model) openAuthProvider(providerID string) {
	state := m.authUI
	if state == nil {
		return
	}
	state.returnScreen = state.screen
	state.providerID = providerID
	if state.preserveSecret {
		state.preserveSecret = false
	} else {
		state.secret = ""
		state.cursor = 0
	}
	if state.method == "api-key" {
		state.screen = authAPIKeyScreen
		profile, _ := m.authProfileValue(state, state.selectedProfile)
		state.apiKey = profile.APIKey
		state.status = "Enter an API key. The value stays in this masked field."
		m.status = "API key registration"
		m.refreshViewport(false)
		return
	}
	if state.method == "chatgpt-oauth" {
		state.screen = authAccountsScreen
		state.index = 0
		state.status = "Loading ChatGPT accounts."
		m.status = state.status
		m.loadAuthAccounts(state)
		m.refreshViewport(false)
		return
	}
	state.status = "This authentication method has no TUI flow."
	m.status = state.status
	m.refreshViewport(false)
}

func (m *model) loadAuthAccounts(state *authUIState) {
	if state == nil || m.authUI != state {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	state.accountsReadJobID = job.id
	generation, coreID, scope, providerID, profile := state.generation, state.coreID, state.scope, state.providerID, state.selectedProfile
	m.launch(job, func() tea.Msg {
		value, err := m.backend.AuthAccounts(ctx, scope, providerID, profile)
		return authAccountsLoadedMsg{jobID: job.id, generation: generation, coreID: coreID, scope: scope, providerID: providerID, profile: profile, value: value, err: err}
	})
}

func (m *model) acceptAuthAccounts(msg authAccountsLoadedMsg) tea.Cmd {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.coreID != msg.coreID || state.scope != msg.scope ||
		state.accountsReadJobID != msg.jobID || !m.authRouteCurrent(state) ||
		state.providerID != msg.providerID || state.selectedProfile != msg.profile || state.screen != authAccountsScreen {
		return nil
	}
	state.accountsReadJobID = 0
	if msg.err != nil {
		state.status = "Could not load ChatGPT accounts."
		m.status = state.status
		m.refreshViewport(false)
		return nil
	}
	state.accounts = msg.value
	state.ownerID = state.accounts.OwnerID
	state.status = "Enter selects a connected account; a adds; r reauthenticates the selected account."
	state.index = accountIndex(state.accounts.Accounts, state.accounts.SelectedAccountID)
	if state.selectionRecoveryAccountID != "" {
		state.index = accountIndex(state.accounts.Accounts, state.selectionRecoveryAccountID)
		state.status = "Account saved, but selection was not completed. Select the saved account and press Enter to use it."
	}
	m.status = "ChatGPT accounts"
	m.refreshViewport(false)
	for index := len(state.accounts.Attempts) - 1; index >= 0; index-- {
		attempt := state.accounts.Attempts[index]
		sameOwner := attempt.CoreID == state.coreID && attempt.OwnerID != "" && attempt.OwnerID == state.accounts.OwnerID
		sameTarget := attempt.CoreID == state.coreID && attempt.ProviderID == state.providerID && attempt.AuthProfile == state.selectedProfile
		if sameOwner || sameTarget {
			if attempt.AttemptID == state.attemptID && state.attempt != nil && state.attempt.Terminal() {
				return nil
			}
			state.attemptID = attempt.AttemptID
			state.attemptCoreID = attempt.CoreID
			if state.attemptCoreID == "" {
				state.attemptCoreID = state.coreID
			}
			state.attemptScope = state.scope
			state.attemptProvider = attempt.ProviderID
			state.attemptProfile = attempt.AuthProfile
			state.attemptOwnerID = attempt.OwnerID
			state.attempt = &attempt
			if !attempt.Terminal() {
				state.screen = authOAuthScreen
				state.status = attemptStatus(attempt)
				m.status = state.status
				m.refreshViewport(false)
			}
			return m.readAuthAttempt(state)
		}
	}
	return nil
}

func accountIndex(accounts []api.AuthAccount, accountID string) int {
	for index, account := range accounts {
		if account.AccountID == accountID {
			return index
		}
	}
	if len(accounts) > 0 {
		return 0
	}
	return -1
}

func (m *model) registerAuthAPIKey() {
	state := m.authUI
	if state == nil || state.secret == "" || strings.TrimSpace(state.secret) == "" {
		if state != nil {
			state.status = "Enter an API key before saving."
			m.refreshViewport(false)
		}
		return
	}
	value := state.secret
	state.secret = ""
	state.cursor = 0
	mutationID := uuid.NewString()
	request := api.AuthRequest{
		CoreID: state.coreID, SessionID: state.scope.SessionID, Workspace: state.scope.Workspace,
		ProviderID: state.providerID, AuthProfile: state.selectedProfile, MutationID: mutationID, Value: value,
	}
	selectionRequest := state.selectionRequest
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	state.attemptJobID = job.id
	generation, coreID, providerID, profile := state.generation, state.coreID, state.providerID, state.selectedProfile
	state.status = "Saving API key in Core."
	m.status = state.status
	m.refreshViewport(false)
	m.launch(job, func() tea.Msg {
		saved, err := m.backend.RegisterAPIKey(ctx, request)
		if err == nil {
			return authAPIKeySavedMsg{jobID: job.id, generation: generation, coreID: coreID, mutationID: mutationID, providerID: providerID, profile: profile, value: saved}
		}
		profiles, readErr := m.backend.AuthProfiles(ctx, selectionRequest)
		return authAPIKeySavedMsg{jobID: job.id, generation: generation, coreID: coreID, mutationID: mutationID, providerID: providerID, profile: profile, profiles: profiles, err: err, readErr: readErr}
	})
}

func (m *model) acceptAuthAPIKeySaved(msg authAPIKeySavedMsg) {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.coreID != msg.coreID || state.providerID != msg.providerID ||
		state.selectedProfile != msg.profile || state.screen != authAPIKeyScreen || state.attemptJobID != msg.jobID || !m.authContextCurrent(state) {
		return
	}
	state.attemptJobID = 0
	if msg.err == nil {
		state.apiKey = &msg.value
		state.status = "API key saved in Core."
		m.status = state.status
		m.refreshViewport(false)
		m.applyPendingAuthRouteRebase(state)
		return
	}
	if msg.readErr == nil {
		if profile, ok := findAuthProfile(msg.profiles, msg.profile); ok {
			state.apiKey = profile.APIKey
			if profile.APIKey != nil && profile.APIKey.MutationID == msg.mutationID {
				state.status = "API key saved in Core; the confirmation response was interrupted."
				m.status = "API key saved"
				m.refreshViewport(false)
				m.applyPendingAuthRouteRebase(state)
				return
			}
		}
		state.profiles = msg.profiles
	}
	state.status = "API key save result is unknown. Current registration state was refreshed when available; press Enter again only to make a new registration."
	m.status = "API key save result unknown"
	m.refreshViewport(false)
	m.applyPendingAuthRouteRebase(state)
}

func findAuthProfile(profiles api.AuthProfiles, name string) (api.AuthProfile, bool) {
	for _, profile := range profiles.Profiles {
		if profile.AuthProfile == name {
			return profile, true
		}
	}
	return api.AuthProfile{}, false
}

func (m *model) selectAuthAccount() {
	state := m.authUI
	if state == nil || state.index < 0 || state.index >= len(state.accounts.Accounts) {
		return
	}
	account := state.accounts.Accounts[state.index]
	if !account.Connected {
		m.beginAuthAttempt(account.AccountID)
		return
	}
	request := api.AuthRequest{
		CoreID: state.coreID, SessionID: state.scope.SessionID, Workspace: state.scope.Workspace,
		ProviderID: state.providerID, AuthProfile: state.selectedProfile, AccountID: account.AccountID,
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	state.attemptJobID = job.id
	generation, coreID, providerID, profile := state.generation, state.coreID, state.providerID, state.selectedProfile
	state.status = "Saving selected account in Core."
	m.status = state.status
	m.refreshViewport(false)
	m.launch(job, func() tea.Msg {
		value, err := m.backend.SelectAuthAccount(ctx, request)
		return authAccountSelectedMsg{jobID: job.id, generation: generation, coreID: coreID, providerID: providerID, profile: profile, value: value, err: err}
	})
}

func (m *model) acceptAuthAccountSelected(msg authAccountSelectedMsg) {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.coreID != msg.coreID || state.providerID != msg.providerID ||
		state.selectedProfile != msg.profile || state.screen != authAccountsScreen || state.attemptJobID != msg.jobID || !m.authContextCurrent(state) {
		return
	}
	state.attemptJobID = 0
	if msg.err != nil {
		state.status = "Could not save account selection."
		m.status = state.status
		m.refreshViewport(false)
		m.applyPendingAuthRouteRebase(state)
		return
	}
	state.accounts = msg.value
	state.index = accountIndex(state.accounts.Accounts, state.accounts.SelectedAccountID)
	state.status = "Account selection saved in Core."
	state.selectionRecoveryAccountID = ""
	m.selectionGeneration++
	m.status = "ChatGPT account selected for the next model catalog and run"
	m.refreshViewport(false)
	if state.routeRebasePending {
		m.applyPendingAuthRouteRebase(state)
	} else {
		m.loadAuthAccounts(state)
	}
	return
}
