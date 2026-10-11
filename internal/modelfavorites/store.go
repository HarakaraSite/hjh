// Package modelfavorites owns the Core-local FIFO and persistent favorite sets.
package modelfavorites

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
	"sync"
	"syscall"
)

const schemaVersion = 1

// Store serializes same-Core operations and owns the favorite state directory.
// Directory updates are also protected by a Linux flock so separate Cores
// sharing the directory merge their changes.
type Store struct {
	directory string
	dirErr    error

	mu        sync.Mutex
	cond      *sync.Cond
	queue     []*job
	accepting bool
	started   bool
	closed    chan struct{}
	closeOnce sync.Once
}

type job struct {
	ctx       context.Context
	operation func(context.Context) (any, error)
	result    chan outcome
}

type outcome struct {
	value any
	err   error
}

type diskState struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Revision      uint64              `json:"revision"`
	Providers     map[string][]string `json:"providers"`
}

// New creates a favorite store without touching disk. An empty directory uses
// XDG_STATE_HOME or ~/.local/state/hjh/model-favorites.
func New(directory string) *Store {
	s := &Store{accepting: true, closed: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	if directory == "" {
		resolved, err := defaultDirectory()
		if err != nil {
			s.dirErr = err
			return s
		}
		directory = resolved
	}
	s.directory = directory
	return s
}

func defaultDirectory() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "hjh", "model-favorites"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve favorite state directory: %w", err)
	}
	return filepath.Join(home, ".local", "state", "hjh", "model-favorites"), nil
}

// Execute reserves one FIFO ticket before running operation. If the caller is
// canceled while the operation is queued or running, it may return immediately;
// an operation already running remains in the FIFO until it finishes.
func Execute[T any](s *Store, ctx context.Context, operation func(context.Context) (T, error)) (T, error) {
	var zero T
	value, err := s.execute(ctx, func(operationCtx context.Context) (any, error) {
		return operation(operationCtx)
	})
	if err != nil {
		return zero, err
	}
	return value.(T), nil
}

func (s *Store) execute(ctx context.Context, operation func(context.Context) (any, error)) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if !s.accepting {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if !s.started {
		s.started = true
		go s.run()
	}
	queued := &job{ctx: ctx, operation: operation, result: make(chan outcome, 1)}
	s.queue = append(s.queue, queued)
	s.cond.Signal()
	s.mu.Unlock()

	select {
	case result := <-queued.result:
		return result.value, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var ErrClosed = errors.New("model favorites store is closed")

func (s *Store) run() {
	defer close(s.closed)
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && s.accepting {
			s.cond.Wait()
		}
		if len(s.queue) == 0 && !s.accepting {
			s.mu.Unlock()
			return
		}
		current := s.queue[0]
		copy(s.queue, s.queue[1:])
		s.queue[len(s.queue)-1] = nil
		s.queue = s.queue[:len(s.queue)-1]
		s.mu.Unlock()

		if err := current.ctx.Err(); err != nil {
			current.result <- outcome{err: err}
			continue
		}
		value, err := current.operation(current.ctx)
		current.result <- outcome{value: value, err: err}
	}
}

// BeginClose stops new FIFO tickets without waiting for accepted operations.
func (s *Store) BeginClose() {
	s.mu.Lock()
	if s.accepting {
		s.accepting = false
		if s.started {
			s.cond.Broadcast()
		} else {
			s.closeOnce.Do(func() { close(s.closed) })
		}
	}
	s.mu.Unlock()
}

// Close stops admission and joins all tickets accepted before the stop.
func (s *Store) Close() error {
	s.BeginClose()
	<-s.closed
	return nil
}

// Get reads a provider's current favorite snapshot. It does not create a file.
func (s *Store) Get(ctx context.Context, configPath, providerID string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	state, err := s.read(scopeID(configPath))
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot(configPath, providerID, state), nil
}

// Set applies an explicit desired favorite value and returns the persisted
// snapshot. It is intentionally not context-cancelable after registration.
func (s *Store) Set(configPath, providerID, modelID string, favorite bool) (Snapshot, error) {
	if s.dirErr != nil {
		return Snapshot{}, s.dirErr
	}
	id := scopeID(configPath)
	if err := os.MkdirAll(s.directory, 0755); err != nil {
		return Snapshot{}, fmt.Errorf("create model favorites directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(s.directory, id+".lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return Snapshot{}, fmt.Errorf("open model favorites lock: %w", err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Snapshot{}, fmt.Errorf("lock model favorites: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	state, err := s.readUnlocked(id)
	if err != nil {
		return Snapshot{}, err
	}
	ids := state.Providers[providerID]
	position := sort.SearchStrings(ids, modelID)
	present := position < len(ids) && ids[position] == modelID
	changed := false
	switch {
	case favorite && !present:
		ids = append(ids, "")
		copy(ids[position+1:], ids[position:])
		ids[position] = modelID
		changed = true
	case !favorite && present:
		ids = append(ids[:position], ids[position+1:]...)
		changed = true
	}
	if changed {
		state.Revision++
		if len(ids) == 0 {
			delete(state.Providers, providerID)
		} else {
			state.Providers[providerID] = ids
		}
		if err = s.write(id, state); err != nil {
			return Snapshot{}, err
		}
	}
	return snapshot(configPath, providerID, state), nil
}

// Snapshot is the current favorite metadata for one provider.
type Snapshot struct {
	ScopeID    string
	ProviderID string
	Revision   uint64
	ModelIDs   []string
}

func snapshot(configPath, providerID string, state diskState) Snapshot {
	ids := append([]string(nil), state.Providers[providerID]...)
	if ids == nil {
		ids = []string{}
	}
	return Snapshot{ScopeID: scopeID(configPath), ProviderID: providerID, Revision: state.Revision, ModelIDs: ids}
}

func scopeID(configPath string) string {
	digest := sha256.Sum256([]byte(configPath))
	return hex.EncodeToString(digest[:])
}

func (s *Store) read(id string) (diskState, error) {
	if s.dirErr != nil {
		return diskState{}, s.dirErr
	}
	state, err := s.readUnlocked(id)
	if err != nil {
		return diskState{}, err
	}
	return state, nil
}

func (s *Store) readUnlocked(id string) (diskState, error) {
	path := filepath.Join(s.directory, id+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return diskState{SchemaVersion: schemaVersion, Providers: map[string][]string{}}, nil
	}
	if err != nil {
		return diskState{}, fmt.Errorf("read model favorites: %w", err)
	}
	var decoded struct {
		SchemaVersion *int                `json:"schemaVersion"`
		Revision      *uint64             `json:"revision"`
		Providers     map[string][]string `json:"providers"`
	}
	if err = json.Unmarshal(data, &decoded); err != nil {
		return diskState{}, fmt.Errorf("decode model favorites: %w", err)
	}
	if decoded.SchemaVersion == nil || *decoded.SchemaVersion != schemaVersion || decoded.Revision == nil || decoded.Providers == nil {
		return diskState{}, fmt.Errorf("decode model favorites: unsupported or incomplete state")
	}
	state := diskState{SchemaVersion: *decoded.SchemaVersion, Revision: *decoded.Revision, Providers: decoded.Providers}
	for provider, ids := range state.Providers {
		if ids == nil {
			return diskState{}, fmt.Errorf("decode model favorites: provider %q model IDs are missing", provider)
		}
		if !sort.StringsAreSorted(ids) {
			return diskState{}, fmt.Errorf("decode model favorites: provider %q model IDs are not sorted", provider)
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return diskState{}, fmt.Errorf("decode model favorites: provider %q has duplicate model IDs", provider)
			}
		}
	}
	return state, nil
}

func (s *Store) write(id string, state diskState) error {
	path := filepath.Join(s.directory, id+".json")
	temporary, err := os.CreateTemp(s.directory, id+".tmp-*")
	if err != nil {
		return fmt.Errorf("create model favorites temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	encoder := json.NewEncoder(temporary)
	if err = encoder.Encode(state); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write model favorites: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return fmt.Errorf("close model favorites temporary file: %w", err)
	}
	if err = os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish model favorites: %w", err)
	}
	return nil
}
