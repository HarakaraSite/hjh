package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// Status is the display projection of the selected ChatGPT account.
type Status struct {
	Connected        bool
	Identity         string
	Account          string
	ExpiresAt        time.Time
	PlanUsageEnabled bool
}

type Options struct {
	// StateDir is the credential storage root (its credentials/ subdirectory
	// contains the records). When empty, os.UserConfigDir()/hjh is used.
	StateDir   string
	HTTPClient *http.Client
}

type Manager struct {
	stateDir   string
	httpClient *http.Client

	mu           sync.Mutex
	attempts     map[string]*attemptJob
	slotAttempts map[string]string
	closed       bool
	closeStarted bool
	closeDone    chan struct{}
	closeErr     error
	lifeCtx      context.Context
	lifeCancel   context.CancelFunc
	jobs         sync.WaitGroup
	ticketTail   chan struct{}

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
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	ticketTail := make(chan struct{})
	close(ticketTail)
	return &Manager{
		stateDir:     options.StateDir,
		httpClient:   client,
		attempts:     make(map[string]*attemptJob),
		slotAttempts: make(map[string]string),
		closeDone:    make(chan struct{}),
		lifeCtx:      lifeCtx,
		lifeCancel:   lifeCancel,
		ticketTail:   ticketTail,
	}
}

// BeginClose stops accepting new credential operations and cancels pending
// authorization work. Accepted saves remain owned by Close until they finish.
func (m *Manager) BeginClose() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.lifeCancel()
	m.mu.Unlock()
}

// Close stops accepting operations, cancels OAuth work, and joins accepted
// credential tickets and attempt jobs. It is safe to call more than once.
func (m *Manager) Close() error {
	m.BeginClose()
	m.mu.Lock()
	if m.closeStarted {
		done := m.closeDone
		m.mu.Unlock()
		<-done
		return m.closeErr
	}
	m.closeStarted = true
	m.mu.Unlock()
	m.jobs.Wait()

	m.mu.Lock()
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
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory for credentials: %w", err)
	}
	return filepath.Join(configDir, "hjh"), nil
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
	// Standard profiles use this explicit prefix so they follow XDG_CONFIG_HOME
	// even when the variable is unset and the platform default must be used.
	const configPrefix = "${XDG_CONFIG_HOME}/"
	if strings.HasPrefix(path, configPrefix) {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("expand credential config path: %w", err)
		}
		return filepath.Join(configDir, strings.TrimPrefix(path, configPrefix)), nil
	}
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
