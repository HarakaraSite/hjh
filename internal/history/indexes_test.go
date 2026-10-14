package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestWritableOpenRemovesRedundantIndexesAndPreservesHistoryAndUniqueness(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession(ctx, SessionInput{WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, BeginExecutionInput{Task: "index check", SessionID: session.ID,
		Model: json.RawMessage(`{}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var saved []Record
	for _, payload := range []json.RawMessage{json.RawMessage(`{"name":"first"}`), json.RawMessage(`{"name":"second"}`)} {
		record, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "resource_revision", Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, record)
	}
	// Recreate the previous v4 catalog to exercise an existing database.
	if _, err := store.db.Exec(`CREATE INDEX semantic_records_execution_ordinal ON semantic_records(execution_id,ordinal);
		CREATE INDEX execution_display_positions_execution ON execution_display_positions(execution_id);`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var count int
	if err := reader.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index'
		AND name IN ('semantic_records_execution_ordinal','execution_display_positions_execution')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("read-only open index count=%d err=%v, want 2", count, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND sql IS NOT NULL`).Scan(&count); err != nil || count != 9 {
		t.Fatalf("writable open index count=%d err=%v, want 9", count, err)
	}
	stored, err := writer.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != len(saved) {
		t.Fatalf("records=%d, want %d", len(stored), len(saved))
	}
	for i, record := range stored {
		if record.ID != saved[i].ID || record.Ordinal != saved[i].Ordinal || string(record.Payload) != string(saved[i].Payload) {
			t.Fatalf("record %d changed: got %#v want %#v", i, record, saved[i])
		}
	}
	if _, err := writer.db.Exec(`INSERT INTO semantic_records
		SELECT 'duplicate-record',execution_id,ordinal,kind,observed_at,payload_json,content_digest
		FROM semantic_records WHERE record_id=?`, saved[0].ID); err == nil {
		t.Fatal("duplicate execution/ordinal was accepted after removing explicit index")
	}
	if _, err := writer.db.Exec(`INSERT INTO execution_display_positions
		SELECT session_correlation,execution_ordinal+100,execution_id FROM execution_display_positions WHERE execution_id=?`, execution.ID); err == nil {
		t.Fatal("duplicate execution display position was accepted after removing explicit index")
	}
	if _, err := writer.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "resource_revision", Payload: json.RawMessage(`{"name":"third"}`)}); err != nil {
		t.Fatalf("cannot append history after index removal: %v", err)
	}
}
