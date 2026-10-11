// Package recall projects one settled execution's public conversation evidence
// into a provider-independent reference message.
package recall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"hjh/internal/contracts"
)

const messagePreamble = "参考情報は停止した過去executionの記録です。未完了の呼び出しを実行待ちとして復元していません。\n\n"

type Execution struct {
	ExecutionID       string
	SessionID         string
	ParentExecutionID string
	Turn              int64
	AgentName         string
	Selection         contracts.ModelSelection
	Lifecycle         string
	Outcome           string
	Task              string
}

type Message struct {
	ExecutionID string
	Ordinal     int64
	JSON        json.RawMessage
}

type Record struct {
	ID           string
	ExecutionID  string
	Ordinal      int64
	Kind         string
	Payload      json.RawMessage
	Conversation *contracts.ConversationMetadata
}

// Evidence contains only the selected execution. The history adapter should
// omit configuration, request wire bodies, and record content blobs.
type Evidence struct {
	Execution Execution
	Messages  []Message
	Records   []Record
}

type SourceSummary struct {
	SourceExecutionID string                   `json:"sourceExecutionId"`
	Turn              int64                    `json:"turn"`
	AgentName         string                   `json:"agentName"`
	Selection         contracts.ModelSelection `json:"selection"`
	Outcome           string                   `json:"outcome"`
	Task              string                   `json:"task"`
}

type ToolCall struct {
	CallID    string          `json:"callId,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type ToolResult struct {
	CallID   string          `json:"callId,omitempty"`
	Name     string          `json:"name"`
	Text     string          `json:"text,omitempty"`
	Outcome  string          `json:"outcome,omitempty"`
	Failure  json.RawMessage `json:"failure,omitempty"`
	Terminal string          `json:"terminal,omitempty"`
}

type Activity struct {
	SourceLocator contracts.ExecutionLocator `json:"sourceLocator"`
	CallID        string                     `json:"callId,omitempty"`
	ParentCallID  string                     `json:"parentCallId,omitempty"`
	Name          string                     `json:"name"`
	Status        string                     `json:"status"`
}

type Observation struct {
	Kind                string                      `json:"kind"`
	SourceLocator       contracts.ExecutionLocator  `json:"sourceLocator"`
	ResultSourceLocator *contracts.ExecutionLocator `json:"resultSourceLocator,omitempty"`
	ModelStep           int                         `json:"modelStep,omitempty"`
	Text                string                      `json:"text,omitempty"`
	Call                *ToolCall                   `json:"call,omitempty"`
	Result              *ToolResult                 `json:"result,omitempty"`
	Status              string                      `json:"status,omitempty"`
	Activity            []Activity                  `json:"activity,omitempty"`
}

type Projection struct {
	SchemaVersion     int                      `json:"schemaVersion"`
	SourceExecutionID string                   `json:"sourceExecutionId"`
	SessionID         string                   `json:"sessionId"`
	Turn              int64                    `json:"turn"`
	AgentName         string                   `json:"agentName"`
	Selection         contracts.ModelSelection `json:"selection"`
	Lifecycle         string                   `json:"lifecycle"`
	Outcome           string                   `json:"outcome"`
	Task              string                   `json:"task"`
	AutomaticReplay   bool                     `json:"automaticReplay"`
	InheritedSources  []SourceSummary          `json:"inheritedSources"`
	Observations      []Observation            `json:"observations"`
}

type payloadRecord struct {
	Kind         string `json:"kind"`
	Phase        string `json:"phase"`
	ID           string `json:"id"`
	Text         string `json:"text"`
	Role         string `json:"role"`
	Name         string `json:"name"`
	CallID       string `json:"callId"`
	ParentCallID string `json:"parentCallId"`
	Parent       string `json:"parent"`
	Outcome      string `json:"outcome"`
	Error        string `json:"error"`
	ModelStep    int    `json:"modelStep"`
	FinishReason string `json:"finishReason"`
	Payload      struct {
		Text string `json:"text"`
	} `json:"payload"`
	Input        json.RawMessage                `json:"input"`
	Result       json.RawMessage                `json:"result"`
	Request      contracts.RequestIdentity      `json:"request"`
	Conversation contracts.ConversationMetadata `json:"conversation"`
	Locator      struct {
		ExecutionID    string `json:"executionId"`
		MessageOrdinal int64  `json:"messageOrdinal"`
		ItemIndex      int    `json:"itemIndex"`
	} `json:"locator"`
	Recall *contracts.RecallMarker `json:"recall"`
}

type messageEnvelope struct {
	Role     string          `json:"role"`
	Text     *string         `json:"text"`
	Steering *bool           `json:"steering"`
	Content  json.RawMessage `json:"content"`
}

type visibleText struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type candidate struct {
	ordinal int64
	obs     Observation
}

// Project builds a public-only projection. Provider state is never read from
// saved messages, and record payloads contribute only the named public fields.
func Project(evidence Evidence) (Projection, error) {
	e := evidence.Execution
	projection := Projection{
		SchemaVersion: 1, SourceExecutionID: e.ExecutionID, SessionID: e.SessionID,
		Turn: e.Turn, AgentName: e.AgentName, Selection: e.Selection,
		Lifecycle: e.Lifecycle, Outcome: e.Outcome, Task: e.Task,
		AutomaticReplay: false, InheritedSources: []SourceSummary{}, Observations: []Observation{},
	}
	if e.ExecutionID == "" {
		return Projection{}, fmt.Errorf("recall projection: missing execution ID")
	}

	messages := make(map[int64]messageEnvelope, len(evidence.Messages))
	messageRaw := make(map[int64]json.RawMessage, len(evidence.Messages))
	for _, saved := range evidence.Messages {
		var message messageEnvelope
		if err := json.Unmarshal(saved.JSON, &message); err != nil {
			return Projection{}, fmt.Errorf("recall projection: decode message %d: %w", saved.Ordinal, err)
		}
		messages[saved.Ordinal] = message
		messageRaw[saved.Ordinal] = saved.JSON
	}
	records := append([]Record(nil), evidence.Records...)
	sort.SliceStable(records, func(i, j int) bool { return records[i].Ordinal < records[j].Ordinal })

	byMessage := make(map[int64][]Record)
	messageAssistantSteps := map[int]bool{}
	for _, record := range records {
		v := decodeRecord(record)
		if v.Locator.MessageOrdinal >= 0 && hasMessageLocator(v) {
			byMessage[v.Locator.MessageOrdinal] = append(byMessage[v.Locator.MessageOrdinal], record)
			if message := messages[v.Locator.MessageOrdinal]; message.Role == "assistant" {
				step := v.Request.ModelStep
				if step == 0 {
					step = v.ModelStep
				}
				if step != 0 {
					messageAssistantSteps[step] = true
				}
			}
		}
	}

	var candidates []candidate
	var inherited []SourceSummary
	seenObservations := map[string]bool{}
	seenSources := map[string]bool{}
	seenMessages := map[int64]bool{}
	seenAssistantStep := map[int]bool{}
	seenToolCalls := map[string]bool{}

	addObservation := func(at int64, obs Observation) {
		key := observationKey(obs)
		if seenObservations[key] {
			return
		}
		seenObservations[key] = true
		candidates = append(candidates, candidate{ordinal: at, obs: obs})
	}
	addInherited := func(summary SourceSummary) {
		if summary.SourceExecutionID == "" || seenSources[summary.SourceExecutionID] {
			return
		}
		seenSources[summary.SourceExecutionID] = true
		inherited = append(inherited, summary)
	}

	for _, record := range records {
		v := decodeRecord(record)
		if hasMessageLocator(v) {
			messageOrdinal := v.Locator.MessageOrdinal
			if seenMessages[messageOrdinal] {
				continue
			}
			message, ok := messages[messageOrdinal]
			if !ok {
				continue
			}
			seenMessages[messageOrdinal] = true
			locator := contracts.ExecutionLocator{ExecutionID: e.ExecutionID, RecordID: record.ID,
				Ordinal: record.Ordinal, MessageOrdinal: messageOrdinal, ItemIndex: v.Locator.ItemIndex}
			if v.Recall != nil || v.Conversation.EntityKind == "recall" {
				if v.Recall == nil {
					continue
				}
				var savedMessage contracts.Message
				if err := json.Unmarshal(messageRaw[messageOrdinal], &savedMessage); err != nil {
					return Projection{}, fmt.Errorf("recall projection: decode reference message: %w", err)
				}
				nested, err := DecodeMessage(savedMessage)
				if err != nil {
					return Projection{}, fmt.Errorf("recall projection: decode reference at record %s: %w", record.ID, err)
				}
				for _, summary := range nested.InheritedSources {
					addInherited(summary)
				}
				addInherited(summaryFromProjection(nested))
				for _, obs := range nested.Observations {
					addObservation(record.Ordinal, obs)
				}
				continue
			}
			if message.Role == "user" && (isSteering(message) || v.Conversation.EntityKind == "steering") {
				text := messageVisibleText(message)
				if text != "" {
					addObservation(record.Ordinal, Observation{Kind: "steering", SourceLocator: locator, Text: text, Status: "applied"})
				}
				continue
			}
			if message.Role == "assistant" {
				calls, err := decodeCalls(message.Content)
				if err != nil {
					return Projection{}, fmt.Errorf("recall projection: decode assistant message %d calls: %w", messageOrdinal, err)
				}
				step := v.Request.ModelStep
				if step == 0 {
					step = v.ModelStep
				}
				if len(calls) != 0 {
					results := resultMessages(messages, messageOrdinal)
					messageText := messageVisibleText(message)
					for index, call := range calls {
						callLocator := locator
						callLocator.ItemIndex = index
						obs := Observation{Kind: "tool_exchange", SourceLocator: callLocator, ModelStep: step,
							Call: &ToolCall{CallID: call.CallID, Name: call.Name, Arguments: cloneJSON(call.Arguments)}, Status: "incomplete"}
						if index == 0 {
							obs.Text = messageText
						}
						if foundResult, found := results[call.CallID]; found {
							result := foundResult.result
							obs.Result = &ToolResult{CallID: result.CallID, Name: result.Name, Text: result.Text,
								Outcome: result.Outcome, Failure: cloneJSON(result.Failure), Terminal: result.Terminal}
							resultLocator := contracts.ExecutionLocator{ExecutionID: e.ExecutionID, MessageOrdinal: foundResult.ordinal}
							for _, linked := range byMessage[foundResult.ordinal] {
								resultLocator.RecordID, resultLocator.Ordinal = linked.ID, linked.Ordinal
								break
							}
							obs.ResultSourceLocator = &resultLocator
							obs.Status = result.Outcome
							if obs.Status == "" {
								obs.Status = "completed"
							}
						}
						if obs.Result == nil {
							obs.Activity = recordActivity(records, call.CallID, e.ExecutionID)
						}
						addObservation(record.Ordinal, obs)
						if call.CallID != "" {
							seenToolCalls[call.CallID] = true
						}
					}
					if step != 0 {
						seenAssistantStep[step] = true
					}
					continue
				}
				text := messageVisibleText(message)
				if text == "" {
					continue
				}
				kind := "assistant"
				status := "completed"
				if v.Phase == "incomplete" || recordHasFinishReason(records, step, "length") {
					kind, status = "assistant_partial", "incomplete"
				}
				addObservation(record.Ordinal, Observation{Kind: kind, SourceLocator: locator, ModelStep: step, Text: text, Status: status})
				if step != 0 {
					seenAssistantStep[step] = true
				}
			}
			continue
		}
		if record.Kind == "model_result" {
			step := v.Request.ModelStep
			if step == 0 {
				step = v.ModelStep
			}
			if step == 0 || messageAssistantSteps[step] || seenAssistantStep[step] {
				continue
			}
			text := v.Text
			var result struct {
				Text         string `json:"text"`
				FinishReason string `json:"finishReason"`
			}
			_ = json.Unmarshal(v.Result, &result)
			if text == "" {
				text = result.Text
			}
			if text == "" {
				continue
			}
			kind, status := "assistant", "completed"
			if v.Phase == "incomplete" || result.FinishReason == "length" {
				kind, status = "assistant_partial", "incomplete"
			}
			locator := recordLocator(e.ExecutionID, record, v)
			addObservation(record.Ordinal, Observation{Kind: kind, SourceLocator: locator, ModelStep: step, Text: text, Status: status})
			seenAssistantStep[step] = true
			continue
		}
		if v.Conversation.EntityKind == "assistant" && v.Phase == "incomplete" && v.Text == "" {
			partialText := v.Payload.Text
			if partialText != "" {
				step := v.Request.ModelStep
				if step == 0 {
					step = v.ModelStep
				}
				if step == 0 || !seenAssistantStep[step] {
					locator := recordLocator(e.ExecutionID, record, v)
					addObservation(record.Ordinal, Observation{Kind: "assistant_partial", SourceLocator: locator, ModelStep: step,
						Text: partialText, Status: "incomplete"})
					if step != 0 {
						seenAssistantStep[step] = true
					}
				}
			}
		}
	}

	// A message can exist without its semantic locator in imported history. Use
	// its saved order as a fallback, while excluding the original task message.
	messageOrdinals := make([]int64, 0, len(messages))
	for ordinal := range messages {
		messageOrdinals = append(messageOrdinals, ordinal)
	}
	sort.Slice(messageOrdinals, func(i, j int) bool { return messageOrdinals[i] < messageOrdinals[j] })
	for _, ordinal := range messageOrdinals {
		if seenMessages[ordinal] {
			continue
		}
		m := messages[ordinal]
		linked := byMessage[ordinal]
		locator := contracts.ExecutionLocator{ExecutionID: e.ExecutionID, MessageOrdinal: ordinal}
		var messageRecord payloadRecord
		if len(linked) != 0 {
			messageRecord = decodeRecord(linked[0])
			locator.RecordID, locator.Ordinal = linked[0].ID, linked[0].Ordinal
			locator.ItemIndex = messageRecord.Locator.ItemIndex
		}
		if m.Role == "user" && isSteering(m) {
			addObservation(ordinal, Observation{Kind: "steering", SourceLocator: locator, Text: messageVisibleText(m), Status: "applied"})
			continue
		}
		if m.Role != "assistant" {
			continue
		}
		calls, err := decodeCalls(m.Content)
		if err != nil {
			return Projection{}, fmt.Errorf("recall projection: decode assistant message %d calls: %w", ordinal, err)
		}
		step := messageRecord.Request.ModelStep
		if step == 0 {
			step = messageRecord.ModelStep
		}
		if len(calls) != 0 {
			results := resultMessages(messages, ordinal)
			messageText := messageVisibleText(m)
			for index, call := range calls {
				callLocator := locator
				callLocator.ItemIndex = index
				obs := Observation{Kind: "tool_exchange", SourceLocator: callLocator, ModelStep: step,
					Call: &ToolCall{CallID: call.CallID, Name: call.Name, Arguments: cloneJSON(call.Arguments)}, Status: "incomplete"}
				if index == 0 {
					obs.Text = messageText
				}
				if foundResult, found := results[call.CallID]; found {
					result := foundResult.result
					obs.Result = &ToolResult{CallID: result.CallID, Name: result.Name, Text: result.Text,
						Outcome: result.Outcome, Failure: cloneJSON(result.Failure), Terminal: result.Terminal}
					resultLocator := contracts.ExecutionLocator{ExecutionID: e.ExecutionID, MessageOrdinal: foundResult.ordinal}
					for _, linkedResult := range byMessage[foundResult.ordinal] {
						resultLocator.RecordID, resultLocator.Ordinal = linkedResult.ID, linkedResult.Ordinal
						break
					}
					obs.ResultSourceLocator = &resultLocator
					obs.Status = result.Outcome
					if obs.Status == "" {
						obs.Status = "completed"
					}
				}
				if obs.Result == nil {
					obs.Activity = recordActivity(records, call.CallID, e.ExecutionID)
				}
				addObservation(ordinal, obs)
				if call.CallID != "" {
					seenToolCalls[call.CallID] = true
				}
			}
			if step != 0 {
				seenAssistantStep[step] = true
			}
			continue
		}
		text := messageVisibleText(m)
		if text == "" {
			continue
		}
		kind, status := "assistant", "completed"
		if messageRecord.Phase == "incomplete" || recordHasFinishReason(records, step, "length") {
			kind, status = "assistant_partial", "incomplete"
		}
		addObservation(ordinal, Observation{Kind: kind, SourceLocator: locator, ModelStep: step, Text: text, Status: status})
		if step != 0 {
			seenAssistantStep[step] = true
		}
	}

	// Semantic call/result rows fill evidence absent from the messages without
	// replacing any message-backed model call.
	for _, record := range records {
		v := decodeRecord(record)
		if record.Kind != "tool_call" || v.CallID == "" || seenToolCalls[v.CallID] {
			continue
		}
		// Calls with a parent belong to an outer call's activity metadata. They
		// are not independent model exchanges and their full input/result must
		// not be projected a second time.
		if v.ParentCallID != "" {
			continue
		}
		step := v.Request.ModelStep
		if step == 0 {
			step = v.ModelStep
		}
		locator := recordLocator(e.ExecutionID, record, v)
		obs := Observation{Kind: "tool_exchange", SourceLocator: locator, ModelStep: step, Status: "incomplete",
			Call: &ToolCall{CallID: v.CallID, Name: v.Name, Arguments: cloneJSON(v.Input)},
		}
		if resultRecord, ok := findToolResult(records, v.CallID); ok {
			resultValue := decodeRecord(resultRecord)
			obs.Result = &ToolResult{CallID: v.CallID, Name: resultValue.Name, Text: string(resultValue.Result), Outcome: resultValue.Outcome}
			resultLocator := recordLocator(e.ExecutionID, resultRecord, resultValue)
			obs.ResultSourceLocator = &resultLocator
			obs.Status = resultValue.Phase
		}
		if obs.Result == nil {
			obs.Activity = recordActivity(records, v.CallID, e.ExecutionID)
		}
		addObservation(record.Ordinal, obs)
	}

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].ordinal < candidates[j].ordinal })
	projection.Observations = make([]Observation, 0, len(candidates))
	for _, item := range candidates {
		projection.Observations = append(projection.Observations, item.obs)
	}
	projection.InheritedSources = inherited
	return projection, nil
}

// MessageText encodes a projection as a normal user message. Its body is
// deliberately independent of the source provider's replay format.
func MessageText(projection Projection) (string, error) {
	body, err := json.Marshal(projection)
	if err != nil {
		return "", fmt.Errorf("recall encode projection: %w", err)
	}
	return messagePreamble + string(body), nil
}

// DecodeMessage reads a reference message made by MessageText.
func DecodeMessage(message contracts.Message) (Projection, error) {
	text := message.VisibleText()
	if !strings.HasPrefix(text, messagePreamble) {
		return Projection{}, fmt.Errorf("recall decode reference: missing projection preamble")
	}
	var projection Projection
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(text, messagePreamble))), &projection); err != nil {
		return Projection{}, fmt.Errorf("recall decode reference projection: %w", err)
	}
	return projection, nil
}

func messageVisibleText(message messageEnvelope) string {
	if message.Text != nil {
		return *message.Text
	}
	var content visibleText
	if json.Unmarshal(message.Content, &content) == nil && content.Kind == "text" {
		return content.Text
	}
	return ""
}

func isSteering(message messageEnvelope) bool { return message.Steering != nil && *message.Steering }

func decodeCalls(raw json.RawMessage) ([]contracts.ToolCall, error) {
	var calls []contracts.ToolCall
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &calls); err != nil {
		// Text content is the ordinary assistant shape, not a malformed call list.
		var text visibleText
		if json.Unmarshal(raw, &text) == nil {
			return nil, nil
		}
		return nil, err
	}
	return calls, nil
}

func resultMessages(messages map[int64]messageEnvelope, after int64) map[string]struct {
	ordinal int64
	result  contracts.ToolResult
} {
	ordinals := make([]int64, 0, len(messages))
	for ordinal := range messages {
		if ordinal > after && messages[ordinal].Role == "tool" {
			ordinals = append(ordinals, ordinal)
		}
	}
	sort.Slice(ordinals, func(i, j int) bool { return ordinals[i] < ordinals[j] })
	results := make(map[string]struct {
		ordinal int64
		result  contracts.ToolResult
	})
	for _, ordinal := range ordinals {
		var values []contracts.ToolResult
		if err := json.Unmarshal(messages[ordinal].Content, &values); err != nil {
			continue
		}
		for _, result := range values {
			if result.CallID != "" {
				if _, exists := results[result.CallID]; !exists {
					results[result.CallID] = struct {
						ordinal int64
						result  contracts.ToolResult
					}{ordinal, result}
				}
			}
		}
	}
	return results
}

func decodeRecord(record Record) payloadRecord {
	var value payloadRecord
	_ = json.Unmarshal(record.Payload, &value)
	if value.CallID == "" {
		value.CallID = value.ID
	}
	if value.ParentCallID == "" {
		value.ParentCallID = value.Parent
	}
	if value.Conversation.EntityKind == "" && record.Conversation != nil {
		value.Conversation = *record.Conversation
	}
	return value
}

func hasMessageLocator(value payloadRecord) bool {
	return value.Locator.ExecutionID != "" && value.Locator.MessageOrdinal >= 0 && value.Conversation.EntityKind != ""
}

func recordLocator(executionID string, record Record, value payloadRecord) contracts.ExecutionLocator {
	locator := contracts.ExecutionLocator{ExecutionID: executionID, RecordID: record.ID, Ordinal: record.Ordinal}
	if hasMessageLocator(value) {
		locator.MessageOrdinal, locator.ItemIndex = value.Locator.MessageOrdinal, value.Locator.ItemIndex
	}
	return locator
}

func recordActivity(records []Record, parentCallID, executionID string) []Activity {
	children := make(map[string][]Record)
	for _, record := range records {
		value := decodeRecord(record)
		if value.ParentCallID == "" || value.CallID == "" {
			continue
		}
		children[value.ParentCallID] = append(children[value.ParentCallID], record)
	}

	var result []Activity
	indexes := make(map[string]int)
	visited := make(map[string]bool)
	var visit func(string)
	visit = func(parent string) {
		if visited[parent] {
			return
		}
		visited[parent] = true
		for _, record := range children[parent] {
			value := decodeRecord(record)
			status := value.Phase
			if status == "" {
				status = value.Outcome
			}
			activity := Activity{SourceLocator: recordLocator(executionID, record, value), CallID: value.CallID,
				ParentCallID: value.ParentCallID, Name: value.Name, Status: status}
			if index, exists := indexes[value.CallID]; exists {
				// A started and a settled observation share the same call ID. Keep
				// the latest persisted state as one lightweight activity item.
				result[index] = activity
			} else {
				indexes[value.CallID] = len(result)
				result = append(result, activity)
			}
			visit(value.CallID)
		}
	}
	visit(parentCallID)
	return result
}

func findToolResult(records []Record, callID string) (Record, bool) {
	for _, record := range records {
		value := decodeRecord(record)
		if record.Kind == "tool_result" && value.CallID == callID {
			return record, true
		}
	}
	return Record{}, false
}

func recordHasFinishReason(records []Record, step int, reason string) bool {
	for _, record := range records {
		value := decodeRecord(record)
		if record.Kind == "model_result" && value.Request.ModelStep == step {
			if value.FinishReason == reason {
				return true
			}
			var result struct {
				FinishReason string `json:"finishReason"`
			}
			_ = json.Unmarshal(value.Result, &result)
			if result.FinishReason == reason {
				return true
			}
		}
	}
	return false
}

func observationKey(obs Observation) string {
	locator := obs.SourceLocator
	return fmt.Sprintf("%s|%s|%d|%d|%d|%s|%s", locator.ExecutionID, locator.RecordID,
		locator.Ordinal, locator.MessageOrdinal, locator.ItemIndex, obs.Kind, callID(obs.Call))
}

func callID(call *ToolCall) string {
	if call == nil {
		return ""
	}
	return call.CallID
}

func summaryFromProjection(projection Projection) SourceSummary {
	return SourceSummary{SourceExecutionID: projection.SourceExecutionID, Turn: projection.Turn,
		AgentName: projection.AgentName, Selection: projection.Selection, Outcome: projection.Outcome, Task: projection.Task}
}

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}
