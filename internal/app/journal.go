package app

import (
	"context"
	"encoding/json"
	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/history"
	"sync"
	"time"
)

type journal struct {
	mu                sync.Mutex
	ctx               context.Context
	store             *history.Store
	executionID       string
	parentExecutionID string
	control           *executionControl
	links             map[string][]history.Relation
	progress          contracts.Observer
	starts            map[string]string
	partial           map[int]string
	firstText         map[int]int64
}

func newJournal(ctx context.Context, store *history.Store, id string, progress contracts.Observer) *journal {
	return &journal{ctx: ctx, store: store, executionID: id, progress: progress, links: map[string][]history.Relation{}, starts: map[string]string{}, partial: map[int]string{}, firstText: map[int]int64{}}
}
func (j *journal) observe(o contracts.Observation) error {
	o.ExecutionID = j.executionID
	o.ParentExecutionID = j.parentExecutionID
	if o.Time.IsZero() {
		o.Time = time.Now().UTC()
	}
	j.mu.Lock()
	err := j.persist(o)
	j.mu.Unlock()
	if err != nil {
		return j.control.storage(err)
	}
	if j.progress != nil {
		return j.progress(o)
	}
	return nil
}
func (j *journal) persist(o contracts.Observation) error {
	if o.Time.IsZero() {
		o.Time = time.Now().UTC()
	}
	kind := "model_result"
	started := o.Phase == "started" || o.Phase == "start"
	switch o.Kind {
	case "model_tool", "tool", "generated", "execution", "agent":
		kind = "tool_result"
		if started {
			kind = "tool_call"
		}
		if o.Kind == "execution" {
			kind = "execution_admission"
		}
	case "host":
		kind = "effect_observation"
	case "hook":
		kind = "context_update"
	case "control":
		kind = "control_decision"
	case "model_request":
		kind = "model_request"
	case "assistant_text":
		kind = "assistant_message"
	}
	relations := append([]history.Relation(nil), j.links[o.ID]...)
	if parent := j.starts[o.Parent]; parent != "" {
		relations = append(relations, history.Relation{Relation: "parent_call", TargetRecordID: parent, Mandatory: true, Resolved: true})
	}
	if !started {
		if start := j.starts[o.ID]; start != "" {
			relations = append(relations, history.Relation{Relation: "call_start", TargetRecordID: start, Mandatory: true, Resolved: true})
		}
	}
	r, err := j.store.AppendOccurrence(j.ctx, j.executionID, history.OccurrenceInput{Kind: kind, Payload: contracts.JSON(o), Relations: relations})
	if err != nil {
		return err
	}
	if started && o.ID != "" {
		j.starts[o.ID] = r.ID
	}
	if o.Kind == "assistant_text" {
		var result struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(o.Result, &result) == nil {
			j.partial[o.ModelStep] = result.Text
		}
		if j.firstText[o.ModelStep] == 0 {
			j.firstText[o.ModelStep] = r.Ordinal
		}
		if err = j.store.SetAssistantText(j.ctx, j.executionID, "parent", int64(o.ModelStep), int64(o.ModelStep), j.firstText[o.ModelStep], contracts.JSON(map[string]any{"kind": "assistant_message", "observedAt": o.Time, "payload": map[string]any{"text": j.partial[o.ModelStep]}})); err != nil {
			return err
		}
	}
	return nil
}

// executionSnapshot preserves the exact resolved Worker composition.
func executionSnapshot(snapshot extensions.Snapshot) ([]byte, error) {
	return snapshot.JSON()
}

func (j *journal) startReference(id string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.starts[id]
}
func (j *journal) link(id string, relation history.Relation) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.links[id] = append(j.links[id], relation)
}
