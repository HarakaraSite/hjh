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
	case "/provider", "/providers", "/models", "/effort", "/auth", "/login":
		m.connectionCommand(fields)
	case "/help":
		m.appendEntry("help", helpText)
		m.status = "help"
	case "/quit":
		if len(fields) != 1 {
			m.status = "usage: /quit"
			return
		}
		if m.activeRun != nil {
			m.requestExit()
		} else {
			m.quitting = true
		}
	case "/new":
		if len(fields) != 1 {
			m.status = "usage: /new"
			return
		}
		m.changeSession(sessionAction{newSession: true})
	case "/resume":
		if len(fields) != 2 {
			m.status = "usage: /resume SESSION_ID"
			return
		}
		m.changeSession(sessionAction{resumeID: fields[1]})
	case "/sessions":
		if len(fields) != 1 {
			m.status = "usage: /sessions"
			return
		}
		ctx, cancel := context.WithCancel(m.ctx)
		job := m.newJob(cancel)
		generation := m.sessionGeneration
		m.status = "loading Sessions"
		m.launch(job, func() tea.Msg {
			sessions, err := m.backend.Sessions(ctx)
			return sessionsLoadedMsg{jobID: job.id, generation: generation, sessions: sessions, err: err}
		})
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
		m.query("history · "+sessionID, func(ctx context.Context) (string, error) {
			messages, err := m.backend.Conversation(ctx, sessionID, from, to)
			if err != nil {
				return "", err
			}
			return formatConversation(messages), nil
		})
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
		m.query("executions · "+sessionID, func(ctx context.Context) (string, error) {
			executions, err := m.backend.Executions(ctx, sessionID)
			if err != nil {
				return "", err
			}
			return formatExecutions(executions), nil
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
			if m.modelID == "" {
				m.status = "model will come from config or the selected Session"
			} else {
				m.status = "model for next run: " + m.modelID
			}
			return
		}
		if len(fields) != 2 {
			m.status = "usage: /model MODEL_ID"
			return
		}
		m.modelID = fields[1]
		m.modelSet = true
		m.status = "model for next run: " + m.modelID
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
		m.roleID = fields[1]
		m.roleSet = true
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

func (m *model) queryLogin(title string, operation func(context.Context) (api.Login, error)) {
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	generation := m.sessionGeneration
	m.status = "loading " + title
	m.launch(job, func() tea.Msg {
		login, err := operation(ctx)
		if err != nil {
			return operationResultMsg{jobID: job.id, generation: generation, title: title, err: err}
		}
		text := login.AuthorizationURL + "\n\nOpen this URL in your browser, then submit /login complete " + login.ProviderID + " for a local browser callback, or /login callback " + login.ProviderID + " CALLBACK_URL"
		return operationResultMsg{jobID: job.id, generation: generation, title: title, text: text, login: &login}
	})
}

func (m *model) changeSession(action sessionAction) {
	if m.activeRun != nil {
		m.pendingSession = &action
		m.requestRunCancel(m.activeRun)
		if action.newSession {
			m.status = "stopping current run before starting a new Session"
		} else {
			m.status = "stopping current run before resuming " + action.resumeID
		}
		return
	}
	m.applySessionAction(action)
}

func (m *model) applySessionAction(action sessionAction) {
	m.sessionGeneration++
	if action.newSession {
		m.sessionLoading = false
		m.sessionID = ""
		m.lastRunModel = ""
		m.lastRunRole = ""
		m.lastSelection = contracts.ModelSelection{}
		m.entries = nil
		m.status = "new conversation · Session is created on the first run"
		m.refreshViewport(true)
		return
	}
	m.sessionLoading = true
	m.lastRunModel = ""
	m.lastRunRole = ""
	m.lastSelection = contracts.ModelSelection{}
	m.loadSession(action.resumeID, m.sessionGeneration)
}

func formatSessionPicker(sessions []api.Session, selected int) string {
	var b strings.Builder
	b.WriteString("Saved Sessions  ·  ↑/↓ or j/k selects · Enter resumes · Esc closes")
	for i, session := range sessions {
		b.WriteString("\n\n")
		marker := "  "
		if i == selected {
			marker = "▶ "
		}
		title := "(untitled)"
		if session.Title != nil {
			title = *session.Title
		}
		fmt.Fprintf(&b, "%s%s · %s\n  model: %s · turns: %d · updated: %s\n  workspace: %s",
			marker, title, session.ID, selectedModel(session.ActiveModel), session.TurnCount, session.UpdatedAt, session.WorkspaceRoot)
	}
	return b.String()
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

var helpText = strings.Join([]string{
	"/sessions                         choose a saved Session with arrows and Enter",
	"/new                              start a new conversation in this workspace",
	"/resume SESSION_ID                load an adopted conversation",
	"/history [FROM_TURN TO_TURN]      show the complete adopted turn range",
	"/executions                       list executions, including failed and cancelled runs",
	"/inspect ID [FROM TO]             show execution, children, messages, records, configuration, and context",
	"/model MODEL_ID                   select the model for the next run",
	"/providers                        show external provider definitions",
	"/provider PROVIDER_ID             select a connection for the next run",
	"/models [PROVIDER_ID]              show models for a connection",
	"/effort EFFORT                     select reasoning effort for the next run",
	"/login PROVIDER_ID                 Continue with ChatGPT",
	"/login complete PROVIDER_ID         complete a local browser callback",
	"/login callback PROVIDER_ID URL     complete login with the browser callback URL",
	"/auth [PROVIDER_ID]                show ChatGPT login status",
	"/roles                            show configured roles and their tools",
	"/role [NAME]                      show role state or select a role for the next run",
	"/help                             show these commands",
	"/quit                             stop an active run, then exit",
	"Enter submits; Alt+Enter or Ctrl+J inserts a newline. Esc stops a run and keeps the UI open.",
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
