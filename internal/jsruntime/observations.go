package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"hjh/internal/contracts"
)

type activeObservation struct {
	started contracts.Observation
	order   int
	// The agent loop records model-declared calls; the runtime owns nested
	// calls and must not save a second start/result for the same invocation.
	external bool
}

type delegationObservationEnd struct {
	phase string
	err   error
}

func (o *vmOwner) start(parent, kind, name string, input json.RawMessage) (string, error) {
	return o.startObservation(uuid.NewString(), parent, kind, name, input, false)
}

func (o *vmOwner) startCall(modelCallID, parent, name string, input json.RawMessage) (string, error) {
	if modelCallID == "" {
		return o.start(parent, "tool", name, input)
	}
	return o.startObservation(modelCallID, parent, "tool", name, input, true)
}

func (o *vmOwner) startObservation(id, parent, kind, name string, input json.RawMessage, external bool) (string, error) {
	if len(input) == 0 || !json.Valid(input) {
		input = json.RawMessage("null")
	}
	observation := contracts.Observation{
		ID: id, Parent: parent, Kind: kind, Name: name, Phase: "started", Time: time.Now().UTC(),
		Input: append(json.RawMessage(nil), input...),
	}
	if !external && o.options.Observer != nil {
		if err := o.options.Observer(observation); err != nil {
			return "", err
		}
	}
	o.sequence++
	o.active[id] = activeObservation{started: observation, order: o.sequence, external: external}
	if o.callCtx != nil {
		o.callStarts[id] = observation
	}
	return id, nil
}

func (o *vmOwner) end(id, phase string, result json.RawMessage, eventErr error) error {
	active, ok := o.active[id]
	if !ok {
		return nil
	}
	delete(o.active, id)
	if len(result) == 0 || !json.Valid(result) {
		result = json.RawMessage("null")
	}
	observation := active.started
	observation.Phase = phase
	observation.Time = time.Now().UTC()
	observation.Input = nil
	observation.Result = append(json.RawMessage(nil), result...)
	if eventErr != nil {
		observation.Error = eventErr.Error()
	}
	if !active.external && o.options.Observer != nil {
		return o.options.Observer(observation)
	}
	return nil
}

func (o *vmOwner) markDelegationAncestors(agentID, phase string, eventErr error) {
	started, ok := o.callStarts[agentID]
	if !ok {
		return
	}
	for parent := started.Parent; parent != "" && parent != o.callRoot; {
		observation, exists := o.callStarts[parent]
		if !exists {
			return
		}
		if observation.Kind == "tool" || observation.Kind == "hook" {
			if _, active := o.active[parent]; active {
				previous, set := o.delegationEnds[parent]
				if !set || terminalPriority(phase) > terminalPriority(previous.phase) {
					o.delegationEnds[parent] = delegationObservationEnd{phase: phase, err: eventErr}
				}
			}
		}
		parent = observation.Parent
	}
}

func (o *vmOwner) closeDelegationObservations() error {
	scopes := make(map[string]struct{}, len(o.delegationEnds))
	for id := range o.delegationEnds {
		scopes[id] = struct{}{}
	}
	for id, started := range o.callStarts {
		if started.Kind == "tool" && started.Name == "run_agent" {
			if _, active := o.active[id]; active {
				scopes[id] = struct{}{}
			}
		}
	}
	cancelled := delegationObservationEnd{phase: "cancelled", err: context.Canceled}
	for id, started := range o.callStarts {
		if _, active := o.active[id]; !active || (started.Kind != "tool" && started.Kind != "hook") {
			continue
		}
		if _, owned := scopes[id]; owned {
			o.delegationEnds[id] = cancelled
			continue
		}
		for parent := started.Parent; parent != ""; {
			if _, inScope := scopes[parent]; inScope {
				o.delegationEnds[id] = cancelled
				break
			}
			ancestor, exists := o.callStarts[parent]
			if !exists {
				break
			}
			parent = ancestor.Parent
		}
	}
	ids := make([]string, 0, len(o.delegationEnds))
	for id := range o.delegationEnds {
		// The outer call has its own result and is ended by call(), including
		// a direct model call to run_agent. Keep it as a descendant scope seed.
		if id == o.callRoot {
			continue
		}
		if _, active := o.active[id]; active {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return o.active[ids[i]].order > o.active[ids[j]].order })
	var resultErr error
	for _, id := range ids {
		terminal := o.delegationEnds[id]
		resultErr = errors.Join(resultErr, o.end(id, terminal.phase, json.RawMessage("null"), terminal.err))
	}
	o.delegationEnds = map[string]delegationObservationEnd{}
	return resultErr
}

func terminalPriority(phase string) int {
	switch phase {
	case "cancelled":
		return 3
	case "failed":
		return 2
	default:
		return 1
	}
}

func (o *vmOwner) finishOpen(phase string, err error) {
	open := make([]activeObservation, 0, len(o.active))
	for _, active := range o.active {
		open = append(open, active)
	}
	sort.Slice(open, func(i, j int) bool { return open[i].order > open[j].order })
	for _, active := range open {
		_ = o.end(active.started.ID, phase, json.RawMessage("null"), err)
	}
}
