package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

func (m *model) moveAuthSelection(delta int) {
	state := m.authUI
	if state == nil {
		return
	}
	count := m.authSelectionCount()
	if count == 0 {
		return
	}
	state.index = min(count-1, max(0, state.index+delta))
	m.refreshViewport(false)
}

func (m *model) authSelectionCount() int {
	state := m.authUI
	if state == nil {
		return 0
	}
	switch state.screen {
	case authProfilesScreen:
		return len(state.profiles.Profiles)
	case authMethodsScreen:
		profile, ok := m.authProfileValue(state, state.selectedProfile)
		if ok {
			return len(profile.Methods)
		}
	case authProvidersScreen:
		profile, ok := m.authProfileValue(state, state.selectedProfile)
		if ok {
			return len(authProviders(profile, state.method))
		}
	case authAccountsScreen:
		return len(state.accounts.Accounts)
	}
	return 0
}

func methodIndex(state *authUIState, method string) int {
	profile, ok := findAuthProfile(state.profiles, state.selectedProfile)
	if !ok {
		return 0
	}
	for index, value := range orderedAuthMethods(profile.Methods) {
		if value == method {
			return index
		}
	}
	return 0
}

func providerIndex(state *authUIState, providerID string) int {
	profile, ok := findAuthProfile(state.profiles, state.selectedProfile)
	if !ok {
		return 0
	}
	for index, provider := range authProviders(profile, state.method) {
		if provider.ID == providerID {
			return index
		}
	}
	return 0
}

func (m *model) authRows() ([]string, []int, int, int, int) {
	state := m.authUI
	if state == nil {
		return nil, nil, 0, -1, 0
	}
	width := max(1, m.width)
	rows := make([]string, 0)
	starts := make([]int, 0)
	cursorRow, cursorCell := -1, 0
	appendWrapped := func(text string) {
		rows = append(rows, renderer.WrapText(text, width)...)
	}
	appendWrapped("Authentication · provider and account settings apply to later model lookup and runs")
	rows = append(rows, "")
	selected := state.index
	switch state.screen {
	case authProfilesScreen:
		if len(state.profiles.Profiles) == 0 {
			appendWrapped(state.status)
		}
		for index, profile := range state.profiles.Profiles {
			providerNames := make([]string, 0, len(profile.Providers))
			for _, provider := range profile.Providers {
				label := provider.DisplayName
				if label == "" {
					label = provider.ID
				}
				providerNames = append(providerNames, label+" ("+provider.Kind+")")
			}
			methods := orderedAuthMethods(profile.Methods)
			status := profileAuthStatus(profile)
			row := fmt.Sprintf("%s · %s · %s · %s", profile.AuthProfile, strings.Join(methods, ", "), strings.Join(providerNames, ", "), status)
			rows, starts = appendAuthSelectedRow(rows, starts, row, index == selected, width)
		}
	case authMethodsScreen:
		appendWrapped("Profile: " + state.selectedProfile)
		rows = append(rows, "")
		profile, _ := m.authProfileValue(state, state.selectedProfile)
		methods := orderedAuthMethods(profile.Methods)
		for index, method := range methods {
			label := method
			if method == "api-key" {
				label = "API key"
			} else if method == "chatgpt-oauth" {
				label = "ChatGPT account"
			}
			rows, starts = appendAuthSelectedRow(rows, starts, label, index == selected, width)
		}
	case authProvidersScreen:
		appendWrapped("Profile: " + state.selectedProfile + " · method: " + state.method)
		rows = append(rows, "")
		profile, _ := m.authProfileValue(state, state.selectedProfile)
		for index, provider := range authProviders(profile, state.method) {
			label := provider.DisplayName
			if label == "" {
				label = provider.ID
			}
			rows, starts = appendAuthSelectedRow(rows, starts, label+" · "+provider.ID, index == selected, width)
		}
	case authAPIKeyScreen:
		appendWrapped("Provider: " + state.providerID + " · profile: " + state.selectedProfile)
		appendWrapped("Current source: " + apiKeyStateLabel(state.apiKey))
		rows = append(rows, "")
		appendWrapped("Paste or type an API key, then press Enter to save it in Core.")
		appendWrapped("The saved value is never shown here. Esc returns to authentication methods.")
		rows = append(rows, "")
		prefix := "API key: "
		cursorRow = len(rows)
		cursorCell = utf8.RuneCountInString(prefix) + min(state.cursor, utf8.RuneCountInString(state.secret))
		appendWrapped(prefix + strings.Repeat("•", utf8.RuneCountInString(state.secret)))
		if state.status != "" {
			rows = append(rows, "")
			appendWrapped(state.status)
		}
	case authAccountsScreen:
		appendWrapped("Provider: " + state.providerID + " · profile: " + state.selectedProfile)
		appendWrapped("Selected account: " + accountLabel(state.accounts, state.accounts.SelectedAccountID))
		rows = append(rows, "")
		if len(state.accounts.Accounts) == 0 {
			appendWrapped("No ChatGPT accounts are listed yet. Press a to add one.")
		}
		for index, account := range state.accounts.Accounts {
			label := account.Label
			if label == "" {
				label = account.AccountID
			}
			connection := "not connected"
			if account.Connected {
				connection = "connected"
			}
			if account.AccountID == state.accounts.SelectedAccountID {
				connection += " · selected"
			}
			if account.ExpiresAt != nil {
				connection += " · token expires " + account.ExpiresAt.Format("2006-01-02 15:04 MST")
			}
			if account.PlanUsageEnabled {
				connection += " · plan usage"
			}
			row := fmt.Sprintf("%s · %s · %s", label, account.AccountID, connection)
			rows, starts = appendAuthSelectedRow(rows, starts, row, index == selected, width)
		}
		if state.status != "" {
			rows = append(rows, "")
			appendWrapped(state.status)
		}
	case authOAuthScreen:
		appendWrapped("ChatGPT authorization · " + attemptStatusValue(state.attempt))
		rows = append(rows, "")
		if state.attempt != nil && state.attempt.AuthorizationURL != "" {
			appendWrapped("Open this URL in a browser:")
			appendWrapped(state.attempt.AuthorizationURL)
		}
		if state.attempt != nil && state.attempt.RedirectURI != "" {
			rows = append(rows, "")
			appendWrapped("Local callback: " + state.attempt.RedirectURI)
		}
		rows = append(rows, "")
		appendWrapped(state.status)
		if state.attempt != nil && (state.attempt.Phase == "ready" || state.attempt.Phase == "waiting_callback") {
			appendWrapped("Enter opens the masked callback field. Esc cancels this attempt.")
		} else if state.attempt != nil && state.attempt.Phase == "starting" {
			appendWrapped("Esc cancels this attempt while Core prepares authorization.")
		} else if state.attempt != nil && (state.attempt.Phase == "exchanging" || state.attempt.Phase == "saving") {
			appendWrapped("Esc returns to the conversation while Core finishes this attempt.")
		}
	case authCallbackScreen:
		appendWrapped("Paste the full browser callback URL into this masked field.")
		appendWrapped("Enter sends it to Core · Ctrl+U clears it · Esc cancels this attempt")
		rows = append(rows, "")
		prefix := "Callback URL: "
		cursorRow = len(rows)
		cursorCell = utf8.RuneCountInString(prefix) + min(state.cursor, utf8.RuneCountInString(state.secret))
		appendWrapped(prefix + strings.Repeat("•", utf8.RuneCountInString(state.secret)))
		rows = append(rows, "")
		appendWrapped(state.status)
	}
	return rows, starts, selected, cursorRow, cursorCell
}

func appendAuthSelectedRow(rows []string, starts []int, text string, selected bool, width int) ([]string, []int) {
	marker := "  "
	if selected {
		marker = "▶ "
	}
	starts = append(starts, len(rows))
	rows = append(rows, renderer.WrapText(marker+text, width)...)
	return rows, starts
}

func orderedAuthMethods(methods []string) []string {
	result := append([]string(nil), methods...)
	sort.Strings(result)
	return result
}

func (m *model) authHelp() string {
	if m.authUI == nil {
		return "Enter send · Alt+Enter newline · F1 cancel · F4 Sessions · /help"
	}
	switch m.authUI.screen {
	case authAPIKeyScreen:
		return "Enter save API key · Esc back · Ctrl+U clear"
	case authAccountsScreen:
		return "↑/↓ select · Enter choose/connect · a add · r reauthenticate · Esc back"
	case authOAuthScreen:
		return "Enter paste callback · Esc cancel/return"
	case authCallbackScreen:
		return "Enter send callback · Ctrl+U clear · Esc cancel"
	default:
		return "↑/↓ select · Enter open · Esc back"
	}
}

func profileAuthStatus(profile api.AuthProfile) string {
	parts := make([]string, 0, 2)
	if containsString(profile.Methods, "api-key") {
		parts = append(parts, "API key: "+apiKeyStateLabel(profile.APIKey))
	}
	if containsString(profile.Methods, "chatgpt-oauth") && profile.ChatGPT != nil {
		parts = append(parts, "ChatGPT: "+accountLabel(*profile.ChatGPT, profile.ChatGPT.SelectedAccountID))
	}
	return strings.Join(parts, " · ")
}

func apiKeyStateLabel(state *api.APIKeyState) string {
	if state == nil {
		return "unknown"
	}
	if state.Registered {
		return "registered (source: " + state.Source + ")"
	}
	return "not registered (source: " + state.Source + ")"
}

func accountLabel(accounts api.AuthAccounts, accountID string) string {
	for _, account := range accounts.Accounts {
		if account.AccountID == accountID {
			if account.Label != "" {
				return account.Label
			}
			return account.AccountID
		}
	}
	if accountID != "" {
		return accountID
	}
	return "none"
}

func attemptStatusValue(attempt *api.AuthAttempt) string {
	if attempt == nil {
		return "checking Core status"
	}
	return attempt.Phase
}

func formatAuthProfiles(profiles api.AuthProfiles) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Effective target: %s · profile: %s · method: %s\n", profiles.Target.ProviderID, profiles.Target.AuthProfile, profiles.Target.Kind)
	for _, profile := range profiles.Profiles {
		fmt.Fprintf(&b, "%s · %s · %s\n", profile.AuthProfile, strings.Join(profile.Methods, ", "), profileAuthStatus(profile))
		for _, provider := range profile.Providers {
			name := provider.DisplayName
			if name == "" {
				name = provider.ID
			}
			fmt.Fprintf(&b, "  %s · %s (%s)\n", name, provider.ID, provider.Kind)
		}
		if profile.ChatGPT != nil {
			for _, account := range profile.ChatGPT.Accounts {
				state := "not connected"
				if account.Connected {
					state = "connected"
				}
				fmt.Fprintf(&b, "  account %s · %s · %s\n", account.Label, account.AccountID, state)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
