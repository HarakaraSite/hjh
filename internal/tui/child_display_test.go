package tui

import (
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

func TestChildDetailIsHiddenButRetainedForInspection(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	for i, kind := range []string{"task", "thinking", "tool", "assistant", "steering"} {
		e := conversationEntity("child-"+kind, kind, "child detail "+kind, int64(i+1))
		e.ParentExecutionID = "root"
		e.Name, e.Preview = "read_file", "hidden-child-path"
		m.sessionEntities[e.ID] = e
	}
	parent := conversationEntity("parent", "assistant", "親が取り上げた子の報告", 6)
	parent.Status = "completed"
	m.sessionEntities[parent.ID] = parent
	for _, transcript := range []string{m.renderTranscript(), m.ensureTranscript().styled} {
		plain := renderer.ReceiptText(transcript)
		if strings.Contains(plain, "child detail") || strings.Contains(plain, "hidden-child-path") || !strings.Contains(plain, parent.Text) {
			t.Fatalf("child detail leaked or parent report was lost: %q", plain)
		}
	}
	if len(m.mergedEntities()) != 6 {
		t.Fatal("presentation filter removed stored entities from inspection")
	}
	for _, e := range m.mergedEntities() {
		if e.ParentExecutionID != "" && e.Text == "" {
			t.Fatal("hidden child detail was modified")
		}
	}
}

func TestChildStartAndTerminalArePrintedOnceWithoutBlockingParent(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	child := conversationEntity("child", "execution", "", 1)
	child.ExecutionID, child.ParentExecutionID = "109423a3-ed00-4757-8a0a-70beb5620809", "root"
	child.Status = "streaming"
	m.sessionEntities[child.ID] = child
	m.View()
	child.Status, child.Outcome = "completed", "final"
	m.sessionEntities[child.ID] = child
	parent := conversationEntity("parent", "assistant", "parent final", 2)
	parent.Status = "completed"
	m.sessionEntities[parent.ID] = parent
	m.invalidateTranscript()
	m.View()
	m.View()
	plain := renderer.ReceiptText(output.String())
	for _, line := range []string{"child 109423a3 · started", "child 109423a3 · completed", parent.Text} {
		if strings.Count(plain, line) != 1 {
			t.Fatalf("lifecycle/answer printed incorrectly for %q: %q", line, plain)
		}
	}
	if strings.Contains(plain, child.ExecutionID) {
		t.Fatal("human-facing child lifecycle used the full execution ID")
	}
	full := renderer.ReceiptText(m.renderFullTranscript())
	if !strings.Contains(full, "child 109423a3 · completed") || !strings.Contains(full, parent.Text) {
		t.Fatalf("restored child lifecycle missing: %q", full)
	}
}

func TestChildProgressShowsOnlyToolNameAndRemainsOutsideReceipts(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	child := conversationEntity("child", "execution", "", 1)
	child.ExecutionID, child.ParentExecutionID, child.Status = "109423a3-1234", "root", "streaming"
	tool := conversationEntity("child-tool", "tool", "hidden tool result", 2)
	tool.ExecutionID, tool.ParentExecutionID = child.ExecutionID, "root"
	tool.Name, tool.Preview, tool.Status = "read_file", "hidden-tool-argument", "streaming"
	other := conversationEntity("other-child", "execution", "", 3)
	other.ExecutionID, other.ParentExecutionID, other.Status = "5a5c173d-1234", "root", "streaming"
	m.activeRun = &activeRun{entities: map[string]api.ConversationEntity{child.ID: child, tool.ID: tool, other.ID: other}}
	rows := m.childProgressRows()
	if len(rows) != 2 || renderer.ReceiptText(rows[0]) != "child 109423a3 · working · read_file" || renderer.ReceiptText(rows[1]) != "child 5a5c173d · working" {
		t.Fatalf("unexpected child progress: %q", rows)
	}
	frame, _, _, _, _, _ := m.screenFrame(m.footerRows())
	if !strings.Contains(renderer.ReceiptText(strings.Join(frame.LiveRows, "\n")), "working · read_file") ||
		strings.Contains(renderer.ReceiptText(strings.Join(frame.CommittedRows, "\n")), "working") {
		t.Fatal("child progress was not confined to the live band")
	}
	if strings.Contains(m.ensureTranscript().plain, tool.Preview) || strings.Contains(m.ensureTranscript().plain, tool.Text) {
		t.Fatal("child tool detail reached the transcript")
	}
	tool.Status = "completed"
	m.activeRun.entities[tool.ID] = tool
	if row := renderer.ReceiptText(m.childProgressRows()[0]); strings.Contains(row, "read_file") {
		t.Fatalf("completed tool still shown as current work: %q", row)
	}
	child.Status = "completed"
	m.activeRun.entities[child.ID] = child
	if rows := m.childProgressRows(); len(rows) != 1 || renderer.ReceiptText(rows[0]) != "child 5a5c173d · working" {
		t.Fatalf("settling one child removed the other child's progress: %q", rows)
	}
	other.Status = "completed"
	m.activeRun.entities[other.ID] = other
	if len(m.childProgressRows()) != 0 {
		t.Fatal("settled child remained in the live progress band")
	}
}

func TestChildFailureKeepsItsReasonVisible(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	entity := api.ConversationEntity{Kind: "execution", ExecutionID: "child-12345678", ParentExecutionID: "root", Status: "incomplete", Error: "provider failed\nupstream unavailable"}
	lines := m.childSummaryLines(entity)
	line := childSummaryRow(lines[1], 80)
	if !strings.Contains(line, redSGR) || !strings.Contains(renderer.ReceiptText(line), "failed · provider failed upstream unavailable") {
		t.Fatalf("child failure lost its visible reason: %q", line)
	}
}
