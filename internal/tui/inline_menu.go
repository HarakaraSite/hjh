package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/sessionid"
	"hjh/internal/tui/renderer"
)

type inlineOption struct{ label, detail string }

// Every choice occupies one terminal row. The window follows the selection.
func inlineMenuRows(title string, notices []string, options []inlineOption, selected, width, available int) []string {
	if available < 2 {
		return nil
	}
	width = max(1, width)
	if len(options) > 0 {
		title += fmt.Sprintf(" · %d/%d", selected+1, len(options))
	}
	row := func(text string) string { return ansi.Truncate(strings.Join(strings.Fields(text), " "), width, "…") }
	rows := []string{styledText(row(title), defaultDimSGR)}
	// Leave a row for choices even while a catalog or favorite save is pending.
	for _, notice := range notices {
		if len(rows) >= available-1 {
			break
		}
		rows = append(rows, styledText(row(notice), defaultDimSGR))
	}
	count := min(len(options), 8, available-len(rows))
	start := max(0, min(selected-count/2, len(options)-count))
	for i := start; i < start+count; i++ {
		marker := "  "
		if i == selected {
			marker = "> "
		}
		label := ansi.Truncate(marker+strings.Join(strings.Fields(options[i].label), " "), width, "…")
		if i == selected {
			label = styledText(label, blueSGR)
		}
		if room := width - ansi.StringWidth(label) - 3; room > 0 && options[i].detail != "" {
			label += styledText(" · "+ansi.Truncate(strings.Join(strings.Fields(options[i].detail), " "), room, "…"), defaultDimSGR)
		}
		rows = append(rows, label)
	}
	return rows
}

func (m *model) screenOverlayOpen() bool { return m.authUI != nil && m.authUI.open }

func (m *model) pickerRows(available int) []string {
	if m.screenOverlayOpen() || !m.pickerOpen {
		return nil
	}
	if m.connectionPicker != nil {
		return m.connectionPickerRows(available)
	}
	title := "Sessions"
	var notices []string
	if m.sessionPickerNotice != "" {
		notices = append(notices, m.sessionPickerNotice)
	}
	if m.sessionDeleteTarget != "" {
		title = "Delete Session? · " + m.shortSessionID(m.sessionDeleteTarget)
	}
	if m.sessionDeleteJobID != 0 {
		title = "Deleting Session"
		if m.sessionDeleteTarget != "" {
			title += " · " + m.shortSessionID(m.sessionDeleteTarget)
		}
	}
	options := sessionPickerOptions(m.sessions, m.sessionID)
	if m.pickerJobID != 0 {
		options = nil
		notices = append(notices, "Loading Sessions…")
	}
	return inlineMenuRows(title, notices, options, m.pickerIndex, m.width, available)
}

func sessionPickerOptions(sessions []api.Session, current string) []inlineOption {
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	options := make([]inlineOption, len(sessions))
	for i, s := range sessions {
		title := sessionTitle(s)
		if title == "" {
			title = "(untitled)"
		}
		label := sessionid.Short(s.ID, ids...)
		if s.ID == current {
			label += " ✓"
		}
		if s.ActiveRunID != "" {
			label += " · running"
		}
		label += " · " + title
		detail := fmt.Sprintf("%s · %d turns · %s · %s", selectedModel(s.ActiveModel), s.TurnCount, s.UpdatedAt, s.WorkspaceRoot)
		options[i] = inlineOption{label: label, detail: detail}
	}
	return options
}

func (m *model) pickerPageSize() int {
	available := max(2, m.height-len(renderer.WrapText(strings.Join(m.footerRows(), "\n"), max(1, m.width)))-len(renderer.WrapText(m.textarea.View(), max(1, m.width)))-len(m.childProgressRows())-2)
	rows := m.pickerRows(available)
	count := 0
	for _, row := range rows {
		plain := ansi.Strip(row)
		if strings.HasPrefix(plain, "> ") || strings.HasPrefix(plain, "  ") {
			count++
		}
	}
	return max(1, count)
}
