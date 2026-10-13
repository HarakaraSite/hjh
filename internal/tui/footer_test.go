package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/contracts"
)

func TestFooterUsesThreeRowsWithAdjacentEffortAndCurrentSession(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{Workspace: "/home/agent/projects/hjh"})
	defer func() { cancel(); m.shutdown() }()
	m.width = 100
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706"
	m.sessionTitle = "README要約"
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	rows := m.footerRows()
	for i := range rows {
		rows[i] = ansi.Strip(rows[i])
	}
	if len(rows) != 3 || rows[2] != "openrouter / model-name / high" {
		t.Fatalf("footer rows = %q", rows)
	}
	if !strings.HasPrefix(rows[1], "● ready   /home/agent/projects/hjh") || !strings.HasSuffix(rows[1], "README要約 · b71a29a6") {
		t.Fatalf("Session row = %q", rows[1])
	}
	if strings.Contains(rows[0], "F1") || !strings.Contains(rows[0], "Enter send") {
		t.Fatalf("idle controls = %q", rows[0])
	}
	m.width = 30
	m.sessionTitle = strings.Repeat("長いタイトル🐈", 12)
	rows = m.footerRows()
	for i := range rows {
		rows[i] = ansi.Strip(rows[i])
	}
	for _, row := range rows {
		if ansi.StringWidth(row) > m.width {
			t.Fatalf("footer overflows: %q", row)
		}
	}
	if !strings.HasSuffix(rows[1], "b71a29a6") || !strings.HasSuffix(rows[2], " / high") {
		t.Fatalf("narrow footer lost ID/effort: %q", rows)
	}
}

func TestFooterRunUsesCoreStartTimeAndAvailableControls(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.width = 120
	selection := contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	m.footerClockJobID = 4
	m.activeRun = &activeRun{jobID: 4, id: "run", coreID: "core", startedAt: time.Now().Add(-65 * time.Second), actualSelection: selection,
		hasActualSelection: true, inputAcceptance: api.InputAcceptance{FollowUp: true, Steering: true}}
	m.footerSelectionSet = true
	m.footerSelection.Selection = selection
	rows := m.footerRows()
	for i := range rows {
		rows[i] = ansi.Strip(rows[i])
	}
	if !strings.Contains(rows[1], "working 01:05") || strings.Contains(rows[2], "next:") {
		t.Fatalf("run footer = %q", rows)
	}
	for _, key := range []string{"F1 cancel", "F2 queue", "F3 steer"} {
		if !strings.Contains(rows[0], key) {
			t.Fatalf("missing run operation %q: %q", key, rows[0])
		}
	}
	if strings.Contains(rows[0], "Enter send") {
		t.Fatalf("running footer offers unavailable submit: %q", rows[0])
	}
	cmd := update(m, footerTickMsg{jobID: 4})
	if cmd == nil || m.footerClockFrame != 1 {
		t.Fatal("working spinner did not advance")
	}
	m.activeRun.canceling = true
	if controls := ansi.Strip(m.footerControls(120)); strings.Contains(controls, "F1") || strings.Contains(controls, "F2") || strings.Contains(controls, "F3") {
		t.Fatalf("cancelling controls = %q", controls)
	}
	m.activeRun = nil
	if cmd := update(m, footerTickMsg{jobID: 4}); cmd != nil {
		t.Fatal("idle footer clock kept running")
	}
	update(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if !strings.Contains(ansi.Strip(m.footerControls(120)), "Enter choose") || !strings.Contains(ansi.Strip(m.footerControls(120)), "Tab complete") {
		t.Fatal("slash draft did not change controls")
	}
}

func TestFooterSelectionReadsCorePreviewAndRejectsPreviousSelection(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core"}, selection: api.SelectionPreview{
		Selection: contracts.ModelSelection{Provider: "openrouter", ModelID: "from-config", Effort: "high"},
	}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.footerInitialized = true
	m.requestFooterSelection()
	first := receive(t, messages).(footerSelectionLoadedMsg)
	update(m, first)
	if got := m.footerModel(120); got != "openrouter / from-config / high" {
		t.Fatalf("resolved config footer = %q", got)
	}
	m.setProvider("another")
	second := receive(t, messages).(footerSelectionLoadedMsg)
	update(m, first)
	if m.footerSelectionSet {
		t.Fatal("old selection restored after provider change")
	}
	second.value.Selection = contracts.ModelSelection{Provider: "another", ModelID: "new-default", Effort: "low"}
	update(m, second)
	if got := m.footerModel(120); got != "another / new-default / low" {
		t.Fatalf("new route footer = %q", got)
	}
}
