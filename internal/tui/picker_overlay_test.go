package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"hjh/internal/tui/renderer"
)

func TestInlinePickerDisplaysCompletedConversationWithoutScreenSwitch(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	m.width = 100
	m.layout()
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
	output.Reset()
	m.View()
	if strings.Contains(output.String(), ansi.SetMode(ansi.ModeAltScreenSaveCursor)) || strings.Count(renderer.ReceiptText(output.String()), answer.Text) != 1 || len(m.pendingSealRows) != 0 {
		t.Fatalf("inline picker did not print parent exactly once: %q", output.String())
	}
	m.closeConnectionPicker()
	output.Reset()
	m.View()
	if strings.Contains(renderer.ReceiptText(output.String()), answer.Text) {
		t.Fatal("closing inline picker repeated completed response")
	}
}

func TestAuthenticationOverlayKeepsConversationPending(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	m.width = 100
	m.layout()
	answer := conversationEntity("answer", "assistant", "ongoing response", 1)
	m.sessionEntities[answer.ID] = answer
	m.View()
	m.pickerOpen = true
	m.authUI = &authUIState{open: true, screen: authProfilesScreen}
	answer.Text = "completed parent while authenticating"
	answer.Status = "completed"
	m.sessionEntities[answer.ID] = answer
	m.invalidateTranscript()
	m.ensureTranscript()
	m.sealCurrentFrame()
	output.Reset()
	m.View()
	if !strings.Contains(output.String(), ansi.SetMode(ansi.ModeAltScreenSaveCursor)) || strings.Contains(renderer.ReceiptText(output.String()), answer.Text) || len(m.pendingSealRows) == 0 {
		t.Fatal("auth overlay consumed conversation")
	}
	m.authUI.open = false
	m.pickerOpen = false
	output.Reset()
	m.View()
	if !strings.Contains(output.String(), ansi.ResetMode(ansi.ModeAltScreenSaveCursor)) || strings.Count(renderer.ReceiptText(output.String()), answer.Text) != 1 || len(m.pendingSealRows) != 0 {
		t.Fatal("auth close lost or repeated response")
	}
}
