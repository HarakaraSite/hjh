package sessionid

import (
	"path/filepath"
	"sort"
	"time"
)

// Candidate contains only the metadata needed to select a saved Session.
// CLI history and TUI resume use the same workspace scope and ordering.
type Candidate struct{ ID, WorkspaceRoot, UpdatedAt string }

func Latest(sessions []Candidate, workspace string) (string, bool) {
	filtered := make([]Candidate, 0, len(sessions))
	for _, session := range sessions {
		if workspace == "" || filepath.Clean(session.WorkspaceRoot) == filepath.Clean(workspace) {
			filtered = append(filtered, session)
		}
	}
	if len(filtered) == 0 {
		return "", false
	}
	sort.Slice(filtered, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, filtered[i].UpdatedAt)
		right, rightErr := time.Parse(time.RFC3339Nano, filtered[j].UpdatedAt)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.After(right)
		}
		if filtered[i].UpdatedAt != filtered[j].UpdatedAt {
			return filtered[i].UpdatedAt > filtered[j].UpdatedAt
		}
		return filtered[i].ID < filtered[j].ID
	})
	return filtered[0].ID, true
}
