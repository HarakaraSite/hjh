// Package trace owns spike call identity and events. It is not a product history schema.
package trace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

type Event struct {
	Sequence int             `json:"sequence"`
	Time     time.Time       `json:"time"`
	ID       string          `json:"id"`
	Parent   string          `json:"parent,omitempty"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Phase    string          `json:"phase"`
	Input    json.RawMessage `json:"input,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type Recorder struct {
	mu       sync.Mutex
	sink     Sink
	sequence int
	next     int
	open     map[string]Event
	err      error
	Observe  func(Event)
}

// Sink decides when an event is saved. SQLite commits before Append returns.
type Sink interface {
	Append(Event) error
	Close() error
}

type fileSink struct {
	file    *os.File
	encoder *json.Encoder
}

func (s *fileSink) Append(e Event) error { return s.encoder.Encode(e) }
func (s *fileSink) Close() error         { return errors.Join(s.file.Sync(), s.file.Close()) }

func NewWithSink(sink Sink) *Recorder {
	return &Recorder{sink: sink, open: map[string]Event{}}
}

func New(path string) (*Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return NewWithSink(&fileSink{file: f, encoder: json.NewEncoder(f)}), nil
}

func (r *Recorder) append(e Event) {
	r.sequence++
	e.Sequence = r.sequence
	e.Time = time.Now().UTC()
	if err := r.sink.Append(e); err != nil && r.err == nil {
		r.err = err
	}
	if r.Observe != nil {
		r.Observe(e)
	}
}

func (r *Recorder) Start(parent, kind, name string, input json.RawMessage) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	e := Event{ID: fmt.Sprintf("call-%d", r.next), Parent: parent, Kind: kind, Name: name, Phase: "started", Input: input}
	r.open[e.ID] = e
	r.append(e)
	return e.ID
}

func (r *Recorder) End(id, phase string, result json.RawMessage, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.open[id]
	if !ok {
		return
	}
	delete(r.open, id)
	e.Phase = phase
	e.Input = nil
	e.Result = result
	if err != nil {
		e.Error = err.Error()
	}
	r.append(e)
}

// Settle closes tool calls whose JS finally blocks could not run after interruption.
// Execution is ended by its owner after all Go operations have stopped.
func (r *Recorder) Settle(execution, phase string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.open {
		if id == execution {
			continue
		}
		delete(r.open, id)
		e.Phase = phase
		e.Input = nil
		if err != nil {
			e.Error = err.Error()
		}
		r.append(e)
	}
}

func (r *Recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(r.err, r.sink.Close())
}

func Read(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	var events []Event
	for d.More() {
		var e Event
		if err := d.Decode(&e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}
