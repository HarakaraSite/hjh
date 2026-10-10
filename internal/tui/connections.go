package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"hjh/internal/api"
	"hjh/internal/apiclient"
)

func (m *model) nextConnectionLabel() string {
	provider := m.providerID
	if provider == "" {
		provider = m.lastSelection.Provider
	}
	if provider == "" {
		provider = "Session/config"
	}
	effort := m.effort
	if effort == "" {
		effort = "inherited/default"
	}
	return provider + " · effort: " + effort
}

func (m *model) connectionCommand(fields []string) {
	providerID := m.providerID
	if providerID == "" {
		providerID = m.lastSelection.Provider
	}
	workspace := m.workspace
	scope := apiclient.Scope{SessionID: m.sessionID, Workspace: workspace}
	switch fields[0] {
	case "/provider":
		if len(fields) == 1 {
			m.status = "next connection: " + m.nextConnectionLabel()
			return
		}
		if len(fields) != 2 {
			m.status = "usage: /provider PROVIDER_ID"
			return
		}
		m.providerID = fields[1]
		m.modelID, m.effort, m.authProfile = "", "", ""
		m.modelSet = false
		m.status = "next connection: " + m.providerID + " · destination defaults; use /model and /effort to override"
	case "/effort":
		if len(fields) == 1 {
			m.status = "next connection: " + m.nextConnectionLabel()
			return
		}
		if len(fields) != 2 {
			m.status = "usage: /effort EFFORT"
			return
		}
		m.effort = fields[1]
		m.status = "effort for next run: " + m.effort
	case "/providers":
		m.query("providers", func(ctx context.Context) (string, error) {
			definitions, err := m.backend.Providers(ctx, scope)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, d := range definitions {
				fmt.Fprintf(&b, "%s · %s\n  %s · auth: %s · default model: %s\n", d.ID, d.DisplayName, d.API, d.Auth.Kind, d.DefaultModel)
			}
			return b.String(), nil
		})
	case "/models":
		if len(fields) > 2 {
			m.status = "usage: /models [PROVIDER_ID]"
			return
		}
		if len(fields) == 2 {
			providerID = fields[1]
		}
		m.query("models · "+providerID, func(ctx context.Context) (string, error) {
			models, err := m.backend.Models(ctx, scope, providerID)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, model := range models {
				fmt.Fprintf(&b, "%s · %s\n", model.ID, model.Name)
			}
			return b.String(), nil
		})
	case "/auth":
		if len(fields) > 2 {
			m.status = "usage: /auth [PROVIDER_ID]"
			return
		}
		if len(fields) == 2 {
			providerID = fields[1]
		}
		m.query("authentication", func(ctx context.Context) (string, error) {
			status, err := m.backend.LoginStatus(ctx, scope, providerID)
			if err != nil {
				return "", err
			}
			b, err := json.MarshalIndent(status, "", "  ")
			return string(b), err
		})
	case "/login":
		if len(fields) == 3 && fields[1] == "complete" {
			providerID = fields[2]
			login := m.loginScope(providerID, scope)
			m.query("ChatGPT login", func(ctx context.Context) (string, error) {
				status, err := m.backend.CompleteLogin(ctx, login, "")
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Connected: %t · account: %s", status.Connected, status.Account), nil
			})
			return
		}
		if len(fields) == 4 && fields[1] == "callback" {
			providerID, callback := fields[2], fields[3]
			login := m.loginScope(providerID, scope)
			m.query("ChatGPT login", func(ctx context.Context) (string, error) {
				status, err := m.backend.CompleteLogin(ctx, login, callback)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Connected: %t · account: %s", status.Connected, status.Account), nil
			})
			return
		}
		if len(fields) > 2 {
			m.status = "usage: /login [PROVIDER_ID], /login complete PROVIDER_ID, or /login callback PROVIDER_ID CALLBACK_URL"
			return
		}
		if len(fields) == 2 {
			providerID = fields[1]
		}
		m.queryLogin("Continue with ChatGPT", func(ctx context.Context) (api.Login, error) {
			return m.backend.BeginLogin(ctx, scope, providerID)
		})
	}
}

func (m *model) loginScope(providerID string, fallback apiclient.Scope) api.Login {
	if m.pendingLogin != nil && m.pendingLogin.ProviderID == providerID {
		return *m.pendingLogin
	}
	return api.Login{Workspace: fallback.Workspace, ProviderID: providerID, AuthProfile: m.authProfile}
}
