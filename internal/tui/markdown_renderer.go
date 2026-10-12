package tui

import (
	"errors"
	"strings"

	"charm.land/glamour/v2"
	glamansi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/tui/renderer"
)

const solarizedMarkdownBackground = "#002b36"

var solarizedMarkdownBackgroundSGR = ansi.Style{}.
	BackgroundColor(ansi.HexColor(solarizedMarkdownBackground)).String()

func (m *model) renderMarkdown(text string) string {
	if text == "" {
		return ""
	}
	width := max(1, m.width)
	if m.markdownRenderer == nil || m.markdownRendererWidth != width {
		var err error
		m.markdownRenderer, err = glamour.NewTermRenderer(glamour.WithStyles(solarizedDarkMarkdownStyle()), glamour.WithWordWrap(width), glamour.WithTableWrap(true))
		if err != nil {
			m.renderErr = errors.Join(m.renderErr, err)
			return styledText("Markdown rendering failed: "+err.Error()+"\n", redSGR)
		}
		m.markdownRendererWidth = width
	}
	output, err := m.markdownRenderer.Render(text)
	if err != nil {
		m.renderErr = errors.Join(m.renderErr, err)
		return styledText("Markdown rendering failed: "+err.Error()+"\n", redSGR)
	}
	// Keep inter-block spacing attached to the original source boundary. Glamour
	// supplies document margins even when rendering a single streaming block.
	lines := strings.Split(output, "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[0])) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	closing := len(text) - len(strings.TrimRight(text, "\n"))
	output = strings.Join(lines, "\n") + strings.Repeat("\n", closing)
	return paintMarkdownBackground(output, width)
}

// paintMarkdownBackground owns the background for every visible Markdown row.
// Glamour and Chroma emit SGR sequences inside the text, so reassert the same
// background after each sequence and close each row before it reaches the
// transcript writer. The latter keeps Markdown color out of adjacent TUI rows.
func paintMarkdownBackground(text string, width int) string {
	// Glamour may exceed its requested width around inline-code and list
	// boundaries. Wrap before painting so every physical continuation row,
	// including a lone punctuation mark, receives full-width padding.
	lines := renderer.WrapText(text, width)
	for index, line := range lines {
		if index == len(lines)-1 && strings.HasSuffix(text, "\n") {
			lines[index] = ""
			continue // A final line terminator is not an additional blank row.
		}

		line = withMarkdownBackgroundAfterSGR(line)
		line = solarizedMarkdownBackgroundSGR + line
		padding := max(0, width-ansi.StringWidth(line))
		// Reset foreground/attributes before padding, then restore only the
		// Markdown background so the row reaches terminal width uniformly.
		line += resetSGR + solarizedMarkdownBackgroundSGR + strings.Repeat(" ", padding) + resetSGR
		lines[index] = line
	}
	return strings.Join(lines, "\n")
}

func withMarkdownBackgroundAfterSGR(text string) string {
	var out strings.Builder
	var state byte
	for offset := 0; offset < len(text); {
		sequence, _, size, nextState := ansi.GraphemeWidth.DecodeSequenceInString(text[offset:], state, nil)
		if size <= 0 {
			break
		}
		out.WriteString(sequence)
		if strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m") ||
			strings.HasPrefix(sequence, "\x9b") && strings.HasSuffix(sequence, "m") {
			out.WriteString(solarizedMarkdownBackgroundSGR)
		}
		offset += size
		state = nextState
	}
	return out.String()
}

// Glamour has no built-in Solarized style. Use the canonical palette for
// Markdown and Chroma's existing Solarized Dark theme for fenced code.
func solarizedDarkMarkdownStyle() glamansi.StyleConfig {
	style := styles.DarkStyleConfig
	style.Document.Color = new("#839496")
	style.Document.BackgroundColor = nil
	style.Document.Margin = new(uint)
	style.Document.BlockPrefix, style.Document.BlockSuffix = "", ""
	style.Heading.Color = new("#268bd2")
	style.Heading.Bold = new(true)
	style.H1, style.H2, style.H3 = glamansi.StyleBlock{}, glamansi.StyleBlock{}, glamansi.StyleBlock{}
	style.H4, style.H5, style.H6 = glamansi.StyleBlock{}, glamansi.StyleBlock{}, glamansi.StyleBlock{}
	style.BlockQuote.Color = new("#93a1a1")
	style.Strong.Color = new("#93a1a1")
	style.Emph.Color = new("#2aa198")
	style.HorizontalRule.Color = new("#586e75")
	style.Link.Color, style.LinkText.Color = new("#268bd2"), new("#2aa198")
	style.Image.Color, style.ImageText.Color = new("#6c71c4"), new("#586e75")
	style.Code.Color, style.Code.BackgroundColor = new("#b58900"), nil
	style.CodeBlock.Color, style.CodeBlock.BackgroundColor = new("#839496"), nil
	style.CodeBlock.Margin = new(uint)
	style.CodeBlock.Chroma = nil
	style.CodeBlock.Theme = "solarized-dark"
	return style
}
