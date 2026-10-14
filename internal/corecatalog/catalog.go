// Package corecatalog owns local Core discovery records. Runtime state is read
// through the public Core API; records do not own a process or its executions.
package corecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hjh/internal/api"
	"hjh/internal/apiclient"
)

type Entry struct {
	CoreID    string `json:"coreId"`
	Endpoint  string `json:"endpoint"`
	Workspace string `json:"workspace"`
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"`
	DBPath    string `json:"dbPath"`
}

type Status struct {
	Entry
	State      string              `json:"state"`
	APIVersion string              `json:"apiVersion,omitempty"`
	Error      string              `json:"error,omitempty"`
	Sessions   *int                `json:"sessions,omitempty"`
	ActiveRuns []api.CoreActiveRun `json:"activeRuns,omitempty"`
	Phase      string              `json:"phase,omitempty"`
}

func Directory() (string, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(root, "hjh", "cores"), nil
}

// Register returns the removal operation for this serve invocation's record.
func Register(entry Entry) (func() error, error) {
	directory, err := Directory()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, ".register-*")
	if err != nil {
		return nil, err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(append(body, '\n')); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, entry.CoreID+".json")
	if err := os.Rename(temporary, path); err != nil {
		return nil, err
	}
	return func() error {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}, nil
}

// Entries reads existing registrations without creating directories or files.
// A crashed Core's record remains available for the caller to report.
func Entries(workspace string) ([]Entry, error) {
	directory, err := Directory()
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(directory, file.Name()))
		if err != nil {
			return nil, err
		}
		var entry Entry
		if err := json.Unmarshal(body, &entry); err != nil {
			return nil, fmt.Errorf("read Core registration %s: %w", file.Name(), err)
		}
		if workspace == "" || entry.Workspace == workspace {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].StartedAt != entries[j].StartedAt {
			return entries[i].StartedAt < entries[j].StartedAt
		}
		return entries[i].CoreID < entries[j].CoreID
	})
	return entries, nil
}

func Select(entries []Entry, selector string) (Entry, error) {
	for _, entry := range entries {
		if entry.CoreID == selector {
			return entry, nil
		}
	}
	var matches []Entry
	for _, entry := range entries {
		if strings.HasPrefix(entry.CoreID, selector) {
			matches = append(matches, entry)
		}
	}
	if len(matches) == 0 {
		return Entry{}, fmt.Errorf("core %q not found", selector)
	}
	if len(matches) > 1 {
		return Entry{}, fmt.Errorf("core prefix %q is ambiguous (%d matches)", selector, len(matches))
	}
	return matches[0], nil
}

// Probe uses the server's current identity rather than a PID or persisted state.
// The short deadline applies to discovery, never to graceful shutdown joins.
func Probe(ctx context.Context, entry Entry) (Status, error) {
	status := Status{Entry: entry, State: "unreachable"}
	client, err := apiclient.New(entry.Endpoint)
	if err != nil {
		return status, err
	}
	defer client.Close()
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	core, err := client.Core(probeCtx)
	if err != nil {
		status.Error = err.Error()
		return status, nil
	}
	if entry.CoreID != "" && core.CoreID != entry.CoreID {
		status.State = "replaced"
		return status, nil
	}
	status.CoreID, status.Workspace = core.CoreID, core.Workspace
	status.State, status.APIVersion = "running", core.APIVersion
	return status, nil
}

func Current(entry Entry, core api.Core) Status {
	entry.CoreID, entry.Workspace = core.CoreID, core.Workspace
	return Status{Entry: entry, State: "running", APIVersion: core.APIVersion}
}
