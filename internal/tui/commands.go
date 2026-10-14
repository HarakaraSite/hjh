package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
)

func (m *model) handleCommand(line string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "/provider", "/effort", "/auth", "/login":
		m.connectionCommand(fields)
	case "/help":
		m.appendEntry("help", helpText)
		m.status = "help"
	case "/quit":
		if len(fields) != 1 {
			m.status = "usage: /quit"
			return
		}
		m.requestCoreShutdown()
	case "/detach":
		if len(fields) != 1 {
			m.status = "usage: /detach"
			return
		}
		m.requestDetach()
	case "/rename":
		m.renameSession(strings.TrimSpace(strings.TrimPrefix(line, "/rename")))
	case "/new":
		if len(fields) != 1 {
			m.status = "usage: /new"
			return
		}
		m.changeSession(sessionAction{newSession: true})
	case "/resume":
		if len(fields) != 2 {
			m.status = "usage: /resume SESSION_ID|latest"
			return
		}
		m.resolveResume(fields[1])
	case "/sessions":
		if len(fields) != 1 {
			m.status = "usage: /sessions"
			return
		}
		m.openSessionPicker()
	case "/history":
		if len(fields) != 1 && len(fields) != 3 {
			m.status = "usage: /history [FROM_TURN TO_TURN]"
			return
		}
		if m.sessionID == "" {
			m.status = "start or resume a Session before viewing history"
			return
		}
		from, to := int64(0), int64(-1)
		if len(fields) == 3 {
			var err error
			if from, err = strconv.ParseInt(fields[1], 10, 64); err != nil {
				m.status = "history turn bounds must be integers"
				return
			}
			if to, err = strconv.ParseInt(fields[2], 10, 64); err != nil {
				m.status = "history turn bounds must be integers"
				return
			}
		}
		sessionID := m.sessionID
		m.query("history · "+m.shortSessionID(sessionID), func(ctx context.Context) (string, error) {
			messages, err := m.backend.Conversation(ctx, sessionID, from, to)
			if err != nil {
				return "", err
			}
			return formatConversation(messages), nil
		})
	case "/inputs":
		if len(fields) != 1 {
			m.status = "usage: /inputs"
			return
		}
		if m.sessionID == "" {
			m.status = "start or resume a Session before listing runtime inputs"
			return
		}
		sessionID := m.sessionID
		m.query("runtime inputs · "+m.shortSessionID(sessionID), func(ctx context.Context) (string, error) {
			page, err := m.backend.Inputs(ctx, sessionID, "", 0, false)
			if err != nil {
				return "", err
			}
			return formatRuntimeInputs(page.Receipts), nil
		})
	case "/context":
		m.contextCommand(fields)
	case "/recall":
		m.recallCommand(fields)
	case "/executions":
		if len(fields) != 1 {
			m.status = "usage: /executions"
			return
		}
		if m.sessionID == "" {
			m.status = "start or resume a Session before listing executions"
			return
		}
		sessionID := m.sessionID
		m.query("executions · "+m.shortSessionID(sessionID), func(ctx context.Context) (string, error) {
			executions, err := m.backend.Executions(ctx, sessionID)
			if err != nil {
				return "", err
			}
			return formatExecutions(executions), nil
		})
	case "/tools":
		var lines []string
		for _, entity := range m.mergedEntities() {
			if entity.Kind != "tool" {
				continue
			}
			lines = append(lines, fmt.Sprintf("%d · %s %s · %s · %s", len(lines)+1, entity.Name, entity.Preview, entity.Status, entity.ID))
		}
		if len(lines) == 0 {
			m.status = "no tool calls in this Session"
		} else {
			m.appendEntry("tools", strings.Join(lines, "\n")+"\n/tool NUMBER shows one saved call/result")
		}
	case "/tool":
		if len(fields) != 2 {
			m.status = "usage: /tool NUMBER_OR_ENTITY_ID (see /tools)"
			return
		}
		var selected *api.ConversationEntity
		number, _ := strconv.Atoi(fields[1])
		index := 0
		for _, entity := range m.mergedEntities() {
			if entity.Kind != "tool" {
				continue
			}
			index++
			if entity.ID == fields[1] || (number > 0 && index == number) {
				selected = &entity
				break
			}
		}
		if selected == nil || selected.Detail == nil {
			m.status = "tool detail not found (see /tools)"
			return
		}
		locator := *selected.Detail
		m.query("tool · "+selected.Name, func(ctx context.Context) (string, error) {
			record, err := m.backend.Record(ctx, locator.ExecutionID, locator.Ordinal)
			if err != nil {
				return "", err
			}
			return formatToolDetail(record), nil
		})
	case "/inspect":
		if len(fields) != 2 && len(fields) != 4 {
			m.status = "usage: /inspect EXECUTION_ID [FROM_ORDINAL TO_ORDINAL]"
			return
		}
		from, to := int64(0), int64(-1)
		if len(fields) == 4 {
			var err error
			if from, err = strconv.ParseInt(fields[2], 10, 64); err != nil {
				m.status = "inspection ordinal bounds must be integers"
				return
			}
			if to, err = strconv.ParseInt(fields[3], 10, 64); err != nil {
				m.status = "inspection ordinal bounds must be integers"
				return
			}
		}
		id := fields[1]
		m.query("inspection · "+id, func(ctx context.Context) (string, error) {
			inspection, err := m.backend.Inspect(ctx, id, from, to)
			if err != nil {
				return "", err
			}
			return formatInspection(inspection), nil
		})
	case "/model":
		if len(fields) == 1 {
			m.openConnectionPicker(modelPicker)
			return
		}
		if len(fields) != 2 {
			m.status = "usage: /model MODEL_ID"
			return
		}
		m.setModel(fields[1])
	case "/roles":
		if len(fields) != 1 {
			m.status = "usage: /roles"
			return
		}
		scope := apiclient.Scope{SessionID: m.sessionID, Workspace: m.workspace}
		m.query("roles", func(ctx context.Context) (string, error) {
			catalog, err := m.backend.Roles(ctx, scope)
			if err != nil {
				return "", err
			}
			return formatRoleCatalog(catalog), nil
		})
	case "/role":
		if len(fields) == 1 {
			m.status = "current effective role: " + m.currentRoleLabel() + " · next run: " + m.nextRoleLabel()
			return
		}
		if len(fields) != 2 {
			m.status = "usage: /role [NAME]"
			return
		}
		before := m.selectionRequest()
		m.roleID = fields[1]
		m.roleSet = true
		if before != m.selectionRequest() {
			m.nextSelectionChanged()
			if !m.headerPrinted {
				m.requestHeaderOrientation()
			}
		}
		m.status = "next run role: " + m.roleID
		if m.activeRun != nil {
			m.status += " · current effective role: " + m.currentRoleLabel()
		}
	default:
		m.status = "unknown command · use /help"
	}
}

func (m *model) currentRoleLabel() string {
	if m.activeRun != nil {
		if m.activeRun.actualRole != "" {
			return m.activeRun.actualRole + " (running)"
		}
		return "resolving (running)"
	}
	if m.lastRunRole != "" {
		return m.lastRunRole + " (last effective)"
	}
	return "none yet"
}

func (m *model) nextRoleLabel() string {
	if m.roleSet {
		return m.roleID
	}
	return "Session/config"
}

func (m *model) query(title string, operation func(context.Context) (string, error)) {
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	generation := m.sessionGeneration
	m.status = "loading " + title
	m.launch(job, func() tea.Msg {
		text, err := operation(ctx)
		return operationResultMsg{jobID: job.id, generation: generation, title: title, text: text, err: err}
	})
}

func (m *model) changeSession(action sessionAction) {
	m.resumeRequest++
	m.resumePending = false
	if !action.newSession && action.resumeID == m.sessionID {
		m.status = "current Session · " + m.shortSessionID(m.sessionID)
		return
	}
	if m.activeRun != nil {
		m.pendingSession = &action
		m.requestInputChainCancel(m.activeRun)
		if action.newSession {
			m.status = "stopping current run chain before starting a new Session"
		} else {
			m.status = "stopping current run chain before resuming " + m.shortSessionID(action.resumeID)
		}
		return
	}
	if m.chainCancel != nil && !m.chainCancelSettled {
		m.pendingSession = &action
		m.requestInputChainCancel(nil)
		m.status = "waiting for the current run chain to settle before switching Session"
		return
	}
	m.applySessionAction(action)
}

func (m *model) applySessionAction(action sessionAction) {
	if action.newSession && m.sessionID != "" {
		m.rememberSelection()
	}
	m.closeConnectionPicker()
	m.closeSessionPicker()
	m.sealOpeningHeaderForSessionSwitch()
	m.sealCurrentFrame()
	m.sessionGeneration++
	m.selectionGeneration++
	m.resetHeaderOrientation()
	m.resetFooterSelection()
	m.sessionTitle = ""
	m.sessionCreatedAt = ""
	m.sessionEntities = make(map[string]api.ConversationEntity)
	m.clearRuntimeInputs()
	m.runtimeReadPending = false
	m.runtimeReadJobID = 0
	m.receiptByPart = make(map[string]int)
	m.receiptOffset = 0
	m.lastTranscriptBytes = 0
	m.invalidateTranscript()
	if action.newSession {
		m.headerPrinted = false
		m.sessionLoading = false
		m.sessionID = ""
		m.lastRunRole = ""
		m.lastSelection = contracts.ModelSelection{}
		m.entries = nil
		m.status = "new conversation · Session is created on the first run"
		m.requestFooterSelection()
		m.rebaseAuthUIForCurrentRoute()
		m.requestHeaderOrientation()
		m.refreshViewport(true)
		return
	}
	m.sessionLoading = true
	m.lastRunRole = ""
	m.lastSelection = contracts.ModelSelection{}
	m.loadSession(action.resumeID, m.sessionGeneration)
}

func formatExecutions(executions []api.Execution) string {
	if len(executions) == 0 {
		return "No executions in this Session."
	}
	var b strings.Builder
	for i, execution := range executions {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%s  ·  %s / %s · role: %s\nturn: %d · model: %s\ntask: %s",
			execution.ID, execution.Lifecycle, execution.Outcome, execution.AgentName,
			execution.Turn, selectedModel(execution.Model), execution.Task)
		if execution.ParentExecutionID != "" {
			fmt.Fprintf(&b, "\nparent: %s · spawn call: %s", execution.ParentExecutionID, execution.SpawnCallID)
		}
	}
	return b.String()
}

func formatConversation(messages []api.Message) string {
	entries := conversationEntries(messages)
	if len(entries) == 0 {
		return "No adopted messages in this turn range."
	}
	var b strings.Builder
	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(entry.role)
		b.WriteString(":\n")
		b.WriteString(entry.text)
	}
	return b.String()
}

func conversationEntries(messages []api.Message) []displayEntry {
	entries := make([]displayEntry, 0, len(messages))
	for _, saved := range messages {
		var message contracts.Message
		if err := json.Unmarshal(saved.JSON, &message); err != nil {
			entries = append(entries, displayEntry{role: "message", text: string(saved.JSON)})
			continue
		}
		text := message.VisibleText()
		if text == "" {
			text = messageText(message.Content)
		}
		entries = append(entries, displayEntry{role: message.Role, text: text})
	}
	return entries
}

func formatInspection(inspection api.Inspection) string {
	var b strings.Builder
	execution := inspection.Execution
	fmt.Fprintf(&b, "execution %s\n%s / %s · turn %d · model %s · role %s\ntask: %s",
		execution.ID, execution.Lifecycle, execution.Outcome, execution.Turn,
		selectedModel(execution.Model), execution.AgentName, execution.Task)
	if execution.ParentExecutionID != "" {
		fmt.Fprintf(&b, "\nparent: %s · spawn call: %s", execution.ParentExecutionID, execution.SpawnCallID)
	}
	if len(inspection.Children) > 0 {
		b.WriteString("\n\nchildren:")
		for _, child := range inspection.Children {
			fmt.Fprintf(&b, "\n%s · %s / %s · role: %s · spawn call: %s\ntask: %s",
				child.ID, child.Lifecycle, child.Outcome, child.AgentName, child.SpawnCallID, child.Task)
		}
	}
	if len(inspection.Configuration) > 0 {
		b.WriteString("\n\nconfiguration:\n")
		b.WriteString(messageText(inspection.Configuration))
	}
	if len(inspection.Context) > 0 {
		b.WriteString("\n\ncontext:\n")
		b.WriteString(messageText(inspection.Context))
	}
	if len(inspection.Messages) > 0 {
		b.WriteString("\n\nmessages:")
		for _, saved := range inspection.Messages {
			var message contracts.Message
			text := string(saved.JSON)
			role := "message"
			if err := json.Unmarshal(saved.JSON, &message); err == nil {
				role = message.Role
				text = message.VisibleText()
				if text == "" {
					text = messageText(message.Content)
				}
			}
			fmt.Fprintf(&b, "\n\nordinal %d · %s:\n%s", saved.Ordinal, role, text)
		}
	}
	if len(inspection.Records) > 0 {
		b.WriteString("\n\nrecords:")
		for _, record := range inspection.Records {
			fmt.Fprintf(&b, "\n\nordinal %d · %s · %s\npayload:\n%s",
				record.Ordinal, record.Kind, record.ID, messageText(record.Payload))
			var snapshot struct {
				DetailAvailable *bool                    `json:"detailAvailable"`
				SnapshotSave    contracts.SnapshotStatus `json:"snapshotSave"`
			}
			if json.Unmarshal(record.Payload, &snapshot) == nil && snapshot.DetailAvailable != nil {
				fmt.Fprintf(&b, "\nsnapshot · %s · detail available: %t", snapshot.SnapshotSave.State, *snapshot.DetailAvailable)
			}
			if len(record.Content) > 0 {
				b.WriteString("\ncontent:\n")
				if utf8.Valid(record.Content) {
					b.Write(record.Content)
				} else {
					encoded, _ := json.Marshal(record.Content)
					b.Write(encoded)
				}
			}
			for _, relation := range record.Relations {
				fmt.Fprintf(&b, "\nrelation %s -> %s (mandatory=%t resolved=%t)",
					relation.Relation, relation.TargetRecordID, relation.Mandatory, relation.Resolved)
			}
		}
	}
	return b.String()
}

func formatToolDetail(record api.Record) string {
	var payload struct {
		Input   json.RawMessage `json:"input"`
		Result  json.RawMessage `json:"result"`
		Content json.RawMessage `json:"content"`
		Text    string          `json:"text"`
		Error   string          `json:"error"`
	}
	_ = json.Unmarshal(record.Payload, &payload)
	if payload.Text != "" {
		return payload.Text
	}
	if len(payload.Result) > 0 {
		var result struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(payload.Result, &result) == nil && result.Text != "" {
			return result.Text
		}
		return messageText(payload.Result)
	}
	if len(payload.Input) > 0 {
		return messageText(payload.Input)
	}
	if len(payload.Content) > 0 {
		var results []contracts.ToolResult
		if json.Unmarshal(payload.Content, &results) == nil && len(results) > 0 {
			var text []string
			for _, result := range results {
				text = append(text, result.Text)
			}
			return strings.Join(text, "\n")
		}
		return messageText(payload.Content)
	}
	if payload.Error != "" {
		return payload.Error
	}
	return messageText(record.Payload)
}

var helpText = slashCommandHelp() + "\n" + strings.Join([]string{
	"Enter submits while idle; during a run it keeps the draft. F2 reserves a follow-up and F3 sends steering when shown.",
	"Alt/Shift/Ctrl+Enter inserts a newline; F1 cancels the run input chain; Ctrl+C clears the draft.",
	"F4 opens sessions; Ctrl+D detaches; Ctrl+Q stops Core and exits.",
	"Model search accepts text and paste; arrows, PgUp/PgDn, Home/End navigate; Esc closes a picker.",
	"Type / to find commands; arrows select, Tab completes, Enter chooses, Esc closes suggestions.",
}, "\n")

func formatRoleCatalog(catalog api.RoleCatalog) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Default role: %s", catalog.DefaultRole)
	if len(catalog.Roles) == 0 {
		b.WriteString("\n\nNo configured roles.")
		return b.String()
	}
	for _, role := range catalog.Roles {
		fmt.Fprintf(&b, "\n\n%s", role.Name)
		if role.Name == catalog.DefaultRole {
			b.WriteString(" (default)")
		}
		if role.Description != "" {
			b.WriteString("\n")
			b.WriteString(role.Description)
		}
		if len(role.Tools) == 0 {
			b.WriteString("\ntools: (none)")
		} else {
			fmt.Fprintf(&b, "\ntools: %s", strings.Join(role.Tools, ", "))
		}
	}
	return b.String()
}

// messageText renders explicitly requested saved evidence. Live progress uses
// observationSummary and does not print these payloads.
func messageText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err == nil {
		return formatted.String()
	}
	return string(raw)
}
