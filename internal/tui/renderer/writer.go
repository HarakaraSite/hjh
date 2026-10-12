// Package renderer writes an inline TUI on the terminal's normal screen.
// Conversation rows are append-only; only LiveRows belong to the mutable frame.
package renderer

import (
	"errors"
	"io"
	"math"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	carriageReturn = "\r"
	lineEnd        = "\r\n"
	eraseLine      = "\x1b[2K"
	saveCursor     = "\x1b7"
	restoreCursor  = "\x1b8"
)

type Size struct {
	Columns int
	Rows    int
}

// Cursor is a zero-based cell position within LiveRows.
type Cursor struct {
	Row  int
	Cell int
}

// Frame contains rows to append exactly once, rows moved from the previous live
// body into scrollback, and the current mutable screen band. Row slices must
// already be wrapped to Size.Columns.
type Frame struct {
	CommittedRows []string
	SealedRows    []string
	LiveRows      []string
	Cursor        Cursor
	Size          Size
}

type previousFrame struct {
	liveRows []string
	cursor   Cursor
	size     Size
}

// Writer owns the current inline frame. It never clears the full screen or writes
// to terminal scrollback control sequences.
type Writer struct {
	out      io.Writer
	previous *previousFrame
	started  bool
}

func New(out io.Writer) *Writer { return &Writer{out: out} }

// Start enables the input protocols used by the editor. Bubble Tea's renderer
// is directed to a discard sink, so the writer owns these terminal modes.
func (w *Writer) Start() error {
	if w.started {
		return nil
	}
	sequence := ansi.SetModeBracketedPaste + ansi.SetModifyOtherKeys2 +
		ansi.PushKittyKeyboard(ansi.KittyDisambiguateEscapeCodes) + ansi.RequestKittyKeyboard
	if err := writeAll(w.out, []byte(sequence)); err != nil {
		return err
	}
	w.started = true
	return nil
}

// Render appends committed rows and redraws the live band in one terminal write.
func (w *Writer) Render(frame Frame) error {
	if w.out == nil {
		return errors.New("normal-screen renderer has no output writer")
	}
	frame.Size.Columns = max(1, frame.Size.Columns)
	frame.Size.Rows = max(1, frame.Size.Rows)
	if len(frame.LiveRows) == 0 {
		frame.LiveRows = []string{""}
	}
	frame.Cursor = clampCursor(frame.Cursor, frame.LiveRows)

	previous := w.previous
	if previous != nil && len(frame.CommittedRows) == 0 && len(frame.SealedRows) == 0 &&
		sameRows(previous.liveRows, frame.LiveRows) &&
		previous.cursor == frame.Cursor && previous.size == frame.Size {
		return nil
	}

	retainedPrefix := 0
	if previous != nil && len(frame.CommittedRows) == 0 && len(frame.SealedRows) == 0 && previous.size == frame.Size {
		prefixLimit := min(previous.cursor.Row, frame.Cursor.Row)
		for retainedPrefix < prefixLimit && previous.liveRows[retainedPrefix] == frame.LiveRows[retainedPrefix] {
			retainedPrefix++
		}
	}

	previousRows := []string(nil)
	previousCursorRow := 0
	if previous != nil {
		previousRows = previous.liveRows[retainedPrefix:]
		previousCursorRow = previous.cursor.Row - retainedPrefix
	}
	nextLiveRows := frame.LiveRows[retainedPrefix:]
	nextCursorRow := frame.Cursor.Row - retainedPrefix

	var out strings.Builder
	if previous == nil {
		out.WriteString(carriageReturn)
		out.WriteString(eraseLine)
	} else {
		cursorOffset := reflowHeight(previousRows[:clamp(previousCursorRow, 0, len(previousRows))], frame.Size.Columns)
		cursorOffset += previous.cursor.Cell / frame.Size.Columns
		visibleRows := min(reflowHeight(previousRows, frame.Size.Columns), frame.Size.Rows)
		writeClearLiveBand(&out, cursorOffset, visibleRows)
	}

	for _, row := range frame.SealedRows {
		out.WriteString(row)
		out.WriteString(lineEnd)
	}
	for _, row := range frame.CommittedRows {
		out.WriteString(row)
		out.WriteString(lineEnd)
	}
	for index, row := range nextLiveRows {
		if index > 0 {
			out.WriteString(lineEnd)
		}
		out.WriteString(row)
	}

	// Return from the final row (which may have delayed-wrap pending) to the
	// requested caret cell. Positioning is relative to the live band only.
	out.WriteString(carriageReturn)
	rowsFromLastLiveRow := len(nextLiveRows) - 1 - nextCursorRow
	if rowsFromLastLiveRow > 0 {
		writeMoveUp(&out, rowsFromLastLiveRow)
	}
	writeMoveRight(&out, frame.Cursor.Cell)

	if err := writeAll(w.out, []byte(out.String())); err != nil {
		return err
	}
	w.previous = &previousFrame{
		liveRows: append([]string(nil), frame.LiveRows...),
		cursor:   frame.Cursor,
		size:     frame.Size,
	}
	return nil
}

// Close erases the live band while leaving committed conversation in scrollback.
func (w *Writer) Close() error {
	previous := w.previous
	var out strings.Builder
	if previous != nil {
		if len(previous.liveRows) > 0 {
			cursorOffset := reflowHeight(previous.liveRows[:previous.cursor.Row], previous.size.Columns)
			cursorOffset += previous.cursor.Cell / previous.size.Columns
			visibleRows := min(reflowHeight(previous.liveRows, previous.size.Columns), previous.size.Rows)
			writeClearLiveBand(&out, cursorOffset, visibleRows)
		} else {
			out.WriteString(carriageReturn)
		}
	}
	if w.started {
		out.WriteString(ansi.ResetModeBracketedPaste)
		out.WriteString(ansi.ResetModifyOtherKeys)
		out.WriteString(ansi.PopKittyKeyboard(1))
	}
	if err := writeAll(w.out, []byte(out.String())); err != nil {
		return err
	}
	w.previous = nil
	w.started = false
	return nil
}

// WrapText wraps logical lines to terminal cell width, preserving empty lines.
func WrapText(text string, columns int) []string {
	columns = max(1, columns)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return LogicalRows(ansi.Hardwrap(text, columns, true))
}

// WrapOffsets returns the plain-text byte offset at the start of each terminal
// row produced by WrapText. Offsets stop only at grapheme boundaries.
func WrapOffsets(text string, columns int) []int {
	columns = max(1, columns)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	starts := []int{0}
	cell := 0
	var state byte
	for offset := 0; offset < len(text); {
		sequence, width, size, nextState := ansi.GraphemeWidth.DecodeSequenceInString(text[offset:], state, nil)
		if size <= 0 {
			break
		}
		if sequence == "\n" {
			starts = append(starts, offset+size)
			cell = 0
		} else {
			if width > 0 && cell+width > columns {
				starts = append(starts, offset)
				cell = 0
			}
			cell += width
		}
		offset += size
		state = nextState
	}
	return starts
}

// TailOffset returns the byte offset where the last rows terminal rows begin.
// It uses the same grapheme and ANSI width rules as WrapText so a streaming
// source can move its sealed prefix into scrollback without retaining an
// oversized live frame.
func TailOffset(text string, columns, rows int) int {
	columns = max(1, columns)
	rows = max(1, rows)
	starts := []int{0}
	cell := 0
	var state byte
	for offset := 0; offset < len(text); {
		sequence, width, size, nextState := ansi.GraphemeWidth.DecodeSequenceInString(text[offset:], state, nil)
		if size <= 0 {
			break
		}
		if sequence == "\n" {
			starts = append(starts, offset+size)
			cell = 0
		} else {
			if width > 0 && cell+width > columns {
				starts = append(starts, offset)
				cell = 0
			}
			cell += width
		}
		offset += size
		state = nextState
	}
	index := max(0, len(starts)-rows)
	return starts[index]
}

// ReceiptText returns the visible text used to track how much conversation has
// reached terminal scrollback. ANSI styling and renderer-added line padding do
// not belong to the receipt because their byte length can change as a Markdown
// paragraph grows.
func ReceiptText(text string) string {
	lines := strings.Split(ansi.Strip(text), "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	return strings.Join(lines, "\n")
}

// StyledOffset maps a ReceiptText byte offset back into its styled source.
// It keeps any style controls that precede the next visible grapheme with the
// suffix, so a source receipt never starts in the middle of a terminal style.
func StyledOffset(styled, receipt string, offset int) int {
	offset = max(0, min(offset, len(receipt)))
	if offset == len(receipt) {
		return len(styled)
	}

	visible := 0
	var state byte
	controlStart := -1
	for sourceOffset := 0; sourceOffset < len(styled); {
		sequence, _, size, nextState := ansi.GraphemeWidth.DecodeSequenceInString(styled[sourceOffset:], state, nil)
		if size <= 0 {
			break
		}
		if isTerminalControl(sequence) {
			if controlStart < 0 {
				controlStart = sourceOffset
			}
			sourceOffset += size
			state = nextState
			continue
		}
		if visible >= offset || visible+len(sequence) > offset {
			if controlStart >= 0 {
				return controlStart
			}
			return sourceOffset
		}
		if strings.HasPrefix(receipt[visible:], sequence) {
			visible += len(sequence)
		} else if strings.Trim(sequence, " \t") != "" {
			// If the renderer changed visible content before this boundary, keep
			// the range attached to that content rather than skipping it.
			if controlStart >= 0 {
				return controlStart
			}
			return sourceOffset
		}
		sourceOffset += size
		state = nextState
		controlStart = -1
	}
	return len(styled)
}

func isTerminalControl(sequence string) bool {
	return strings.HasPrefix(sequence, "\x1b") || strings.HasPrefix(sequence, "\x9b")
}

func reflowHeight(rows []string, columns int) int {
	columns = max(1, columns)
	height := 0
	for _, row := range rows {
		height += max(1, int(math.Ceil(float64(ansi.StringWidth(row))/float64(columns))))
	}
	return height
}

func writeClearLiveBand(out *strings.Builder, cursorOffset, visibleRows int) {
	writeMoveUp(out, cursorOffset)
	out.WriteString(carriageReturn)
	out.WriteString(saveCursor)
	for row := 0; row < visibleRows; row++ {
		out.WriteString(eraseLine)
		if row+1 < visibleRows {
			out.WriteString("\x1b[1B")
		}
	}
	out.WriteString(restoreCursor)
	out.WriteString(carriageReturn)
}

func writeMoveUp(out *strings.Builder, rows int) {
	if rows > 0 {
		out.WriteString("\x1b[")
		out.WriteString(itoa(rows))
		out.WriteByte('A')
	}
}

func writeMoveRight(out *strings.Builder, cells int) {
	if cells > 0 {
		out.WriteString("\x1b[")
		out.WriteString(itoa(cells))
		out.WriteByte('C')
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}

func clampCursor(cursor Cursor, rows []string) Cursor {
	cursor.Row = clamp(cursor.Row, 0, len(rows)-1)
	width := max(0, ansi.StringWidth(rows[cursor.Row]))
	cursor.Cell = clamp(cursor.Cell, 0, width)
	return cursor
}

func clamp(value, lower, upper int) int {
	return max(lower, min(value, upper))
}

func sameRows(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func writeAll(out io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := out.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
