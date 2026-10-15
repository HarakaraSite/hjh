package app

import (
	"encoding/json"
	"sync"
)

// ContextMemory owns opt-in extension state for a Service's lifetime. Execution
// scopes are private copies; only a canonically adopted root publishes them.
type ContextMemory struct {
	mu       sync.Mutex
	sessions map[string]map[string]json.RawMessage
}

type contextMemoryScope struct {
	mu        sync.Mutex
	owner     *ContextMemory
	sessionID string
	values    map[string]json.RawMessage
}

func NewContextMemory() *ContextMemory {
	return &ContextMemory{sessions: make(map[string]map[string]json.RawMessage)}
}

func copyContextValues(values map[string]json.RawMessage) map[string]json.RawMessage {
	copy := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		copy[key] = append(json.RawMessage(nil), value...)
	}
	return copy
}

func (o *ContextMemory) begin(sessionID string) *contextMemoryScope {
	scope := &contextMemoryScope{owner: o, sessionID: sessionID, values: make(map[string]json.RawMessage)}
	if o != nil {
		o.mu.Lock()
		defer o.mu.Unlock()
		scope.values = copyContextValues(o.sessions[sessionID])
	}
	return scope
}

func (s *contextMemoryScope) get(key string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return append(json.RawMessage(nil), value...)
	}
	return json.RawMessage("null")
}

func (s *contextMemoryScope) set(key string, value json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append(json.RawMessage(nil), value...)
}

func (s *contextMemoryScope) publish() {
	if s.owner == nil {
		return
	}
	s.mu.Lock()
	values := copyContextValues(s.values)
	s.mu.Unlock()
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if len(values) != 0 {
		s.owner.sessions[s.sessionID] = values
	}
}

func (o *ContextMemory) deleteSession(id string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.sessions, id)
}
