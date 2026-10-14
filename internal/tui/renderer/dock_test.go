package renderer

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDockKeepsEditorAtBottomAsBodyGrowsAndCommits(t *testing.T) {
	var output bytes.Buffer
	w := New(&output)
	controls := []string{"divider", "> draft", "help", "state", "model"}
	frame := Frame{Size: Size{Columns: 40, Rows: 15}, DockRows: len(controls)}
	for i, body := range [][]string{nil, {"stream one"}, {"stream one", "stream two", "stream three"}, nil, {"next stream"}, nil} {
		frame.SealedRows, frame.CommittedRows = nil, nil
		if i == 0 {
			frame.SealedRows = []string{"opening header"}
		}
		if i == 3 {
			frame.CommittedRows = []string{"finished answer one"}
		}
		if i == 5 {
			frame.CommittedRows = []string{"finished answer two"}
		}
		frame.LiveRows = append(append([]string(nil), body...), controls...)
		frame.Cursor = Cursor{Row: len(body) + 1, Cell: 8}
		if err := w.Render(frame); err != nil {
			t.Fatal(err)
		}
		physicalCursor := frame.Size.Rows - len(w.previous.liveRows) + w.previous.cursor.Row
		if want := frame.Size.Rows - len(controls) + 1; physicalCursor != want {
			t.Fatalf("frame %d moved editor to row %d, want %d", i, physicalCursor, want)
		}
		if !sameRows(w.previous.liveRows[len(w.previous.liveRows)-len(controls):], controls) {
			t.Fatalf("frame %d displaced footer rows", i)
		}
	}
	for _, committed := range []string{"opening header", "finished answer one", "finished answer two"} {
		if strings.Count(output.String(), committed) != 1 {
			t.Fatalf("docking repeated or lost committed text: %q", committed)
		}
	}
	output.Reset()
	frame.SealedRows, frame.CommittedRows = nil, nil
	if err := w.Render(frame); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("unchanged docked frame was repainted")
	}
}

func TestResizeWaitsForActualCursorBeforeErasingOrCommitting(t *testing.T) {
	var output bytes.Buffer
	w := New(&output)
	w.trackCursorReports = true
	frame := Frame{Size: Size{Columns: 60, Rows: 18}, DockRows: 5,
		LiveRows: []string{"divider", "> draft", "help", "state", "model"}, Cursor: Cursor{Row: 1, Cell: 7}}
	frame.CommittedRows = make([]string, 25)
	for i := range frame.CommittedRows {
		frame.CommittedRows[i] = "saved row"
	}
	if err := w.Render(frame); err != nil {
		t.Fatal(err)
	}
	for _, resized := range []struct {
		rows, cursorRow, anchor int
	}{{14, 13, 12}, {28, 24, 23}} {
		output.Reset()
		frame.Size.Rows = resized.rows
		frame.CommittedRows = []string{"new immutable row"}
		previous := w.previous
		for range 2 {
			if err := w.Render(frame); !errors.Is(err, ErrAwaitingCursor) {
				t.Fatalf("resize did not wait for terminal position: %v", err)
			}
		}
		if output.String() != ansi.RequestCursorPositionReport || w.previous != previous {
			t.Fatal("awaiting cursor erased conversation, duplicated a query or advanced the frame")
		}
		w.ObserveCursor(resized.cursorRow, 7)
		if err := w.Render(frame); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), ansi.CursorPosition(1, resized.anchor+1)) || strings.Count(output.String(), "new immutable row") != 1 {
			t.Fatalf("resize ignored reported live anchor or lost its pending commit: %q", output.String())
		}
		if row := frame.Size.Rows - len(w.previous.liveRows) + w.previous.cursor.Row; row != resized.rows-4 {
			t.Fatalf("resized editor row=%d, want %d", row, resized.rows-4)
		}
	}
}

func TestDockConsumesSpaceThenScrollsWithoutResendingConversation(t *testing.T) {
	var output bytes.Buffer
	w := New(&output)
	frame := Frame{Size: Size{Columns: 40, Rows: 10}, DockRows: 2,
		LiveRows: []string{"> draft", "footer"}, Cursor: Cursor{Row: 0, Cell: 7}}
	for _, text := range []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth"} {
		frame.CommittedRows = []string{text}
		if err := w.Render(frame); err != nil {
			t.Fatal(err)
		}
		row := frame.Size.Rows - len(w.previous.liveRows) + w.previous.cursor.Row
		if row != 8 {
			t.Fatalf("committing %s moved editor to %d", text, row)
		}
		if strings.Count(output.String(), text) != 1 {
			t.Fatalf("committed text duplicated: %s", text)
		}
	}
	if strings.Contains(output.String(), "\x1b[2J") || strings.Contains(output.String(), ansi.SetMode(ansi.ModeAltScreenSaveCursor)) {
		t.Fatal("docking cleared history or enabled the alternate screen")
	}
	if strings.Count(output.String(), ansi.SetModeSynchronizedOutput) != 10 || strings.Count(output.String(), ansi.ResetModeSynchronizedOutput) != 10 {
		t.Fatal("clear, move and draw were not enclosed in complete synchronized frames")
	}
}
