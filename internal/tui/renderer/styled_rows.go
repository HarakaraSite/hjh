package renderer

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// LogicalRows carries styles and hyperlinks across newlines and closes them at
// each row's end. Any row can then be the first row of a mutable live window.
func LogicalRows(text string) []string {
	var rows []string
	var row strings.Builder
	var sgr, link string
	var state byte
	finish := func() {
		if link != "" {
			row.WriteString("\x1b]8;;\x1b\\")
		}
		if sgr != "" {
			row.WriteString("\x1b[0m")
		}
		rows = append(rows, row.String())
		row.Reset()
	}
	for offset := 0; offset < len(text); {
		sequence, _, size, nextState := ansi.GraphemeWidth.DecodeSequenceInString(text[offset:], state, nil)
		if size <= 0 {
			break
		}
		if sequence == "\n" {
			finish()
			row.WriteString(sgr)
			row.WriteString(link)
		} else {
			row.WriteString(sequence)
			if strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m") {
				if sequence == "\x1b[m" || sequence == "\x1b[0m" {
					sgr = ""
				} else {
					sgr += sequence
				}
			}
			if strings.HasPrefix(sequence, "\x1b]8;") {
				body := strings.TrimSuffix(strings.TrimSuffix(sequence[4:], "\a"), "\x1b\\")
				parts := strings.SplitN(body, ";", 2)
				if len(parts) == 2 && parts[1] != "" {
					link = sequence
				} else {
					link = ""
				}
			}
		}
		offset += size
		state = nextState
	}
	finish()
	return rows
}
