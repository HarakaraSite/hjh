package history

import (
	"encoding/json"
	"fmt"

	"hjh/spikes/s01/internal/trace"
)

type Call struct {
	ID     string          `json:"id"`
	Parent string          `json:"parent,omitempty"`
	Kind   string          `json:"kind"`
	Name   string          `json:"name"`
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type View struct {
	Run      string        `json:"run"`
	OwnerPID int           `json:"ownerPID"`
	Root     string        `json:"root"`
	Status   string        `json:"status"`
	Events   []trace.Event `json:"events"`
	Calls    []Call        `json:"calls"`
}

func (s *Store) Read(run string) (View, error) {
	v := View{Run: run}
	if err := s.db.QueryRow("SELECT owner_pid FROM runs WHERE id=?", run).Scan(&v.OwnerPID); err != nil {
		return v, err
	}
	events, err := s.Events(run)
	if err != nil {
		return v, err
	}
	v.Events = events
	indexes := map[string]int{}
	for i, e := range events {
		if e.Sequence != i+1 {
			return v, fmt.Errorf("event sequence: %d instead of %d", e.Sequence, i+1)
		}
		if e.Phase == "started" {
			if _, exists := indexes[e.ID]; exists {
				return v, fmt.Errorf("duplicate call start: %s", e.ID)
			}
			if e.Parent != "" {
				if _, exists := indexes[e.Parent]; !exists {
					return v, fmt.Errorf("missing parent: %s", e.Parent)
				}
			}
			indexes[e.ID] = len(v.Calls)
			v.Calls = append(v.Calls, Call{ID: e.ID, Parent: e.Parent, Kind: e.Kind, Name: e.Name, Status: "pending", Input: e.Input})
			if e.Kind == "execution" {
				v.Root = e.ID
			}
		} else {
			index, exists := indexes[e.ID]
			if !exists {
				return v, fmt.Errorf("end without start: %s", e.ID)
			}
			c := &v.Calls[index]
			if c.Status != "pending" {
				return v, fmt.Errorf("duplicate terminal evidence: %s", e.ID)
			}
			c.Status = e.Phase
			c.Result = e.Result
			c.Error = e.Error
		}
	}
	if index, ok := indexes[v.Root]; ok {
		v.Status = v.Calls[index].Status
	}
	return v, nil
}
