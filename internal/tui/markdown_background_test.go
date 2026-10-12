package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"hjh/internal/tui/renderer"
)

func TestMarkdownBackgroundPaintsEveryRenderedCellAndPreservesReceipt(t *testing.T) {
	const width = 72
	source := "# Heading\n\nParagraph with **strong**, *emphasis*, `inline code`, and [link](https://example.test).\n\n- list item\n\n> quoted row\n\n| name | value |\n| --- | --- |\n| cat | 42 |\n\n```go\nfmt.Println(\"highlighted\")\n```\n"
	m := newModel(nil, nil, nil, Options{})
	defer m.shutdown()
	m.width = width

	blocks, _ := m.markdownBlocksForSource("background", source)
	var reconstructed strings.Builder
	var rendered strings.Builder
	for _, block := range blocks {
		reconstructed.WriteString(source[block.start:block.end])
		rendered.WriteString(block.render)
	}
	if reconstructed.String() != source || m.markdownSourceCache["background"].source != source {
		t.Fatal("background painting changed the original Markdown source or its cached receipt")
	}

	plain := renderer.ReceiptText(rendered.String())
	for _, want := range []string{"Heading", "list item", "quoted row", "cat", "inline code", "highlighted"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered Markdown omitted %q: %q", want, plain)
		}
	}
	if !strings.Contains(rendered.String(), "38;2;38;139;210") || !strings.Contains(rendered.String(), "38;2;181;137;0") {
		t.Fatalf("Markdown foreground theme colors were lost: %q", rendered.String())
	}

	rows := strings.Split(rendered.String(), "\n")
	paintedRows := 0
	paintedPadding := 0
	for index, row := range rows {
		if index == len(rows)-1 && row == "" {
			continue // The final line terminator is not a painted blank row.
		}
		paintedRows++
		if got := ansi.StringWidth(row); got != width {
			t.Fatalf("painted Markdown row width = %d, want %d: %q", got, width, row)
		}
		trailing := len(strings.TrimRight(ansi.Strip(row), " "))
		if trailing < len(ansi.Strip(row)) {
			paintedPadding++
		}
		assertMarkdownCellsUseBase03(t, row)
		if !strings.HasSuffix(row, resetSGR) {
			t.Fatalf("Markdown row left its style open: %q", row)
		}
	}
	if paintedRows == 0 || paintedPadding == 0 {
		t.Fatalf("test did not cover painted rows and right padding: rows=%d padding=%d", paintedRows, paintedPadding)
	}
}

func TestWrappedOperationListPaintsShortContinuationRowsToTerminalWidth(t *testing.T) {
	// From b71a29a6. Glamour can exceed its wrap width around inline-code/list
	// boundaries; the final hard wrap must still paint the short last row.
	const width = 94
	source := "## Main operations\n- `hjh run`: executes a request (stdout = final answer, stderr = progress/IDs, Ctrl-C to stop). `--stream` displays output incrementally.\n- `hjh history`: shows conversation / Markdown / execution evidence (NDJSON) via `--view session|canonical|detail`.\n"
	m := newModel(nil, nil, nil, Options{})
	defer m.shutdown()
	m.width = width
	blocks, _ := m.markdownBlocksForSource("operations", source)
	for _, block := range blocks {
		for _, row := range renderer.WrapText(block.render, width) {
			if row == "" {
				continue
			}
			if got := ansi.StringWidth(row); got != width {
				t.Fatalf("wrapped Markdown continuation only paints %d of %d cells: %q", got, width, ansi.Strip(row))
			}
			assertMarkdownCellsUseBase03(t, row)
		}
	}
	if m.markdownSourceCache["operations"].source != source {
		t.Fatal("wrapping changed the original Markdown")
	}
}

func assertMarkdownCellsUseBase03(t *testing.T, row string) {
	t.Helper()
	background := ""
	var state byte
	for offset := 0; offset < len(row); {
		sequence, cellWidth, size, nextState := ansi.GraphemeWidth.DecodeSequenceInString(row[offset:], state, nil)
		if size <= 0 {
			t.Fatalf("failed to decode rendered Markdown row at byte %d: %q", offset, row)
		}
		if isMarkdownTestSGR(sequence) {
			background = markdownTestBackgroundAfterSGR(sequence, background)
		} else if cellWidth > 0 && background != solarizedMarkdownBackground {
			t.Fatalf("Markdown cell %q has background %q, want %s (row %q)", sequence, background, solarizedMarkdownBackground, row)
		}
		offset += size
		state = nextState
	}
}

func isMarkdownTestSGR(sequence string) bool {
	return strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m") ||
		strings.HasPrefix(sequence, "\x9b") && strings.HasSuffix(sequence, "m")
}

func markdownTestBackgroundAfterSGR(sequence, current string) string {
	body := strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m")
	body = strings.TrimSuffix(strings.TrimPrefix(body, "\x9b"), "m")
	parameters := strings.Split(body, ";")
	for index := 0; index < len(parameters); index++ {
		parts := strings.Split(parameters[index], ":")
		code, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		switch {
		case code == 0 || code == 49:
			current = ""
		case code >= 40 && code <= 47 || code >= 100 && code <= 107:
			current = "other"
		case code == 48 && len(parts) > 1:
			current = markdownTestColonBackground(parts)
		case code == 48 && index+1 < len(parameters):
			mode, _ := strconv.Atoi(parameters[index+1])
			if mode == 5 && index+2 < len(parameters) {
				current = "other"
				index += 2
			} else if mode == 2 && index+4 < len(parameters) {
				red, _ := strconv.Atoi(parameters[index+2])
				green, _ := strconv.Atoi(parameters[index+3])
				blue, _ := strconv.Atoi(parameters[index+4])
				current = markdownTestRGBBackground(red, green, blue)
				index += 4
			} else {
				current = "other"
			}
		}
	}
	return current
}

func markdownTestColonBackground(parts []string) string {
	if len(parts) >= 5 && parts[1] == "2" {
		red, _ := strconv.Atoi(parts[len(parts)-3])
		green, _ := strconv.Atoi(parts[len(parts)-2])
		blue, _ := strconv.Atoi(parts[len(parts)-1])
		return markdownTestRGBBackground(red, green, blue)
	}
	return "other"
}

func markdownTestRGBBackground(red, green, blue int) string {
	expectedRed, expectedGreen, expectedBlue, _ := ansi.HexColor(solarizedMarkdownBackground).RGBA()
	if uint32(red) == expectedRed>>8 && uint32(green) == expectedGreen>>8 && uint32(blue) == expectedBlue>>8 {
		return solarizedMarkdownBackground
	}
	return "other"
}
