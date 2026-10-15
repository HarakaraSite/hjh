package tui

import (
	"strings"
	"testing"

	"hjh/internal/tui/renderer"
)

func TestRetrySeparatesReceiptFromFailedBodyAndReloadHidesIt(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	failed := conversationEntity("first-attempt", "assistant", "FAILED_PREFIX\n\nfailed trailing paragraph", 1)
	failed.Status = "streaming"
	thinking := conversationEntity("first-thinking", "thinking", "FAILED_THINKING", 2)
	thinking.Status = "streaming"
	m.sessionEntities[failed.ID], m.sessionEntities[thinking.ID] = failed, thinking
	m.View()
	failed.Status, thinking.Status = "superseded", "superseded"
	m.sessionEntities[failed.ID], m.sessionEntities[thinking.ID] = failed, thinking
	notice := conversationEntity("retry-notice", "notice", "RETRYING · provider response invalid · 1/1 · 2s", 3)
	notice.Name, notice.Status = "provider_retry", "completed"
	m.sessionEntities[notice.ID] = notice
	m.invalidateTranscript()
	m.View()
	good := conversationEntity("retry-attempt", "assistant", "NEW_COMPLETE_RESPONSE", 4)
	good.Status = "completed"
	m.sessionEntities[good.ID] = good
	m.invalidateTranscript()
	m.View()
	m.View()
	plain := renderer.ReceiptText(output.String())
	if strings.Count(plain, good.Text) != 1 || strings.Count(plain, notice.Text) != 1 {
		t.Fatalf("retry body lost or merged with old receipt: %q", plain)
	}
	for _, rendered := range []string{m.renderFullTranscript(), m.renderTranscript()} {
		plain := renderer.ReceiptText(rendered)
		if strings.Contains(plain, "FAILED_") || !strings.Contains(plain, good.Text) || !strings.Contains(plain, notice.Text) {
			t.Fatalf("reloaded/displayed retry includes failed attempt: %q", plain)
		}
	}
	if strings.Contains(renderer.ReceiptText(m.ensureTranscript().styled), "FAILED_") {
		t.Fatal("failed attempt remained in the pending live display")
	}
	if len(m.mergedEntities()) != 4 {
		t.Fatal("presentation removed failed attempt from inspection")
	}
}
