package main

import (
	"hjh/internal/api"
	"hjh/internal/history"
)

func historySessionIDs(sessions []history.Session) []string {
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
	}
	return ids
}

func apiSessionIDs(sessions []api.Session) []string {
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
	}
	return ids
}
