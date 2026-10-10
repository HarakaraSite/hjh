package history

import (
	"encoding/json"
	"time"
)

// InitSessions adds only S-04 tables. S-02's event store remains the execution authority.
func (s *Store) InitSessions() error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS configurations(id TEXT PRIMARY KEY,descriptor TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS config_assets(config_id TEXT NOT NULL REFERENCES configurations(id),path TEXT NOT NULL,content BLOB NOT NULL,PRIMARY KEY(config_id,path))`,
		`CREATE TABLE IF NOT EXISTS sessions(id TEXT PRIMARY KEY,title TEXT NOT NULL,created TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS turns(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES sessions(id),ordinal INTEGER NOT NULL,run_id TEXT NOT NULL UNIQUE REFERENCES runs(id),config_id TEXT NOT NULL REFERENCES configurations(id),request TEXT NOT NULL,response TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL,result TEXT NOT NULL DEFAULT 'null',error TEXT NOT NULL DEFAULT '',UNIQUE(session_id,ordinal))`,
		`CREATE TABLE IF NOT EXISTS adoptions(session_id TEXT NOT NULL REFERENCES sessions(id),position INTEGER NOT NULL,turn_id TEXT NOT NULL UNIQUE REFERENCES turns(id),PRIMARY KEY(session_id,position))`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateSession(id, title string) error {
	_, err := s.db.Exec("INSERT INTO sessions(id,title,created) VALUES(?,?,?)", id, title, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type Turn struct {
	ID            string          `json:"id"`
	Session       string          `json:"session"`
	Ordinal       int             `json:"ordinal"`
	Run           string          `json:"run"`
	Configuration string          `json:"configuration"`
	Request       string          `json:"request"`
	Response      string          `json:"response,omitempty"`
	Outcome       string          `json:"outcome"`
	Result        json.RawMessage `json:"result"`
	Error         string          `json:"error,omitempty"`
	Adopted       bool            `json:"adopted"`
}

func (s *Store) StartTurn(session, id, run, configuration, request string) (Turn, error) {
	t := Turn{ID: id, Session: session, Run: run, Configuration: configuration, Request: request, Outcome: "running", Result: json.RawMessage("null")}
	tx, err := s.db.Begin()
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow("SELECT COALESCE(MAX(ordinal),0)+1 FROM turns WHERE session_id=?", session).Scan(&t.Ordinal); err != nil {
		return t, err
	}
	_, err = tx.Exec("INSERT INTO turns(id,session_id,ordinal,run_id,config_id,request,outcome) VALUES(?,?,?,?,?,?,?)", id, session, t.Ordinal, run, configuration, request, t.Outcome)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

func (s *Store) FinishTurn(id, outcome, response string, result json.RawMessage, message string) error {
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	_, err := s.db.Exec("UPDATE turns SET outcome=?,response=?,result=?,error=? WHERE id=?", outcome, response, string(result), message, id)
	return err
}

// Adopt is an explicit operation by the execution owner after a successful response is saved.
// The marker selects both request and response without a second conversation authority.
func (s *Store) Adopt(session, turn string, beforeCommit func()) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var position int
	if err = tx.QueryRow("SELECT COALESCE(MAX(position),0)+1 FROM adoptions WHERE session_id=?", session).Scan(&position); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO adoptions(session_id,position,turn_id) VALUES(?,?,?)", session, position, turn); err != nil {
		return err
	}
	if beforeCommit != nil {
		beforeCommit()
	}
	return tx.Commit()
}

type SessionInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Created string `json:"created"`
	Turns   int    `json:"turns"`
	Adopted int    `json:"adopted"`
	Events  int    `json:"events"`
}

func (s *Store) SessionInfo(id string) (SessionInfo, error) {
	v := SessionInfo{ID: id}
	err := s.db.QueryRow(`SELECT title,created,
(SELECT COUNT(*) FROM turns WHERE session_id=?),
(SELECT COUNT(*) FROM adoptions WHERE session_id=?),
(SELECT COUNT(*) FROM events e JOIN turns t ON t.run_id=e.run_id WHERE t.session_id=?)
FROM sessions WHERE id=?`, id, id, id, id).Scan(&v.Title, &v.Created, &v.Turns, &v.Adopted, &v.Events)
	return v, err
}
