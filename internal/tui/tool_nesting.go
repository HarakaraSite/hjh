package tui

import (
	"strings"

	"hjh/internal/api"
)

// Tool starts precede their nested calls in the conversation's saved order.
// Resolve the depth from all visible entities, including already printed rows.
func toolNestingDepths(entities []api.ConversationEntity) map[string]int {
	depths := make(map[string]int)
	for _, entity := range entities {
		if entity.Kind != "tool" {
			continue
		}
		depth := 0
		if parentDepth, ok := depths[entity.ParentEntityID]; ok {
			depth = parentDepth + 1
		}
		depths[entity.ID] = depth
	}
	return depths
}

func nestedToolDisplayLine(entity api.ConversationEntity, depth int) string {
	var prefix string
	if depth > 0 {
		prefix = styledText(strings.Repeat("  ", depth)+"↳ ", defaultDimSGR)
	}
	return prefix + toolDisplayLine(entity)
}
