package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/tui/renderer"
)

const toolReceiptSource = "tool"

func (m *model) mergedEntities() []api.ConversationEntity {
	merged := make(map[string]api.ConversationEntity, len(m.sessionEntities)+len(m.activeRunEntities()))
	for id, entity := range m.sessionEntities {
		merged[id] = entity
	}
	for id, entity := range m.activeRunEntities() {
		merged[id] = entity
	}
	entities := make([]api.ConversationEntity, 0, len(merged))
	for _, entity := range merged {
		entities = append(entities, entity)
	}
	sort.Slice(entities, func(i, j int) bool {
		a, b := entities[i], entities[j]
		if a.Position.RootExecutionOrdinal != b.Position.RootExecutionOrdinal {
			return a.Position.RootExecutionOrdinal < b.Position.RootExecutionOrdinal
		}
		if a.Position.FirstSequence != b.Position.FirstSequence {
			return a.Position.FirstSequence < b.Position.FirstSequence
		}
		if a.Position.ItemIndex != b.Position.ItemIndex {
			return a.Position.ItemIndex < b.Position.ItemIndex
		}
		return a.ID < b.ID
	})
	return entities
}

func (m *model) activeRunEntities() map[string]api.ConversationEntity {
	if m.activeRun == nil {
		return nil
	}
	return m.activeRun.entities
}

func (m *model) renderTranscript() string {
	doc := m.ensureTranscript()
	if !doc.fullStyledReady {
		doc.fullStyled = m.renderFullTranscript()
		doc.fullStyledReady = true
	}
	return doc.fullStyled
}

func (m *model) renderFullTranscript() string {
	var styled strings.Builder
	entities := m.displayEntities()
	toolDepths := toolNestingDepths(entities)
	visibleEntityCount := 0
	previousEntityHasBody := false
	previousEntityEndsWithNewline := false
	for _, entity := range entities {
		prefix := ""
		if visibleEntityCount > 0 && previousEntityHasBody &&
			!previousEntityEndsWithNewline {
			prefix = "\n"
		}
		visibleEntityCount++
		if entity.Kind == "execution" {
			styled.WriteString(prefix)
			for _, line := range m.childSummaryLines(entity) {
				styled.WriteString(childSummaryRow(line, max(1, m.width)))
			}
			previousEntityHasBody, previousEntityEndsWithNewline = true, true
			continue
		}
		if entity.Kind == "tool" {
			styled.WriteString(prefix + nestedToolDisplayLine(entity, toolDepths[entity.ID]))
			previousEntityHasBody = true
			previousEntityEndsWithNewline = true
			continue
		}
		label := prefix + entityLabelStyle(entity, max(1, m.width))
		styled.WriteString(label)
		blocks, _ := m.bodyBlocksForSource("entity:"+entity.ID+":body", entity.Text, entity.Kind)
		currentEntityHasBody := false
		for _, block := range blocks {
			styled.WriteString(block.render)
			currentEntityHasBody = currentEntityHasBody || block.plain != ""
		}
		if entity.Kind != "execution" {
			if meta := entityMeta(entity); meta != "" {
				formatted, _ := m.renderedMetadataSource(meta, entity.Error != "" || entity.Status == "incomplete")
				styled.WriteString("\n" + formatted.styled)
				currentEntityHasBody = true
			}
		}
		previousEntityHasBody = currentEntityHasBody
		previousEntityEndsWithNewline = strings.HasSuffix(entity.Text, "\n")
		if meta := entityMeta(entity); meta != "" {
			previousEntityEndsWithNewline = strings.HasSuffix(meta, "\n")
		}
	}
	for index, entry := range m.entries {
		prefix := "\n"
		if (visibleEntityCount == 0 && index == 0) || !previousEntityHasBody ||
			previousEntityEndsWithNewline {
			prefix = ""
		}
		label := prefix + entryLabelStyle(entry.role)
		styled.WriteString(label)
		blocks, _ := m.bodyBlocksForSource(fmt.Sprintf("entry:%d:body", index), entry.text, entryBodyKind(entry.role))
		for _, block := range blocks {
			styled.WriteString(block.render)
		}
		previousEntityHasBody = entry.text != ""
		previousEntityEndsWithNewline = strings.HasSuffix(entry.text, "\n")
	}
	return styled.String()
}

func entityLabel(entity api.ConversationEntity) string {
	child := entity.ParentExecutionID != ""
	var label string
	switch entity.Kind {
	case "task":
		label = "you"
	case "assistant":
		label = "assistant"
	case "thinking":
		label = "thinking"
		if entity.ThinkingKind == "summary" {
			label += " summary"
		}
	case "tool":
		label = "tool"
		if entity.Name != "" {
			label += " / " + entity.Name
		}
	case "execution":
		label = "execution"
	default:
		label = entity.Kind
		if label == "" {
			label = "conversation"
		}
	}
	if child {
		label = fmt.Sprintf("child %s · %s", entity.ExecutionID, label)
	}
	return label
}

func toolDisplayLine(entity api.ConversationEntity) string {
	line := styledText(entityLabel(entity), yellowDimSGR)
	if preview := strings.Join(strings.Fields(entity.Preview), " "); preview != "" {
		line += styledText(" · "+preview, defaultDimSGR)
	}
	marker := toolStateMarker(entity)
	if marker == "✗" {
		marker = styledText(marker, redSGR)
	}
	return line + " " + marker + "\n"
}

func toolStateMarker(entity api.ConversationEntity) string {
	if entity.Error != "" || entity.Status == "incomplete" {
		return "✗"
	}
	switch strings.ToLower(entity.Outcome) {
	case "failed", "cancelled", "interrupted", "error":
		return "✗"
	}
	if entity.Status == "completed" {
		return "✓"
	}
	return "…"
}

const editorVerticalPadding = 1

// editorRows includes the full-width background above and below the textarea.
// Keep these rows in the live dock, never in the conversation or its receipts.
func (m *model) editorRows() []string {
	if m.authUI != nil && m.authUI.open {
		return nil
	}
	columns := max(1, m.width)
	styles := m.textarea.Styles()
	base := styles.Blurred.Base
	if m.textarea.Focused() {
		base = styles.Focused.Base
	}
	blank := base.Render(strings.Repeat(" ", columns))
	inputRows := renderer.WrapText(m.textarea.View(), columns)
	rows := make([]string, 0, len(inputRows)+2*editorVerticalPadding)
	for range editorVerticalPadding {
		rows = append(rows, blank)
	}
	rows = append(rows, inputRows...)
	for range editorVerticalPadding {
		rows = append(rows, blank)
	}
	return rows
}

func (m *model) screenFrame(footer []string) (renderer.Frame, *tea.Cursor, string, int, int, []string) {
	columns := max(1, m.width)
	rows := max(1, m.height)
	doc := m.ensureTranscript()
	openingReady := m.openingHeaderReady()
	footerRows := renderer.WrapText(strings.Join(footer, "\n"), columns)
	controlsRows := footerRows[:min(1, len(footerRows))]
	infoRows := footerRows[len(controlsRows):]
	inputRows := m.editorRows()
	childRows := m.childProgressRows()
	if m.screenOverlayOpen() {
		childRows = nil
	}
	available := rows - len(footerRows) - len(inputRows) - len(childRows) - 2
	commandRows := m.commandMenuRows(available)
	if m.pickerOpen && !m.screenOverlayOpen() {
		commandRows = m.pickerRows(available)
	}
	divider := m.footerDivider(columns)
	var committed []string
	var bodyRows []string
	committedThrough := doc.startRow
	bodyStart := doc.startRow
	bodyHeight := max(1, rows-len(footerRows)-len(inputRows)-len(childRows)-len(commandRows)-1)
	if m.screenOverlayOpen() {
		m.viewport.SetHeight(bodyHeight)
		m.refreshViewport(false)
		bodyRows = renderer.WrapText(m.viewport.View(), columns)
	} else {
		safeRow := len(doc.rows)
		if doc.sealPlainLimit >= 0 {
			safeRow = rowBoundaryForOffset(doc.rowStarts, doc.plain, doc.sealPlainLimit)
		}
		if m.headerPrinted || !m.deferTranscriptUntilHeader || openingReady {
			committedThrough = doc.startRow
			for _, part := range doc.parts {
				if part.mutable || part.rowEnd > safeRow {
					break
				}
				if part.rowEnd <= committedThrough {
					continue
				}
				// Only complete, immutable parts advance the scrollback boundary.
				committedThrough = part.rowEnd
			}
		}
		bodyStart = committedThrough
		if committedThrough > doc.startRow {
			committed = append(committed, doc.rows[doc.startRow:committedThrough]...)
		}
		bodyEnd := len(doc.rows)
		if m.deferTranscriptUntilHeader && !m.headerPrinted && !openingReady {
			// Input remains usable while orientation is pending. The opening
			// header and saved body appear together once metadata is ready.
			bodyStart, bodyEnd = 0, 0
		} else {
			for _, part := range doc.parts {
				if part.mutable {
					// Keep the preview inside the first mutable source block so later
					// conversation text cannot push its newest tail out of the live band.
					bodyStart = max(bodyStart, part.rowStart)
					bodyEnd = part.rowEnd
					break
				}
			}
		}
		bodyEnd = min(bodyEnd, len(doc.rows))
		bodyStart = min(bodyStart, bodyEnd)
		if bodyEnd-bodyStart > bodyHeight {
			bodyStart = bodyEnd - bodyHeight
		}
		if bodyStart < bodyEnd {
			bodyRows = append(bodyRows, doc.rows[bodyStart:bodyEnd]...)
		}
	}
	liveRows := make([]string, 0, len(bodyRows)+1+len(footerRows)+len(inputRows))
	liveRows = append(liveRows, bodyRows...)
	liveRows = append(liveRows, childRows...)
	liveRows = append(liveRows, commandRows...)
	liveRows = append(liveRows, divider)
	liveRows = append(liveRows, controlsRows...)
	liveRows = append(liveRows, inputRows...)
	liveRows = append(liveRows, infoRows...)
	frame := renderer.Frame{
		CommittedRows: committed,
		LiveRows:      liveRows,
		DockRows:      len(childRows) + len(commandRows) + 1 + len(inputRows) + len(footerRows),
		Cursor:        renderer.Cursor{Row: max(0, len(bodyRows)+len(childRows)+len(commandRows)+1+len(controlsRows)+editorVerticalPadding), Cell: 0},
		Size:          renderer.Size{Columns: columns, Rows: rows},
	}
	var cursor *tea.Cursor
	if m.authUI != nil && m.authUI.open {
		_, _, _, cursorLine, cursorCell := m.authRows()
		if cursorLine >= 0 {
			visibleRow := cursorLine - m.viewport.YOffset()
			if visibleRow >= 0 && visibleRow < len(bodyRows) {
				cursor = &tea.Cursor{Position: tea.Position{X: cursorCell, Y: visibleRow}}
				frame.Cursor = renderer.Cursor{Row: cursor.Position.Y, Cell: cursor.Position.X}
			}
		}
	} else if !m.pickerOpen {
		if textareaCursor := m.textarea.Cursor(); textareaCursor != nil {
			cursor = textareaCursor
			cursor.Position.Y += len(bodyRows) + len(childRows) + len(commandRows) + 1 + len(controlsRows) + editorVerticalPadding
			frame.Cursor = renderer.Cursor{Row: cursor.Position.Y, Cell: cursor.Position.X}
		}
	}
	content := strings.Join(liveRows, "\n")
	return frame, cursor, content, committedThrough, bodyStart, bodyRows
}

func (m *model) sealCurrentFrame() {
	m.sealTranscriptDocument()
}

func (m *model) applySessionSnapshot(snapshot api.ConversationSnapshot) {
	m.sessionEntities = cloneEntities(snapshot.Entities)
	m.receiptByPart = make(map[string]int)
	m.receiptOffset = 0
	m.lastTranscriptBytes = 0
	m.invalidateTranscript()
}

func cloneEntities(entities map[string]api.ConversationEntity) map[string]api.ConversationEntity {
	copyOf := make(map[string]api.ConversationEntity, len(entities))
	for id, entity := range entities {
		copyOf[id] = entity
	}
	return copyOf
}
