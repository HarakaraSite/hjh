package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestConversationSnapshotAndUpdateShareJSONEncoding(t *testing.T) {
	snapshot := ShareConversationSnapshot(ConversationSnapshot{
		ScopeID: "run-1", ViewID: "view-1", Revision: 3,
		Order:    []string{"entity-1"},
		Entities: map[string]ConversationEntity{"entity-1": {ID: "entity-1", Kind: "assistant", Text: "hello"}},
	})
	snapshotCopy := snapshot
	first, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(snapshotCopy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("shared snapshot JSON changed: %s != %s", first, second)
	}
	if &snapshot.serialized.data[0] != &snapshotCopy.serialized.data[0] {
		t.Fatal("snapshot copies did not reuse the same serialized bytes")
	}
	first, err = json.Marshal(RunSnapshot{RunState: RunState{CoreID: "core-1", RunID: "run-1", Seq: 1}, Conversation: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	second, err = json.Marshal(RunSnapshot{RunState: RunState{CoreID: "core-1", RunID: "run-1", Seq: 2}, Conversation: snapshotCopy})
	if err != nil {
		t.Fatal(err)
	}
	var firstFrame, secondFrame map[string]json.RawMessage
	if err := json.Unmarshal(first, &firstFrame); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second, &secondFrame); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstFrame["conversation"], secondFrame["conversation"]) {
		t.Fatal("run snapshot did not reuse the conversation JSON shape")
	}

	update := ShareConversationUpdate(ConversationUpdate{
		ScopeID: "run-1", ViewID: "view-1", BaseRevision: 3, Revision: 4,
		Upserts: []ConversationEntity{{ID: "entity-1", Kind: "assistant", Text: "hello world"}},
	})
	updateCopy := update
	first, err = json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	second, err = json.Marshal(updateCopy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("shared update JSON changed: %s != %s", first, second)
	}
	if &update.serialized.data[0] != &updateCopy.serialized.data[0] {
		t.Fatal("update copies did not reuse the same serialized bytes")
	}
	first, err = json.Marshal(RunUpdate{RunState: RunState{CoreID: "core-1", RunID: "run-1", Seq: 1}, Conversation: &update})
	if err != nil {
		t.Fatal(err)
	}
	second, err = json.Marshal(RunUpdate{RunState: RunState{CoreID: "core-1", RunID: "run-1", Seq: 2}, Conversation: &updateCopy})
	if err != nil {
		t.Fatal(err)
	}
	firstFrame, secondFrame = nil, nil
	if err := json.Unmarshal(first, &firstFrame); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second, &secondFrame); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstFrame["conversation"], secondFrame["conversation"]) {
		t.Fatal("run update did not reuse the conversation JSON shape")
	}
}
