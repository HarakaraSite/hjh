package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	KindNone         = "none"
	KindAPIKey       = "api-key"
	KindChatGPTOAuth = "chatgpt-oauth"
)

// Profile names an external credential source. Env and File resolve API keys;
// OAuthFile, when set, chooses the hjh-owned ChatGPT credential record path.
type Profile struct {
	Env       string `json:"env,omitempty"`
	File      string `json:"file,omitempty"`
	OAuthFile string `json:"oauthFile,omitempty"`
}

// Resolved contains a token for the transport and a stable, non-secret
// identity for provider state that is scoped to a credential profile/account.
type Resolved struct {
	Token    string
	Identity string
}

// Login describes a pending browser authorization. The redirect URI is also
// used to validate a callback URL pasted from a browser on another machine.
type Login struct {
	AuthorizationURL string
	RedirectURI      string
}

// Status contains displayable account state and never includes a token.
type Status struct {
	Connected        bool
	Identity         string
	Account          string
	ExpiresAt        time.Time
	PlanUsageEnabled bool
}

type Options struct {
	// StateDir is the hjh state directory. When empty, XDG_STATE_HOME/hjh or
	// ~/.local/state/hjh is used.
	StateDir   string
	HTTPClient *http.Client
}

type Manager struct {
	stateDir   string
	httpClient *http.Client

	mu        sync.Mutex
	pending   map[string]*pendingLogin
	live      map[*pendingLogin]struct{}
	closed    bool
	closeDone chan struct{}
	closeErr  error

	discoveryMu sync.Mutex
	discovery   openIDConfiguration
	discoveryAt time.Time

	jwksMu sync.Mutex
	jwks   map[string]rsaPublicKey
	jwksAt time.Time
}

func NewManager(options Options) *Manager {
	client := options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Manager{
		stateDir:   options.StateDir,
		httpClient: client,
		pending:    make(map[string]*pendingLogin),
		live:       make(map[*pendingLogin]struct{}),
		closeDone:  make(chan struct{}),
	}
}

// Close stops all loopback listeners owned by the manager and waits for their
// Serve goroutines to exit. It is safe to call Close more than once.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		done := m.closeDone
		m.mu.Unlock()
		<-done
		return m.closeErr
	}
	m.closed = true
	pending := make([]*pendingLogin, 0, len(m.live))
	for login := range m.live {
		pending = append(pending, login)
	}
	m.mu.Unlock()

	var closeErrors []error
	for _, login := range pending {
		if err := m.retirePending(login.profileName, login); err != nil {
			closeErrors = append(closeErrors, err)
		}
		<-login.watcherDone
	}

	m.mu.Lock()
	m.closeErr = errors.Join(closeErrors...)
	close(m.closeDone)
	err := m.closeErr
	m.mu.Unlock()
	return err
}

// Resolve obtains an API key from an external env/file reference, returns an
// empty token for unauthenticated connections, or returns a current ChatGPT
// access token while refreshing its rotating token when needed.
func (m *Manager) Resolve(ctx context.Context, kind, profileName string, profile Profile) (Resolved, error) {
	switch kind {
	case KindNone:
		return Resolved{Identity: profileIdentity(profileName)}, nil
	case KindAPIKey:
		return resolveAPIKey(profileName, profile)
	case KindChatGPTOAuth:
		return m.resolveChatGPT(ctx, profileName, profile)
	default:
		return Resolved{}, fmt.Errorf("unsupported credential kind %q", kind)
	}
}

// Status reports the saved ChatGPT account associated with a profile. An
// account that has not signed in is represented by Connected=false.
func (m *Manager) Status(ctx context.Context, profileName string, profile Profile) (Status, error) {
	path, err := m.oauthPath(profileName, profile)
	if err != nil {
		return Status{}, err
	}
	var record oauthRecord
	found := false
	err = withFileLock(ctx, path, func() error {
		var readErr error
		record, found, readErr = readOAuthRecord(path)
		return readErr
	})
	if err != nil {
		return Status{}, err
	}
	if !found {
		return Status{}, nil
	}
	return statusFor(record), nil
}

func resolveAPIKey(profileName string, profile Profile) (Resolved, error) {
	if profile.Env != "" {
		if value, ok := os.LookupEnv(profile.Env); ok && value != "" {
			return Resolved{Token: value, Identity: profileIdentity(profileName)}, nil
		}
	}
	if profile.File != "" {
		path, err := expandHome(profile.File)
		if err != nil {
			return Resolved{}, err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return Resolved{}, fmt.Errorf("read credential file %s: %w", path, err)
		}
		key := strings.TrimSpace(string(content))
		if key == "" {
			return Resolved{}, fmt.Errorf("credential file is empty: %s", path)
		}
		return Resolved{Token: key, Identity: profileIdentity(profileName)}, nil
	}
	if profile.Env != "" {
		return Resolved{}, fmt.Errorf("credential environment variable %q is empty", profile.Env)
	}
	return Resolved{}, fmt.Errorf("credential profile %q has no environment or file source", profileName)
}

func profileIdentity(profileName string) string {
	digest := sha256.Sum256([]byte("hjh:credential-profile:\x00" + profileName))
	return "credential:" + hex.EncodeToString(digest[:])
}

func (m *Manager) stateRoot() (string, error) {
	if m.stateDir != "" {
		return m.stateDir, nil
	}
	if base := os.Getenv("XDG_STATE_HOME"); base != "" {
		return filepath.Join(base, "hjh"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory for credential state: %w", err)
	}
	return filepath.Join(home, ".local", "state", "hjh"), nil
}

func (m *Manager) oauthPath(profileName string, profile Profile) (string, error) {
	if profile.OAuthFile != "" {
		path, err := expandHome(profile.OAuthFile)
		if err != nil {
			return "", err
		}
		return filepath.Abs(path)
	}
	root, err := m.stateRoot()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(profileName))
	return filepath.Abs(filepath.Join(root, "credentials", "chatgpt", hex.EncodeToString(digest[:])+".json"))
}

func expandHome(path string) (string, error) {
	if path == "~" {
		return os.UserHomeDir()
	}
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand credential path: %w", err)
	}
	return filepath.Join(home, path[2:]), nil
}
