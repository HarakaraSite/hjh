package sessionid

import "testing"

func TestLatestSelectsWorkspaceByUpdatedAtAndStableID(t *testing.T) {
	sessions := []Candidate{
		{ID: "other", WorkspaceRoot: "/other", UpdatedAt: "2026-10-14T12:00:00Z"},
		{ID: "old", WorkspaceRoot: "/work", UpdatedAt: "2026-10-13T10:00:00Z"},
		{ID: "latest-b", WorkspaceRoot: "/work", UpdatedAt: "2026-10-14T10:00:00Z"},
		{ID: "latest-a", WorkspaceRoot: "/work", UpdatedAt: "2026-10-14T10:00:00Z"},
	}
	if id, ok := Latest(sessions, "/work/."); !ok || id != "latest-a" {
		t.Fatalf("latest Session = %q, %t", id, ok)
	}
	if id, ok := Latest(sessions, "/empty"); ok || id != "" {
		t.Fatalf("empty workspace selected %q", id)
	}
}
