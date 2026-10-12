package tui

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/tui/renderer"
)

type cursorReportOutput struct{ bytes.Buffer }

func (*cursorReportOutput) Fd() uintptr { return 1 }

func TestResizeCursorReplyDoesNotPrematurelyAcknowledgeConversation(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	var output cursorReportOutput
	m.writer = renderer.New(&output)
	m.headerPrinted = true
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	m.View()
	update(m, tea.WindowSizeMsg{Width: 60, Height: 18})
	answer := conversationEntity("new-answer", "assistant", "new saved answer after resize", 1)
	answer.Status = "completed"
	m.sessionEntities[answer.ID] = answer
	m.invalidateTranscript()
	output.Reset()
	m.View()
	if output.String() != ansi.RequestCursorPositionReport || m.renderErr != nil || m.receiptByPart["entity:new-answer:body"] != 0 {
		t.Fatalf("pending resize erased, failed, or acknowledged unseen content: output=%q err=%v", output.String(), m.renderErr)
	}
	update(m, tea.CursorPositionMsg{X: 2, Y: 17})
	m.View()
	if m.renderErr != nil || m.receiptByPart["entity:new-answer:body"] != len(answer.Text) || strings.Count(renderer.ReceiptText(output.String()), answer.Text) != 1 {
		t.Fatalf("cursor reply did not commit pending conversation exactly once: output=%q err=%v", output.String(), m.renderErr)
	}
}
