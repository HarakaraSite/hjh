package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
)

// Match Henji's terminal palette; ordinary metadata retains the user's default
// foreground. Conversation body styles are separate from Markdown rendering.
const (
	resetSGR      = "\x1b[0m"
	yellowSGR     = "\x1b[33m"
	yellowDimSGR  = "\x1b[33;2m"
	defaultDimSGR = "\x1b[39;2m"
	magentaDimSGR = "\x1b[35;2m"
	redSGR        = "\x1b[31m"
	blueSGR       = "\x1b[34m"
	userPanelSGR  = "\x1b[39;48;5;237m"
)

func styledText(text, sgr string) string {
	if text == "" || sgr == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if line != "" {
			lines[index] = sgr + line + resetSGR
		}
	}
	return strings.Join(lines, "\n")
}

func userPanel(text string, width int) string {
	lines := strings.Split(ansi.Hardwrap(text, max(1, width), true), "\n")
	for index, line := range lines {
		if index == len(lines)-1 && line == "" {
			continue
		}
		lines[index] = userPanelSGR + line + strings.Repeat(" ", max(0, width-ansi.StringWidth(line))) + resetSGR
	}
	return strings.Join(lines, "\n")
}

func entityLabelStyle(entity api.ConversationEntity, width int) string {
	label := entityLabel(entity) + ":"
	switch entity.Kind {
	case "task":
		return userPanel(yellowSGR+label+userPanelSGR, width) + "\n"
	case "assistant":
		return styledText(label, yellowSGR) + "\n"
	case "thinking", "tool":
		return styledText(label, yellowDimSGR) + "\n"
	default:
		return styledText(label, magentaDimSGR) + "\n"
	}
}

func entryBodyKind(role string) string {
	if strings.HasPrefix(role, "tool · ") {
		return "tool-detail"
	}
	switch role {
	case "run ended":
		return "failure"
	case "tool detail", "tools":
		return "tool-detail"
	default:
		return "system"
	}
}

func entryLabelStyle(role string) string {
	style := magentaDimSGR
	switch entryBodyKind(role) {
	case "failure":
		style = redSGR
	case "tool-detail":
		style = yellowDimSGR
	}
	return styledText(role+":", style) + "\n"
}

func nativeBody(source, kind string, width int) string {
	switch kind {
	case "task":
		return userPanel(source, width)
	case "thinking", "tool-detail":
		return styledText(source, defaultDimSGR)
	case "failure":
		return styledText(source, redSGR)
	case "system", "execution":
		return styledText(source, magentaDimSGR)
	default:
		return source
	}
}

func editorStyles() textarea.Styles {
	styles := textarea.DefaultDarkStyles()
	for _, state := range []*textarea.StyleState{&styles.Focused, &styles.Blurred} {
		base := lipgloss.NewStyle().Background(lipgloss.Color("237"))
		state.Base = base
		state.CursorLine = base
		state.Text = base
		state.Prompt = base
		state.Placeholder = base.Faint(true)
		state.EndOfBuffer = base
	}
	return styles
}
