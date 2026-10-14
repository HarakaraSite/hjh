package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

func TestSlashMenuTypingNavigationCompletionAndDispatch(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	update(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if !m.commandMenuVisible() || len(m.commandMenu.options) != len(slashCommands) {
		t.Fatal("typing / did not automatically show the command catalog")
	}
	for _, removed := range []string{"/providers", "/models"} {
		if isSlashCommand(removed) || strings.Contains(helpText, removed) {
			t.Fatalf("removed listing command is still recognized or advertised: %s", removed)
		}
	}
	update(m, tea.KeyPressMsg{Code: 'r', Text: "r"})
	update(m, tea.KeyPressMsg{Code: 'e', Text: "e"})
	if m.commandMenu.options[0].name != "/resume" || m.commandMenu.options[1].name != "/rename" {
		t.Fatalf("prefix matches did not lead the menu: %+v", m.commandMenu.options)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.textarea.Value() != "/re" || m.commandMenu.options[m.commandMenu.index].name != "/rename" {
		t.Fatal("arrow navigation changed the draft instead of the selection")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.textarea.Value() != "/rename " || m.commandMenuVisible() || len(m.entries) != 0 || m.activeRun != nil {
		t.Fatal("Tab must complete the selected name without executing it")
	}
	m.clearDraft()
	update(m, tea.PasteMsg{Content: "/mdl"})
	if !m.commandMenuVisible() || m.commandMenu.options[0].name != "/model" {
		t.Fatal("ordered-character command-name search did not match /mdl")
	}
	m.clearDraft()
	update(m, tea.PasteMsg{Content: "/he"})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.textarea.Value() != "" || m.commandMenuVisible() || m.activeRun != nil || len(m.entries) != 1 || m.entries[0].role != "help" || !strings.Contains(m.entries[0].text, "Choose reasoning effort") {
		t.Fatal("Enter did not execute /help through the existing command handler")
	}
}

func TestSlashMenuArgumentsEscapeAndEditorInput(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	update(m, tea.PasteMsg{Content: "/ren"})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.textarea.Value() != "/rename " || m.commandMenuVisible() || strings.Contains(m.status, "usage") || m.activeRun != nil {
		t.Fatal("argument-requiring command did not leave the editor ready for arguments")
	}
	update(m, tea.PasteMsg{Content: "日本語のタイトル"})
	if m.textarea.Value() != "/rename 日本語のタイトル" || m.commandMenuVisible() {
		t.Fatal("argument typing reopened or replaced the command menu")
	}
	m.clearDraft()
	update(m, tea.PasteMsg{Content: "/eff"})
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.textarea.Value() != "/eff" || m.commandMenuVisible() {
		t.Fatal("Escape must close only the suggestions and keep the draft")
	}
	m.View()
	if m.commandMenuVisible() {
		t.Fatal("rendering reopened dismissed suggestions")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if !m.commandMenuVisible() || m.textarea.Value() != "/eff" {
		t.Fatal("Tab did not reopen the dismissed menu")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.textarea.Value() != "/ef" || !m.commandMenuVisible() || m.commandMenu.index != 0 {
		t.Fatal("editing did not refresh the menu after dismissal")
	}
	for _, draft := range []string{"ordinary request", "/model chosen-model", "first line\n/eff", "/eff\nsecond line", "/not-a-command"} {
		m.clearDraft()
		update(m, tea.PasteMsg{Content: draft})
		if m.commandMenuVisible() || m.textarea.Value() != draft {
			t.Fatalf("suggestions changed a non-command-name draft: %q", draft)
		}
	}
}

func TestSlashMenuEnterOpensEffortPickerAndKeepsActiveRun(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1),
		selection: api.SelectionPreview{},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.startRun("ongoing task")
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{RunID: call.request.RunID}, nil)
	update(m, tea.PasteMsg{Content: "/eff"})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	loadedConnectionPicker(t, backend, m, messages)
	if m.connectionPicker == nil || m.connectionPicker.kind != effortPicker || m.commandMenuVisible() || m.activeRun == nil || m.activeRun.id != call.request.RunID {
		t.Fatal("command menu did not open effort picker while preserving the active run")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.pickerOpen || m.commandMenuVisible() || m.textarea.Value() != "" || m.activeRun == nil {
		t.Fatal("return from the effort picker restored stale command suggestions")
	}
}

func TestSlashMenuDockKeepsEditorAndHistory(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	m.headerPrinted = true
	update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	answer := conversationEntity("saved", "assistant", "Saved answer remains in history.", 1)
	answer.Status = "completed"
	m.sessionEntities[answer.ID] = answer
	m.invalidateTranscript()
	m.View()
	initialOutput := output.String()
	output.Reset()
	frame, _, _, _, _, _ := m.screenFrame(m.footerRows())
	initialEditorRow := m.height - len(frame.LiveRows) + frame.Cursor.Row
	update(m, tea.PasteMsg{Content: "/"})
	update(m, tea.KeyPressMsg{Code: tea.KeyUp})
	frame, cursor, content, _, _, _ := m.screenFrame(m.footerRows())
	if !strings.Contains(content, "Commands") || !strings.Contains(content, "/quit") || strings.Contains(strings.Join(frame.CommittedRows, "\n"), "Commands") {
		t.Fatal("scrolling suggestions did not show the last command as temporary dock content")
	}
	if cursor == nil || m.height-len(frame.LiveRows)+cursor.Position.Y != initialEditorRow {
		t.Fatal("opening suggestions moved the editor off its bottom position")
	}
	m.View()
	progress := conversationEntity("progress", "assistant", "Streaming while the menu is open.", 2)
	progress.Status = "streaming"
	m.sessionEntities[progress.ID] = progress
	m.invalidateTranscript()
	m.View()
	progress.Text, progress.Status = "Completed while the menu is open.", "completed"
	m.sessionEntities[progress.ID] = progress
	m.invalidateTranscript()
	m.View()
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m.View()
	combined := initialOutput + output.String()
	if strings.Contains(combined, ansi.SetMode(ansi.ModeAltScreenSaveCursor)) || strings.Count(renderer.ReceiptText(combined), answer.Text) != 1 || m.sessionEntities[answer.ID].Text != answer.Text {
		t.Fatal("suggestions switched screens or duplicated/lost saved history")
	}
	if strings.Count(renderer.ReceiptText(combined), progress.Text) != 1 {
		t.Fatalf("commands menu lost or repeated the answer completed while it was open: %q", renderer.ReceiptText(combined))
	}
	update(m, tea.WindowSizeMsg{Width: 50, Height: 14})
	update(m, tea.KeyPressMsg{Code: tea.KeyTab})
	frame, cursor, _, _, _, _ = m.screenFrame(m.footerRows())
	if !m.commandMenuVisible() || len(frame.LiveRows) > m.height || cursor == nil {
		t.Fatal("suggestions did not fit the smaller terminal")
	}
	for _, row := range frame.LiveRows {
		if ansi.StringWidth(row) > m.width {
			t.Fatalf("suggestion frame exceeded terminal width: %q", row)
		}
	}
}
