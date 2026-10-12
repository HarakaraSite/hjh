package renderer

import (
	"strings"
	"testing"
)

func TestWrappedLiveTailRetainsColorAndClosesLink(t *testing.T) {
	link := "\x1b]8;;https://example.test\a"
	text := "\x1b[39;2m" + link + "日本語の長いパス" + "\x1b]8;;\a\x1b[0m"
	rows := WrapText(text, 6)
	if len(rows) < 2 {
		t.Fatal("test did not produce a live tail")
	}
	for _, row := range rows {
		if !strings.Contains(row, "\x1b[39;2m") || !strings.Contains(row, link) || !strings.HasSuffix(row, "\x1b[0m") {
			t.Fatalf("independent row lost or leaked style: %q", row)
		}
	}
	if ReceiptText(strings.Join(rows, "")) != "日本語の長いパス" {
		t.Fatal("style balancing changed wrapped text")
	}
}
