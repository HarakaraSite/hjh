package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/buildinfo"
)

func TestHeaderRecordsOpeningOnceAndFooterTracksSession(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "a1b2c3d4-7552-485d-9922-a1d7ae426706"}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work/hjh"})
	defer func() { cancel(); m.shutdown() }()
	out := attachWriter(m)
	m.View()
	if !strings.Contains(out.String(), "╭─ hjh v"+buildinfo.Version) || !strings.Contains(out.String(), "not created yet") {
		t.Fatalf("new opening header missing: %s", out.String())
	}
	out.Reset()
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706" // Core admission binds the first run.
	m.sessionTitle = "READMEの要約"
	m.View()
	if strings.Contains(out.String(), "hjh v") || !strings.Contains(out.String(), "READMEの要約 · b71a29a6") {
		t.Fatalf("admission duplicated header or lost current identity: %s", out.String())
	}
	m.sessionTitle = "新しい名前"
	update(m, tea.WindowSizeMsg{Width: 48, Height: 14})
	out.Reset()
	m.View()
	if strings.Contains(out.String(), "hjh v") || !strings.Contains(out.String(), "新しい名前 · b71a29a6") {
		t.Fatalf("rename/resize duplicated header or lost footer: %s", out.String())
	}
	m.applySessionAction(sessionAction{newSession: true})
	update(m, receive(t, messages))
	out.Reset()
	m.View()
	if strings.Count(out.String(), "hjh v") != 1 || !strings.Contains(out.String(), "Session new") {
		t.Fatalf("/new did not print its own opening header: %s", out.String())
	}
}

func TestSavedHeaderShowsMetadataAndFitsTerminal(t *testing.T) {
	m := newModel(t.Context(), nil, &fakeBackend{}, Options{Core: api.Core{CoreID: "a1b2c3d4-7552-485d-9922-a1d7ae426706"}, SessionID: "b71a29a6-7552-485d-9922-a1d7ae426706"})
	title := strings.Repeat("日本語🙂", 15)
	m.setSessionMetadata(api.Session{ID: m.sessionID, Title: &title, CreatedAt: "2026-10-12T01:23:00Z", WorkspaceRoot: "/home/agent/projects/hjh"})
	m.width, m.height = 80, 24
	rows := m.headerRows()
	text := strings.Join(rows, "\n")
	for _, marker := range []string{"Core a1b2c3d4", "Session b71a29a6", "2026-10-12", "/home/agent/projects/hjh", "日本語"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("saved header missing %s: %s", marker, text)
		}
	}
	if strings.Contains(text, "agent:") || strings.Contains(text, "runtime:") {
		t.Fatal("excluded header fields present")
	}
	for _, row := range rows {
		if ansi.StringWidth(row) != 80 {
			t.Fatalf("boxed row width = %d: %s", ansi.StringWidth(row), row)
		}
	}
	m.width, m.height = 50, 14
	m.workspace = "/home/agent/projects/hjh/dist/i17-live/workspace"
	rows = m.headerRows()
	if len(rows) != 2 || !strings.Contains(rows[1], "Session b71a29a6") || !strings.HasSuffix(rows[1], "workspace") {
		t.Fatalf("compact header loses Session or workspace: %v", rows)
	}
	for _, row := range rows {
		if ansi.StringWidth(row) > 50 {
			t.Fatalf("compact row overflow: %s", row)
		}
	}
	if !strings.Contains(m.footerDivider(m.width), "b71a29a6") {
		t.Fatal("long title obscures short Session ID")
	}
}
