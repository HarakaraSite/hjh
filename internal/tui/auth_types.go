package tui

import (
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

type authScreen string

const (
	authProfilesScreen  authScreen = "profiles"
	authMethodsScreen   authScreen = "methods"
	authProvidersScreen authScreen = "providers"
	authAPIKeyScreen    authScreen = "api-key"
	authAccountsScreen  authScreen = "accounts"
	authOAuthScreen     authScreen = "oauth"
	authCallbackScreen  authScreen = "callback"
)

type authUIState struct {
	open                       bool
	generation                 uint64
	sessionGeneration          uint64
	coreID                     string
	scope                      apiclient.Scope
	selectionRequest           api.SelectionRequest
	profilesJobID              uint64
	accountsReadJobID          uint64
	targetProvider             string
	profiles                   api.AuthProfiles
	screen                     authScreen
	returnScreen               authScreen
	selectedProfile            string
	method                     string
	providerID                 string
	index                      int
	ownerID                    string
	selectionRecoveryAccountID string
	preserveSecret             bool
	routeRebasePending         bool
	apiKey                     *api.APIKeyState
	accounts                   api.AuthAccounts
	secret                     string
	cursor                     int
	status                     string

	attempt           *api.AuthAttempt
	attemptID         string
	attemptCoreID     string
	attemptScope      apiclient.Scope
	attemptProvider   string
	attemptProfile    string
	attemptOwnerID    string
	attemptJobID      uint64
	beginPending      bool
	cancelRequested   bool
	cancelIntent      chan struct{}
	pollJobID         uint64
	deleteJobID       uint64
	callbackSubmitted bool
}

type authProfilesLoadedMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	request    api.SelectionRequest
	value      api.AuthProfiles
	err        error
}

type authAccountsLoadedMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	scope      apiclient.Scope
	providerID string
	profile    string
	value      api.AuthAccounts
	err        error
}

type authAPIKeySavedMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	mutationID string
	providerID string
	profile    string
	value      api.APIKeyState
	profiles   api.AuthProfiles
	err        error
	readErr    error
}

type authAccountSelectedMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	providerID string
	profile    string
	value      api.AuthAccounts
	err        error
}

type authAttemptStartedMsg struct {
	jobID        uint64
	generation   uint64
	coreID       string
	providerID   string
	profile      string
	ownerID      string
	attempt      api.AuthAttempt
	err          error
	cancelIntent chan struct{}
	ack          chan struct{}
}

type authAttemptReadMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	attemptID  string
	attempt    api.AuthAttempt
	err        error
}

type authAttemptCompletedMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	attemptID  string
	attempt    api.AuthAttempt
	err        error
}

type authAttemptCancelledMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	attemptID  string
	attempt    api.AuthAttempt
	err        error
}

type authAttemptDeletedMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	attemptID  string
	err        error
}

type authPollTickMsg struct {
	generation uint64
	coreID     string
	attemptID  string
}
