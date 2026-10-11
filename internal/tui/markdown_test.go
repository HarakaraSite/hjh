package tui

import (
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

func TestMarkdownTranscriptKeepsSourceMarkdownUnmodified(t *testing.T) {
	model := newModel(nil, nil, nil, Options{})
	defer model.shutdown()
	source := "本文\n\n## 見出し\n\n- 項目A\n- 項目B\n\n```go\nfmt.Println(\"ok\")\n```\n\n**記号**と[link](https://example.test)\n"
	var styled strings.Builder
	for _, block := range markdownBlocks(source, model.renderMarkdown) {
		styled.WriteString(block.render)
	}
	if got := styled.String(); got != source {
		t.Fatalf("Markdown rendering changed the source:\n got %q\nwant %q", got, source)
	}
	if strings.Contains(styled.String(), "\x1b[") {
		t.Fatalf("raw Markdown unexpectedly gained terminal styling: %q", styled.String())
	}
}
