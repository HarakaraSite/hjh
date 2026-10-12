package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"hjh/internal/agent"
	"hjh/internal/contracts"
)

type childInstruction struct {
	receipt  contracts.ChildInstructionReceipt
	message  []byte
	previous *childInstruction
	done     chan struct{}
	finished bool
	claimed  bool
	saveErr  error
}

// childInstructionOwner is scoped to one child execution. It owns acceptance
// order and the AdditionalInput boundary without inheriting Core input state.
type childInstructionOwner struct {
	mu                sync.Mutex
	executionID       string
	parentExecutionID string
	accepting         bool
	sequence          uint64
	items             []*childInstruction
	journal           *journal
	journalErr        error
	journalReady      chan struct{}
	attachOnce        sync.Once
	closeOnce         sync.Once
	closeErr          error
}

var _ agent.AdditionalInput = (*childInstructionOwner)(nil)

func newChildInstructionOwner(executionID, parentExecutionID string) *childInstructionOwner {
	return &childInstructionOwner{executionID: executionID, parentExecutionID: parentExecutionID,
		accepting: true, journalReady: make(chan struct{})}
}

func (o *childInstructionOwner) attachJournal(j *journal, err error) {
	o.attachOnce.Do(func() {
		o.mu.Lock()
		o.journal, o.journalErr = j, err
		o.mu.Unlock()
		close(o.journalReady)
	})
}

// Accept registers before waiting for storage, so a final boundary either
// includes this instruction or closes admission before it can be registered.
func (o *childInstructionOwner) Accept(ctx context.Context, request contracts.AgentSteerRequest) (contracts.ChildInstructionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return contracts.ChildInstructionReceipt{}, err
	}
	steering := true
	message := contracts.User(request.Text)
	message.Steering = &steering
	body, err := contracts.EncodeJSON(message)
	if err != nil {
		return contracts.ChildInstructionReceipt{}, err
	}
	item := &childInstruction{message: append([]byte(nil), body...), done: make(chan struct{})}
	o.mu.Lock()
	if !o.accepting {
		o.mu.Unlock()
		return contracts.ChildInstructionReceipt{}, errors.New("child instruction admission is closed")
	}
	o.sequence++
	now := time.Now().UTC().Format(time.RFC3339Nano)
	item.receipt = contracts.ChildInstructionReceipt{
		InstructionID: uuid.NewString(), ExecutionID: o.executionID, ParentExecutionID: o.parentExecutionID,
		CallID: request.CallID, Sequence: o.sequence, Status: "processing", AcceptedAt: now, UpdatedAt: now,
	}
	if len(o.items) > 0 {
		item.previous = o.items[len(o.items)-1]
	}
	o.items = append(o.items, item)
	o.mu.Unlock()

	// Once registered, finish the save even if the calling JS operation is
	// cancelled. Leaving an unresolved in-flight item would block final close.
	if item.previous != nil {
		<-item.previous.done
	}
	<-o.journalReady
	o.mu.Lock()
	j, journalErr := o.journal, o.journalErr
	o.mu.Unlock()
	if j == nil {
		if journalErr == nil {
			journalErr = errors.New("child execution journal is unavailable")
		}
		o.finish(item, journalErr)
		return contracts.ChildInstructionReceipt{}, journalErr
	}
	receipt := item.receipt
	receipt.Status = "accepted"
	updated, saveErr := j.recordChildInstructionDecision(receipt, "accepted", item.message)
	if saveErr != nil {
		o.finish(item, saveErr)
		return contracts.ChildInstructionReceipt{}, saveErr
	}
	o.mu.Lock()
	item.receipt, item.finished = updated, true
	close(item.done)
	o.mu.Unlock()
	return updated, nil
}

func (o *childInstructionOwner) finish(item *childInstruction, err error) {
	o.mu.Lock()
	item.saveErr, item.finished = err, true
	close(item.done)
	o.mu.Unlock()
}

func (o *childInstructionOwner) Apply(ctx context.Context, final bool) ([]contracts.Message, error) {
	items, err := o.pending(ctx, final)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	messages := make([]contracts.Message, 0, len(items))
	for _, item := range items {
		var message contracts.Message
		if err := json.Unmarshal(item.message, &message); err != nil {
			return messages, err
		}
		message.ExecutionFact = &contracts.ExecutionFact{Kind: "user_message", Role: "user", Text: message.VisibleText(),
			ParentExecutionID: item.receipt.ParentExecutionID,
			Conversation:      contracts.ConversationMetadata{EntityKind: "steering", LocalKey: "input/" + item.receipt.InstructionID}}
		receipt := item.receipt
		o.mu.Lock()
		j := o.journal
		o.mu.Unlock()
		if j == nil {
			return messages, errors.New("child execution journal is unavailable")
		}
		if _, err := j.appendChildInstructionMessage(message, item.message, &receipt); err != nil {
			return messages, err
		}
		o.mu.Lock()
		item.receipt = receipt
		o.mu.Unlock()
		messages = append(messages, message)
	}
	return messages, nil
}

func (o *childInstructionOwner) pending(ctx context.Context, final bool) ([]*childInstruction, error) {
	for {
		o.mu.Lock()
		var waits []chan struct{}
		if final {
			for _, item := range o.items {
				if !item.finished {
					waits = append(waits, item.done)
				}
			}
		}
		if len(waits) == 0 {
			var pending []*childInstruction
			for _, item := range o.items {
				if item.finished && item.saveErr == nil && item.receipt.Status == "accepted" && !item.claimed {
					item.claimed = true
					pending = append(pending, item)
				}
			}
			if final && len(pending) == 0 {
				o.accepting = false
			}
			o.mu.Unlock()
			return pending, nil
		}
		o.mu.Unlock()
		for _, done := range waits {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
			}
		}
	}
}

func (o *childInstructionOwner) Receipts() []contracts.ChildInstructionReceipt {
	o.mu.Lock()
	defer o.mu.Unlock()
	receipts := make([]contracts.ChildInstructionReceipt, 0, len(o.items))
	for _, item := range o.items {
		if !item.finished || item.saveErr != nil {
			continue
		}
		receipt := item.receipt
		if receipt.AppliedMessageOrdinal != nil {
			ordinal := *receipt.AppliedMessageOrdinal
			receipt.AppliedMessageOrdinal = &ordinal
		}
		receipts = append(receipts, receipt)
	}
	return receipts
}

func (o *childInstructionOwner) Close(reason string) error {
	o.closeOnce.Do(func() { o.closeErr = o.close(reason) })
	return o.closeErr
}

func (o *childInstructionOwner) close(reason string) error {
	o.mu.Lock()
	o.accepting = false
	var waits []chan struct{}
	for _, item := range o.items {
		if !item.finished {
			waits = append(waits, item.done)
		}
	}
	o.mu.Unlock()
	for _, done := range waits {
		<-done
	}
	<-o.journalReady
	o.mu.Lock()
	j := o.journal
	var pending []*childInstruction
	for _, item := range o.items {
		if item.finished && item.saveErr == nil && item.receipt.Status == "accepted" {
			pending = append(pending, item)
		}
	}
	o.mu.Unlock()
	if j == nil {
		return nil
	}
	if j.control != nil && j.control.failure() != nil {
		reason = "run_failed"
	}
	var closeErr error
	for _, item := range pending {
		receipt := item.receipt
		receipt.Status, receipt.Reason = "unapplied", reason
		receipt.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		updated, err := j.recordChildInstructionDecision(receipt, "unapplied", nil)
		if err != nil {
			closeErr = errors.Join(closeErr, err)
			continue
		}
		o.mu.Lock()
		item.receipt = updated
		o.mu.Unlock()
	}
	return closeErr
}
