package renderer

import "github.com/charmbracelet/x/ansi"

// A picker has a separate mutable frame and no append-only conversation.
// DEC 1049 saves/restores the normal screen and cursor, so closing the picker
// returns to the same visible history without copying it into scrollback again.
func (w *Writer) renderOverlay(frame Frame) error {
	if w.overlay == nil {
		if err := writeAll(w.out, []byte(ansi.SetMode(ansi.ModeAltScreenSaveCursor))); err != nil {
			return err
		}
		w.overlay = New(w.out)
		// The entire temporary screen can be redrawn, unlike committed normal
		// history. It needs no asynchronous cursor query during resize.
		w.overlay.trackCursorReports = false
		w.waitingSize, w.resizeCursor = nil, nil
	}
	if w.overlay.previous != nil && w.overlay.previous.size != frame.Size {
		w.overlay.previous = nil
	}
	frame.Overlay = false
	frame.CommittedRows, frame.SealedRows = nil, nil
	return w.overlay.Render(frame)
}

func (w *Writer) leaveOverlay() error {
	if w.overlay == nil {
		return nil
	}
	if err := writeAll(w.out, []byte(ansi.ResetMode(ansi.ModeAltScreenSaveCursor))); err != nil {
		return err
	}
	w.overlay = nil
	return nil
}
