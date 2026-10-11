package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hjh/internal/api"
)

// Target is the provider credential target resolved from the active config.
// ConfigPath is the normalized absolute path of that config; Name is its
// effective authProfile.
type Target struct {
	ConfigPath   string
	ProviderID   string
	Name         string
	Kind         string
	OverrideFile string
	Profile      Profile
}

type ResolveTarget func(context.Context) (Target, error)

type TargetState struct {
	Target  Target
	APIKey  *api.APIKeyState
	ChatGPT *api.AuthAccounts
}

// Binding captures the credential owner and, for OAuth, the selected account
// at preparation time. Its resolver never follows a later account selection.
type Binding struct {
	Kind      string
	OwnerID   string
	AccountID string

	resolve func(context.Context) (Resolved, error)
}

func (b *Binding) Resolve(ctx context.Context) (Resolved, error) {
	if b == nil || b.resolve == nil {
		return Resolved{}, errors.New("credential binding is not initialized")
	}
	return b.resolve(ctx)
}

type accountMetadata struct {
	SchemaVersion     int      `json:"schemaVersion"`
	Revision          uint64   `json:"revision"`
	SelectedAccountID string   `json:"selectedAccountId"`
	AccountIDs        []string `json:"accountIds"`
}

type apiKeyRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	Revision      uint64 `json:"revision"`
	MutationID    string `json:"mutationId"`
	Key           string `json:"key"`
}

func (m *Manager) Inspect(ctx context.Context, resolve func(context.Context) ([]Target, error)) ([]TargetState, error) {
	var states []TargetState
	err := m.runTicket(ctx, false, func(operationCtx context.Context) error {
		targets, err := resolve(operationCtx)
		if err != nil {
			return err
		}
		states = make([]TargetState, 0, len(targets))
		for _, target := range targets {
			target, err = normalizeTarget(target)
			if err != nil {
				return err
			}
			state := TargetState{Target: target}
			switch target.Kind {
			case KindAPIKey:
				keyState, err := m.apiKeyState(operationCtx, target)
				if err != nil {
					return err
				}
				state.APIKey = &keyState
			case KindChatGPTOAuth:
				accounts, err := m.accountsForTarget(operationCtx, target)
				if err != nil {
					return err
				}
				state.ChatGPT = &accounts
			}
			states = append(states, state)
		}
		return nil
	})
	return states, err
}

func (m *Manager) RegisterAPIKey(ctx context.Context, resolve ResolveTarget, mutationID, value string) (api.APIKeyState, error) {
	var state api.APIKeyState
	err := m.runTicket(ctx, true, func(operationCtx context.Context) error {
		target, err := resolve(operationCtx)
		if err != nil {
			return err
		}
		target, err = normalizeTarget(target)
		if err != nil {
			return err
		}
		if target.Kind != KindAPIKey {
			return fmt.Errorf("provider %s uses %s authentication", target.ProviderID, target.Kind)
		}
		if mutationID == "" {
			return errors.New("API key mutation ID is required")
		}
		path, err := m.apiKeyPath(target)
		if err != nil {
			return err
		}
		var record apiKeyRecord
		err = withFileLock(operationCtx, path, func() error {
			current, found, readErr := readAPIKeyRecord(path)
			if readErr != nil {
				return readErr
			}
			if found && current.MutationID == mutationID && current.Key == value {
				record = current
				return nil
			}
			revision := uint64(1)
			if found {
				revision = current.Revision + 1
			}
			record = apiKeyRecord{SchemaVersion: 1, Revision: revision, MutationID: mutationID, Key: value}
			return writeAPIKeyRecord(path, record)
		})
		if err != nil {
			return err
		}
		state, err = m.apiKeyState(operationCtx, target)
		if err != nil {
			return err
		}
		// Registration completion describes the saved managed record even when
		// an explicit credential-file override currently supplies the run.
		state.Registered = true
		state.Revision = record.Revision
		state.MutationID = record.MutationID
		return nil
	})
	return state, err
}

func (m *Manager) Accounts(ctx context.Context, resolve ResolveTarget) (api.AuthAccounts, error) {
	var accounts api.AuthAccounts
	err := m.runTicket(ctx, false, func(operationCtx context.Context) error {
		target, err := resolve(operationCtx)
		if err != nil {
			return err
		}
		target, err = normalizeTarget(target)
		if err != nil {
			return err
		}
		if target.Kind != KindChatGPTOAuth {
			return fmt.Errorf("provider %s uses %s authentication", target.ProviderID, target.Kind)
		}
		accounts, err = m.accountsForTarget(operationCtx, target)
		return err
	})
	return accounts, err
}

func (m *Manager) SelectAccount(ctx context.Context, resolve ResolveTarget, accountID string) (api.AuthAccounts, error) {
	var accounts api.AuthAccounts
	err := m.runTicket(ctx, true, func(operationCtx context.Context) error {
		target, err := resolve(operationCtx)
		if err != nil {
			return err
		}
		target, err = normalizeTarget(target)
		if err != nil {
			return err
		}
		if target.Kind != KindChatGPTOAuth {
			return fmt.Errorf("provider %s uses %s authentication", target.ProviderID, target.Kind)
		}
		basePath, err := m.oauthPath(target.Name, target.Profile)
		if err != nil {
			return err
		}
		metadataPath := oauthMetadataPath(basePath)
		err = withAccountMetadataLock(operationCtx, basePath, func() error {
			metadata, found, err := readAccountMetadata(metadataPath)
			if err != nil {
				return err
			}
			if !found {
				metadata = defaultAccountMetadata()
			}
			if !containsAccount(metadata.AccountIDs, accountID) {
				return fmt.Errorf("ChatGPT account %q is not registered for this credential owner", accountID)
			}
			accountPath := oauthAccountPath(basePath, accountID)
			var connected bool
			if err := withFileLock(operationCtx, accountPath, func() error {
				_, connected, err = readOAuthRecord(accountPath)
				return err
			}); err != nil {
				return err
			}
			if !connected {
				return fmt.Errorf("ChatGPT account %q is not connected", accountID)
			}
			if metadata.SelectedAccountID == accountID {
				return nil
			}
			metadata.SelectedAccountID = accountID
			metadata.Revision++
			return writeAccountMetadata(metadataPath, metadata)
		})
		if err != nil {
			return err
		}
		accounts, err = m.accountsForTarget(operationCtx, target)
		return err
	})
	return accounts, err
}

func (m *Manager) Capture(ctx context.Context, target Target) (*Binding, error) {
	var binding *Binding
	err := m.runTicket(ctx, false, func(operationCtx context.Context) error {
		normalized, err := normalizeTarget(target)
		if err != nil {
			return err
		}
		binding, err = m.capture(operationCtx, normalized)
		return err
	})
	return binding, err
}

func (m *Manager) capture(ctx context.Context, target Target) (*Binding, error) {
	switch target.Kind {
	case KindAPIKey:
		value, identity, ownerID, err := m.captureAPIKey(ctx, target)
		if err != nil {
			return nil, err
		}
		return &Binding{Kind: target.Kind, OwnerID: ownerID,
			resolve: func(context.Context) (Resolved, error) { return Resolved{Token: value, Identity: identity}, nil }}, nil
	case KindChatGPTOAuth:
		path, err := m.oauthPath(target.Name, target.Profile)
		if err != nil {
			return nil, err
		}
		metadataPath := oauthMetadataPath(path)
		var metadata accountMetadata
		err = withAccountMetadataLock(ctx, path, func() error {
			var found bool
			var readErr error
			metadata, found, readErr = readAccountMetadata(metadataPath)
			if readErr == nil && !found {
				metadata = defaultAccountMetadata()
			}
			return readErr
		})
		if err != nil {
			return nil, err
		}
		accountID := metadata.SelectedAccountID
		ownerID := oauthOwnerID(path)
		accountPath := oauthAccountPath(path, accountID)
		return &Binding{Kind: target.Kind, OwnerID: ownerID, AccountID: accountID,
			resolve: func(resolveCtx context.Context) (Resolved, error) {
				return m.resolveOAuthAccount(resolveCtx, target.Name, accountPath)
			}}, nil
	case KindNone:
		key, err := m.BindingKey(target)
		if err != nil {
			return nil, err
		}
		identity := profileIdentity(target.Name)
		return &Binding{Kind: target.Kind, OwnerID: key,
			resolve: func(context.Context) (Resolved, error) { return Resolved{Identity: identity}, nil }}, nil
	default:
		return nil, fmt.Errorf("unsupported credential kind %q", target.Kind)
	}
}

// BindingKey identifies a credential owner without reading or resolving its
// secret. OAuth selection is excluded because a prepared run pins its account.
func (m *Manager) BindingKey(target Target) (string, error) {
	target, err := normalizeTarget(target)
	if err != nil {
		return "", err
	}
	switch target.Kind {
	case KindChatGPTOAuth:
		path, err := m.oauthPath(target.Name, target.Profile)
		if err != nil {
			return "", err
		}
		return target.Kind + "\x00" + oauthOwnerID(path), nil
	case KindAPIKey:
		if target.OverrideFile != "" {
			return target.Kind + "\x00override-file\x00" + target.OverrideFile, nil
		}
		path, err := m.apiKeyPath(target)
		if err != nil {
			return "", err
		}
		return target.Kind + "\x00" + path, nil
	case KindNone:
		return target.Kind + "\x00" + target.ConfigPath + "\x00" + target.Name, nil
	default:
		return "", fmt.Errorf("unsupported credential kind %q", target.Kind)
	}
}

func (m *Manager) captureAPIKey(ctx context.Context, target Target) (value, identity, ownerID string, err error) {
	key, err := m.BindingKey(target)
	if err != nil {
		return "", "", "", err
	}
	ownerID = digestID(key)
	if target.OverrideFile != "" {
		value, err = readExternalAPIKey(target.OverrideFile, Profile{})
		if err != nil {
			return "", "", "", err
		}
		return value, profileIdentity(target.Name), ownerID, nil
	}
	path, err := m.apiKeyPath(target)
	if err != nil {
		return "", "", "", err
	}
	var record apiKeyRecord
	var found bool
	err = withFileLock(ctx, path, func() error {
		var readErr error
		record, found, readErr = readAPIKeyRecord(path)
		return readErr
	})
	if err != nil {
		return "", "", "", err
	}
	if found {
		identity = apiKeyRevisionIdentity(ownerID, record.Revision)
		return record.Key, identity, ownerID, nil
	}
	value, err = readExternalAPIKey("", target.Profile)
	if err != nil {
		return "", "", "", err
	}
	return value, profileIdentity(target.Name), ownerID, nil
}

func readExternalAPIKey(overrideFile string, profile Profile) (string, error) {
	if overrideFile != "" {
		path, err := normalizeFilePath(overrideFile)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read credential file %s: %w", path, err)
		}
		key := strings.TrimSpace(string(data))
		if key == "" {
			return "", fmt.Errorf("credential file is empty: %s", path)
		}
		return key, nil
	}
	if profile.Env != "" {
		if value, ok := os.LookupEnv(profile.Env); ok && value != "" {
			return value, nil
		}
	}
	if profile.File != "" {
		path, err := normalizeFilePath(profile.File)
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read credential file %s: %w", path, err)
		}
		key := strings.TrimSpace(string(data))
		if key == "" {
			return "", fmt.Errorf("credential file is empty: %s", path)
		}
		return key, nil
	}
	if profile.Env != "" {
		return "", fmt.Errorf("credential environment variable %q is empty", profile.Env)
	}
	return "", errors.New("credential profile has no environment or file source")
}

func (m *Manager) apiKeyState(ctx context.Context, target Target) (api.APIKeyState, error) {
	path, err := m.apiKeyPath(target)
	if err != nil {
		return api.APIKeyState{}, err
	}
	var record apiKeyRecord
	var found bool
	err = withFileLock(ctx, path, func() error {
		var readErr error
		record, found, readErr = readAPIKeyRecord(path)
		return readErr
	})
	if err != nil {
		return api.APIKeyState{}, err
	}
	state := api.APIKeyState{Registered: found}
	if found {
		state.Revision = record.Revision
		state.MutationID = record.MutationID
	}
	switch {
	case target.OverrideFile != "":
		state.Source = "override-file"
		_, err = readExternalAPIKey(target.OverrideFile, Profile{})
	case found:
		state.Source = "registered"
	case target.Profile.Env != "" && envCredentialPresent(target.Profile.Env):
		state.Source = "env"
	case target.Profile.File != "":
		state.Source = "file"
		_, err = readExternalAPIKey("", Profile{File: target.Profile.File})
	default:
		state.Source = "missing"
	}
	if errors.Is(err, os.ErrNotExist) {
		state.Source = "missing"
		err = nil
	}
	if err != nil {
		return api.APIKeyState{}, err
	}
	return state, nil
}

func envCredentialPresent(name string) bool {
	value, ok := os.LookupEnv(name)
	return ok && value != ""
}

func (m *Manager) apiKeyPath(target Target) (string, error) {
	root, err := m.stateRoot()
	if err != nil {
		return "", err
	}
	profileDigest := sha256.Sum256([]byte(target.Name))
	path := filepath.Join(root, "credentials", "api-keys", credentialScopeID(target.ConfigPath), hex.EncodeToString(profileDigest[:])+".json")
	return filepath.Abs(path)
}

func (m *Manager) accountsForTarget(ctx context.Context, target Target) (api.AuthAccounts, error) {
	basePath, err := m.oauthPath(target.Name, target.Profile)
	if err != nil {
		return api.AuthAccounts{}, err
	}
	metadataPath := oauthMetadataPath(basePath)
	var result api.AuthAccounts
	err = withAccountMetadataLock(ctx, basePath, func() error {
		metadata, found, err := readAccountMetadata(metadataPath)
		if err != nil {
			return err
		}
		if !found {
			metadata = defaultAccountMetadata()
		}
		result = api.AuthAccounts{OwnerID: oauthOwnerID(basePath), Revision: metadata.Revision, SelectedAccountID: metadata.SelectedAccountID}
		result.Accounts = make([]api.AuthAccount, 0, len(metadata.AccountIDs))
		for _, accountID := range metadata.AccountIDs {
			accountPath := oauthAccountPath(basePath, accountID)
			var record oauthRecord
			var connected bool
			if err := withFileLock(ctx, accountPath, func() error {
				var readErr error
				record, connected, readErr = readOAuthRecord(accountPath)
				return readErr
			}); err != nil {
				return err
			}
			label := accountID
			account := api.AuthAccount{AccountID: accountID, Connected: connected}
			if accountID == "primary" {
				label = "標準アカウント"
			}
			if connected {
				if record.Email != "" {
					label = record.Email
				}
				expires := record.ExpiresAt
				account.ExpiresAt = &expires
				account.PlanUsageEnabled = hasScope(record.Scopes, chatGPTPlanScope)
			}
			account.Label = label
			result.Accounts = append(result.Accounts, account)
		}
		return nil
	})
	if err != nil {
		return api.AuthAccounts{}, err
	}
	result.Attempts = m.attemptsForOwner(result.OwnerID)
	return result, nil
}

func (m *Manager) attemptsForOwner(ownerID string) []api.AuthAttempt {
	m.mu.Lock()
	ids := make([]string, 0, len(m.attempts))
	jobs := make(map[string]*attemptJob, len(m.attempts))
	for id, job := range m.attempts {
		if job.ownerID == ownerID {
			ids = append(ids, id)
			jobs[id] = job
		}
	}
	m.mu.Unlock()
	sort.Strings(ids)
	result := make([]api.AuthAttempt, 0, len(ids))
	for _, id := range ids {
		result = append(result, jobs[id].snapshot())
	}
	return result
}

func (m *Manager) resolveOAuthAccount(ctx context.Context, profileName, path string) (Resolved, error) {
	var resolved Resolved
	err := withFileLock(ctx, path, func() error {
		record, found, readErr := readOAuthRecord(path)
		if readErr != nil {
			return readErr
		}
		if !found {
			return fmt.Errorf("ChatGPT login is required for credential profile %q", profileName)
		}
		if !hasScope(record.Scopes, chatGPTPlanScope) {
			return errors.New("ChatGPT plan usage is not enabled for this account")
		}
		if !record.ExpiresAt.After(time.Now().Add(30 * time.Second)) {
			if record.Refresh == "" {
				return errors.New("ChatGPT access token expired and no refresh token is available")
			}
			if err := m.refreshRecord(ctx, path, &record); err != nil {
				return err
			}
		}
		if !hasScope(record.Scopes, chatGPTPlanScope) {
			return errors.New("ChatGPT plan usage is not enabled for this account")
		}
		resolved = Resolved{Token: record.Access, Identity: accountIdentity(record.Issuer, record.ClientID, record.Subject)}
		return nil
	})
	if err != nil {
		return Resolved{}, err
	}
	return resolved, nil
}

func (m *Manager) runTicket(ctx context.Context, detached bool, action func(context.Context) error) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("credential manager is closing")
	}
	previous := m.ticketTail
	next := make(chan struct{})
	m.ticketTail = next
	m.jobs.Add(1)
	m.mu.Unlock()
	releaseTicket := true
	defer func() {
		if releaseTicket {
			close(next)
			m.jobs.Done()
		}
	}()
	if detached {
		<-previous
	} else {
		select {
		case <-previous:
		case <-ctx.Done():
			releaseTicket = false
			go func() {
				<-previous
				close(next)
				m.jobs.Done()
			}()
			return ctx.Err()
		}
	}
	if detached {
		return action(context.Background())
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return action(ctx)
}

func normalizeTarget(target Target) (Target, error) {
	if target.ConfigPath == "" {
		return Target{}, errors.New("credential target has no configuration path")
	}
	path, err := filepath.Abs(target.ConfigPath)
	if err != nil {
		return Target{}, fmt.Errorf("resolve credential configuration path: %w", err)
	}
	target.ConfigPath = filepath.Clean(path)
	if target.OverrideFile != "" {
		target.OverrideFile, err = normalizeFilePath(target.OverrideFile)
		if err != nil {
			return Target{}, err
		}
	}
	return target, nil
}

func normalizeFilePath(path string) (string, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve credential file path: %w", err)
	}
	return filepath.Clean(absolute), nil
}

func credentialScopeID(configPath string) string {
	digest := sha256.Sum256([]byte(configPath))
	return hex.EncodeToString(digest[:])
}

func oauthOwnerID(path string) string {
	digest := sha256.Sum256([]byte(path))
	return hex.EncodeToString(digest[:])
}

func digestID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func apiKeyRevisionIdentity(ownerID string, revision uint64) string {
	return fmt.Sprintf("credential:%s:%d", ownerID, revision)
}

func oauthMetadataPath(basePath string) string { return basePath + ".accounts.json" }

func oauthAccountPath(basePath, accountID string) string {
	if accountID == "primary" {
		return basePath
	}
	return filepath.Join(basePath+".accounts", accountID+".json")
}

func defaultAccountMetadata() accountMetadata {
	return accountMetadata{SchemaVersion: 1, SelectedAccountID: "primary", AccountIDs: []string{"primary"}}
}

func readAccountMetadata(path string) (accountMetadata, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return accountMetadata{}, false, nil
	}
	if err != nil {
		return accountMetadata{}, false, fmt.Errorf("read ChatGPT account metadata: %w", err)
	}
	var metadata accountMetadata
	if jsonErr := json.Unmarshal(data, &metadata); jsonErr != nil || metadata.SchemaVersion != 1 || metadata.SelectedAccountID == "" || len(metadata.AccountIDs) == 0 || !containsAccount(metadata.AccountIDs, "primary") || !containsAccount(metadata.AccountIDs, metadata.SelectedAccountID) {
		return accountMetadata{}, false, errors.New("could not read ChatGPT account metadata")
	}
	seen := make(map[string]struct{}, len(metadata.AccountIDs))
	for _, id := range metadata.AccountIDs {
		if id == "" {
			return accountMetadata{}, false, errors.New("could not read ChatGPT account metadata")
		}
		if _, ok := seen[id]; ok {
			return accountMetadata{}, false, errors.New("could not read ChatGPT account metadata")
		}
		seen[id] = struct{}{}
	}
	return metadata, true, nil
}

func writeAccountMetadata(path string, metadata accountMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode ChatGPT account metadata: %w", err)
	}
	return writePrivateFile(path, data)
}

func containsAccount(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func readAPIKeyRecord(path string) (apiKeyRecord, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return apiKeyRecord{}, false, nil
	}
	if err != nil {
		return apiKeyRecord{}, false, fmt.Errorf("read registered API key: %w", err)
	}
	var record apiKeyRecord
	if jsonErr := json.Unmarshal(data, &record); jsonErr != nil || record.SchemaVersion != 1 || record.Revision == 0 || record.MutationID == "" {
		return apiKeyRecord{}, false, errors.New("could not read registered API key")
	}
	return record, true, nil
}

func writeAPIKeyRecord(path string, record apiKeyRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode registered API key: %w", err)
	}
	return writePrivateFile(path, data)
}
