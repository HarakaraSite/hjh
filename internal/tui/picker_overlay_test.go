package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"hjh/internal/tui/renderer"
)

func TestPickerKeepsNewConversationPendingForNormalScreen(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	answer := conversationEntity("answer", "assistant", "ongoing response", 1)
	m.sessionEntities[answer.ID] = answer
	m.View()
	m.pickerOpen = true
	m.connectionPicker = &connectionPickerState{kind: effortPicker}
	answer.Text = "completed parent answer while choosing effort"
	answer.Status = "completed"
	m.sessionEntities[answer.ID] = answer
	m.invalidateTranscript()
	m.ensureTranscript()
	m.sealCurrentFrame()
	if len(m.pendingSealRows) == 0 {
		t.Fatal("completed parent response did not produce pending sealed rows")
	}
	output.Reset()
	m.View()
	if !strings.Contains(output.String(), ansi.SetAltScreenSaveCursorMode) || strings.Contains(renderer.ReceiptText(output.String()), answer.Text) || len(m.pendingSealRows) == 0 {
		t.Fatalf("picker consumed or displayed the pending conversation: %q", output.String())
	}
	m.pickerOpen = false
	m.connectionPicker = nil
	output.Reset()
	m.View()
	text := renderer.ReceiptText(output.String())
	if !strings.Contains(output.String(), ansi.ResetAltScreenSaveCursorMode) || strings.Count(text, answer.Text) != 1 || len(m.pendingSealRows) != 0 {
		t.Fatalf("returning to normal screen lost/repeated the completed response: %q", output.String())
	}
	if m.sessionEntities[answer.ID].Text != answer.Text {
		t.Fatal("picker changed the saved conversation projection")
	}
}
