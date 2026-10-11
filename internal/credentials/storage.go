package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type oauthRecord struct {
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	Email     string    `json:"email,omitempty"`
	ClientID  string    `json:"client_id"`
	HostID    string    `json:"ext_agent_host_id"`
	IDToken   string    `json:"id_token"`
	Access    string    `json:"access_token"`
	Refresh   string    `json:"refresh_token,omitempty"`
	TokenType string    `json:"token_type"`
	ExpiresAt time.Time `json:"expires_at"`
	Scopes    []string  `json:"scopes"`
	SavedAt   time.Time `json:"saved_at"`
}

// oauthRegistration stores an issued public-client registration before a
// token record exists. It contains no account token material.
type oauthRegistration struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	HostID   string `json:"ext_agent_host_id"`
}

type hostRecord struct {
	ID string `json:"ext_agent_host_id"`
}

func (m *Manager) ensureHostID(ctx context.Context) (string, error) {
	root, err := m.stateRoot()
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(root, "credentials", "chatgpt", "host.json"))
	if err != nil {
		return "", fmt.Errorf("resolve ChatGPT host identity path: %w", err)
	}
	var hostID string
	err = withFileLock(ctx, path, func() error {
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			var record hostRecord
			if json.Unmarshal(data, &record) != nil || record.ID == "" {
				return errors.New("could not read ChatGPT host identity")
			}
			hostID = record.ID
			return nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read ChatGPT host identity: %w", readErr)
		}
		id, idErr := newHostID()
		if idErr != nil {
			return idErr
		}
		encoded, marshalErr := json.Marshal(hostRecord{ID: id})
		if marshalErr != nil {
			return fmt.Errorf("encode ChatGPT host identity: %w", marshalErr)
		}
		if writeErr := writePrivateFile(path, encoded); writeErr != nil {
			return writeErr
		}
		hostID = id
		return nil
	})
	if err != nil {
		return "", err
	}
	return hostID, nil
}

func newHostID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("create ChatGPT host identity: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	text := hex.EncodeToString(id[:])
	return "urn:uuid:" + text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}

func withFileLock(ctx context.Context, path string, action func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create credential state directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open credential lock: %w", err)
	}
	defer lock.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("lock credential state: %w", err)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return action()
}

// withAccountMetadataLock serializes access to the account metadata file at
// basePath + ".accounts.json" using its contract lock path,
// basePath + ".accounts.lock".
func withAccountMetadataLock(ctx context.Context, basePath string, action func() error) error {
	return withFileLock(ctx, basePath+".accounts", action)
}

func readOAuthRecord(path string) (oauthRecord, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return oauthRecord{}, false, nil
	}
	if err != nil {
		return oauthRecord{}, false, fmt.Errorf("read ChatGPT credential record: %w", err)
	}
	var record oauthRecord
	if json.Unmarshal(data, &record) != nil || record.Issuer == "" || record.Subject == "" || record.ClientID == "" || record.Access == "" {
		return oauthRecord{}, false, errors.New("could not read ChatGPT credential record")
	}
	return record, true, nil
}

func writeOAuthRecord(path string, record oauthRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode ChatGPT credential record: %w", err)
	}
	return writePrivateFile(path, data)
}

func oauthRegistrationPath(path string) string {
	return path + ".registration.json"
}

func readOAuthRegistration(path string) (oauthRegistration, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return oauthRegistration{}, false, nil
	}
	if err != nil {
		return oauthRegistration{}, false, fmt.Errorf("read ChatGPT client registration: %w", err)
	}
	var registration oauthRegistration
	if json.Unmarshal(data, &registration) != nil || registration.Issuer == "" || registration.ClientID == "" || registration.HostID == "" {
		return oauthRegistration{}, false, errors.New("could not read ChatGPT client registration")
	}
	return registration, true, nil
}

func writeOAuthRegistration(path string, registration oauthRegistration) error {
	data, err := json.Marshal(registration)
	if err != nil {
		return fmt.Errorf("encode ChatGPT client registration: %w", err)
	}
	return writePrivateFile(path, data)
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create credential state directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".hjh-credential-*")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect credential file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write credential file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close credential file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("save credential file: %w", err)
	}
	return nil
}
