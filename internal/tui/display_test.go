package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

func TestToolProjectionIsCompactOnceAndReopenShowsOutcome(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()

	tool := conversationEntity("tool-call", "tool", "", 1)
	tool.Name = "run_js"
	tool.Preview = "node script.js"
	tool.Detail = &api.ConversationDetailLocator{
		ExecutionID: "root-1", RecordID: "record-1", Ordinal: 7, CallID: "call-1",
	}
	m.sessionEntities[tool.ID] = tool
	m.View()
	firstDisplay := renderer.ReceiptText(output.String())
	if strings.Count(firstDisplay, "tool / run_js") != 1 || !strings.Contains(firstDisplay, "node script.js …") {
		t.Fatalf("initial tool activity row was not displayed exactly once: %q", firstDisplay)
	}

	tool.Text = "FULL TOOL RESULT THAT BELONGS IN /tool"
	tool.Status = "completed"
	tool.Outcome = "success"
	m.sessionEntities[tool.ID] = tool
	m.invalidateTranscript()
	output.Reset()
	m.View()
	completionDisplay := renderer.ReceiptText(output.String())
	if strings.Contains(completionDisplay, "tool / run_js") || strings.Contains(completionDisplay, tool.Text) {
		t.Fatalf("tool completion re-emitted its row or full result: %q", completionDisplay)
	}

	// A newly opened Session starts with no display receipt, so it presents the
	// saved completion state in the same compact row once.
	reopened, _, reopenCancel := testModel(backend, Options{Core: backend.core})
	defer func() { reopenCancel(); reopened.shutdown() }()
	reopened.sessionEntities[tool.ID] = tool
	transcript := renderer.ReceiptText(reopened.renderTranscript())
	if strings.Count(transcript, "tool / run_js") != 1 || !strings.Contains(transcript, "node script.js ✓") {
		t.Fatalf("reopened completed tool did not show its saved state: %q", transcript)
	}
	if strings.Contains(transcript, tool.Text) {
		t.Fatalf("reopened conversation copied the full tool result into the transcript: %q", transcript)
	}

	failed := tool
	failed.ID = "failed-tool"
	failed.Name = "write_file"
	failed.Outcome = "failed"
	failed.Error = "write failed"
	reopened.sessionEntities[failed.ID] = failed
	reopened.invalidateTranscript()
	transcript = renderer.ReceiptText(reopened.renderTranscript())
	if !strings.Contains(transcript, "tool / write_file · node script.js ✗") {
		t.Fatalf("reopened failed tool did not show its saved failure state: %q", transcript)
	}
}

func TestEmptyAssistantEntityIsHiddenUntilItHasText(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()

	assistant := conversationEntity("assistant", "assistant", "", 1)
	m.sessionEntities[assistant.ID] = assistant
	if transcript := renderer.ReceiptText(m.renderTranscript()); strings.Contains(transcript, "assistant:") {
		t.Fatalf("empty assistant entity created a visible row: %q", transcript)
	}

	assistant.Text = "answer arrives"
	m.sessionEntities[assistant.ID] = assistant
	m.invalidateTranscript()
	transcript := renderer.ReceiptText(m.renderTranscript())
	if !strings.Contains(transcript, "assistant:\nanswer arrives") {
		t.Fatalf("assistant label/body did not appear after text arrived: %q", transcript)
	}
}

func TestThinkingLabelsDistinguishTextFromSummary(t *testing.T) {
	if label := entityLabel(api.ConversationEntity{Kind: "thinking", ThinkingKind: "reasoning"}); label != "thinking" {
		t.Fatalf("readable thinking label = %q", label)
	}
	if label := entityLabel(api.ConversationEntity{Kind: "thinking", ThinkingKind: "summary"}); label != "thinking summary" {
		t.Fatalf("thinking summary label = %q", label)
	}
}

func TestRealEditorCursorTracksTextAndFooterLayout(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	conversation := conversationEntity("saved-answer", "assistant", "会話本文", 1)
	conversation.Status = "completed"
	m.sessionEntities[conversation.ID] = conversation
	if m.textarea.VirtualCursor() {
		t.Fatal("textarea still uses a virtual cursor instead of terminal cursor position")
	}
	if m.textarea.Placeholder != "" {
		t.Fatal("input still displays an unwanted placeholder")
	}

	checkFrame := func(value string, width, height int, wantX int) {
		t.Helper()
		m.width, m.height = width, height
		m.layout()
		m.textarea.SetValue(value)
		m.textarea, _ = m.textarea.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
		textareaCursor := m.textarea.Cursor()
		if textareaCursor == nil {
			t.Fatal("textarea did not calculate its focused cursor position")
		}
		frame, cursor, _, _, _, bodyRows := m.screenFrame(m.footerRows())
		if !strings.Contains(renderer.ReceiptText(strings.Join(frame.CommittedRows, "\n")), conversation.Text) {
			t.Fatalf("conversation body was not placed before the editor: committed=%v", frame.CommittedRows)
		}
		if cursor == nil {
			t.Fatal("focused editor did not publish a real terminal cursor")
		}
		inputRows := renderer.WrapText(m.textarea.View(), width)
		footerRows := m.footerRows()[1:]
		helpStart := len(bodyRows) + 1 // divider then controls/notice
		editorStart := helpStart + 1
		footerStart := editorStart + len(inputRows)
		if footerStart > len(frame.LiveRows) || len(frame.LiveRows)-footerStart != len(footerRows) {
			t.Fatalf("footer did not follow the editor controls: rows=%v", frame.LiveRows)
		}
		actualHelp := renderer.ReceiptText(frame.LiveRows[helpStart])
		actualFooter := strings.ReplaceAll(renderer.ReceiptText(strings.Join(frame.LiveRows[footerStart:], "\n")), "\n", "")
		if !strings.Contains(actualHelp, "Enter send") || !strings.Contains(actualFooter, "ready") {
			t.Fatalf("controls above or metadata below the editor misplaced: rows=%v", frame.LiveRows)
		}
		if cursor.Position.X != wantX || frame.Cursor.Cell != wantX {
			t.Fatalf("cursor x for %q = %d/frame %d, want %d", value, cursor.Position.X, frame.Cursor.Cell, wantX)
		}
		wantY := editorStart + textareaCursor.Position.Y
		if textareaCursor.Position.Y < 0 || textareaCursor.Position.Y >= len(inputRows) ||
			cursor.Position.Y != wantY || frame.Cursor.Row != wantY || frame.Cursor.Row >= footerStart || frame.Cursor.Row <= helpStart {
			t.Fatalf("cursor for %q was outside the editor viewport: cursor=%+v frame=%+v rows=%v", value, cursor, frame.Cursor, frame.LiveRows)
		}
	}

	checkFrame("日本語", 20, 18, 8)
	checkFrame("first\n日本語", 20, 18, 8)
	checkFrame("123456789012345", 10, 18, 9)
	checkFrame("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\n日本語", 20, 10, 8)

	// The viewport has moved to keep the last line visible while the editor stays
	// below controls and above the metadata footer.
	m.width, m.height = 20, 10
	m.layout()
	m.textarea.SetValue("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\n日本語")
	m.textarea, _ = m.textarea.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	textareaCursor := m.textarea.Cursor()
	frame, cursor, content, _, _, bodyRows := m.screenFrame(m.footerRows())
	content = renderer.ReceiptText(content)
	if textareaCursor == nil || cursor.Position.Y != len(bodyRows)+2+textareaCursor.Position.Y {
		t.Fatalf("scrolled editor cursor row=%d, relative cursor=%v", cursor.Position.Y, textareaCursor)
	}
	if strings.Index(content, "Enter send") > strings.Index(content, "line 6") ||
		strings.Index(content, "ready") < strings.Index(content, "line 6") || cursor.Position.Y >= len(frame.LiveRows)-2 {
		t.Fatalf("input, controls, footer, and cursor are not ordered in the live frame: %q", content)
	}
}
