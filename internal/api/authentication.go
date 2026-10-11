package api

import "time"

type AuthTarget struct {
	ProviderID  string `json:"providerId"`
	AuthProfile string `json:"authProfile"`
	Kind        string `json:"kind"`
}
type AuthProvider struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Kind        string `json:"kind"`
}
type APIKeyState struct {
	Registered bool   `json:"registered"`
	Source     string `json:"source"`
	Revision   uint64 `json:"revision"`
	MutationID string `json:"mutationId,omitempty"`
}
type AuthAccount struct {
	AccountID        string     `json:"accountId"`
	Label            string     `json:"label"`
	Connected        bool       `json:"connected"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
	PlanUsageEnabled bool       `json:"planUsageEnabled"`
}
type AuthAccounts struct {
	OwnerID           string        `json:"ownerId"`
	Revision          uint64        `json:"revision"`
	SelectedAccountID string        `json:"selectedAccountId"`
	Accounts          []AuthAccount `json:"accounts"`
	Attempts          []AuthAttempt `json:"attempts"`
}
type AuthProfile struct {
	AuthProfile string         `json:"authProfile"`
	Methods     []string       `json:"methods"`
	Providers   []AuthProvider `json:"providers"`
	APIKey      *APIKeyState   `json:"apiKey,omitempty"`
	ChatGPT     *AuthAccounts  `json:"chatGPT,omitempty"`
}
type AuthProfiles struct {
	ScopeID  string        `json:"scopeId"`
	Target   AuthTarget    `json:"target"`
	Profiles []AuthProfile `json:"profiles"`
}
type AuthAttempt struct {
	CoreID           string `json:"coreId"`
	AttemptID        string `json:"attemptId"`
	ScopeID          string `json:"scopeId"`
	ProviderID       string `json:"providerId"`
	AuthProfile      string `json:"authProfile"`
	OwnerID          string `json:"ownerId"`
	AccountID        string `json:"accountId"`
	Phase            string `json:"phase"`
	AuthorizationURL string `json:"authorizationUrl,omitempty"`
	RedirectURI      string `json:"redirectUri,omitempty"`
	Error            *Error `json:"error,omitempty"`
	AccountSaved     bool   `json:"accountSaved"`
	SelectionSaved   bool   `json:"selectionSaved"`
}

func (a AuthAttempt) Terminal() bool {
	return a.Phase == "completed" || a.Phase == "failed" || a.Phase == "cancelled"
}

type AuthRequest struct {
	CoreID      string `json:"coreId"`
	SessionID   string `json:"sessionId,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	ProviderID  string `json:"providerId"`
	AuthProfile string `json:"authProfile,omitempty"`
	MutationID  string `json:"mutationId,omitempty"`
	Value       string `json:"value,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	AttemptID   string `json:"attemptId,omitempty"`
	CallbackURL string `json:"callbackUrl,omitempty"`
}

// ModelsResult is a client-side bundle; HTTP continues to return the models array.
type ModelsResult struct {
	Models        []ModelInfo
	AuthOwnerID   string
	AuthAccountID string
}
