package renderer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPickerOverlayRestoresNormalFrameWithoutRepeatingConversation(t *testing.T) {
	var output bytes.Buffer
	w := New(&output)
	normal := Frame{CommittedRows: []string{"saved conversation"}, LiveRows: []string{"> draft", "footer"},
		DockRows: 2, Cursor: Cursor{Row: 0, Cell: 7}, Size: Size{Columns: 40, Rows: 12}}
	if err := w.Render(normal); err != nil {
		t.Fatal(err)
	}
	previous := w.previous
	picker := Frame{Overlay: true, SealedRows: []string{"pending parent answer"}, LiveRows: []string{"picker options", "> draft", "footer"},
		DockRows: 2, Cursor: Cursor{Row: 1, Cell: 7}, Size: normal.Size}
	if err := w.Render(picker); err != nil {
		t.Fatal(err)
	}
	picker.LiveRows[0] = "picker selected option"
	if err := w.Render(picker); err != nil {
		t.Fatal(err)
	}
	if w.previous != previous || strings.Contains(output.String(), "pending parent answer") {
		t.Fatal("picker changed the underlying normal frame or printed pending conversation")
	}
	normal.CommittedRows = nil
	if err := w.Render(normal); err != nil {
		t.Fatal(err)
	}
	if w.previous != previous || w.overlay != nil {
		t.Fatal("closing picker did not restore its original frame")
	}
	for _, sequence := range []string{ansi.SetAltScreenSaveCursorMode, ansi.ResetAltScreenSaveCursorMode, "saved conversation"} {
		if strings.Count(output.String(), sequence) != 1 {
			t.Fatalf("overlay repeated/lost %q: %q", sequence, output.String())
		}
	}
}

func TestClosePickerRestoresNormalScreenBeforeClearingEditor(t *testing.T) {
	var output bytes.Buffer
	w := New(&output)
	frame := Frame{LiveRows: []string{"> draft", "footer"}, DockRows: 2, Size: Size{Columns: 40, Rows: 12}}
	if err := w.Render(frame); err != nil {
		t.Fatal(err)
	}
	frame.Overlay = true
	if err := w.Render(frame); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), ansi.ResetAltScreenSaveCursorMode) || !strings.Contains(output.String(), eraseLine) {
		t.Fatalf("close cleared the wrong screen or left the picker active: %q", output.String())
	}
}

func TestPickerResizeRedrawsTemporaryScreenWithoutCursorQuery(t *testing.T) {
	var output bytes.Buffer
	w := New(&output)
	w.trackCursorReports = true
	frame := Frame{LiveRows: []string{"picker", "> draft", "footer"}, Overlay: true,
		DockRows: 2, Size: Size{Columns: 40, Rows: 12}}
	if err := w.Render(frame); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	frame.Size = Size{Columns: 32, Rows: 18}
	if err := w.Render(frame); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), ansi.RequestCursorPositionReport) || w.overlay.waitingSize != nil || w.previous != nil {
		t.Fatal("temporary screen resize queried a cursor or changed normal frame state")
	}
}
