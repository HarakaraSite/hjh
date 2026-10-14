package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestWALReadersCanKeepSnapshotWhileHistoryIsWritten(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	var mode string
	var sync int
	if err := writer.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := writer.db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" || sync != 2 {
		t.Fatalf("journal=%s synchronous=%d, want wal/FULL", mode, sync)
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("initial snapshot count=%d err=%v", count, err)
	}
	session, err := writer.CreateSession(ctx, SessionInput{WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("reader changed snapshot count=%d err=%v", count, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Session(ctx, session.ID); err != nil {
		t.Fatalf("new read cannot see committed session: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Session(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteSession(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Session(ctx, session.ID); err != ErrNotFound {
		t.Fatalf("deleted session err=%v", err)
	}
}

func TestExistingSchemaDatabaseSwitchesToWALOnWritableOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := reader.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("read-only changed mode=%s err=%v", mode, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("writable open mode=%s err=%v", mode, err)
	}
}
