package tui

import (
	"errors"
	"strings"

	"charm.land/glamour/v2"
	glamansi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"github.com/charmbracelet/x/ansi"
)

const solarizedMarkdownBackground = "#002b36"

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
	lines = strings.Split(output, "\n")
	background := ansi.Style{}.BackgroundColor(ansi.HexColor(solarizedMarkdownBackground)).String()
	for index, line := range lines {
		if index == len(lines)-1 && line == "" {
			continue // The final line terminator is not an additional blank row.
		}
		if strings.TrimSpace(ansi.Strip(line)) == "" {
			lines[index] = background + strings.Repeat(" ", width) + resetSGR
		}
	}
	return strings.Join(lines, "\n")
}

// Glamour has no built-in Solarized style. Use the canonical palette for
// Markdown and Chroma's existing Solarized Dark theme for fenced code.
func solarizedDarkMarkdownStyle() glamansi.StyleConfig {
	style := styles.DarkStyleConfig
	style.Document.Color = new("#839496")
	style.Document.BackgroundColor = new(solarizedMarkdownBackground)
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
	style.Code.Color, style.Code.BackgroundColor = new("#b58900"), new("#073642")
	style.CodeBlock.Color, style.CodeBlock.BackgroundColor = new("#839496"), new(solarizedMarkdownBackground)
	style.CodeBlock.Margin = new(uint)
	style.CodeBlock.Chroma = nil
	style.CodeBlock.Theme = "solarized-dark"
	return style
}
