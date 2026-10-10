package history

import (
	"encoding/json"
	"hjh/spikes/s01/internal/trace"
)

type Pair struct {
	Turn          string `json:"turn"`
	Position      int    `json:"position"`
	Configuration string `json:"configuration"`
	Request       string `json:"request"`
	Response      string `json:"response"`
}
type Window struct {
	Pairs       []Pair `json:"pairs"`
	RowsDecoded int    `json:"rowsDecoded"`
	TextBytes   int    `json:"textBytes"`
}

func conversationQuery(before int) (string, []any) {
	query := `SELECT a.turn_id,a.position,t.config_id,t.request,t.response FROM adoptions a JOIN turns t ON t.id=a.turn_id WHERE a.session_id=?`
	var args []any
	if before != 0 {
		query += " AND a.position<?"
		args = append(args, before)
	}
	return query + " ORDER BY a.position DESC LIMIT ?", args
}

// Conversation returns only the selected adopted pairs; it never reads event or asset payloads.
func (s *Store) Conversation(session string, before, count int) (Window, error) {
	v := Window{Pairs: []Pair{}}
	query, tail := conversationQuery(before)
	args := append([]any{session}, tail...)
	args = append(args, count)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Pair
		if err = rows.Scan(&p.Turn, &p.Position, &p.Configuration, &p.Request, &p.Response); err != nil {
			return v, err
		}
		v.Pairs = append(v.Pairs, p)
		v.RowsDecoded++
		v.TextBytes += len(p.Request) + len(p.Response)
	}
	for left, right := 0, len(v.Pairs)-1; left < right; left, right = left+1, right-1 {
		v.Pairs[left], v.Pairs[right] = v.Pairs[right], v.Pairs[left]
	}
	return v, rows.Err()
}

func (s *Store) ConversationPlan(session string, before, count int) ([]string, error) {
	query, tail := conversationQuery(before)
	args := append([]any{session}, tail...)
	args = append(args, count)
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return nil, err
		}
		result = append(result, detail)
	}
	return result, rows.Err()
}

func (s *Store) Turns(session string, after, count int) ([]Turn, error) {
	rows, err := s.db.Query(`SELECT t.id,t.ordinal,t.run_id,t.config_id,t.request,t.response,t.outcome,t.result,t.error,EXISTS(SELECT 1 FROM adoptions a WHERE a.turn_id=t.id) FROM turns t WHERE t.session_id=? AND t.ordinal>? ORDER BY t.ordinal LIMIT ?`, session, after, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	turns := []Turn{}
	for rows.Next() {
		var t Turn
		t.Session = session
		var result string
		if err = rows.Scan(&t.ID, &t.Ordinal, &t.Run, &t.Configuration, &t.Request, &t.Response, &t.Outcome, &result, &t.Error, &t.Adopted); err != nil {
			return nil, err
		}
		t.Result = json.RawMessage(result)
		turns = append(turns, t)
	}
	return turns, rows.Err()
}

func (s *Store) EventPage(run string, after, count int) ([]trace.Event, error) {
	rows, err := s.db.Query("SELECT payload FROM events WHERE run_id=? AND sequence>? ORDER BY sequence LIMIT ?", run, after, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []trace.Event{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var e trace.Event
		if err = json.Unmarshal([]byte(data), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
