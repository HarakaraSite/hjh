package history

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRenameSessionUpdatesOnlyDisplayMetadata(t *testing.T) {
	ctx := context.Background()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	before, err := store.CreateSession(ctx, SessionInput{
		ID: "rename-session", WorkspaceRoot: "/tmp/workspace",
		AgentChoice: json.RawMessage(`{"name":"default"}`),
		ActiveModel: json.RawMessage(`{"provider":"test","modelId":"test-model"}`),
		CreatedAt:   "2001-01-01T00:00:00.000Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.RenameSession(ctx, before.ID, "Saved title")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title == nil || *updated.Title != "Saved title" || updated.UpdatedAt == before.UpdatedAt {
		t.Fatalf("renamed Session metadata = %#v, before %#v", updated, before)
	}
	if updated.StateRevision != before.StateRevision || updated.NextTurn != before.NextTurn ||
		updated.MessageCount != before.MessageCount || updated.TurnCount != before.TurnCount {
		t.Fatalf("rename changed Session adoption state: before=%#v after=%#v", before, updated)
	}
	if _, err := store.RenameSession(ctx, "missing-session", "ignored"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename missing Session error = %v, want ErrNotFound", err)
	}
}
