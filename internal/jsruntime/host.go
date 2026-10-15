package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-quickjs/go-quickjs"
	"hjh/internal/contracts"
	"hjh/internal/host"
)

func (o *vmOwner) startHost(name, parent string, input json.RawMessage) (quickjs.Value, error) {
	if o.callCtx == nil {
		return quickjs.Value{}, errors.New("host operation called outside a Worker call")
	}
	if len(input) == 0 || !json.Valid(input) {
		return quickjs.Value{}, fmt.Errorf("host operation %s arguments are not valid JSON", name)
	}
	id, err := o.start(parent, "host", name, input)
	if err != nil {
		return quickjs.Value{}, err
	}
	promise := o.rt.NewPromise()
	o.pending[id] = promise
	o.jobs.Add(1)
	ctx := o.callCtx
	go func() {
		defer o.jobs.Done()
		value, callErr := o.host.Call(ctx, name, input)
		var observation json.RawMessage
		if projected, ok := value.(host.ObservedResult); ok {
			value = projected.Value
			var err error
			observation, err = json.Marshal(projected.Observation)
			callErr = errors.Join(callErr, err)
		}
		encoded, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			callErr = errors.Join(callErr, encodeErr)
		}
		o.queueCompletion(jobCompletion{id: id, value: encoded, observation: observation, err: callErr})
	}()
	return promise.Value(), nil
}

func (o *vmOwner) startHostValue(name, parent, input string) quickjs.Value {
	promise, err := o.startHost(name, parent, json.RawMessage(input))
	if err != nil {
		return o.rejectedPromise(err)
	}
	return promise
}

func (o *vmOwner) startAgentValue(parent, input string) quickjs.Value {
	promise, err := o.startAgent(parent, json.RawMessage(input))
	if err != nil {
		return o.rejectedPromise(err)
	}
	return promise
}

func (o *vmOwner) startAgent(parent string, input json.RawMessage) (quickjs.Value, error) {
	if o.callCtx == nil || o.agentCtx == nil {
		return quickjs.Value{}, errors.New("agent operation called outside a Worker call")
	}
	if len(input) == 0 || !json.Valid(input) {
		return quickjs.Value{}, errors.New("agent operation arguments are not valid JSON")
	}
	var arguments struct {
		Task  *string                       `json:"task"`
		Role  string                        `json:"role"`
		Model *contracts.AgentModelOverride `json:"model"`
		Tools *[]string                     `json:"tools"`
	}
	if err := json.Unmarshal(input, &arguments); err != nil {
		return quickjs.Value{}, fmt.Errorf("agent operation arguments are not valid: %w", err)
	}
	if arguments.Task == nil {
		return quickjs.Value{}, errors.New("api.agents.run requires a string task field")
	}
	callID, err := o.start(parent, "agent", "agents.run", input)
	if err != nil {
		return quickjs.Value{}, err
	}
	promise := o.rt.NewPromise()
	o.agentPending[callID] = promise

	ctx, cancel := context.WithCancel(o.agentCtx)
	job := &agentJob{cancel: cancel, done: make(chan struct{})}
	o.agentJobs[callID] = job
	o.jobs.Add(1)
	request := contracts.AgentRunRequest{Task: *arguments.Task, Role: arguments.Role, Model: arguments.Model, Tools: arguments.Tools, CallID: callID}
	go func() {
		defer close(job.done)
		defer o.jobs.Done()
		var result contracts.AgentRunResult
		var runErr error
		if o.options.Agents == nil {
			runErr = errors.New("agent runner is not configured")
		} else {
			result, runErr = o.options.Agents.RunAgent(ctx, request)
		}
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil {
			runErr = errors.Join(runErr, encodeErr)
		}
		o.queueCompletion(jobCompletion{id: callID, value: encoded, err: runErr, agent: true})
	}()
	return promise.Value(), nil
}

func (o *vmOwner) closeAgentScope() {
	if o.agentClosed {
		return
	}
	o.agentClosed = true
	if o.agentCancel != nil {
		o.agentCancel()
	}
	for _, job := range o.agentJobs {
		job.cancel()
	}
}

func (o *vmOwner) joinAgents() error {
	o.closeAgentScope()
	jobs := make([]*agentJob, 0, len(o.agentJobs))
	for _, job := range o.agentJobs {
		jobs = append(jobs, job)
	}
	for _, job := range jobs {
		<-job.done
	}
	return o.recordAgentCompletionsAfterSettlement()
}

func (o *vmOwner) recordAgentCompletionsAfterSettlement() error {
	var retained []jobCompletion
	var resultErr error
	o.completeMu.Lock()
	completions := o.completions
	o.completions = nil
	o.completeMu.Unlock()
	for _, completion := range completions {
		if !completion.agent {
			retained = append(retained, completion)
			continue
		}
		resultErr = errors.Join(resultErr, o.settleAgent(completion, false))
	}
	if len(retained) > 0 {
		o.completeMu.Lock()
		o.completions = append(retained, o.completions...)
		o.completeMu.Unlock()
	}
	return resultErr
}

func (o *vmOwner) rejectedPromise(err error) quickjs.Value {
	promise := o.rt.NewPromise()
	_ = promise.Reject(err.Error())
	return promise.Value()
}

func (o *vmOwner) resolveHost(completion jobCompletion) error {
	promise := o.pending[completion.id]
	if promise == nil {
		return nil
	}
	phase := completionPhase(completion.err)
	endErr := o.end(completion.id, phase, completion.observationValue(), completion.err)
	if endErr != nil {
		completion.err = errors.Join(completion.err, endErr)
	}
	var promiseErr error
	if completion.err == nil {
		promiseErr = promise.Resolve(string(completion.value))
	} else {
		promiseErr = promise.Reject(completion.err.Error())
	}
	delete(o.pending, completion.id)
	return errors.Join(endErr, promiseErr)
}

func (o *vmOwner) resolveAgent(completion jobCompletion) error {
	return o.settleAgent(completion, true)
}

func (o *vmOwner) settleAgent(completion jobCompletion, deliver bool) error {
	promise := o.agentPending[completion.id]
	if promise == nil {
		return nil
	}
	phase := completionPhase(completion.err)
	o.markDelegationAncestors(completion.id, phase, completion.err)
	endErr := o.end(completion.id, phase, completion.value, completion.err)
	if endErr != nil {
		completion.err = errors.Join(completion.err, endErr)
	}
	var promiseErr error
	if deliver {
		if completion.err == nil {
			promiseErr = promise.Resolve(string(completion.value))
		} else {
			promiseErr = promise.Reject(completion.err.Error())
		}
	}
	delete(o.agentPending, completion.id)
	return errors.Join(endErr, promiseErr)
}

func completionPhase(err error) string {
	if err == nil {
		return "completed"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	return "failed"
}

func (o *vmOwner) queueCompletion(completion jobCompletion) {
	o.completeMu.Lock()
	o.completions = append(o.completions, completion)
	o.completeMu.Unlock()
	select {
	case o.completeWake <- struct{}{}:
	default:
	}
}

func (o *vmOwner) hasCompletions() bool {
	o.completeMu.Lock()
	defer o.completeMu.Unlock()
	return len(o.completions) != 0
}

func (o *vmOwner) resolveCompletions() error {
	for {
		o.completeMu.Lock()
		if len(o.completions) == 0 {
			o.completeMu.Unlock()
			return nil
		}
		completion := o.completions[0]
		o.completions[0] = jobCompletion{}
		o.completions = o.completions[1:]
		o.completeMu.Unlock()
		var err error
		if completion.agent {
			if o.agentClosed {
				err = o.settleAgent(completion, false)
			} else {
				err = o.resolveAgent(completion)
			}
		} else {
			err = o.resolveHost(completion)
		}
		if err != nil {
			return err
		}
	}
}

func (o *vmOwner) recordCompletionsOnShutdown() {
	for {
		o.completeMu.Lock()
		if len(o.completions) == 0 {
			o.completeMu.Unlock()
			return
		}
		completion := o.completions[0]
		o.completions[0] = jobCompletion{}
		o.completions = o.completions[1:]
		o.completeMu.Unlock()
		if completion.agent {
			_ = o.settleAgent(completion, false)
			continue
		}
		_ = o.end(completion.id, completionPhase(completion.err), completion.observationValue(), completion.err)
		delete(o.pending, completion.id)
	}
}

func (o *vmOwner) stopPending(err error) {
	if err == nil {
		err = context.Canceled
	}
	for id := range o.pending {
		_ = o.end(id, "cancelled", json.RawMessage("null"), err)
		delete(o.pending, id)
	}
}
