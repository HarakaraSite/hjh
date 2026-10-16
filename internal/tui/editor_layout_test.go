package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

func TestEditorPaddingMatchesBackgroundAndFrameGeometry(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()

	for _, value := range []string{"", "first\n日本語", "one\ntwo\nthree\nfour\nfive\nsix"} {
		for _, size := range [][2]int{{80, 24}, {20, 12}} {
			update(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.textarea.SetValue(value)
			m.textarea, _ = m.textarea.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
			update(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			input := renderer.WrapText(m.textarea.View(), m.width)
			frame, cursor, _, _, _, body := m.screenFrame(m.footerRows())
			start := len(body) + 2 // divider and controls
			end := start + len(input) + 2
			blank := m.textarea.Styles().Focused.Base.Render(strings.Repeat(" ", m.width))
			if frame.LiveRows[start] != blank || frame.LiveRows[end-1] != blank {
				t.Fatalf("padding differs from editor background for %q: %q", value, frame.LiveRows[start:end])
			}
			for _, row := range frame.LiveRows[start:end] {
				if ansi.StringWidth(row) != m.width || !strings.Contains(row, "48;5;237m") {
					t.Fatalf("editor/padding width or background differs: %q", row)
				}
			}
			if strings.Join(frame.LiveRows[start+1:end-1], "\n") != strings.Join(input, "\n") {
				t.Fatal("padding changed textarea rendering")
			}
			if len(frame.LiveRows)-end != 2 || frame.DockRows != len(input)+6 || len(frame.LiveRows) > m.height {
				t.Fatalf("padded editor/footer height not accounted for: dock=%d rows=%d", frame.DockRows, len(frame.LiveRows))
			}
			if m.viewport.Height() != max(1, m.height-frame.DockRows) {
				t.Fatalf("resize viewport did not reserve padded dock: height=%d dock=%d", m.viewport.Height(), frame.DockRows)
			}
			relative := m.textarea.Cursor()
			if cursor == nil || relative == nil || cursor.Position.Y != start+1+relative.Position.Y || frame.Cursor.Row != cursor.Position.Y {
				t.Fatalf("cursor did not account for top padding: cursor=%+v relative=%+v", cursor, relative)
			}

			// A mutable response must use only the remaining body area; padding
			// must neither cover its newest row nor enter conversation receipts.
			answer := conversationEntity("stream", "assistant", strings.Repeat("body row ", 300)+"TAIL", 1)
			answer.Status = "streaming"
			m.sessionEntities[answer.ID] = answer
			m.invalidateTranscript()
			frame, _, _, _, _, body = m.screenFrame(m.footerRows())
			if len(body) != m.height-frame.DockRows || len(frame.LiveRows) != m.height || !strings.Contains(ansi.Strip(body[len(body)-1]), "TAIL") {
				t.Fatalf("body height did not reserve the padded dock: body=%d dock=%d height=%d", len(body), frame.DockRows, m.height)
			}
			delete(m.sessionEntities, answer.ID)
			m.invalidateTranscript()
		}
	}
}

func TestEditorDockStableDuringGenerationAndPickerReturn(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	m.headerPrinted = true
	update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.textarea.SetValue("draft\nsecond line")
	initialPanelRow := -1
	check := func() {
		t.Helper()
		frame, _, _, _, _, _ := m.screenFrame(m.footerRows())
		input := renderer.WrapText(m.textarea.View(), m.width)
		panelStart := len(frame.LiveRows) - 2 - len(input) - 2
		physicalPanelStart := m.height - len(frame.LiveRows) + panelStart
		if initialPanelRow < 0 {
			initialPanelRow = physicalPanelStart
		}
		if physicalPanelStart != initialPanelRow {
			t.Fatalf("editor panel moved: row=%d, initial=%d", physicalPanelStart, initialPanelRow)
		}
		if got := ansi.Strip(frame.LiveRows[panelStart]); got != strings.Repeat(" ", m.width) {
			t.Fatalf("top padding lost: %q", got)
		}
		if !m.pickerOpen {
			relative := m.textarea.Cursor()
			if m.height-len(frame.LiveRows)+frame.Cursor.Row != physicalPanelStart+1+relative.Position.Y {
				t.Fatal("physical cursor no longer tracks the padded editor")
			}
		}
		m.View() // Exercise the production writer and receipt advancement too.
	}
	check()
	m.activeRun = &activeRun{id: "run", inputAcceptance: api.InputAcceptance{FollowUp: true, Steering: true}}
	answer := conversationEntity("answer", "assistant", "short response", 1)
	answer.Status = "streaming"
	for _, text := range []string{"short response", strings.Repeat("growing response\n", 30)} {
		answer.Text = text
		m.sessionEntities[answer.ID] = answer
		m.invalidateTranscript()
		check()
	}
	m.openConnectionPicker(effortPicker)
	check()
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	check()
	m.openSessionPicker()
	check()
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	check()
	answer.Status = "completed"
	m.sessionEntities[answer.ID] = answer
	m.invalidateTranscript()
	m.activeRun = nil
	check()
	if m.textarea.Value() != "draft\nsecond line" || m.renderErr != nil {
		t.Fatalf("picker return lost draft or rendering failed: draft=%q err=%v", m.textarea.Value(), m.renderErr)
	}
	if strings.Contains(m.renderTranscript(), strings.Repeat(" ", m.width)) {
		t.Fatal("editor padding leaked into the conversation")
	}
}

func TestFooterOmitsNewlineHintButAltEnterStillInsertsNewline(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.width = 200
	for _, run := range []*activeRun{nil, {id: "run", inputAcceptance: api.InputAcceptance{FollowUp: true, Steering: true}}} {
		m.activeRun = run
		if controls := ansi.Strip(m.footerControls(m.width)); strings.Contains(controls, "Alt+Enter") || strings.Contains(controls, "newline") {
			t.Fatalf("footer still advertises newline: %q", controls)
		}
		m.textarea.SetValue("first")
		update(m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
		update(m, tea.PasteMsg{Content: "second"})
		if m.textarea.Value() != "first\nsecond" || m.activeRun != run {
			t.Fatalf("Alt+Enter changed submit/run behavior: draft=%q run=%+v", m.textarea.Value(), m.activeRun)
		}
	}
}

func TestEditorPaddingPickerPageMatchesVisibleChoices(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	update(m, tea.WindowSizeMsg{Width: 80, Height: 12})
	m.pickerOpen = true
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		m.sessions = append(m.sessions, api.Session{ID: id})
	}
	frame, _, _, _, _, _ := m.screenFrame(m.footerRows())
	visibleChoices := 0
	for _, row := range frame.LiveRows {
		plain := ansi.Strip(row)
		if strings.HasPrefix(plain, "> ") || strings.HasPrefix(plain, "  ") && strings.TrimSpace(plain) != "" {
			visibleChoices++
		}
	}
	visibleChoices-- // textarea prompt is not a picker choice
	if visibleChoices != 3 || m.pickerPageSize() != visibleChoices {
		t.Fatalf("paging height differs from visible menu: visible=%d page=%d", visibleChoices, m.pickerPageSize())
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.pickerIndex != visibleChoices {
		t.Fatalf("Page Down skipped choices: index=%d visible=%d", m.pickerIndex, visibleChoices)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.pickerIndex != 0 {
		t.Fatalf("Page Up did not return to first page: index=%d", m.pickerIndex)
	}
}
