package tui

import (
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/tui/renderer"
	"strings"
	"testing"
)

func TestMarkdownBlocksKeepSourceStartsForHeadingListAndFence(t *testing.T) {
	source := "最初の段落です。\n\n## 見出し\n\n- 項目A\n- 項目B\n\n```go\nfmt.Println(\"ok\")\n```\n"
	blocks := markdownBlocks(source, func(block string) string { return block })
	if len(blocks) < 4 {
		t.Fatalf("Markdown block boundaries = %d, want paragraph, heading, list, and code", len(blocks))
	}
	var reconstructed strings.Builder
	for _, block := range blocks {
		reconstructed.WriteString(source[block.start:block.end])
	}
	if reconstructed.String() != source {
		t.Fatalf("source ranges did not cover Markdown input exactly:\n got %q\nwant %q", reconstructed.String(), source)
	}
	for _, marker := range []string{"## 見出し", "- 項目A", "```go"} {
		markerStart := strings.Index(source, marker)
		found := false
		for _, block := range blocks {
			if block.start <= markerStart && markerStart < block.end {
				if block.start != markerStart {
					t.Fatalf("block containing %q starts at %d, want syntax start %d", marker, block.start, markerStart)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no source block contains Markdown syntax %q", marker)
		}
	}
}

func TestGlamourFormatsAnswerWithoutChangingSource(t *testing.T) {
	m := newModel(t.Context(), nil, nil, Options{})
	defer m.shutdown()
	source := "本文\n\n## 見出し\n\n- 項目A\n- 項目B\n\n```go\nfmt.Println(\"ok\")\n```\n\n**強調**と[link](https://example.test)\n"
	blocks, _ := m.markdownBlocksForSource("answer", source)
	var styled strings.Builder
	var reconstructed strings.Builder
	for _, block := range blocks {
		styled.WriteString(block.render)
		reconstructed.WriteString(source[block.start:block.end])
	}
	plain := renderer.ReceiptText(styled.String())
	for _, text := range []string{"見出し", "• 項目A", "• 項目B", "fmt.Println(\"ok\")", "強調", "https://example.test"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("formatted answer omitted %q: %q", text, plain)
		}
	}
	if strings.Contains(plain, "## 見出し") || strings.Contains(plain, "**強調**") || strings.Contains(plain, "```go") || !strings.Contains(styled.String(), "\x1b[") {
		t.Fatalf("answer was not formatted/colored: %q", styled.String())
	}
	if reconstructed.String() != source || m.markdownSourceCache["answer"].source != source {
		t.Fatal("Glamour changed the original Markdown source")
	}
	for _, width := range []int{60, 30} {
		m.width = width
		blocks, _ := m.markdownBlocksForSource("answer", source)
		for _, block := range blocks {
			for _, row := range renderer.WrapText(block.render, width) {
				if ansi.StringWidth(row) > width {
					t.Fatalf("answer exceeds terminal width: %q", row)
				}
			}
		}
	}
}

func TestGlamourRendersTableAndRefreshesWidthCache(t *testing.T) {
	m := newModel(t.Context(), nil, nil, Options{})
	defer m.shutdown()
	source := "| ファイル | 内容 |\n| --- | --- |\n| README.md | 日本語🐈を含む説明です。 |\n"
	m.width = 64
	wide, _ := m.markdownBlocksForSource("table", source)
	if len(wide) != 1 {
		t.Fatalf("table was split before rendering: %d blocks", len(wide))
	}
	wideText := wide[0].render
	m.width = 32
	narrow, _ := m.markdownBlocksForSource("table", source)
	if wideText == narrow[0].render {
		t.Fatal("resize reused an answer formatted for the old width")
	}
	plain := renderer.ReceiptText(narrow[0].render)
	for _, text := range []string{"README.md", "日本語🐈"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("narrow table omitted %q: %q", text, plain)
		}
	}
	for _, row := range strings.Split(narrow[0].render, "\n") {
		if ansi.StringWidth(row) > 32 {
			t.Fatalf("table was not formatted to available width: %q", row)
		}
	}
}
