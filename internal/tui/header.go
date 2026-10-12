package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/buildinfo"
	"hjh/internal/sessionid"
)

// headerRows records the opening state once. Current Session facts belong in
// the live footer, so admission, rename and resize never append another header.
func (m *model) headerRows() []string {
	width := max(1, min(160, m.width))
	workspace := m.workspace
	if workspace == "" {
		workspace = "(current directory)"
	}
	id, title, created := m.shortSessionID(m.sessionID), m.sessionTitle, sessionTimestamp(m.sessionCreatedAt)
	if id == "" {
		id, title, created = "new", "new conversation", "not created yet"
	} else if title == "" {
		title = "(untitled)"
	}
	identity := "Core " + sessionid.Short(m.core.CoreID) + " · Session " + id
	if width < 64 || m.height < 16 {
		prefix := identity + " · "
		path := workspace
		available := max(1, width-ansi.StringWidth(prefix))
		if ansi.StringWidth(path) > available {
			path = ansi.TruncateLeft(path, ansi.StringWidth(path)-available+ansi.StringWidth("…"), "…")
		}
		return []string{
			ansi.Truncate(fmt.Sprintf("hjh v%s · %s · %s", buildinfo.Version, created, title), width, "…"),
			ansi.Truncate(prefix+path, width, "…"),
		}
	}
	inside := width - 2
	line := func(label, value string) string {
		text := ansi.Truncate(fmt.Sprintf(" %-10s %s", label, value), inside, "…")
		return "│" + text + strings.Repeat(" ", max(0, inside-ansi.StringWidth(text))) + "│"
	}
	heading := "─ hjh v" + buildinfo.Version + " "
	base, contextSources, skills := m.headerOrientationValues()
	return []string{
		"╭" + heading + strings.Repeat("─", max(0, inside-ansi.StringWidth(heading))) + "╮",
		line("session:", title),
		line("created:", created),
		line("IDs:", identity),
		line("workspace:", workspace),
		line("base:", base),
		line("context:", contextSources),
		line("skills:", skills),
		"╰" + strings.Repeat("─", inside) + "╯",
	}
}

func sessionTimestamp(value string) string {
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "—"
	}
	return stamp.Local().Format("2006-01-02 15:04 MST")
}

func (m *model) setSessionMetadata(session api.Session) {
	m.sessionTitle = sessionTitle(session)
	m.sessionCreatedAt = session.CreatedAt
	m.workspace = session.WorkspaceRoot
}

func (m *model) sessionFooter() string {
	id := m.shortSessionID(m.sessionID)
	if id == "" {
		return "session: new · created on first run"
	}
	title := m.sessionTitle
	if title == "" {
		title = "(untitled)"
	}
	prefix := "session: " + id + " · "
	return ansi.Truncate(prefix+ansi.Truncate(title, max(1, m.width-ansi.StringWidth(prefix)), "…"), max(1, m.width), "…")
}
