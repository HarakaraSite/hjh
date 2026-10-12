package tui

import (
	"strings"
	"testing"

	"hjh/internal/tui/renderer"
)

func TestReadyHeaderAndLongRestoredAnswerCommitInOneFrame(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{SessionID: "saved"})
	defer func() { cancel(); m.shutdown() }()
	m.width, m.height = 48, 20
	m.layout()
	m.deferTranscriptUntilHeader = true
	m.headerOrientationPending = true
	answer := conversationEntity("saved-answer", "assistant", "## 見出し\n\n"+strings.Repeat("復元した本文です。\n\n", 60)+"\n最終回答の末尾", 1)
	answer.Status = "completed"
	m.sessionEntities[answer.ID] = answer
	out := attachWriter(m)
	m.View()
	if strings.Contains(out.String(), "復元した本文") {
		t.Fatal("restored content escaped into the terminal before its opening header")
	}
	m.headerOrientationPending = false
	out.Reset()
	m.View()
	printed := renderer.ReceiptText(out.String())
	if strings.Count(printed, "復元した本文です。") != 60 || !strings.Contains(printed, "最終回答の末尾") || strings.Index(printed, "hjh v") > strings.Index(printed, "復元した本文") {
		t.Fatalf("opening did not print the complete answer after its header: %q", printed)
	}
	if m.receiptByPart["entity:"+answer.ID+":body"] != len(answer.Text) {
		t.Fatal("opening frame did not acknowledge original source bytes")
	}
	out.Reset()
	m.View()
	if strings.Contains(out.String(), "復元した本文") {
		t.Fatal("subsequent footer rendering duplicated the restored answer")
	}
}
