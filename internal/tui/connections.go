package tui

import (
	"context"
	"fmt"
	"strings"

	"hjh/internal/apiclient"
)

func (m *model) connectionCommand(fields []string) {
	providerID := m.providerID
	if providerID == "" {
		providerID = m.lastSelection.Provider
	}
	workspace := m.workspace
	scope := apiclient.Scope{SessionID: m.sessionID, Workspace: workspace}
	explicitModelsProvider := ""
	switch fields[0] {
	case "/provider":
		if len(fields) == 1 {
			m.openConnectionPicker(providerPicker)
			return
		}
		if len(fields) != 2 {
			m.status = "usage: /provider PROVIDER_ID"
			return
		}
		m.setProvider(fields[1])
	case "/effort":
		if len(fields) == 1 {
			m.openConnectionPicker(effortPicker)
			return
		}
		if len(fields) != 2 {
			m.status = "usage: /effort EFFORT"
			return
		}
		m.setEffort(fields[1])
	case "/providers":
		m.query("providers", func(ctx context.Context) (string, error) {
			definitions, err := m.backend.Providers(ctx, scope)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, d := range definitions {
				fmt.Fprintf(&b, "%s · %s\n  %s · auth: %s", d.ID, d.DisplayName, d.API, d.Auth.Kind)
				if d.Auth.Profile != "" {
					fmt.Fprintf(&b, " / %s", d.Auth.Profile)
				}
				fmt.Fprintf(&b, " · default model: %s", d.DefaultModel)
				if d.DefaultEffort != "" {
					fmt.Fprintf(&b, " · default effort: %s", d.DefaultEffort)
				}
				b.WriteByte('\n')
			}
			return b.String(), nil
		})
	case "/models":
		if len(fields) > 2 {
			m.status = "usage: /models [PROVIDER_ID]"
			return
		}
		if len(fields) == 2 {
			explicitModelsProvider = fields[1]
		}
		baseRequest := m.selectionRequest()
		modelsTitle := "next run"
		if explicitModelsProvider != "" {
			modelsTitle = explicitModelsProvider
		}
		m.query("models · "+modelsTitle, func(ctx context.Context) (string, error) {
			request := baseRequest
			preview, err := m.backend.Selection(ctx, request)
			if explicitModelsProvider != "" && (err != nil || explicitModelsProvider != preview.Selection.Provider) {
				request.ProviderID = explicitModelsProvider
				request.ModelID, request.Effort, request.AuthProfile = "", "", ""
				preview, err = m.backend.Selection(ctx, request)
			}
			if err != nil {
				return "", err
			}
			resolvedProvider := preview.Selection.Provider
			if resolvedProvider == "" {
				resolvedProvider = explicitModelsProvider
			}
			result, err := m.backend.ModelsWithAuth(ctx, scope, resolvedProvider, preview.Selection.AuthProfile)
			if err != nil {
				return "", err
			}
			var b strings.Builder
			for _, model := range result.Models {
				fmt.Fprintf(&b, "%s · %s", model.ID, model.Name)
				if len(model.Efforts) > 0 {
					fmt.Fprintf(&b, " · efforts: %s", strings.Join(model.Efforts, ", "))
					if model.EffortSource != "" {
						fmt.Fprintf(&b, " (%s)", model.EffortSource)
					}
				}
				b.WriteByte('\n')
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
			request := m.authRequestForProvider(providerID)
			profiles, err := m.backend.AuthProfiles(ctx, request)
			if err != nil {
				return "", err
			}
			return formatAuthProfiles(profiles), nil
		})
	case "/login":
		if len(fields) >= 2 && (fields[1] == "complete" || fields[1] == "callback") {
			m.status = "Use /login and paste the callback URL in the masked authentication screen."
			return
		}
		if len(fields) > 2 {
			m.status = "usage: /login [PROVIDER_ID]"
			return
		}
		loginProviderID := ""
		if len(fields) == 2 {
			loginProviderID = fields[1]
		}
		m.openAuthUI(loginProviderID)
	}
}
