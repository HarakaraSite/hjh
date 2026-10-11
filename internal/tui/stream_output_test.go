package tui

import (
	"hjh/internal/api"
	"hjh/internal/tui/renderer"
	"strings"
	"testing"
)

// The product regression was a streamed README summary: paragraph spacing,
// generated table separators, and rendered URLs changed a glyph-derived receipt.
func TestStreamedMarkdownKeepsTheSameConversationAsFinalRendering(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1", Workspace: "/work"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	source := "READMEを読みました。要約します。\n\n## 技術構成\n\n" +
		"- 本体: Go。会話と履歴を管理します。\n- 接続: 複数のproviderを利用します。\n\n" +
		"| 段階 | 内容 |\n| --- | --- |\n| I-05 | コアとTUIの分離 |\n| I-06 | 会話画面 |\n\n" +
		"```sh\ngo build -o dist/hjh ./cmd/hjh\ngo build -o dist/hjh-tui ./cmd/hjh-tui\n```\n\n" +
		"[現行ソース](https://forge.harakara.site/littleisland/henji-harness) と " +
		"[正本](docs/design/initial-product.md) を参照します。\n\n" +
		strings.Repeat("日本語の段落を途中で分断せずに読む。", 30) + "\n\n最後の応答です。"
	entity := conversationEntity("summary", "assistant", "", 1)
	var committed []string
	render := func() {
		m.sessionEntities[entity.ID] = entity
		m.invalidateTranscript()
		doc := m.ensureTranscript()
		start := doc.startRow
		m.View()
		committed = append(committed, doc.rows[start:doc.startRow]...)
	}
	runes := []rune(source)
	for i := 0; i < len(runes); i += 8 {
		entity.Text = string(runes[:min(len(runes), i+8)])
		render()
	}
	entity.Status = "completed"
	render()
	m.sealCurrentFrame()
	committed = append(committed, m.pendingSealRows...)
	expected := renderer.WrapText(m.renderFullTranscript(), m.width)
	normalize := func(rows []string) string {
		normalized := make([]string, len(rows))
		for i, row := range rows {
			normalized[i] = renderer.ReceiptText(row)
		}
		return strings.Join(normalized, "\n")
	}
	if got, want := normalize(committed), normalize(expected); got != want {
		t.Fatalf("streamed conversation differs from its completed rendering:\n--- emitted ---\n%s\n--- expected ---\n%s", got, want)
	}
}

func TestToolCompletionDoesNotRepeatOrDelayChildAndFinalAnswer(t *testing.T) {
	b := &fakeBackend{core: api.Core{CoreID: "core-1", Workspace: "/work"}}
	m, _, cancel := testModel(b, Options{Core: b.core})
	attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	tool := conversationEntity("pending-tool", "tool", "", 1)
	tool.Name = "run_js"
	tool.Preview = "JavaScript"
	child := conversationEntity("child-body", "assistant", strings.Repeat("子の回答です。\n\n", 30), 2)
	child.ParentExecutionID = "root-1"
	child.Status = "completed"
	final := conversationEntity("final-body", "assistant", "親の最終回答です。", 3)
	final.Status = "completed"
	var committed []string
	collect := func() {
		doc := m.ensureTranscript()
		start := doc.startRow
		m.View()
		committed = append(committed, doc.rows[start:doc.startRow]...)
	}
	m.sessionEntities[tool.ID], m.sessionEntities[child.ID] = tool, child
	collect()
	if got := m.receiptByPart["entity:"+child.ID+":body"]; got != len(child.Text) {
		t.Fatalf("compact pending tool prevented completed child text from reaching scrollback: %d", got)
	}
	tool.Text = "ツールの確認結果です。"
	tool.Status = "completed"
	m.sessionEntities[tool.ID] = tool
	m.sessionEntities[final.ID] = final
	m.invalidateTranscript()
	collect()
	m.sealCurrentFrame()
	committed = append(committed, m.pendingSealRows...)
	text := renderer.ReceiptText(strings.Join(committed, "\n"))
	toolPos, childPos, finalPos := strings.Index(text, "run_js"), strings.Index(text, "子の回答です。"), strings.Index(text, final.Text)
	if toolPos < 0 || childPos <= toolPos || finalPos <= childPos {
		t.Fatalf("conversation order was lost after the pending tool completed:\n%s", text)
	}
	if strings.Count(text, "run_js") != 1 || strings.Contains(text, tool.Text) {
		t.Fatalf("tool completion repeated its activity or copied its full result into conversation:\n%s", text)
	}
	if strings.Count(text, "子の回答です。") != 30 {
		t.Fatalf("hidden child body was lost or duplicated: %d occurrences", strings.Count(text, "子の回答です。"))
	}
}

func TestFinishedConversationSpacingDoesNotDependOnUpdates(t *testing.T) {
	b := &fakeBackend{core: api.Core{CoreID: "core-1", Workspace: "/work"}}
	m, _, cancel := testModel(b, Options{Core: b.core})
	attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	var committed []string
	render := func(entity api.ConversationEntity) {
		m.sessionEntities[entity.ID] = entity
		m.invalidateTranscript()
		doc := m.ensureTranscript()
		start := doc.startRow
		m.View()
		committed = append(committed, doc.rows[start:doc.startRow]...)
	}
	for i, kind := range []string{"assistant", "tool", "assistant"} {
		entity := conversationEntity(kind+strings.Repeat("-", i), kind, "", int64(i+1))
		source := "作業を確認します。\n\n[正本](docs/design/initial-product.md)を参照します。"
		if kind == "tool" {
			entity.Name = "read_file"
			// Tool activity keeps its first printed state; that transition is
			// covered separately. Here compare only conversation/body spacing.
			entity.Status = "completed"
			entity.Outcome = "success"
			source = "ツールの結果です。"
		}
		render(entity)
		runes := []rune(source)
		for n := 0; n < len(runes); n += 8 {
			entity.Text = string(runes[:min(n+8, len(runes))])
			render(entity)
		}
		entity.Status = "completed"
		if kind == "tool" {
			entity.Outcome = "success"
		}
		render(entity)
	}
	m.sealCurrentFrame()
	committed = append(committed, m.pendingSealRows...)
	got := renderer.ReceiptText(strings.Join(committed, "\n"))
	expected := renderer.WrapText(m.renderFullTranscript(), m.width)
	for i, row := range expected {
		expected[i] = renderer.ReceiptText(row)
	}
	if want := strings.Join(expected, "\n"); got != want {
		t.Fatalf("completed conversation spacing changed with streamed updates:\n--- emitted ---\n%s\n--- expected ---\n%s", got, want)
	}
}

func TestLongMutableParagraphShowsNewlyReceivedText(t *testing.T) {
	b := &fakeBackend{core: api.Core{CoreID: "core-1", Workspace: "/work"}}
	m, _, cancel := testModel(b, Options{Core: b.core})
	attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	entity := conversationEntity("long-preview", "assistant", strings.Repeat("生成中の本文です。", 300), 1)
	m.sessionEntities[entity.ID] = entity
	m.View()
	entity.Text += "最新の生成箇所"
	m.sessionEntities[entity.ID] = entity
	m.invalidateTranscript()
	m.View()
	if !strings.Contains(renderer.ReceiptText(strings.Join(m.lastBodyRows, "\n")), "最新の生成箇所") {
		t.Fatal("a long mutable paragraph stops showing newly received text")
	}
	if got := m.receiptByPart["entity:"+entity.ID+":body"]; got != 0 {
		t.Fatalf("mutable preview was permanently committed at source offset %d", got)
	}
}
