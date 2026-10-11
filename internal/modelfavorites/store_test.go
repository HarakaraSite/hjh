package modelfavorites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDesiredFavoriteValuesPersistByConfigAndProvider(t *testing.T) {
	directory := t.TempDir()
	store := New(directory)
	configPath := filepath.Join(t.TempDir(), "config.json")
	otherConfig := filepath.Join(t.TempDir(), "config.json")

	initial, err := store.Get(context.Background(), configPath, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 0 || len(initial.ModelIDs) != 0 {
		t.Fatalf("initial favorites = %+v", initial)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("initial read created favorite files: %v", entries)
	}
	if _, err = store.Set(configPath, "provider-a", "model-b", true); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Set(configPath, "provider-a", "model-a", true); err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.Set(configPath, "provider-a", "model-a", true)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Revision != 2 || !reflect.DeepEqual(unchanged.ModelIDs, []string{"model-a", "model-b"}) {
		t.Fatalf("idempotent favorite result = %+v", unchanged)
	}
	removed, err := store.Set(configPath, "provider-a", "model-b", false)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Revision != 3 || !reflect.DeepEqual(removed.ModelIDs, []string{"model-a"}) {
		t.Fatalf("removed favorite result = %+v", removed)
	}

	reopened := New(directory)
	persisted, err := reopened.Get(context.Background(), configPath, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Revision != 3 || !reflect.DeepEqual(persisted.ModelIDs, []string{"model-a"}) {
		t.Fatalf("reopened favorites = %+v", persisted)
	}
	otherProvider, err := reopened.Get(context.Background(), configPath, "provider-b")
	if err != nil {
		t.Fatal(err)
	}
	if otherProvider.Revision != 3 || len(otherProvider.ModelIDs) != 0 {
		t.Fatalf("other provider favorites = %+v", otherProvider)
	}
	otherScope, err := reopened.Get(context.Background(), otherConfig, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if otherScope.Revision != 0 || len(otherScope.ModelIDs) != 0 {
		t.Fatalf("other config favorites = %+v", otherScope)
	}
}

func TestUnreadableFavoriteStateDoesNotBecomeEmptyOrOverwrite(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	id := scopeID(configPath)
	path := filepath.Join(directory, id+".json")
	if err := os.WriteFile(path, []byte("not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	store := New(directory)
	if _, err := store.Get(context.Background(), configPath, "provider-a"); err == nil {
		t.Fatal("corrupt favorite state read as an empty set")
	}
	if _, err := store.Set(configPath, "provider-a", "model-one", true); err == nil {
		t.Fatal("corrupt favorite state was overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "not-json" {
		t.Fatalf("corrupt file changed to %q", data)
	}
}

func TestSeparateStoresMergeConcurrentProviderUpdates(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	stores := []*Store{New(directory), New(directory)}
	const perProvider = 12
	var group sync.WaitGroup
	errors := make(chan error, 2*perProvider)
	for providerIndex, store := range stores {
		providerID := fmt.Sprintf("provider-%d", providerIndex)
		for modelIndex := 0; modelIndex < perProvider; modelIndex++ {
			group.Add(1)
			go func(store *Store, providerID string, modelIndex int) {
				defer group.Done()
				_, err := store.Set(configPath, providerID, fmt.Sprintf("model-%02d", modelIndex), true)
				if err != nil {
					errors <- err
				}
			}(store, providerID, modelIndex)
		}
	}
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	reader := New(directory)
	for providerIndex := range stores {
		providerID := fmt.Sprintf("provider-%d", providerIndex)
		got, err := reader.Get(context.Background(), configPath, providerID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.ModelIDs) != perProvider {
			t.Fatalf("%s favorites = %v", providerID, got.ModelIDs)
		}
		for i, modelID := range got.ModelIDs {
			if want := fmt.Sprintf("model-%02d", i); modelID != want {
				t.Fatalf("%s model %d = %q, want %q", providerID, i, modelID, want)
			}
		}
	}
}

func TestExecuteRunsFavoriteOperationsInTicketOrder(t *testing.T) {
	store := New(t.TempDir())
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, err := Execute(store, context.Background(), func(context.Context) (struct{}, error) {
			close(firstEntered)
			<-releaseFirst
			return struct{}{}, nil
		})
		firstDone <- err
	}()
	<-firstEntered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		_, err := Execute(store, context.Background(), func(context.Context) (struct{}, error) {
			close(secondEntered)
			return struct{}{}, nil
		})
		secondDone <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		store.mu.Lock()
		queued := len(store.queue)
		store.mu.Unlock()
		if queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second favorite operation did not reserve its ticket")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-secondEntered:
		t.Fatal("second favorite operation passed the active ticket")
	default:
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	default:
		t.Fatal("second favorite operation did not run")
	}
}
