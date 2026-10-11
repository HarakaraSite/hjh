package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

func TestInitialSnapshotPrecedesCoalescedPostCutFacts(t *testing.T) {
	view := NewView("run-1")
	defer view.Release()

	walkEntered := make(chan struct{})
	releaseWalk := make(chan struct{})
	loadDone := make(chan error, 1)
	cutThrough := uint64(1)
	go func() {
		loadDone <- view.LoadCut(context.Background(), &cutThrough, nil, func(visit func(contracts.ExecutionFact) error) error {
			close(walkEntered)
			<-releaseWalk
			return visit(contracts.ExecutionFact{RootExecutionID: "root-1", ExecutionID: "root-1", RootExecutionOrdinal: 1,
				Kind: "task", Role: "user", Text: "task", Conversation: contracts.ConversationMetadata{
					RootExecutionID: "root-1", Sequence: 1, EntityKind: "task", LocalKey: "task", FirstSequence: 1,
				}})
		})
	}()
	<-walkEntered

	view.mu.Lock()
	enqueueDone := make(chan struct{})
	go func() {
		view.Enqueue(executionCommit(2, "途中"))
		view.Enqueue(executionCommit(3, "最新本文"))
		close(enqueueDone)
	}()
	select {
	case <-enqueueDone:
	case <-time.After(time.Second):
		view.mu.Unlock()
		t.Fatal("saved-fact notification waited for entity materialization lock")
	}
	view.mu.Unlock()

	close(releaseWalk)
	if err := <-loadDone; err != nil {
		t.Fatal(err)
	}

	initial, subscription, err := view.SubscribeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if initial.Revision != 0 || initial.ThroughSequence == nil || *initial.ThroughSequence != 1 {
		t.Fatalf("initial snapshot did not preserve the captured cut: %+v", initial)
	}
	if findEntity(initial, "task", "task") == nil || findEntity(initial, "assistant", "最新本文") != nil {
		t.Fatalf("post-cut facts leaked into initial snapshot: %+v", initial)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := view.WaitThrough(ctx, 3); err != nil {
		t.Fatal(err)
	}
	update, ok := subscription.TakeUpdate()
	if !ok || update.BaseRevision != 0 || update.Revision != 1 || update.ThroughSequence == nil || *update.ThroughSequence != 3 {
		t.Fatalf("post-cut facts were not coalesced into one ordered update: %+v", update)
	}
	if len(update.Upserts) != 2 {
		t.Fatalf("coalesced update contains the wrong assistant value: %+v", update.Upserts)
	}
	current, err := view.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if assistant := findEntity(current, "assistant", "最新本文"); assistant == nil || assistant.Status != "streaming" || assistant.Position.FirstSequence != 2 {
		t.Fatalf("stable assistant entity position was lost: %+v", current)
	}
	var execution *api.ConversationEntity
	for _, entity := range current.Entities {
		if entity.Kind == "execution" {
			copy := entity
			execution = &copy
		}
	}
	if execution == nil || execution.Status != "completed" || execution.Position.FirstSequence != 2 {
		t.Fatalf("execution terminal update moved the start position: %+v", current)
	}
}

func TestSameRevisionConsumersShareSnapshotAndUpdateValues(t *testing.T) {
	view := NewView("run-1")
	defer view.Release()
	cutThrough := uint64(1)
	if err := view.LoadCut(context.Background(), &cutThrough, nil, func(visit func(contracts.ExecutionFact) error) error {
		return visit(contracts.ExecutionFact{RootExecutionID: "root-1", ExecutionID: "root-1", RootExecutionOrdinal: 1,
			Kind: "task", Role: "user", Text: "task", Conversation: contracts.ConversationMetadata{
				RootExecutionID: "root-1", Sequence: 1, EntityKind: "task", LocalKey: "task", FirstSequence: 1,
			}})
	}); err != nil {
		t.Fatal(err)
	}

	snapshotOne, subscriptionOne, err := view.SubscribeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer subscriptionOne.Close()
	snapshotTwo, subscriptionTwo, err := view.SubscribeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer subscriptionTwo.Close()
	if fmt.Sprintf("%p", snapshotOne.Entities) != fmt.Sprintf("%p", snapshotTwo.Entities) ||
		len(snapshotOne.Order) > 0 && &snapshotOne.Order[0] != &snapshotTwo.Order[0] {
		t.Fatal("same-revision subscriptions rebuilt the snapshot entities or order")
	}
	firstSnapshotJSON, err := json.Marshal(snapshotOne)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshotJSON, err := json.Marshal(snapshotTwo)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstSnapshotJSON, secondSnapshotJSON) {
		t.Fatalf("same-revision snapshots serialized differently: %s != %s", firstSnapshotJSON, secondSnapshotJSON)
	}

	view.Enqueue(executionCommit(2, "live"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := view.WaitThrough(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if view.snapshotCache != nil {
		t.Fatal("a new revision retained the previous snapshot cache owner")
	}
	updateOne, ok := subscriptionOne.TakeUpdate()
	if !ok {
		t.Fatal("first consumer did not receive the changed revision")
	}
	updateTwo, ok := subscriptionTwo.TakeUpdate()
	if !ok {
		t.Fatal("second consumer did not receive the changed revision")
	}
	if len(updateOne.Upserts) == 0 || len(updateOne.Upserts) != len(updateTwo.Upserts) ||
		&updateOne.Upserts[0] != &updateTwo.Upserts[0] {
		t.Fatal("same-revision consumers did not share their identical change set")
	}
	firstUpdateJSON, err := json.Marshal(updateOne)
	if err != nil {
		t.Fatal(err)
	}
	secondUpdateJSON, err := json.Marshal(updateTwo)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstUpdateJSON, secondUpdateJSON) {
		t.Fatalf("same-revision updates serialized differently: %s != %s", firstUpdateJSON, secondUpdateJSON)
	}
	if view.updateCache != nil {
		t.Fatal("the shared update cache remained after every matching consumer took the frame")
	}
}

func TestWaitThroughBroadcastsPrefixAdvanceToEveryWaiter(t *testing.T) {
	view := NewView("run-1")
	defer view.Release()
	cutThrough := uint64(1)
	if err := view.LoadCut(context.Background(), &cutThrough, nil, nil); err != nil {
		t.Fatal(err)
	}
	_, subscription, err := view.SubscribeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	const waiters = 2
	entered := make(chan struct{}, waiters)
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			results <- view.waitThrough(ctx, 2, func() { entered <- struct{}{} })
		}()
	}
	for i := 0; i < waiters; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatalf("waiter %d did not block on the incomplete prefix: %v", i, ctx.Err())
		}
	}
	view.Enqueue(contracts.ExecutionCommit{RootExecutionID: "root-1", SequenceCount: 1, ThroughSequence: 2})
	for i := 0; i < waiters; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("waiter %d did not observe the final prefix: %v", i, err)
			}
		case <-ctx.Done():
			t.Fatalf("waiter %d remained blocked after the prefix advanced: %v", i, ctx.Err())
		}
	}
}

func TestSnapshotPreservesThinkingKind(t *testing.T) {
	view := NewView("run-1")
	defer view.Release()
	cutThrough := uint64(3)
	key := "rs_1/summary/0"
	if err := view.LoadCut(context.Background(), &cutThrough, nil, func(visit func(contracts.ExecutionFact) error) error {
		return visit(contracts.ExecutionFact{RootExecutionID: "root-1", ExecutionID: "root-1", RootExecutionOrdinal: 1,
			Kind: "assistant_message", Phase: "completed", Text: "public summary", ThinkingKind: "summary",
			Thinking: []contracts.ThinkingItem{{Key: key, Text: "public summary", Kind: "summary"}},
			Conversation: contracts.ConversationMetadata{RootExecutionID: "root-1", Sequence: 3,
				EntityKind: "thinking", LocalKey: "parent/1/" + key, FirstSequence: 2},
		})
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := view.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entity := findEntity(snapshot, "thinking", "public summary")
	if entity == nil || entity.ThinkingKind != "summary" || entity.Position.FirstSequence != 2 {
		t.Fatalf("conversation snapshot lost thinking kind or position: %+v", entity)
	}
}

func executionCommit(sequence uint64, text string) contracts.ExecutionCommit {
	phase := "started"
	if sequence > 2 {
		phase = "completed"
	}
	return contracts.ExecutionCommit{RootExecutionID: "root-1", SequenceCount: 1, ThroughSequence: sequence,
		Facts: []contracts.ExecutionFact{{RootExecutionID: "root-1", ExecutionID: "root-1", RootExecutionOrdinal: 1,
			Kind: "assistant_message", Phase: "delta", Text: text,
			Conversation: contracts.ConversationMetadata{RootExecutionID: "root-1", Sequence: sequence,
				EntityKind: "assistant", LocalKey: "parent/1/answer", FirstSequence: sequence},
		}, {RootExecutionID: "root-1", ExecutionID: "root-1", RootExecutionOrdinal: 1,
			Kind: "execution_admission", Phase: phase,
			Conversation: contracts.ConversationMetadata{RootExecutionID: "root-1", Sequence: sequence,
				EntityKind: "execution", LocalKey: "execution", FirstSequence: sequence, ItemIndex: 1},
		}}}
}

func findEntity(snapshot api.ConversationSnapshot, kind, text string) *api.ConversationEntity {
	for _, entity := range snapshot.Entities {
		if entity.Kind == kind && entity.Text == text {
			copy := entity
			return &copy
		}
	}
	return nil
}
