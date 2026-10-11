package tui

import (
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

func (m *model) handleAuthKey(msg tea.KeyPressMsg) tea.Cmd {
	state := m.authUI
	if state == nil || !state.open {
		return nil
	}
	keyName := msg.String()
	if keyName == "ctrl+d" {
		m.cancelAuthAttempt(state)
		state.open = false
		m.pickerOpen = false
		m.requestDetach()
		if m.quitting {
			return tea.Quit
		}
		return nil
	}
	if keyName == "ctrl+q" {
		m.cancelAuthAttempt(state)
		state.open = false
		m.pickerOpen = false
		m.requestCoreShutdown()
		return nil
	}
	switch state.screen {
	case authAPIKeyScreen, authCallbackScreen:
		return m.handleAuthSecretKey(msg)
	case authOAuthScreen:
		switch keyName {
		case "enter":
			if state.attempt != nil && (state.attempt.Phase == "ready" || state.attempt.Phase == "waiting_callback") {
				state.secret = ""
				state.cursor = 0
				state.screen = authCallbackScreen
				state.status = "Paste the browser callback URL. The value will remain masked."
				m.refreshViewport(false)
			}
		case "esc":
			if state.attempt != nil && (state.attempt.Phase == "exchanging" || state.attempt.Phase == "saving") {
				state.open = false
				m.pickerOpen = false
				m.status = "Authorization continues in Core; reopen /login to check its result."
				m.refreshViewport(false)
				return nil
			}
			m.cancelAuthAttempt(state)
			state.screen = authAccountsScreen
			state.secret = ""
			state.cursor = 0
			state.status = "Cancelling authorization in Core."
			m.status = state.status
			m.refreshViewport(false)
		}
		return nil
	default:
		switch keyName {
		case "esc":
			if state.screen == authProvidersScreen {
				state.screen = authMethodsScreen
				state.index = methodIndex(state, state.method)
				m.refreshViewport(false)
				return nil
			}
			if state.screen == authMethodsScreen {
				state.screen = authProfilesScreen
				state.index = profileIndex(state.profiles.Profiles, state.selectedProfile)
				m.refreshViewport(false)
				return nil
			}
			if state.screen == authAccountsScreen && state.targetProvider == "" {
				state.screen = state.returnScreen
				if state.screen == authMethodsScreen {
					state.index = methodIndex(state, state.method)
				} else if state.screen == authProvidersScreen {
					state.index = providerIndex(state, state.providerID)
				}
				m.refreshViewport(false)
				return nil
			}
			state.secret = ""
			state.cursor = 0
			state.open = false
			m.pickerOpen = false
			m.status = "ready"
			m.refreshViewport(false)
		case "enter":
			switch state.screen {
			case authProfilesScreen:
				if state.index >= 0 && state.index < len(state.profiles.Profiles) {
					m.chooseAuthProfile(state.profiles.Profiles[state.index])
				}
			case authMethodsScreen:
				profile, ok := m.authProfileValue(state, state.selectedProfile)
				methods := orderedAuthMethods(profile.Methods)
				if ok && state.index >= 0 && state.index < len(methods) {
					m.chooseAuthMethod(methods[state.index], "")
				}
			case authProvidersScreen:
				profile, ok := m.authProfileValue(state, state.selectedProfile)
				providers := authProviders(profile, state.method)
				if ok && state.index >= 0 && state.index < len(providers) {
					m.openAuthProvider(providers[state.index].ID)
				}
			case authAccountsScreen:
				m.selectAuthAccount()
			}
		case "up", "k":
			m.moveAuthSelection(-1)
		case "down", "j":
			m.moveAuthSelection(1)
		case "home":
			state.index = 0
			m.refreshViewport(false)
		case "end":
			state.index = max(0, m.authSelectionCount()-1)
			m.refreshViewport(false)
		case "a":
			if state.screen == authAccountsScreen {
				m.beginAuthAttempt("")
			}
		case "r":
			if state.screen == authAccountsScreen && state.index >= 0 && state.index < len(state.accounts.Accounts) {
				m.beginAuthAttempt(state.accounts.Accounts[state.index].AccountID)
			}
		}
	}
	return nil
}

func (m *model) handleAuthSecretKey(msg tea.KeyPressMsg) tea.Cmd {
	state := m.authUI
	if state == nil {
		return nil
	}
	switch msg.String() {
	case "esc":
		state.secret = ""
		state.cursor = 0
		if state.screen == authCallbackScreen {
			m.cancelAuthAttempt(state)
			state.screen = authAccountsScreen
			state.status = "Cancelling authorization in Core."
			m.refreshViewport(false)
			return nil
		}
		state.screen = state.returnScreen
		if state.targetProvider != "" {
			state.open = false
			m.pickerOpen = false
			m.status = "ready"
		} else if state.screen == authMethodsScreen {
			state.index = methodIndex(state, state.method)
		} else if state.screen == authProvidersScreen {
			state.index = providerIndex(state, state.providerID)
		}
		m.refreshViewport(false)
	case "enter":
		if state.screen == authAPIKeyScreen {
			m.registerAuthAPIKey()
		} else {
			m.submitAuthCallback()
		}
	case "ctrl+u":
		state.secret = ""
		state.cursor = 0
		m.refreshViewport(false)
	case "left":
		if state.cursor > 0 {
			state.cursor--
		}
		m.refreshViewport(false)
	case "right":
		if state.cursor < utf8.RuneCountInString(state.secret) {
			state.cursor++
		}
		m.refreshViewport(false)
	case "home":
		state.cursor = 0
		m.refreshViewport(false)
	case "end":
		state.cursor = utf8.RuneCountInString(state.secret)
		m.refreshViewport(false)
	case "backspace", "ctrl+h":
		state.secret, state.cursor = deleteSecretRune(state.secret, state.cursor, false)
		m.refreshViewport(false)
	case "delete":
		state.secret, state.cursor = deleteSecretRune(state.secret, state.cursor, true)
		m.refreshViewport(false)
	default:
		key := msg.Key()
		if key.Mod == 0 && key.Text != "" {
			m.insertAuthSecret(key.Text)
		}
	}
	return nil
}

func deleteSecretRune(value string, cursor int, after bool) (string, int) {
	runes := []rune(value)
	if after {
		if cursor < 0 || cursor >= len(runes) {
			return value, cursor
		}
		runes = append(runes[:cursor], runes[cursor+1:]...)
		return string(runes), cursor
	}
	if cursor <= 0 || cursor > len(runes) {
		return value, cursor
	}
	runes = append(runes[:cursor-1], runes[cursor:]...)
	return string(runes), cursor - 1
}

func (m *model) appendAuthSecret(value string) {
	if m.authUI == nil || (m.authUI.screen != authAPIKeyScreen && m.authUI.screen != authCallbackScreen) {
		return
	}
	m.insertAuthSecret(value)
}

func (m *model) insertAuthSecret(value string) {
	state := m.authUI
	if state == nil || value == "" {
		return
	}
	runes := []rune(state.secret)
	cursor := min(max(0, state.cursor), len(runes))
	insert := []rune(value)
	runes = append(runes[:cursor], append(insert, runes[cursor:]...)...)
	state.secret = string(runes)
	state.cursor = cursor + len(insert)
	m.refreshViewport(false)
}
