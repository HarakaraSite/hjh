package historyview

import (
	"strings"
	"testing"

	"hjh/internal/history"
)

func TestRenderSessionsExtendsCollidingShortIDs(t *testing.T) {
	title := "same work"
	sessions := []history.Session{
		{ID: "session-alpha", WorkspaceRoot: "/work/a", Title: &title, TurnCount: 2, UpdatedAt: "2026-10-12T10:00:00Z"},
		{ID: "session-beta", WorkspaceRoot: "/work/b", Title: &title, TurnCount: 3, UpdatedAt: "2026-10-12T11:00:00Z"},
	}
	got := RenderSessions(sessions)
	for _, expected := range []string{"session-a", "session-b", "same work", "/work/a", "2 turns", "updated 2026-10-12T10:00:00Z"} {
		if !strings.Contains(got, expected) {
			t.Errorf("sessions output omits %q: %s", expected, got)
		}
	}
	if strings.Contains(got, "session-alpha") || strings.Contains(got, "session-beta") {
		t.Fatalf("sessions output did not shorten IDs: %s", got)
	}
}

func TestSelectSessionUsesSharedCaseInsensitivePrefixResolution(t *testing.T) {
	sessions := []history.Session{{ID: "Session-Alpha"}, {ID: "Session-Beta"}}
	selected, ok, err := SelectSession(sessions, "", "SESSION-B")
	if err != nil || !ok || selected.ID != "Session-Beta" {
		t.Fatalf("selected = %+v, %t, %v", selected, ok, err)
	}
	if _, _, err := SelectSession(sessions, "", "session-"); err == nil || !strings.Contains(err.Error(), "Session-A") || !strings.Contains(err.Error(), "Session-B") {
		t.Fatalf("ambiguous result omitted distinguishable candidates: %v", err)
	}
}
