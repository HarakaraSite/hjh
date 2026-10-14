// Package historyview selects and renders human-facing views of saved history.
// It owns no persistence or Core transport.
package historyview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/sessionid"
)

func SessionFromAPI(session api.Session) history.Session {
	return history.Session{ID: session.ID, WorkspaceRoot: session.WorkspaceRoot,
		AgentChoice: append(json.RawMessage(nil), session.AgentChoice...), Agent: session.Agent,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt, Title: session.Title,
		StateRevision: session.StateRevision, NextTurn: session.NextTurn, ActiveModel: append(json.RawMessage(nil), session.ActiveModel...),
		MessageCount: session.MessageCount, ModelChangeCount: session.ModelChangeCount, TurnCount: session.TurnCount,
		PrivateStateFromTurn: session.PrivateStateFromTurn}
}

func ExecutionFromAPI(execution api.Execution) history.Execution {
	return history.Execution{ID: execution.ID, TaskID: execution.TaskID, Task: execution.Task,
		SessionID: execution.SessionID, SessionCorrelation: execution.SessionCorrelation,
		ParentExecutionID: execution.ParentExecutionID, SpawnCallID: execution.SpawnCallID,
		Turn: execution.Turn, BaseRevision: execution.BaseRevision, BaseMessageCount: execution.BaseMessageCount,
		AgentName: execution.AgentName, Model: append(json.RawMessage(nil), execution.Model...),
		Build: append(json.RawMessage(nil), execution.Build...), ConfigurationID: execution.ConfigurationID,
		MaxSteps: execution.MaxSteps, CreatedAt: execution.CreatedAt, SettledAt: execution.SettledAt,
		Lifecycle: execution.Lifecycle, Outcome: execution.Outcome, Adoption: execution.Adoption,
		TerminalRecordID: execution.TerminalRecordID}
}

type Inspection struct {
	Execution     history.Execution
	Children      []history.Execution
	Messages      []history.Message
	Records       []history.Record
	Configuration json.RawMessage
	Context       json.RawMessage
}

func InspectionFromAPI(inspection api.Inspection) Inspection {
	result := Inspection{Execution: ExecutionFromAPI(inspection.Execution),
		Configuration: append(json.RawMessage(nil), inspection.Configuration...),
		Context:       append(json.RawMessage(nil), inspection.Context...)}
	result.Children = make([]history.Execution, len(inspection.Children))
	for i, child := range inspection.Children {
		result.Children[i] = ExecutionFromAPI(child)
	}
	result.Messages = make([]history.Message, len(inspection.Messages))
	for i, message := range inspection.Messages {
		result.Messages[i] = history.Message{ExecutionID: message.ExecutionID, Ordinal: message.Ordinal,
			TurnNumber: message.TurnNumber, JSON: append(json.RawMessage(nil), message.JSON...)}
	}
	result.Records = make([]history.Record, len(inspection.Records))
	for i, record := range inspection.Records {
		result.Records[i] = history.Record{ID: record.ID, ExecutionID: record.ExecutionID, Ordinal: record.Ordinal,
			Kind: record.Kind, ObservedAt: record.ObservedAt, Payload: append(json.RawMessage(nil), record.Payload...),
			Content: append([]byte(nil), record.Content...)}
		var envelope struct {
			Conversation *contracts.ConversationMetadata `json:"conversation"`
		}
		if json.Unmarshal(record.Payload, &envelope) == nil && envelope.Conversation != nil {
			metadata := *envelope.Conversation
			result.Records[i].Conversation = &metadata
		}
		for _, relation := range record.Relations {
			result.Records[i].Relations = append(result.Records[i].Relations, history.Relation{Relation: relation.Relation,
				TargetRecordID: relation.TargetRecordID, Mandatory: relation.Mandatory, Resolved: relation.Resolved})
		}
	}
	return result
}

// RenderSessions formats Session metadata for human-readable CLI output.
// Session IDs are shortened to eight characters unless known IDs share that prefix.
func RenderSessions(sessions []history.Session) string {
	if len(sessions) == 0 {
		return "No saved Sessions.\n"
	}
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
	}
	var out strings.Builder
	for _, session := range sessions {
		fmt.Fprintf(&out, "%s · %s · %s · %d turns · updated %s\n",
			sessionid.Short(session.ID, ids...), sessionTitle(session), session.WorkspaceRoot,
			session.TurnCount, session.UpdatedAt)
	}
	return out.String()
}

// SelectSession resolves an exact ID or unique ID prefix globally, or returns the newest
// Session in workspace when selector is empty. Newest is updatedAt descending,
// then Session ID ascending, matching the legacy CLI's stable tie-break.
func SelectSession(sessions []history.Session, workspace, selector string) (history.Session, bool, error) {
	if selector != "" {
		ids := make([]string, len(sessions))
		for i, session := range sessions {
			ids[i] = session.ID
		}
		resolved, err := sessionid.Resolve(selector, ids)
		if err != nil {
			return history.Session{}, false, err
		}
		for _, session := range sessions {
			if session.ID == resolved {
				return session, true, nil
			}
		}
	}
	candidates := make([]sessionid.Candidate, len(sessions))
	for i, session := range sessions {
		candidates[i] = sessionid.Candidate{ID: session.ID, WorkspaceRoot: session.WorkspaceRoot, UpdatedAt: session.UpdatedAt}
	}
	id, ok := sessionid.Latest(candidates, workspace)
	if ok {
		for _, session := range sessions {
			if session.ID == id {
				return session, true, nil
			}
		}
	}
	return history.Session{}, false, nil
}

func InTurnRange(turn, from, to int64) bool {
	return (from <= 0 || turn >= from) && (to < 0 || turn <= to)
}

func RenderSession(snapshot api.ConversationSnapshot, executions []history.Execution, fromTurn, toTurn int64) string {
	byID := make(map[string]history.Execution, len(executions))
	for _, execution := range executions {
		byID[execution.ID] = execution
	}
	rootTurn := make(map[string]int64, len(executions))
	var rootFor func(string) string
	rootFor = func(id string) string {
		execution, ok := byID[id]
		if !ok || execution.ParentExecutionID == "" {
			return id
		}
		if _, ok := byID[execution.ParentExecutionID]; !ok {
			return id
		}
		return rootFor(execution.ParentExecutionID)
	}
	for _, execution := range executions {
		root := rootFor(execution.ID)
		if execution.ParentExecutionID == "" {
			rootTurn[root] = execution.Turn
		} else if _, ok := rootTurn[root]; !ok {
			rootTurn[root] = execution.Turn
		}
	}
	entities := make([]api.ConversationEntity, 0, len(snapshot.Entities))
	for _, id := range snapshot.Order {
		entity, ok := snapshot.Entities[id]
		if !ok {
			continue
		}
		if fromTurn > 0 || toTurn >= 0 {
			turn := rootTurn[rootFor(entity.ExecutionID)]
			if fromTurn > 0 && turn < fromTurn || toTurn >= 0 && turn > toTurn {
				continue
			}
		}
		entities = append(entities, entity)
	}
	sort.SliceStable(entities, func(i, j int) bool {
		a, b := entities[i], entities[j]
		if a.Position.RootExecutionOrdinal != b.Position.RootExecutionOrdinal {
			return a.Position.RootExecutionOrdinal < b.Position.RootExecutionOrdinal
		}
		if a.Position.FirstSequence != b.Position.FirstSequence {
			return a.Position.FirstSequence < b.Position.FirstSequence
		}
		if a.Position.ItemIndex != b.Position.ItemIndex {
			return a.Position.ItemIndex < b.Position.ItemIndex
		}
		return a.ID < b.ID
	})
	var out strings.Builder
	for i, entity := range entities {
		if i > 0 {
			out.WriteByte('\n')
		}
		child := entity.ParentExecutionID != ""
		label := ""
		switch entity.Kind {
		case "task":
			label = "user>"
		case "assistant":
			label = "assistant>"
			if entity.Status == "streaming" || entity.Status == "incomplete" {
				label = "assistant~"
			}
		case "thinking":
			label = "thinking>"
			if entity.ThinkingKind == "summary" {
				label = "thinking summary>"
			}
			if entity.Status == "streaming" || entity.Status == "incomplete" {
				label = strings.TrimSuffix(label, ">") + "~"
			}
		case "tool":
			label = "tool>"
		case "execution":
			label = "# execution"
		default:
			label = entity.Kind + ">"
		}
		if child {
			label = "child " + entity.ExecutionID + " " + label
		}
		if entity.Kind == "tool" {
			out.WriteString(label + " " + entity.Name)
			if entity.Preview != "" {
				out.WriteString(" · " + entity.Preview)
			}
			status := entity.Outcome
			if status == "" {
				status = entity.Status
			}
			if status != "" {
				out.WriteString(" [" + status + "]")
			}
			continue
		}
		if entity.Kind == "execution" {
			execution := byID[entity.ExecutionID]
			adoption := execution.Adoption
			if adoption == "" {
				adoption = "non_canonical"
			}
			out.WriteString(label + " " + entity.ExecutionID + " · " + adoption + " · " + execution.Outcome)
			continue
		}
		out.WriteString(label + " ")
		out.WriteString(entity.Text)
	}
	if out.Len() == 0 {
		return ""
	}
	return out.String() + "\n"
}

func RenderCanonical(session history.Session, messages []history.Message, knownIDs ...string) string {
	var out strings.Builder
	out.WriteString("# hjh Session History\n\n")
	out.WriteString("- Title: " + markdownInline(sessionTitle(session)) + "\n")
	out.WriteString("- Created: " + markdownInline(session.CreatedAt) + "\n")
	out.WriteString("- Session: " + markdownInline(sessionid.Short(session.ID, knownIDs...)) + "\n")
	out.WriteString("- Agent: " + markdownInline(session.Agent) + "\n")
	committedTurn := session.NextTurn - 1
	if committedTurn < 0 {
		committedTurn = 0
	}
	out.WriteString(fmt.Sprintf("- Through turn: %d\n", committedTurn))
	out.WriteString("- Workspace:\n\n")
	out.WriteString(markdownFence(session.WorkspaceRoot, "") + "\n")
	if len(messages) == 0 {
		return out.String()
	}
	currentTurn := int64(-1)
	turnMessageCount := 0
	for _, stored := range messages {
		if stored.TurnNumber != currentTurn {
			if currentTurn >= 0 {
				out.WriteByte('\n')
			}
			currentTurn = stored.TurnNumber
			turnMessageCount = 0
			out.WriteString(fmt.Sprintf("\n## Turn %d\n\n", currentTurn))
		}
		message, err := decodeContractMessage(stored.JSON)
		if err != nil {
			out.WriteString("### message>\n\n" + markdownFence(string(stored.JSON), "json") + "\n")
			turnMessageCount++
			continue
		}
		switch message.Role {
		case "user":
			label := "user>"
			if turnMessageCount > 0 {
				label = "steer>"
			}
			out.WriteString("### " + label + "\n\n" + markdownFence(message.VisibleText(), "") + "\n")
		case "assistant":
			var calls []contracts.ToolCall
			if json.Unmarshal(message.Content, &calls) == nil && len(calls) > 0 {
				if message.Text != nil {
					out.WriteString("### assistant note>\n\n" + markdownFence(*message.Text, "") + "\n")
				}
				for _, call := range calls {
					out.WriteString("### tool> " + markdownInline(call.Name) + "\n\n")
					out.WriteString("- Call: " + markdownInline(call.CallID) + "\n\n")
					out.WriteString(markdownFence(prettyJSON(call.Arguments), "json") + "\n")
				}
			} else {
				text := message.VisibleText()
				if text != "" {
					out.WriteString("### assistant>\n\n" + markdownFence(text, "") + "\n")
				}
			}
		case "tool":
			var results []contracts.ToolResult
			if json.Unmarshal(message.Content, &results) == nil {
				for _, result := range results {
					out.WriteString("### tool< " + markdownInline(result.Name) + " · " + markdownInline(result.Outcome) + "\n\n")
					out.WriteString("- Call: " + markdownInline(result.CallID) + "\n\n")
					out.WriteString(markdownFence(result.Text, "") + "\n")
				}
			}
		}
		turnMessageCount++
	}
	return out.String()
}

func WriteDetailSession(writer io.Writer, session history.Session) error {
	return writeJSONLine(writer, map[string]any{"kind": "session", "identity": session.ID,
		"value": SessionFromHistory(session)})
}

func WriteDetailExecution(writer io.Writer, execution history.Execution) error {
	return writeJSONLine(writer, map[string]any{"kind": "execution", "identity": execution.ID,
		"value": ExecutionForDetail(execution)})
}

func WriteDetailMessage(writer io.Writer, stored history.Message, turn int64) error {
	return writeJSONLine(writer, map[string]any{"kind": "message",
		"identity": fmt.Sprintf("%s/%d", stored.ExecutionID, stored.Ordinal),
		"value": map[string]any{"executionId": stored.ExecutionID, "ordinal": stored.Ordinal,
			"turnNumber": turn, "json": append(json.RawMessage(nil), stored.JSON...)}})
}

func WriteDetailRecord(writer io.Writer, record history.Record) error {
	return writeJSONLine(writer, map[string]any{"kind": "record", "identity": record.ID,
		"value": map[string]any{"id": record.ID, "executionId": record.ExecutionID, "ordinal": record.Ordinal,
			"kind": record.Kind, "observedAt": record.ObservedAt, "payload": append(json.RawMessage(nil), record.Payload...),
			"content": record.Content, "relations": record.Relations}})
}

// WriteDetailInspection emits the captured execution configuration and context,
// every persisted message and semantic record for one execution.
func WriteDetailInspection(writer io.Writer, inspection Inspection) error {
	if err := WriteDetailExecution(writer, inspection.Execution); err != nil {
		return err
	}
	configuration := map[string]any{"executionId": inspection.Execution.ID,
		"configurationId": inspection.Execution.ConfigurationID, "snapshot": append(json.RawMessage(nil), inspection.Configuration...)}
	if err := writeJSONLine(writer, map[string]any{"kind": "configuration",
		"identity": inspection.Execution.ConfigurationID, "value": configuration}); err != nil {
		return err
	}
	if err := writeJSONLine(writer, map[string]any{"kind": "context", "identity": inspection.Execution.ID,
		"value": map[string]any{"executionId": inspection.Execution.ID,
			"manifest": append(json.RawMessage(nil), inspection.Context...)}}); err != nil {
		return err
	}
	for _, message := range inspection.Messages {
		if err := WriteDetailMessage(writer, message, inspection.Execution.Turn); err != nil {
			return err
		}
	}
	for _, record := range inspection.Records {
		if err := WriteDetailRecord(writer, record); err != nil {
			return err
		}
	}
	return nil
}

// WriteDetailLiveEntities adds public text still held in the active conversation
// state, including partial assistant text and thinking not yet in permanent messages.
func WriteDetailLiveEntities(writer io.Writer, snapshot api.ConversationSnapshot, executions []history.Execution, from, to int64) error {
	turnByExecution := make(map[string]int64, len(executions))
	for _, execution := range executions {
		turnByExecution[execution.ID] = execution.Turn
	}
	ids := append([]string(nil), snapshot.Order...)
	sort.SliceStable(ids, func(i, j int) bool {
		a, b := snapshot.Entities[ids[i]], snapshot.Entities[ids[j]]
		if a.Position.RootExecutionOrdinal != b.Position.RootExecutionOrdinal {
			return a.Position.RootExecutionOrdinal < b.Position.RootExecutionOrdinal
		}
		if a.Position.FirstSequence != b.Position.FirstSequence {
			return a.Position.FirstSequence < b.Position.FirstSequence
		}
		if a.Position.ItemIndex != b.Position.ItemIndex {
			return a.Position.ItemIndex < b.Position.ItemIndex
		}
		return a.ID < b.ID
	})
	for _, id := range ids {
		entity, ok := snapshot.Entities[id]
		if !ok || entity.Kind != "thinking" && !(entity.Kind == "assistant" && (entity.Status == "streaming" || entity.Status == "incomplete")) {
			continue
		}
		if !InTurnRange(turnByExecution[entity.ExecutionID], from, to) {
			continue
		}
		kind := entity.Kind
		if entity.Kind == "thinking" {
			kind = "thinking"
		}
		if err := writeJSONLine(writer, map[string]any{"kind": kind, "identity": entity.ID,
			"value": map[string]any{"executionId": entity.ExecutionID, "parentExecutionId": entity.ParentExecutionID,
				"status": entity.Status, "text": entity.Text, "thinkingKind": entity.ThinkingKind,
				"position": entity.Position}}); err != nil {
			return err
		}
	}
	return nil
}

func SessionFromHistory(session history.Session) api.Session {
	return api.Session{ID: session.ID, WorkspaceRoot: session.WorkspaceRoot,
		AgentChoice: append(json.RawMessage(nil), session.AgentChoice...), Agent: session.Agent,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt, Title: session.Title,
		StateRevision: session.StateRevision, NextTurn: session.NextTurn, ActiveModel: append(json.RawMessage(nil), session.ActiveModel...),
		MessageCount: session.MessageCount, ModelChangeCount: session.ModelChangeCount, TurnCount: session.TurnCount,
		PrivateStateFromTurn: session.PrivateStateFromTurn}
}

func ExecutionForDetail(execution history.Execution) api.Execution {
	return api.Execution{ID: execution.ID, TaskID: execution.TaskID, Task: execution.Task,
		SessionID: execution.SessionID, SessionCorrelation: execution.SessionCorrelation,
		ParentExecutionID: execution.ParentExecutionID, SpawnCallID: execution.SpawnCallID,
		Turn: execution.Turn, BaseRevision: execution.BaseRevision, BaseMessageCount: execution.BaseMessageCount,
		AgentName: execution.AgentName, Model: append(json.RawMessage(nil), execution.Model...),
		Build: append(json.RawMessage(nil), execution.Build...), ConfigurationID: execution.ConfigurationID,
		MaxSteps: execution.MaxSteps, CreatedAt: execution.CreatedAt, SettledAt: execution.SettledAt,
		Lifecycle: execution.Lifecycle, Outcome: execution.Outcome, Adoption: execution.Adoption,
		TerminalRecordID: execution.TerminalRecordID}
}

func writeJSONLine(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

func decodeContractMessage(raw json.RawMessage) (contracts.Message, error) {
	var message contracts.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		return contracts.Message{}, err
	}
	return message, nil
}

func sessionTitle(session history.Session) string {
	if session.Title == nil || *session.Title == "" {
		return "untitled"
	}
	return *session.Title
}

func markdownInline(text string) string {
	fence := strings.Repeat("`", max(1, longestBacktickRun(text)+1))
	padding := ""
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		padding = " "
	}
	return fence + padding + text + padding + fence
}

func markdownFence(text, language string) string {
	fence := strings.Repeat("`", max(3, longestBacktickRun(text)+1))
	separator := "\n"
	if strings.HasSuffix(text, "\n") {
		separator = ""
	}
	return fence + language + "\n" + text + separator + fence
}

func longestBacktickRun(text string) int {
	longest, current := 0, 0
	for _, r := range text {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
		} else {
			current = 0
		}
	}
	return longest
}

func prettyJSON(raw json.RawMessage) string {
	var out bytes.Buffer
	if json.Indent(&out, raw, "", "  ") == nil {
		return out.String()
	}
	return string(raw)
}
