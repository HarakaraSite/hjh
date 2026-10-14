package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/contracts"
)

func TestPickersShareSingleRowInlineChoicesAndPreserveDraft(t *testing.T) {
	for _, kind := range []string{"sessions", "provider", "model", "effort"} {
		t.Run(kind, func(t *testing.T) {
			m, _, cancel := testModel(&fakeBackend{}, Options{})
			defer func() { cancel(); m.shutdown() }()
			m.width = 56
			m.height = 18
			m.layout()
			m.textarea.SetValue("keep this draft")
			m.pickerOpen = true
			selection := api.SelectionPreview{Selection: contracts.ModelSelection{Provider: "test", ModelID: "model", Effort: "high"}}
			switch kind {
			case "sessions":
				title := "very long 日本語 title " + strings.Repeat("long ", 40)
				m.sessions = []api.Session{{ID: "12345678-abcd-efgh", Title: &title}, {ID: "abcdef12-abcd-efgh"}}
				m.sessionID = m.sessions[0].ID
			case "provider":
				m.connectionPicker = &connectionPickerState{kind: providerPicker, preview: selection, providers: []api.ProviderDefinition{{ID: "test", DisplayName: "Test"}, {ID: "other"}}}
			case "model":
				m.connectionPicker = &connectionPickerState{kind: modelPicker, preview: selection, models: []api.ModelInfo{{ID: "model"}, {ID: "other"}}, favoritesKnown: true, favorites: api.ModelFavorites{ModelIDs: []string{"model"}}}
			case "effort":
				m.connectionPicker = &connectionPickerState{kind: effortPicker, preview: selection, index: 1, models: []api.ModelInfo{{ID: "model", Efforts: []string{"high"}, EffortSource: "config"}}}
			}
			rows := m.pickerRows(8)
			choiceCount := 0
			for _, row := range rows {
				plain := ansi.Strip(row)
				if strings.ContainsAny(plain, "\r\n") || ansi.StringWidth(row) > 56 {
					t.Fatalf("choice wrapped beyond one row: %q", row)
				}
				if strings.HasPrefix(plain, "> ") || strings.HasPrefix(plain, "  ") {
					choiceCount++
				}
			}
			if choiceCount != 2 || !strings.Contains(strings.Join(rows, "\n"), "✓") {
				t.Fatalf("choices/current value missing: %v", rows)
			}
			frame, _, content, _, _, _ := m.screenFrame(m.footerRows())
			if frame.Overlay || !strings.Contains(content, "keep this draft") || frame.DockRows < 7 {
				t.Fatalf("menu not inline above draft: %q", content)
			}
			if kind == "model" {
				if !strings.Contains(content, "★") {
					t.Fatal("favorite missing")
				}
				update(m, tea.KeyPressMsg{Code: 'm', Text: "m"})
				if m.connectionPicker.query != "m" || !strings.Contains(strings.Join(m.pickerRows(8), "\n"), "search: m") {
					t.Fatal("query missing")
				}
			}
			update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
			if m.pickerOpen || m.textarea.Value() != "keep this draft" {
				t.Fatal("cancel changed draft or kept picker")
			}
		})
	}
}

func TestSessionPickerDeleteConfirmCancelAndApply(t *testing.T) {
	for _, current := range []bool{false, true} {
		t.Run(map[bool]string{false: "other", true: "current"}[current], func(t *testing.T) {
			calls := make(chan string, 1)
			backend := &fakeBackend{deleteSessionFunc: func(_ context.Context, id string) error { calls <- id; return nil }}
			m, messages, cancel := testModel(backend, Options{})
			defer func() { cancel(); m.shutdown() }()
			m.sessions = []api.Session{{ID: "12345678-deleted"}, {ID: "87654321-kept"}}
			m.pickerOpen = true
			m.sessionID = m.sessions[1].ID
			if current {
				m.sessionID = m.sessions[0].ID
			}
			m.textarea.SetValue("draft survives deletion")
			update(m, tea.KeyPressMsg{Code: tea.KeyDelete})
			if m.sessionDeleteTarget != "12345678-deleted" || !strings.Contains(strings.Join(m.pickerRows(8), "\n"), "Delete Session? · 12345678") {
				t.Fatal("confirmation doesn't identify target")
			}
			update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
			if !m.pickerOpen || m.sessionDeleteTarget != "" || len(calls) != 0 {
				t.Fatal("cancel submitted deletion or closed picker")
			}
			update(m, tea.KeyPressMsg{Code: tea.KeyDelete})
			update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
			if id := <-calls; id != "12345678-deleted" {
				t.Fatal(id)
			}
			updateUntil(t, m, messages, func(msg tea.Msg) bool { _, ok := msg.(sessionDeletedMsg); return ok })
			if len(m.sessions) != 1 || m.sessions[0].ID != "87654321-kept" || m.textarea.Value() != "draft survives deletion" {
				t.Fatal("delete lost remaining session/draft")
			}
			if current && m.sessionID != "" {
				t.Fatal("deleted current session remained attached")
			}
			if !current && (m.sessionID != "87654321-kept" || !m.pickerOpen) {
				t.Fatal("other deletion switched current session")
			}
		})
	}
}

func TestSessionPickerShowsBusyDeletionErrorAndRetainsSession(t *testing.T) {
	backend := &fakeBackend{deleteSessionFunc: func(context.Context, string) error {
		return &api.Error{Code: "session_busy", ActiveRunID: "busy-run", Message: "Session is running. Stop the active run and its follow-up chain before deleting it"}
	}}
	m, messages, cancel := testModel(backend, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.sessions = []api.Session{{ID: "busy"}}
	m.sessionID = "busy"
	m.pickerOpen = true
	update(m, tea.KeyPressMsg{Code: tea.KeyDelete})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	updateUntil(t, m, messages, func(msg tea.Msg) bool { _, ok := msg.(sessionDeletedMsg); return ok })
	if len(m.sessions) != 1 || m.sessionID != "busy" || !m.pickerOpen || !strings.Contains(strings.Join(m.pickerRows(8), "\n"), "Resume this Session and stop its run with F1") {
		t.Fatalf("busy failure discarded session or explanation: %s", m.status)
	}
}

func TestConfirmedSessionDeletionFinishesAfterPickerCloses(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	backend := &fakeBackend{deleteSessionFunc: func(ctx context.Context, id string) error { close(entered); <-release; return ctx.Err() }}
	m, messages, cancel := testModel(backend, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.pickerOpen = true
	m.sessions = []api.Session{{ID: "delete-me"}, {ID: "keep-me"}}
	update(m, tea.KeyPressMsg{Code: tea.KeyDelete})
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	<-entered
	if !strings.Contains(m.footerControls(100), "Esc") || strings.Contains(m.footerControls(100), "cancel") {
		t.Fatal("pending deletion advertised cancellation")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	close(release)
	updateUntil(t, m, messages, func(msg tea.Msg) bool { _, ok := msg.(sessionDeletedMsg); return ok })
	if m.pickerOpen || len(m.sessions) != 1 || m.sessions[0].ID != "keep-me" || !strings.Contains(m.status, "deleted Session") {
		t.Fatal("confirmed deletion was cancelled or reopened picker")
	}
}
