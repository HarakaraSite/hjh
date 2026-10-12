package tui

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	textm "github.com/yuin/goldmark/text"
	"hjh/internal/tui/renderer"
)

type markdownBlock struct {
	start  int
	end    int
	render string
	plain  string
}

type markdownSourceBlock struct {
	start int
	end   int
}

func markdownBlocks(source string, render func(string) string) []markdownBlock {
	if source == "" {
		return nil
	}
	sourceBytes := []byte(source)
	ranges := markdownSourceBlocks(source)
	blocks := make([]markdownBlock, 0, len(ranges))
	for _, sourceBlock := range ranges {
		raw := string(sourceBytes[sourceBlock.start:sourceBlock.end])
		styled := render(raw)
		blocks = append(blocks, markdownBlock{
			start: sourceBlock.start, end: sourceBlock.end,
			render: styled, plain: renderer.ReceiptText(styled),
		})
	}
	return blocks
}

func markdownSourceBlocks(source string) []markdownSourceBlock {
	if source == "" {
		return nil
	}
	sourceBytes := []byte(source)
	doc := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList)).Parser().Parse(textm.NewReader(sourceBytes))
	var starts []int
	for node := doc.FirstChild(); node != nil; node = node.NextSibling() {
		starts = append(starts, markdownNodeStart(sourceBytes, node))
	}
	if len(starts) == 0 {
		return []markdownSourceBlock{{start: 0, end: len(sourceBytes)}}
	}
	blocks := make([]markdownSourceBlock, 0, len(starts))
	start := 0
	for index := range starts {
		end := len(sourceBytes)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		if end < start {
			end = start
		}
		if end > start {
			blocks = append(blocks, markdownSourceBlock{start: start, end: end})
		}
		start = end
	}
	return blocks
}

func markdownNodeStart(source []byte, node ast.Node) int {
	start := len(source)
	_ = ast.Walk(node, func(current ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if lines, ok := current.(interface{ Lines() *textm.Segments }); ok && current.Type() == ast.TypeBlock {
			for index := 0; index < lines.Lines().Len(); index++ {
				segmentStart := lines.Lines().At(index).Start
				if segmentStart >= 0 && segmentStart <= len(source) {
					start = min(start, sourceLineStart(source, segmentStart))
				}
			}
		}
		if text, ok := current.(*ast.Text); ok && text.Segment.Start >= 0 && text.Segment.Start <= len(source) {
			start = min(start, sourceLineStart(source, text.Segment.Start))
		}
		return ast.WalkContinue, nil
	})
	if fenced, ok := node.(*ast.FencedCodeBlock); ok && fenced.Lines().Len() > 0 {
		contentStart := sourceLineStart(source, fenced.Lines().At(0).Start)
		if contentStart > 0 {
			fenceStart := sourceLineStart(source, contentStart-1)
			lineEnd := contentStart - 1
			if lineEnd > fenceStart && source[lineEnd-1] == '\n' {
				lineEnd--
			}
			line := strings.TrimSpace(string(source[fenceStart:lineEnd]))
			if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
				start = min(start, fenceStart)
			}
		}
	}
	if start == len(source) {
		return 0
	}
	return start
}

func sourceLineStart(source []byte, offset int) int {
	offset = max(0, min(offset, len(source)))
	for offset > 0 && source[offset-1] != '\n' {
		offset--
	}
	return offset
}
