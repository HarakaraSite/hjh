// Package history persists spike evidence. Views are derived from append-only events.
package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"hjh/spikes/s01/internal/trace"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON",
		`CREATE TABLE IF NOT EXISTS runs (id TEXT PRIMARY KEY, owner_pid INTEGER NOT NULL, created TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS events (
run_id TEXT NOT NULL REFERENCES runs(id), sequence INTEGER NOT NULL,
call_id TEXT NOT NULL, parent_id TEXT NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL,
phase TEXT NOT NULL, payload TEXT NOT NULL, PRIMARY KEY (run_id,sequence))`,
		`CREATE INDEX IF NOT EXISTS events_call ON events(run_id,call_id,sequence)`,
	} {
		if _, err = db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

func OpenReadOnly(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Register(run string) error {
	_, err := s.db.Exec("INSERT INTO runs(id,owner_pid,created) VALUES(?,?,?)", run, os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type Settings struct {
	Engine      string `json:"engine"`
	Journal     string `json:"journal"`
	Synchronous int    `json:"synchronous"`
}

func (s *Store) Settings() (result Settings, err error) {
	if err = s.db.QueryRow("SELECT sqlite_version()").Scan(&result.Engine); err != nil {
		return
	}
	if err = s.db.QueryRow("PRAGMA journal_mode").Scan(&result.Journal); err != nil {
		return
	}
	err = s.db.QueryRow("PRAGMA synchronous").Scan(&result.Synchronous)
	return
}

type sink struct {
	store        *Store
	run          string
	beforeCommit func(trace.Event)
}

// BeforeCommit is an experiment-only gate for terminating a real writer after INSERT.
func (s *Store) Sink(run string, beforeCommit func(trace.Event)) trace.Sink {
	return &sink{store: s, run: run, beforeCommit: beforeCommit}
}
func (s *sink) Close() error { return nil } // Store owns the connection, shared by recorders.
func (s *sink) Append(e trace.Event) error {
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = insert(tx, s.run, e); err != nil {
		return err
	}
	if s.beforeCommit != nil {
		s.beforeCommit(e)
	}
	return tx.Commit()
}

func insert(tx *sql.Tx, run string, e trace.Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO events(run_id,sequence,call_id,parent_id,kind,name,phase,payload) VALUES(?,?,?,?,?,?,?,?)`, run, e.Sequence, e.ID, e.Parent, e.Kind, e.Name, e.Phase, string(b))
	return err
}

func (s *Store) Events(run string) ([]trace.Event, error) {
	rows, err := s.db.Query("SELECT payload FROM events WHERE run_id=? ORDER BY sequence", run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []trace.Event
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var e trace.Event
		if err = json.Unmarshal([]byte(b), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ReconcileStopped is called only by the experiment owner after writer Wait.
// It is not startup reconciliation and never scans or alters other runs.
func (s *Store) ReconcileStopped(run, reason string) error {
	events, err := s.Events(run)
	if err != nil {
		return err
	}
	open := map[string]trace.Event{}
	root := ""
	sequence := 0
	for _, e := range events {
		sequence = e.Sequence
		if e.Phase == "started" {
			open[e.ID] = e
			if e.Kind == "execution" {
				root = e.ID
			}
		} else {
			delete(open, e.ID)
		}
	}
	if _, ok := open[root]; !ok {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Preserve start order and put the execution's interruption last.
	for _, e := range events {
		if e.Phase != "started" || e.ID == root {
			continue
		}
		if _, ok := open[e.ID]; !ok {
			continue
		}
		sequence++
		e.Sequence = sequence
		e.Time = time.Now().UTC()
		e.Phase = "outcome_unknown"
		e.Input = nil
		e.Error = reason
		if err = insert(tx, run, e); err != nil {
			return err
		}
	}
	e := open[root]
	sequence++
	e.Sequence = sequence
	e.Time = time.Now().UTC()
	e.Phase = "interrupted"
	e.Input = nil
	e.Error = reason
	if err = insert(tx, run, e); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Integrity() error {
	var check string
	if err := s.db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("SQLite integrity_check: %s", check)
	}
	return nil
}
