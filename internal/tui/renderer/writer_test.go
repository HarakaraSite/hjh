package renderer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCommittedRowsAreWrittenOnceAcrossLiveUpdates(t *testing.T) {
	var output bytes.Buffer
	writer := New(&output)
	frame := Frame{
		CommittedRows: []string{"user: こんにちは"},
		LiveRows:      []string{"assistant: 調査中", "status: running"},
		Cursor:        Cursor{Row: 1, Cell: 15},
		Size:          Size{Columns: 40, Rows: 12},
	}
	if err := writer.Render(frame); err != nil {
		t.Fatal(err)
	}
	frame.CommittedRows = nil
	frame.LiveRows[0] = "assistant: 調査を続けています"
	frame.Cursor.Cell = 15
	if err := writer.Render(frame); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "user: こんにちは"); got != 1 {
		t.Fatalf("committed user row was written %d times: %q", got, output.String())
	}
	if !strings.Contains(output.String(), "\x1b[2K") {
		t.Fatalf("live update did not clear its owned rows: %q", output.String())
	}
	if strings.Contains(output.String(), "\x1b[2J") || strings.Contains(output.String(), "\x1b[0J") {
		t.Fatalf("renderer cleared terminal content outside its live band: %q", output.String())
	}
}

func TestSealedBodyRowsMoveToScrollbackWhileOldChromeIsRedrawn(t *testing.T) {
	var output bytes.Buffer
	writer := New(&output)
	old := Frame{
		LiveRows: []string{"header old", "body tail", "footer old"},
		Cursor:   Cursor{Row: 2, Cell: 4},
		Size:     Size{Columns: 40, Rows: 12},
	}
	if err := writer.Render(old); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	current := Frame{
		SealedRows: []string{"body tail"},
		LiveRows:   []string{"header new", "body next", "footer new"},
		Cursor:     Cursor{Row: 2, Cell: 4},
		Size:       Size{Columns: 32, Rows: 10},
	}
	if err := writer.Render(current); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	clearAt, sealedAt, headerAt := strings.Index(got, eraseLine), strings.Index(got, "body tail"), strings.Index(got, "header new")
	if clearAt < 0 || sealedAt <= clearAt || headerAt <= sealedAt {
		t.Fatalf("old live band was not cleared before sealing body and drawing new chrome: %q", got)
	}
	if strings.Count(got, "body tail") != 1 || strings.Count(got, "header new") != 1 {
		t.Fatalf("sealed body or new chrome appeared more than once: %q", got)
	}
	if !sameRows(writer.previous.liveRows, current.LiveRows) {
		t.Fatalf("writer retained stale live rows after sealing: %#v", writer.previous.liveRows)
	}
}

func TestNarrowResizeReflowsJapaneseLiveBandWithoutRecommittingIt(t *testing.T) {
	var output bytes.Buffer
	writer := New(&output)
	text := "日本語の回答が幅の変化後も同じ会話として表示されます"
	wide := Frame{
		CommittedRows: []string{"assistant: 完了済み"},
		LiveRows:      WrapText("assistant: "+text, 36),
		Cursor:        Cursor{Row: 0, Cell: 30},
		Size:          Size{Columns: 36, Rows: 10},
	}
	if err := writer.Render(wide); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	narrowRows := WrapText("assistant: "+text, 14)
	narrow := Frame{
		LiveRows: narrowRows,
		Cursor:   Cursor{Row: len(narrowRows) - 1, Cell: ansi.StringWidth(narrowRows[len(narrowRows)-1])},
		Size:     Size{Columns: 14, Rows: 10},
	}
	if err := writer.Render(narrow); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "assistant: 完了済み") {
		t.Fatalf("resize rewrote already committed content: %q", output.String())
	}
	if !strings.Contains(output.String(), "\x1b[2K") {
		t.Fatalf("resize did not clear its reflowed live band: %q", output.String())
	}
	for _, row := range narrowRows {
		if width := ansi.StringWidth(row); width > 14 {
			t.Fatalf("Japanese live row exceeds new width: %d cells in %q", width, row)
		}
	}
}

func TestCloseErasesLiveRowsAndResetsInputModes(t *testing.T) {
	var output bytes.Buffer
	writer := New(&output)
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Render(Frame{
		CommittedRows: []string{"saved conversation"},
		LiveRows:      []string{"header", "draft: 日本語"},
		Cursor:        Cursor{Row: 1, Cell: 8},
		Size:          Size{Columns: 40, Rows: 12},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{ansi.SetModeBracketedPaste, ansi.PushKittyKeyboard(1), ansi.ResetModeBracketedPaste, ansi.PopKittyKeyboard(1)} {
		if !strings.Contains(got, want) {
			t.Fatalf("output is missing terminal mode sequence %q: %q", want, got)
		}
	}
	if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "\x1b[0J") {
		t.Fatalf("close cleared terminal scrollback: %q", got)
	}
}

func TestWrapTextUsesTerminalCellWidth(t *testing.T) {
	rows := WrapText("go 日本語 a\n", 8)
	if len(rows) < 3 || rows[len(rows)-1] != "" {
		t.Fatalf("logical newline was not preserved: %#v", rows)
	}
	for _, row := range rows {
		if width := ansi.StringWidth(row); width > 8 {
			t.Fatalf("wrapped row exceeds cell width: %d cells in %q", width, row)
		}
	}
}

func TestTailOffsetKeepsOnlyTheLiveRowsForLongStreamingSource(t *testing.T) {
	text := strings.Repeat("Q", 1000)
	for _, width := range []int{80, 44, 80} {
		offset := TailOffset(text, width, 6)
		prefixRows := WrapText(text[:offset], width)
		tailRows := WrapText(text[offset:], width)
		if len(tailRows) > 6 {
			t.Fatalf("width %d kept %d live rows", width, len(tailRows))
		}
		if strings.Join(append(prefixRows, tailRows...), "") != text {
			t.Fatalf("width %d split lost or duplicated source", width)
		}
	}
}

func TestReceiptTextIgnoresMarkdownStylingAndLinePadding(t *testing.T) {
	oldRendered := "\x1b[38;5;252m" + strings.Repeat("Q", 8) + strings.Repeat(" ", 12) + "\x1b[m"
	newRendered := "\x1b[38;5;252m" + strings.Repeat("Q", 9) + strings.Repeat(" ", 11) + "\x1b[m"
	oldReceipt := ReceiptText(oldRendered)
	newReceipt := ReceiptText(newRendered)
	if len(newReceipt) != len(oldReceipt)+1 || newReceipt[:len(oldReceipt)] != oldReceipt {
		t.Fatalf("visible receipt did not grow with the appended source: old=%q new=%q", oldReceipt, newReceipt)
	}
	start := StyledOffset(newRendered, newReceipt, len(oldReceipt))
	if got := ansi.Strip(newRendered[start:]); strings.TrimSpace(got) != "Q" {
		t.Fatalf("styled receipt offset did not isolate the new source: offset=%d suffix=%q", start, got)
	}
}
