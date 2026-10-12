package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/extensions"
	"hjh/internal/history"
	"hjh/internal/toolactivity"
)

type journal struct {
	mu                   sync.Mutex
	ctx                  context.Context
	store                *history.Store
	owner                *executiondata.Owner
	executionID          string
	rootExecutionID      string
	rootExecutionOrdinal int64
	task                 string
	parentExecutionID    string
	spawnCallID          string
	control              *executionControl
	links                map[string][]history.Relation
	progress             contracts.Observer
	starts               map[string]string
	firstSequences       map[string]uint64
	previews             map[string]string
}

func newJournal(ctx context.Context, store *history.Store, id, rootID, task string, owner *executiondata.Owner, progress contracts.Observer) *journal {
	return &journal{ctx: ctx, store: store, owner: owner, executionID: id, rootExecutionID: rootID, task: task, progress: progress,
		links: map[string][]history.Relation{}, starts: map[string]string{}, firstSequences: map[string]uint64{}, previews: map[string]string{}}
}

func (j *journal) observe(o contracts.Observation) error {
	o.ExecutionID = j.executionID
	o.ParentExecutionID = j.parentExecutionID
	if o.Time.IsZero() {
		o.Time = time.Now().UTC()
	}
	j.mu.Lock()
	var saved []contracts.ExecutionFact
	draft := contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, ParentExecutionID: j.parentExecutionID}
	err := j.owner.Commit(j.ctx, draft, func(sequence uint64) (contracts.ExecutionCommit, error) {
		facts, err := j.persist(o, sequence)
		if err != nil {
			return contracts.ExecutionCommit{}, err
		}
		// Store helpers build facts from their transaction inputs, but the
		// execution journal is the authoritative source for live scope. Keep
		// child progress/result facts identical to replay, which restores these
		// values from the saved execution row.
		for index := range facts {
			facts[index].RootExecutionID = j.rootExecutionID
			facts[index].ExecutionID = j.executionID
			facts[index].ParentExecutionID = j.parentExecutionID
			facts[index].RootExecutionOrdinal = j.rootExecutionOrdinal
		}
		saved = facts
		return contracts.ExecutionCommit{Facts: facts, SequenceCount: 1, RootExecutionID: j.rootExecutionID}, nil
	})
	if err == nil {
		j.rememberFirstSequences(saved)
	}
	j.mu.Unlock()
	if err != nil {
		return j.control.storage(err)
	}
	if j.progress != nil {
		return j.progress(o)
	}
	return nil
}

func (j *journal) persist(o contracts.Observation, sequence uint64) ([]contracts.ExecutionFact, error) {
	if o.Kind == "assistant_text" {
		request := contracts.RequestIdentity{ExecutionID: j.executionID, Lane: "parent", RequestOrdinal: o.ModelStep,
			ModelStep: o.ModelStep, PhysicalRequestID: o.PhysicalRequestID}
		facts, err := j.store.SaveAssistantProgress(j.ctx, history.AssistantProgressInput{
			RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, Sequence: sequence,
			Request: request, Text: o.Text, Thinking: o.Thinking, ObservedAt: o.Time.UTC().Format(time.RFC3339Nano),
		})
		return facts, err
	}
	kind, started := observationKind(o)
	if kind == "tool_call" {
		o.Preview = toolactivity.Preview(o.Name, o.Input)
		j.previews[o.ID] = o.Preview
	} else if kind == "tool_result" {
		o.Preview = j.previews[o.ID]
	}
	metadata := j.metadataFor(o, kind, sequence)
	relations := append([]history.Relation(nil), j.links[o.ID]...)
	if parent := j.starts[o.Parent]; parent != "" {
		relations = append(relations, history.Relation{Relation: "parent_call", TargetRecordID: parent, Mandatory: true, Resolved: true})
	}
	if !started {
		if start := j.starts[o.ID]; start != "" {
			relations = append(relations, history.Relation{Relation: "call_start", TargetRecordID: start, Mandatory: true, Resolved: true})
		}
	}
	if o.Kind == "model_result" {
		request := requestForObservation(o, j.executionID)
		phase := "completed"
		if finishReason := observationFinishReason(o); finishReason == "length" {
			phase = "incomplete"
		}
		facts, err := j.store.SaveAssistantResult(j.ctx, history.AssistantResultInput{
			RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, Sequence: sequence,
			Request: request, Payload: contracts.JSON(o), Relations: relations,
			Text: o.Text, Thinking: o.Thinking, Phase: phase,
			ObservedAt: o.Time.UTC().Format(time.RFC3339Nano),
		})
		for index := range facts {
			if facts[index].Conversation.EntityKind == "assistant" {
				facts[index].Name = o.Name
			}
		}
		return facts, err
	}
	payload := contracts.JSON(o)
	if o.Kind == "context" {
		var notice map[string]any
		if err := json.Unmarshal(payload, &notice); err != nil {
			return nil, err
		}
		notice["text"] = o.Preview
		payload = contracts.JSON(notice)
	}
	var metadataPtr *contracts.ConversationMetadata
	if metadata != nil {
		metadataPtr = metadata
	}
	record, err := j.store.AppendOccurrence(j.ctx, j.executionID, history.OccurrenceInput{
		Kind: kind, Payload: payload, Relations: relations, ObservedAt: o.Time.UTC().Format(time.RFC3339Nano), Conversation: metadataPtr,
	})
	if err != nil {
		return nil, err
	}
	if started && o.ID != "" {
		j.starts[o.ID] = record.ID
	}
	facts := make([]contracts.ExecutionFact, 0, 2)
	if metadata != nil {
		parentCallID := o.Parent
		if o.Kind == "execution" {
			parentCallID = j.spawnCallID
		}
		request := requestForObservation(o, j.executionID)
		fact := contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID,
			ParentExecutionID: j.parentExecutionID, RootExecutionOrdinal: j.rootExecutionOrdinal,
			Kind: kind, Phase: o.Phase, Name: o.Name, CallID: o.ID, ParentCallID: parentCallID,
			Preview: o.Preview, Error: o.Error,
			Request: request, Conversation: *metadata,
			Locator: contracts.ExecutionLocator{ExecutionID: j.executionID, RecordID: record.ID, Ordinal: record.Ordinal,
				Lane: request.Lane, ModelStep: request.ModelStep}}
		if kind == "tool_result" {
			fact.Outcome = o.Phase
		}
		if o.Kind == "context" {
			fact.Text = o.Preview
		}
		facts = append(facts, fact)
		if o.Kind == "execution" && started {
			facts = append(facts, contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID,
				ParentExecutionID: j.parentExecutionID, RootExecutionOrdinal: j.rootExecutionOrdinal, ParentCallID: j.spawnCallID,
				Kind: "task", Role: "user", Text: j.task,
				Conversation: contracts.ConversationMetadata{RootExecutionID: j.rootExecutionID, Sequence: sequence,
					EntityKind: "task", LocalKey: "task", FirstSequence: sequence},
				Locator: contracts.ExecutionLocator{ExecutionID: j.executionID}})
		}
	}
	return facts, nil
}

func observationKind(o contracts.Observation) (kind string, started bool) {
	started = o.Phase == "started" || o.Phase == "start"
	switch o.Kind {
	case "model_tool", "tool", "generated", "agent":
		kind = "tool_result"
		if started {
			kind = "tool_call"
		}
	case "execution":
		kind = "execution_admission"
	case "host":
		kind = "effect_observation"
	case "hook":
		kind = "context_update"
	case "context":
		kind = "context_update"
	case "control":
		kind = "control_decision"
	case "assistant_text":
		kind = "assistant_message"
	case "model_request":
		kind = "model_request"
	default:
		kind = "model_result"
	}
	return kind, started
}

func (j *journal) metadataFor(o contracts.Observation, kind string, sequence uint64) *contracts.ConversationMetadata {
	if o.Kind == "context" {
		return &contracts.ConversationMetadata{RootExecutionID: j.rootExecutionID, Sequence: sequence,
			EntityKind: "notice", LocalKey: "context/" + o.Name, FirstSequence: sequence}
	}
	switch kind {
	case "execution_admission":
		return &contracts.ConversationMetadata{RootExecutionID: j.rootExecutionID, Sequence: sequence,
			EntityKind: "execution", LocalKey: "execution", FirstSequence: sequence, ItemIndex: 1}
	case "tool_call", "tool_result":
		key := o.ID
		if key == "" {
			key = fmt.Sprintf("%s/%s", o.Name, o.Parent)
		}
		first := j.firstSequences["tool/"+key]
		if first == 0 {
			first = sequence
		}
		if kind == "tool_call" {
			first = sequence
		}
		return &contracts.ConversationMetadata{RootExecutionID: j.rootExecutionID, Sequence: sequence,
			EntityKind: "tool", LocalKey: key, FirstSequence: first}
	default:
		return nil
	}
}

func requestForObservation(o contracts.Observation, executionID string) contracts.RequestIdentity {
	physicalID := o.PhysicalRequestID
	if physicalID == "" && o.Kind == "model_request" {
		physicalID = o.ID
	}
	return contracts.RequestIdentity{ExecutionID: executionID, Lane: "parent", RequestOrdinal: o.ModelStep,
		ModelStep: o.ModelStep, PhysicalRequestID: physicalID}
}

func observationFinishReason(o contracts.Observation) string {
	var result struct {
		FinishReason string `json:"finishReason"`
	}
	_ = json.Unmarshal(o.Result, &result)
	return result.FinishReason
}

func (j *journal) rememberFirstSequences(facts []contracts.ExecutionFact) {
	for _, fact := range facts {
		if fact.Conversation.LocalKey == "" || fact.Conversation.FirstSequence == 0 {
			continue
		}
		key := fact.Conversation.EntityKind + "/" + fact.Conversation.LocalKey
		if _, exists := j.firstSequences[key]; !exists {
			j.firstSequences[key] = fact.Conversation.FirstSequence
		}
	}
}

func (j *journal) appendMessage(message contracts.Message) error {
	_, err := j.appendMessageOrdinal(message)
	return err
}

func (j *journal) appendMessageOrdinal(message contracts.Message) (int64, error) {
	encoded, err := contracts.EncodeJSON(message)
	if err != nil {
		return 0, err
	}
	return j.appendMessageContent(message, encoded, "", nil)
}

func (j *journal) appendSteeringMessage(message contracts.Message, body []byte, receipt *contracts.InputReceipt) error {
	if receipt == nil {
		return fmt.Errorf("app journal: missing steering receipt")
	}
	steering := true
	message.Steering = &steering
	message.ExecutionFact = &contracts.ExecutionFact{Kind: "user_message", Role: "user", Text: message.VisibleText(),
		Conversation: contracts.ConversationMetadata{EntityKind: "steering", LocalKey: "input/" + receipt.InputID}}
	_, err := j.appendMessageContent(message, body, receipt.ContentDigest, receipt)
	return err
}

func (j *journal) appendChildInstructionMessage(message contracts.Message, body []byte, receipt *contracts.ChildInstructionReceipt) (int64, error) {
	if receipt == nil {
		return 0, fmt.Errorf("app journal: missing child instruction receipt")
	}
	steering := true
	message.Steering = &steering
	message.ExecutionFact = &contracts.ExecutionFact{Kind: "user_message", Role: "user", Text: message.VisibleText(),
		ParentExecutionID: receipt.ParentExecutionID, ParentCallID: receipt.CallID,
		Conversation: contracts.ConversationMetadata{EntityKind: "steering", LocalKey: "input/" + receipt.InstructionID}}
	return j.appendChildInstructionMessageContent(message, body, receipt)
}

func (j *journal) recordChildInstructionDecision(receipt contracts.ChildInstructionReceipt, decision string, body []byte) (contracts.ChildInstructionReceipt, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var updated contracts.ChildInstructionReceipt
	draft := contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, ParentExecutionID: j.parentExecutionID}
	err := j.owner.Commit(j.ctx, draft, func(sequence uint64) (contracts.ExecutionCommit, error) {
		var record history.Record
		var err error
		updated, record, err = j.store.AppendChildInstructionDecision(j.ctx, history.ChildInstructionDecisionInput{
			Receipt: receipt, Decision: decision, Content: body,
		})
		if err != nil {
			return contracts.ExecutionCommit{}, err
		}
		fact := contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID,
			ParentExecutionID: j.parentExecutionID, RootExecutionOrdinal: j.rootExecutionOrdinal,
			Kind: "control_decision", Phase: decision, Name: "agent_instruction",
			CallID: updated.InstructionID, ParentCallID: updated.CallID,
			Locator: contracts.ExecutionLocator{ExecutionID: j.executionID, RecordID: record.ID, Ordinal: record.Ordinal}}
		return contracts.ExecutionCommit{Facts: []contracts.ExecutionFact{fact}, SequenceCount: 1, RootExecutionID: j.rootExecutionID}, nil
	})
	if err != nil {
		return contracts.ChildInstructionReceipt{}, j.control.storage(err)
	}
	return updated, nil
}

func (j *journal) appendChildInstructionMessageContent(message contracts.Message, encoded []byte, receipt *contracts.ChildInstructionReceipt) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	body := append(json.RawMessage(nil), encoded...)
	var ordinal int64
	draft := contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, ParentExecutionID: j.parentExecutionID}
	err := j.owner.Commit(j.ctx, draft, func(sequence uint64) (contracts.ExecutionCommit, error) {
		facts := []contracts.ExecutionFact{*message.ExecutionFact}
		for index := range facts {
			facts[index].RootExecutionID = j.rootExecutionID
			facts[index].ExecutionID = j.executionID
			facts[index].ParentExecutionID = j.parentExecutionID
			facts[index].RootExecutionOrdinal = j.rootExecutionOrdinal
		}
		var saved []contracts.ExecutionFact
		var err error
		ordinal, saved, err = j.store.AppendConversationMessage(j.ctx, history.ConversationMessageInput{
			RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, Sequence: sequence,
			Message: body, ContentDigest: receipt.ContentDigest, Facts: facts, ChildInstructionReceipt: receipt,
		})
		if err != nil {
			return contracts.ExecutionCommit{}, err
		}
		return contracts.ExecutionCommit{Facts: saved, SequenceCount: 1, RootExecutionID: j.rootExecutionID}, nil
	})
	if err != nil {
		return 0, j.control.storage(err)
	}
	j.rememberFirstSequences([]contracts.ExecutionFact{*message.ExecutionFact})
	return ordinal, nil
}

func (j *journal) appendMessageContent(message contracts.Message, encoded []byte, contentDigest string, steeringReceipt *contracts.InputReceipt) (int64, error) {
	j.mu.Lock()
	var saved []contracts.ExecutionFact
	var messageOrdinal int64
	draft := contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, ParentExecutionID: j.parentExecutionID}
	err := j.owner.Commit(j.ctx, draft, func(sequence uint64) (contracts.ExecutionCommit, error) {
		facts := make([]contracts.ExecutionFact, 0, 1)
		var finalize *contracts.RequestIdentity
		if message.ExecutionFact != nil {
			fact := *message.ExecutionFact
			if fact.Conversation.EntityKind == "tool" {
				fact.Preview = j.previews[fact.CallID]
				fact.Text = ""
			}
			fact.RootExecutionID, fact.ExecutionID, fact.ParentExecutionID = j.rootExecutionID, j.executionID, j.parentExecutionID
			fact.RootExecutionOrdinal = j.rootExecutionOrdinal
			if fact.Conversation.EntityKind == "task" {
				fact.ParentCallID = j.spawnCallID
			}
			fact.Request.ExecutionID = j.executionID
			if fact.Conversation.LocalKey != "" {
				fact.Conversation.RootExecutionID = j.rootExecutionID
				fact.Conversation.Sequence = sequence
				if fact.Conversation.FirstSequence == 0 {
					fact.Conversation.FirstSequence = j.firstSequences[fact.Conversation.EntityKind+"/"+fact.Conversation.LocalKey]
				}
			}
			if message.Role == "assistant" {
				finalize = &fact.Request
			}
			facts = append(facts, fact)
		}
		ordinal, savedFacts, err := j.store.AppendConversationMessage(j.ctx, history.ConversationMessageInput{
			RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, Sequence: sequence,
			Message: encoded, ContentDigest: contentDigest, Facts: facts, SteeringReceipt: steeringReceipt, FinalizeRequest: finalize,
		})
		if err != nil {
			return contracts.ExecutionCommit{}, err
		}
		messageOrdinal = ordinal
		saved = savedFacts
		return contracts.ExecutionCommit{Facts: savedFacts, SequenceCount: 1, RootExecutionID: j.rootExecutionID}, nil
	})
	if err == nil {
		j.rememberFirstSequences(saved)
	}
	j.mu.Unlock()
	if err != nil {
		return 0, j.control.storage(err)
	}
	if j.progress != nil && message.ExecutionFact != nil {
		return messageOrdinal, nil
	}
	return messageOrdinal, nil
}

func (j *journal) end(outcome history.Outcome, result any) error {
	terminal, err := contracts.EncodeJSON(result)
	if err != nil {
		return err
	}
	j.mu.Lock()
	draft := contracts.ExecutionFact{RootExecutionID: j.rootExecutionID, ExecutionID: j.executionID, ParentExecutionID: j.parentExecutionID}
	var saved []contracts.ExecutionFact
	err = j.owner.Commit(j.ctx, draft, func(sequence uint64) (contracts.ExecutionCommit, error) {
		facts, err := j.store.EndExecutionWithConversation(j.ctx, j.executionID, outcome, terminal, "",
			j.rootExecutionID, sequence)
		if err != nil {
			return contracts.ExecutionCommit{}, err
		}
		saved = facts
		return contracts.ExecutionCommit{Facts: facts, SequenceCount: 1, RootExecutionID: j.rootExecutionID}, nil
	})
	if err == nil {
		j.rememberFirstSequences(saved)
	}
	j.mu.Unlock()
	if err != nil {
		return j.control.storage(err)
	}
	return nil
}

// executionSnapshot preserves the exact resolved Worker composition.
func executionSnapshot(snapshot extensions.Snapshot) ([]byte, error) {
	return snapshot.JSON()
}

func (j *journal) startReference(id string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.starts[id]
}

func (j *journal) link(id string, relation history.Relation) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.links[id] = append(j.links[id], relation)
}

var _ = json.RawMessage{}
