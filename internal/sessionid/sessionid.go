// Package sessionid provides the human-facing form and resolution of Session IDs.
// Persistence, API requests and in-memory ownership keep the complete ID.
package sessionid

import (
	"fmt"
	"strings"
)

const DisplayLength = 8

// Short returns eight characters, extending only to distinguish known IDs.
func Short(id string, known ...string) string {
	n := min(DisplayLength, len(id))
	for _, other := range known {
		if strings.EqualFold(id, other) {
			continue
		}
		for n < len(id) && strings.HasPrefix(strings.ToLower(other), strings.ToLower(id[:n])) {
			n++
		}
	}
	return id[:n]
}

// Resolve accepts an exact ID or a unique, case-insensitive prefix.
func Resolve(selector string, ids []string) (string, error) {
	for _, id := range ids {
		if strings.EqualFold(id, selector) {
			return id, nil
		}
	}
	var matches []string
	for _, id := range ids {
		if selector != "" && strings.HasPrefix(strings.ToLower(id), strings.ToLower(selector)) {
			matches = append(matches, id)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("session %q not found", Short(selector))
	}
	candidates := make([]string, len(matches))
	for i, id := range matches {
		candidates[i] = Short(id, ids...)
	}
	return "", fmt.Errorf("session prefix %q is ambiguous; use one of: %s", selector, strings.Join(candidates, ", "))
}
