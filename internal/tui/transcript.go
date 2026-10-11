package tui

import (
	"fmt"
	"strings"

	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

type transcriptPart struct {
	key               string
	sourceStart       int
	sourceEnd         int
	plainStart        int
	plainEnd          int
	rowStart          int
	rowEnd            int
	globalSourceStart int
	mutable           bool
}

type transcriptDocument struct {
	fullStyled      string
	fullStyledReady bool
	styled          string
	plain           string
	parts           []transcriptPart
	rows            []string
	rowStarts       []int
	startRow        int
	sourceLength    int
	width           int
	sealPlainLimit  int
}

type markdownSourceCacheEntry struct {
	source    string
	blocks    []markdownBlock
	blockKeys []markdownBlockKey
}

type markdownBlockKey struct {
	source string
}

type markdownBlockCache struct {
	styled string
	plain  string
}

type transcriptLineKey struct {
	width  int
	styled string
	plain  string
}

type transcriptLineCache struct {
	rows    []string
	offsets []int
}

func (m *model) invalidateTranscript() {
	m.transcriptDirty = true
}

func (m *model) ensureTranscript() *transcriptDocument {
	if !m.transcriptDirty && m.transcript != nil && m.transcript.width == max(1, m.width) {
		return m.transcript
	}
	if m.receiptByPart == nil {
		m.receiptByPart = make(map[string]int)
	}
	m.transcript = m.buildTranscript()
	m.transcriptDirty = false
	m.receiptOffset = max(m.receiptOffset, receiptOffset(m.transcript.parts, m.receiptByPart))
	m.lastTranscriptBytes = m.transcript.sourceLength
	return m.transcript
}

func (m *model) markdownBlocksForSource(id, source string) ([]markdownBlock, []markdownBlockKey) {
	if cached, ok := m.markdownSourceCache[id]; ok && cached.source == source {
		return cached.blocks, cached.blockKeys
	}
	if m.markdownSourceCache == nil {
		m.markdownSourceCache = make(map[string]markdownSourceCacheEntry)
	}
	if m.markdownBlockCache == nil {
		m.markdownBlockCache = make(map[markdownBlockKey]markdownBlockCache)
	}
	ranges := markdownSourceBlocks(source)
	blocks := make([]markdownBlock, 0, len(ranges))
	keys := make([]markdownBlockKey, 0, len(ranges))
	for _, sourceBlock := range ranges {
		raw := source[sourceBlock.start:sourceBlock.end]
		key := markdownBlockKey{source: raw}
		cached, ok := m.markdownBlockCache[key]
		if !ok {
			cached.styled = m.renderMarkdown(raw)
			cached.plain = renderer.ReceiptText(cached.styled)
			m.markdownBlockCache[key] = cached
		}
		block := markdownBlock{
			start: sourceBlock.start, end: sourceBlock.end,
			render: cached.styled, plain: cached.plain,
		}
		blocks = append(blocks, block)
		keys = append(keys, key)
	}
	m.markdownSourceCache[id] = markdownSourceCacheEntry{
		source: source, blocks: blocks, blockKeys: keys,
	}
	return blocks, keys
}

func (m *model) renderedMarkdownSource(source string) (markdownBlockCache, markdownBlockKey) {
	key := markdownBlockKey{source: source}
	if m.markdownBlockCache == nil {
		m.markdownBlockCache = make(map[markdownBlockKey]markdownBlockCache)
	}
	cached, ok := m.markdownBlockCache[key]
	if !ok {
		cached.styled = m.renderMarkdown(source)
		cached.plain = renderer.ReceiptText(cached.styled)
		m.markdownBlockCache[key] = cached
	}
	return cached, key
}

func (m *model) wrapTranscriptLines(styled, plain string, width int) ([]string, []int) {
	if m.transcriptLineCache == nil {
		m.transcriptLineCache = make(map[transcriptLineKey]transcriptLineCache)
	}
	nextCache := make(map[transcriptLineKey]transcriptLineCache)
	var rows []string
	var offsets []int
	for lineStart := 0; lineStart <= len(plain); {
		lineEnd := len(plain)
		if nextNewline := strings.IndexByte(plain[lineStart:], '\n'); nextNewline >= 0 {
			lineEnd = lineStart + nextNewline
		}
		styledStart := renderer.StyledOffset(styled, plain, lineStart)
		styledEnd := renderer.StyledOffset(styled, plain, lineEnd)
		if styledEnd < styledStart {
			styledEnd = styledStart
		}
		plainLine := plain[lineStart:lineEnd]
		styledLine := styled[styledStart:styledEnd]
		key := transcriptLineKey{width: width, styled: styledLine, plain: plainLine}
		cached, ok := m.transcriptLineCache[key]
		if !ok {
			cached.rows = renderer.WrapText(styledLine, width)
			cached.offsets = renderer.WrapOffsets(plainLine, width)
		}
		nextCache[key] = cached
		rows = append(rows, cached.rows...)
		for _, offset := range cached.offsets {
			offsets = append(offsets, lineStart+offset)
		}
		if lineEnd == len(plain) {
			break
		}
		lineStart = lineEnd + 1
	}
	m.transcriptLineCache = nextCache
	return rows, offsets
}

func (m *model) buildTranscript() *transcriptDocument {
	doc := &transcriptDocument{width: max(1, m.width), sealPlainLimit: -1}
	var pendingStyled, pendingPlain strings.Builder
	appendPart := func(key, source string, sourceStart, sourceEnd int, styled string, mutable bool) {
		globalStart := doc.sourceLength
		doc.sourceLength += len(source)
		if m.receiptByPart[key] >= sourceEnd {
			return
		}
		plain := renderer.ReceiptText(styled)
		part := transcriptPart{
			key: key, sourceStart: sourceStart, sourceEnd: sourceEnd,
			plainStart: pendingPlain.Len(), globalSourceStart: globalStart,
			mutable: mutable,
		}
		part.plainEnd = part.plainStart + len(plain)
		if mutable && doc.sealPlainLimit < 0 {
			doc.sealPlainLimit = part.plainStart
		}
		doc.parts = append(doc.parts, part)
		pendingStyled.WriteString(styled)
		pendingPlain.WriteString(plain)
	}

	entities := m.mergedEntities()
	activeSources := make(map[string]bool, len(entities)+len(m.entries)*2)
	activeBlocks := make(map[markdownBlockKey]bool)
	visibleEntityCount := 0
	previousEntityHasBody := false
	previousEntityFullyReceipted := false
	previousEntityEndsWithNewline := false
	for _, entity := range entities {
		if entity.Kind == "execution" && entity.ParentExecutionID == "" {
			continue
		}
		if entity.Kind == "assistant" && entity.Text == "" {
			continue
		}
		prefix := ""
		if visibleEntityCount > 0 && previousEntityHasBody &&
			!previousEntityFullyReceipted && !previousEntityEndsWithNewline {
			prefix = "\n"
		}
		visibleEntityCount++
		if entity.Kind == "tool" {
			toolKey := "entity:" + entity.ID + ":tool-line"
			appendPart(toolKey, toolReceiptSource, 0, len(toolReceiptSource), prefix+toolDisplayLine(entity), false)
			previousEntityHasBody = true
			previousEntityFullyReceipted = m.receiptByPart[toolKey] >= len(toolReceiptSource)
			previousEntityEndsWithNewline = true
			continue
		}
		bodyCacheKey := "entity:" + entity.ID + ":body"
		activeSources[bodyCacheKey] = true
		labelKey := "entity:" + entity.ID + ":label"
		canonicalLabel := entityLabel(entity) + ":\n"
		appendPart(labelKey, canonicalLabel, 0, len(canonicalLabel), prefix+canonicalLabel, false)

		blocks, cacheKeys := m.markdownBlocksForSource(bodyCacheKey, entity.Text)
		currentEntityHasBody := false
		for _, key := range cacheKeys {
			activeBlocks[key] = true
		}
		previousReceipt := m.receiptByPart[bodyCacheKey]
		emittedBodyBlock := false
		for blockIndex, block := range blocks {
			lastStreamingBlock := entity.Status == "streaming" && blockIndex == len(blocks)-1
			if lastStreamingBlock && doc.sealPlainLimit < 0 {
				doc.sealPlainLimit = pendingPlain.Len()
			}
			if previousReceipt >= block.end {
				continue
			}
			appendPart(bodyCacheKey, entity.Text[block.start:block.end], block.start, block.end, block.render, lastStreamingBlock)
			emittedBodyBlock = true
			currentEntityHasBody = currentEntityHasBody || block.plain != ""
		}
		if entity.Status == "streaming" && len(blocks) == 0 && doc.sealPlainLimit < 0 {
			doc.sealPlainLimit = pendingPlain.Len()
		}
		if entity.Status == "streaming" && len(blocks) > 0 && !emittedBodyBlock && doc.sealPlainLimit < 0 {
			doc.sealPlainLimit = pendingPlain.Len()
		}

		if entity.Kind != "execution" {
			meta := entityMeta(entity)
			if meta != "" {
				metaCacheKey := "entity:" + entity.ID + ":meta"
				activeSources[metaCacheKey] = true
				formatted, blockKey := m.renderedMarkdownSource(meta)
				activeBlocks[blockKey] = true
				appendPart(metaCacheKey, meta, 0, len(meta), "\n"+formatted.styled, false)
				currentEntityHasBody = true
			}
		}
		previousEntityHasBody = currentEntityHasBody
		previousEntityEndsWithNewline = strings.HasSuffix(entity.Text, "\n")
		if meta := entityMeta(entity); meta != "" {
			previousEntityEndsWithNewline = strings.HasSuffix(meta, "\n")
		}
		previousEntityFullyReceipted = m.receiptByPart[labelKey] >= len(canonicalLabel) &&
			m.receiptByPart[bodyCacheKey] >= len(entity.Text)
	}
	for index, entry := range m.entries {
		bodyCacheKey := fmt.Sprintf("entry:%d:body", index)
		activeSources[bodyCacheKey] = true
		prefix := ""
		if (visibleEntityCount > 0 || index > 0) && previousEntityHasBody &&
			!previousEntityFullyReceipted && !previousEntityEndsWithNewline {
			prefix = "\n"
		}
		labelKey := fmt.Sprintf("entry:%d:label", index)
		canonicalLabel := entry.role + ":\n"
		appendPart(labelKey, canonicalLabel, 0, len(canonicalLabel), prefix+canonicalLabel, false)
		blocks, cacheKeys := m.markdownBlocksForSource(bodyCacheKey, entry.text)
		for _, key := range cacheKeys {
			activeBlocks[key] = true
		}
		previousReceipt := m.receiptByPart[bodyCacheKey]
		for _, block := range blocks {
			if previousReceipt >= block.end {
				continue
			}
			appendPart(bodyCacheKey, entry.text[block.start:block.end], block.start, block.end, block.render, false)
		}
		previousEntityHasBody = entry.text != ""
		previousEntityEndsWithNewline = strings.HasSuffix(entry.text, "\n")
		previousEntityFullyReceipted = m.receiptByPart[labelKey] >= len(canonicalLabel) &&
			m.receiptByPart[bodyCacheKey] >= len(entry.text)
	}
	for id := range m.markdownSourceCache {
		if !activeSources[id] {
			delete(m.markdownSourceCache, id)
		}
	}
	for key := range m.markdownBlockCache {
		if !activeBlocks[key] {
			delete(m.markdownBlockCache, key)
		}
	}
	doc.styled = pendingStyled.String()
	doc.plain = pendingPlain.String()
	if doc.plain != "" {
		doc.rows, doc.rowStarts = m.wrapTranscriptLines(doc.styled, doc.plain, doc.width)
		if len(doc.rows) != len(doc.rowStarts) {
			for len(doc.rowStarts) < len(doc.rows) {
				doc.rowStarts = append(doc.rowStarts, len(doc.plain))
			}
			doc.rowStarts = doc.rowStarts[:len(doc.rows)]
		}
	}
	for index := range doc.parts {
		part := &doc.parts[index]
		part.rowStart = rowBoundaryForOffset(doc.rowStarts, doc.plain, part.plainStart)
		part.rowEnd = rowBoundaryForOffset(doc.rowStarts, doc.plain, part.plainEnd)
	}
	return doc
}

func entityMeta(entity api.ConversationEntity) string {
	var lines []string
	if entity.Outcome != "" {
		lines = append(lines, "Outcome: "+entity.Outcome)
	}
	if entity.Error != "" {
		lines = append(lines, "Error:\n"+entity.Error)
	}
	if entity.Status == "incomplete" {
		lines = append(lines, "incomplete")
	}
	return strings.Join(lines, "\n")
}

func rowBoundaryForOffset(starts []int, plain string, offset int) int {
	if len(starts) == 0 || offset <= 0 {
		return 0
	}
	if offset >= len(plain) {
		if strings.HasSuffix(plain, "\n") {
			return max(0, len(starts)-1)
		}
		return len(starts)
	}
	row := rowForOffset(starts, offset)
	if starts[row] == offset {
		return row
	}
	return row + 1
}

func rowForOffset(starts []int, offset int) int {
	if len(starts) == 0 {
		return 0
	}
	row := 0
	for index, start := range starts {
		if start > offset {
			break
		}
		row = index
	}
	return row
}

func receiptsThroughRow(doc *transcriptDocument, receipts map[string]int, row int) map[string]int {
	updated := make(map[string]int, len(receipts)+len(doc.parts))
	for key, offset := range receipts {
		updated[key] = offset
	}
	for _, part := range doc.parts {
		if part.mutable || part.rowEnd > row {
			break
		}
		updated[part.key] = max(updated[part.key], part.sourceEnd)
	}
	return updated
}

func receiptOffset(parts []transcriptPart, receipts map[string]int) int {
	result := 0
	for _, part := range parts {
		position := receipts[part.key]
		if position < part.sourceEnd {
			break
		}
		result = part.globalSourceStart + part.sourceEnd - part.sourceStart
	}
	return result
}

// sealTranscriptDocument moves the complete stable prefix to scrollback. The
// final streaming Markdown block and everything after it remain mutable.
func (m *model) sealTranscriptDocument() {
	doc := m.transcript
	if doc == nil {
		if len(m.lastBodyRows) > 0 {
			m.pendingSealRows = append(m.pendingSealRows, m.lastBodyRows...)
			m.lastBodyRows = nil
		}
		m.lastBodyStartRow = 0
		return
	}
	if m.receiptByPart == nil {
		m.receiptByPart = make(map[string]int)
	}
	safeRow := len(doc.rows)
	if doc.sealPlainLimit >= 0 {
		safeRow = rowBoundaryForOffset(doc.rowStarts, doc.plain, doc.sealPlainLimit)
	}
	safeRow = max(doc.startRow, min(safeRow, len(doc.rows)))
	if safeRow > doc.startRow {
		m.pendingSealRows = append(m.pendingSealRows, doc.rows[doc.startRow:safeRow]...)
		m.receiptByPart = receiptsThroughRow(doc, m.receiptByPart, safeRow)
		m.receiptOffset = receiptOffset(doc.parts, m.receiptByPart)
	}
	doc.startRow = safeRow
	m.lastBodyRows = nil
	m.lastBodyStartRow = safeRow
}
