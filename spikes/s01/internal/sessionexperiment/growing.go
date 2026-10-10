package sessionexperiment

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"hjh/spikes/s01/internal/session"
)

func (s *suite) growing() (any, error) {
	id := s.prefix + "-large"
	store, err := open(s.db)
	if err != nil {
		return nil, err
	}
	if err = store.CreateSession(id, "S-04 growing history"); err != nil {
		store.Close()
		return nil, err
	}
	p, err := session.Prepare(store, s.settings, s.Exe, s.Work)
	err = errors.Join(err, store.Close())
	if err != nil {
		return nil, err
	}
	var first session.Result
	run := func(i int) error {
		name := fmt.Sprintf("history_%04d", i)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		r, err := s.execute(ctx, name, id, session.GeneratedJS, s.server.URL, p, true)
		if i == 1 {
			first = r
		}
		return err
	}
	for i := 1; i <= 3; i++ {
		if err = run(i); err != nil {
			return nil, err
		}
	}
	small, err := s.inspect("history_small", id, 0, 0, 3)
	if err != nil {
		return nil, err
	}
	for i := 4; i <= s.HistoryTurns+3; i++ {
		if err = run(i); err != nil {
			return nil, err
		}
		if (i-3)%20 == 0 {
			fmt.Printf("history progress: %d additional real turns\n", i-3)
		}
	}
	large, err := s.inspect("history_large", id, 0, 0, 3)
	if err != nil {
		return nil, err
	}
	if len(large.Conversation.Pairs) < 3 {
		return large, errors.New("latest conversation window incomplete")
	}
	before := large.Conversation.Pairs[0].Position
	older, err := s.inspect("history_older", id, before, 0, 3)
	if err != nil {
		return nil, err
	}
	turnPage, err := s.inspect("history_turn_page", id, 0, 3, 3)
	if err != nil {
		return nil, err
	}
	store, err = open(s.db)
	if err != nil {
		return nil, err
	}
	events, e1 := store.EventPage(first.Run, 0, 3)
	next, e2 := store.EventPage(first.Run, 3, 3)
	err = errors.Join(e1, e2, store.Close())
	if err != nil {
		return nil, err
	}
	if err = writeJSON(filepath.Join(s.Out, "history_event_pages.json"), map[string]any{"run": first.Run, "first": events, "next": next}); err != nil {
		return nil, err
	}
	return map[string]any{"session": id, "additionalRealTurns": s.HistoryTurns, "totalTurns": large.Info.Turns, "totalEvents": large.Info.Events, "smallWindowRows": small.Conversation.RowsDecoded, "largeWindowRows": large.Conversation.RowsDecoded, "smallInputBytes": small.InputBytes, "largeInputBytes": large.InputBytes, "smallWindowAllocatedBytes": small.WindowAllocatedBytes, "largeWindowAllocatedBytes": large.WindowAllocatedBytes, "smallWindowDurationUS": small.WindowDurationUS, "largeWindowDurationUS": large.WindowDurationUS, "queryPlan": large.Plan, "latest": "history_large-inspection.json", "older": "history_older-inspection.json", "turnPage": "history_turn_page-inspection.json", "eventPages": "history_event_pages.json", "small": "history_small-inspection.json"}, check(large.Info.Turns == s.HistoryTurns+3 && large.Info.Adopted == s.HistoryTurns+3 && large.Info.Events > small.Info.Events && small.Conversation.RowsDecoded == 3 && large.Conversation.RowsDecoded == 3 && len(large.Input) == 6 && len(older.Conversation.Pairs) == 3 && older.Conversation.Pairs[len(older.Conversation.Pairs)-1].Position < before && len(turnPage.Turns) == 3 && turnPage.Turns[0].Ordinal == 4 && len(events) == 3 && len(next) == 3 && events[0].Sequence == 1 && next[0].Sequence == 4 && large.Turns[0].ID == first.Turn, "history was truncated, projected by loading all rows, or page boundaries mismatched")
}
