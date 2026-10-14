package tui

import "context"

func (m *model) connectionCommand(fields []string) {
	providerID := m.providerID
	if providerID == "" {
		providerID = m.lastSelection.Provider
	}
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
		if len(fields) != 1 {
			m.status = "Use /effort to select an effort from the picker."
			return
		}
		m.openConnectionPicker(effortPicker)
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
