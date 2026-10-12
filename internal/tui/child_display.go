package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/sessionid"
)

// Filter only the normal transcript. The complete entities remain available
// for explicit /tools, /tool and /inspect operations.
func (m *model) displayEntities() []api.ConversationEntity {
	entities := m.mergedEntities()
	visible := make([]api.ConversationEntity, 0, len(entities))
	for _, entity := range entities {
		if entity.ParentExecutionID != "" {
			if entity.Kind != "execution" {
				continue
			}
		} else if entity.Kind == "execution" {
			continue
		}
		if entity.Kind == "assistant" && strings.TrimSpace(entity.Text) == "" {
			continue
		}
		visible = append(visible, entity)
	}
	return visible
}

type childSummary struct {
	key, text, style string
}

func (m *model) childSummaryLines(entity api.ConversationEntity) []childSummary {
	label := "child " + m.shortChildID(entity.ExecutionID)
	lines := []childSummary{{"start", label + " · started", magentaDimSGR}}
	if entity.Status != "completed" && entity.Status != "incomplete" {
		return lines
	}
	state, style := "completed", magentaDimSGR
	if entity.Status == "incomplete" || entity.Error != "" || entity.Outcome == "failed" {
		state, style = "failed", redSGR
	}
	if entity.Outcome == "cancelled" || entity.Outcome == "interrupted" {
		state = entity.Outcome
	}
	text := label + " · " + state
	if entity.Error != "" {
		text += " · " + strings.Join(strings.Fields(entity.Error), " ")
	}
	lines = append(lines, childSummary{"terminal", text, style})
	return lines
}

func (m *model) shortChildID(id string) string {
	known := []string{}
	for _, entities := range []map[string]api.ConversationEntity{m.sessionEntities, m.activeRunEntities()} {
		for _, entity := range entities {
			if entity.Kind == "execution" && entity.ParentExecutionID != "" {
				known = append(known, entity.ExecutionID)
			}
		}
	}
	return sessionid.Short(id, known...)
}

func childSummaryRow(line childSummary, width int) string {
	return styledText(ansi.Truncate(line.text, max(1, width), "…"), line.style) + "\n"
}

// Progress belongs to the live band, so updating a child tool never appends
// another activity line to scrollback or blocks a parent's streaming body.
func (m *model) childProgressRows() []string {
	entities := m.activeRunEntities()
	children := []api.ConversationEntity{}
	latestTools := map[string]api.ConversationEntity{}
	for _, entity := range entities {
		if entity.ParentExecutionID == "" {
			continue
		}
		if entity.Kind == "execution" && entity.Status != "completed" && entity.Status != "incomplete" {
			children = append(children, entity)
		}
		if entity.Kind == "tool" && entity.Status == "streaming" {
			previous := latestTools[entity.ExecutionID]
			if previous.ID == "" || entity.Position.FirstSequence > previous.Position.FirstSequence ||
				entity.Position.FirstSequence == previous.Position.FirstSequence && entity.ID > previous.ID {
				latestTools[entity.ExecutionID] = entity
			}
		}
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].Position.FirstSequence != children[j].Position.FirstSequence {
			return children[i].Position.FirstSequence < children[j].Position.FirstSequence
		}
		return children[i].ID < children[j].ID
	})
	rows := make([]string, 0, len(children))
	for _, child := range children {
		text := "child " + m.shortChildID(child.ExecutionID) + " · working"
		if tool := latestTools[child.ExecutionID]; tool.Name != "" {
			text += " · " + tool.Name
		}
		rows = append(rows, styledText(ansi.Truncate(text, max(1, m.width), "…"), defaultDimSGR))
	}
	return rows
}
