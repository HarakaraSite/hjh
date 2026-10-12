package tui

import (
	"strings"
	"testing"

	"hjh/internal/tui/renderer"
)

func TestWhitespaceOnlyAssistantDoesNotPrintLabelOrDelayTool(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	// Observed in 72dc9c7b: an assistant response contains only two newlines
	// while its useful action is a tool call.
	assistant := conversationEntity("blank-assistant", "assistant", "\n\n", 1)
	tool := conversationEntity("tool", "tool", "", 2)
	tool.Name = "read_file"
	m.sessionEntities[assistant.ID], m.sessionEntities[tool.ID] = assistant, tool
	m.View()
	printed := renderer.ReceiptText(strings.ReplaceAll(output.String(), "\r", ""))
	if strings.Contains(printed, "assistant:") || !strings.Contains(printed, "tool / read_file") {
		t.Fatalf("blank response printed a label or blocked its tool: %q", printed)
	}
	if strings.Contains(renderer.ReceiptText(m.renderTranscript()), "assistant:") || m.sessionEntities[assistant.ID].Text != "\n\n" {
		t.Fatal("restored display included the blank response or altered the original entity")
	}
}

func TestAssistantAppearsWhenStreamingWhitespaceBecomesText(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	assistant := conversationEntity("answer", "assistant", " \n\t\n", 1)
	m.sessionEntities[assistant.ID] = assistant
	m.View()
	if strings.Contains(renderer.ReceiptText(output.String()), "assistant:") {
		t.Fatal("leading streaming whitespace printed an assistant label")
	}
	assistant.Text = "\n\n最初の段落\n\n次の段落"
	assistant.Status = "completed"
	m.sessionEntities[assistant.ID] = assistant
	m.invalidateTranscript()
	output.Reset()
	m.View()
	printed := renderer.ReceiptText(strings.ReplaceAll(output.String(), "\r", ""))
	if strings.Count(printed, "assistant:") != 1 || !strings.Contains(printed, "最初の段落\n\n次の段落") {
		t.Fatalf("visible answer or paragraph spacing was lost: %q", printed)
	}
	if m.markdownSourceCache["entity:answer:body"].source != assistant.Text || m.sessionEntities[assistant.ID].Text != assistant.Text {
		t.Fatal("display filter changed the original answer or source receipt")
	}
}
