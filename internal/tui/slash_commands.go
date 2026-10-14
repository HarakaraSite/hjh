package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

type slashCommand struct {
	name, arguments, description string
	requiresArguments            bool
}

// The same catalog supplies recognition, menu options and command help.
var slashCommands = []slashCommand{
	{"/sessions", "", "Choose a saved session", false},
	{"/new", "", "Start a new conversation in this workspace", false},
	{"/resume", "SESSION_ID|latest", "Resume a session or attach to its running execution", true},
	{"/rename", "TEXT", "Rename the current session", true},
	{"/detach", "", "Exit the TUI and let Core keep running", false},
	{"/history", "[FROM_TURN TO_TURN]", "Show the complete adopted turn range", false},
	{"/inputs", "", "List accepted steering and follow-up receipts", false},
	{"/context", "", "Show instructions, skills, saved input and pending recall", false},
	{"/recall", "[ID|latest|clear]", "Prepare a stopped execution for the next root once", false},
	{"/executions", "", "List executions, including failed and cancelled runs", false},
	{"/tools", "", "List saved tool calls", false},
	{"/tool", "NUMBER_OR_ENTITY_ID", "Show one saved tool call and result", true},
	{"/inspect", "ID [FROM TO]", "Show an execution, children, records and context", true},
	{"/model", "[MODEL_ID]", "Choose a model for the next run", false},
	{"/provider", "[PROVIDER_ID]", "Choose a connection for the next run", false},
	{"/effort", "", "Choose reasoning effort for the next run", false},
	{"/auth", "[PROVIDER_ID]", "Show authentication status", false},
	{"/login", "[PROVIDER_ID]", "Open API-key or ChatGPT account settings", false},
	{"/roles", "", "List configured roles and their tools", false},
	{"/role", "[NAME]", "Show role state or select the next role", false},
	{"/help", "", "Show command help", false},
	{"/quit", "", "Stop Core, wait for saves, then exit", false},
}

type commandMenuState struct {
	value   string
	options []slashCommand
	index   int
}

func (m *model) commandMenuVisible() bool {
	return !m.pickerOpen && m.commandMenu != nil && len(m.commandMenu.options) > 0
}

func (m *model) updateCommandMenu(force bool) {
	value := m.textarea.Value()
	if value != m.commandMenuDismissed {
		m.commandMenuDismissed = ""
	}
	query := strings.TrimLeftFunc(value, unicode.IsSpace)
	if !strings.HasPrefix(query, "/") || strings.ContainsFunc(query, unicode.IsSpace) || strings.ContainsAny(value, "\r\n") || (!force && value == m.commandMenuDismissed) {
		m.commandMenu = nil
		return
	}
	options := matchingSlashCommands(strings.ToLower(query[1:]))
	if len(options) == 0 {
		m.commandMenu = nil
		return
	}
	if !force && m.commandMenu != nil && m.commandMenu.value == value {
		return
	}
	m.commandMenu = &commandMenuState{value: value, options: options}
}

func matchingSlashCommands(query string) []slashCommand {
	type match struct {
		command slashCommand
		score   int
	}
	var matches []match
	for _, command := range slashCommands {
		name := command.name[1:]
		score := 0
		switch {
		case query == "" || name == query:
		case strings.HasPrefix(name, query):
			score = 1
		default:
			// Names are short; an ordered-character match supports /mdl and
			// /prv without a separate search dependency.
			rest := name
			matched := true
			for _, char := range query {
				index := strings.IndexRune(rest, char)
				if index < 0 {
					matched = false
					break
				}
				rest = rest[index+len(string(char)):]
			}
			if !matched {
				continue
			}
			score = 2
		}
		matches = append(matches, match{command: command, score: score})
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score < matches[j].score })
	options := make([]slashCommand, len(matches))
	for i, match := range matches {
		options[i] = match.command
	}
	return options
}

// handleCommandMenuKey runs after global controls and modal pickers, before
// textarea navigation or command submission. Text keys remain editor input.
func (m *model) handleCommandMenuKey(msg tea.KeyPressMsg) bool {
	if m.pickerOpen {
		return false
	}
	if msg.String() == "tab" && !m.commandMenuVisible() {
		m.updateCommandMenu(true)
		return m.commandMenuVisible()
	}
	if !m.commandMenuVisible() {
		return false
	}
	menu := m.commandMenu
	switch msg.String() {
	case "up":
		menu.index = (menu.index + len(menu.options) - 1) % len(menu.options)
	case "down":
		menu.index = (menu.index + 1) % len(menu.options)
	case "pgup", "pgdown":
		delta := 8
		if msg.String() == "pgup" {
			delta = -delta
		}
		menu.index = max(0, min(len(menu.options)-1, menu.index+delta))
	case "esc":
		m.commandMenuDismissed = m.textarea.Value()
		m.commandMenu = nil
	case "tab", "enter":
		command := menu.options[menu.index]
		m.textarea.SetValue(command.name + " ")
		m.textarea.CursorEnd()
		m.noteDraftEdit()
		if msg.String() == "enter" && !command.requiresArguments {
			m.submitDraft()
		} else {
			m.status = "command completed · type arguments or press Enter"
		}
	default:
		return false
	}
	return true
}

func (m *model) commandMenuRows(available int) []string {
	if !m.commandMenuVisible() || available < 2 {
		return nil
	}
	menu := m.commandMenu
	options := make([]inlineOption, len(menu.options))
	for i, command := range menu.options {
		label := command.name
		if command.arguments != "" {
			label += " " + command.arguments
		}
		options[i] = inlineOption{label: label, detail: command.description}
	}
	return inlineMenuRows("Commands", nil, options, menu.index, m.width, available)
}

func slashCommandHelp() string {
	lines := make([]string, 0, len(slashCommands))
	for _, command := range slashCommands {
		label := command.name
		if command.arguments != "" {
			label += " " + command.arguments
		}
		lines = append(lines, fmt.Sprintf("%-34s %s", label, command.description))
	}
	return strings.Join(lines, "\n")
}
