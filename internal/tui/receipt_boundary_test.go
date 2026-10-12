package tui

import (
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

func TestResizeDoesNotAcknowledgeLaterAnswerPastMutableParent(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()

	earlier := conversationEntity("earlier-body", "assistant", "**mutable earlier preview**", 1)
	earlier.Status = "streaming"
	parent := conversationEntity("parent-body", "assistant", "親の確定回答", 2)
	parent.Status = "completed"
	m.sessionEntities[earlier.ID], m.sessionEntities[parent.ID] = earlier, parent
	m.View()
	preview := renderer.ReceiptText(strings.Join(m.lastBodyRows, "\n"))
	if !strings.Contains(preview, "mutable earlier preview") {
		t.Fatal("the live body omitted the first mutable earlier block")
	}
	if strings.Contains(preview, parent.Text) {
		t.Fatal("a later entity displaced the first mutable block in the live preview")
	}
	m.sealCurrentFrame()
	if got := m.receiptByPart["entity:parent-body:body"]; got != 0 {
		t.Fatalf("later answer acknowledged past mutable earlier at offset %d", got)
	}

	earlier.Text += "**"
	earlier.Status = "completed"
	m.sessionEntities[earlier.ID] = earlier
	m.invalidateTranscript()
	output.Reset()
	m.View()
	if !strings.Contains(renderer.ReceiptText(output.String()), parent.Text) {
		t.Fatal("parent answer disappeared when the preceding earlier finished")
	}
}

func TestPendingToolAndHiddenChildDoNotDelayParentCommit(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()

	tool := conversationEntity("pending-tool", "tool", "", 1)
	tool.Name = "run_js"
	child := conversationEntity("completed-child", "assistant", "completed child output stays visible", 2)
	child.ParentExecutionID = "root"
	child.Status = "completed"
	final := conversationEntity("parent-final", "assistant", "parent final answer", 3)
	final.Status = "completed"
	m.sessionEntities[tool.ID] = tool
	m.sessionEntities[child.ID] = child
	m.sessionEntities[final.ID] = final

	m.View()
	committed := renderer.ReceiptText(output.String())
	if strings.Contains(committed, child.Text) || !strings.Contains(committed, final.Text) {
		t.Fatalf("hidden child was displayed or parent output was delayed: %q", committed)
	}
	if got := m.receiptByPart["entity:"+final.ID+":body"]; got != len(final.Text) {
		t.Fatalf("parent output was not committed while the tool was pending: offset=%d want=%d", got, len(final.Text))
	}
}
