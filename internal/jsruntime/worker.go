// Package jsruntime owns a QuickJS VM for the lifetime of one agent Worker.
package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/host"
	"modernc.org/quickjs"
)

type Options struct {
	Snapshot       extensions.Snapshot
	Workspace      string
	Observer       contracts.Observer
	ObserverParent string
	Host           *host.Host
	Agents         contracts.AgentRunner
}

type Worker struct {
	requests chan callRequest
	done     chan struct{}
	cancel   context.CancelFunc
	closeErr error
	defs     []contracts.ToolDefinition
}

type callRequest struct {
	ctx    context.Context
	name   string
	args   json.RawMessage
	result chan callResult
}

type callResult struct {
	value json.RawMessage
	err   error
}

type jobCompletion struct {
	id    string
	value json.RawMessage
	err   error
	agent bool
}

type agentJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

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

type vmOwner struct {
	vm             *quickjs.VM
	ctx            context.Context
	options        Options
	host           *host.Host
	files          map[string][]byte
	defs           []contracts.ToolDefinition
	active         map[string]activeObservation
	callRoot       string
	callStarts     map[string]contracts.Observation
	delegationEnds map[string]delegationObservationEnd
	sequence       int
	pending        map[string]*quickjs.PromiseCapability
	completeWake   chan struct{}
	completeMu     sync.Mutex
	completions    []jobCompletion
	jobs           sync.WaitGroup
	callCtx        context.Context
	agentCtx       context.Context
	agentCancel    context.CancelFunc
	agentClosed    bool
	agentPending   map[string]*quickjs.PromiseCapability
	agentJobs      map[string]*agentJob
}

// New creates a Worker and evaluates its captured extension modules before it
// returns. The VM is created, used, and closed only by its owner goroutine.
func New(ctx context.Context, options Options) (*Worker, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifeCtx, cancel := context.WithCancel(context.Background())
	stopParent := context.AfterFunc(ctx, cancel)
	defs := options.Snapshot.Definitions()
	w := &Worker{
		requests: make(chan callRequest),
		done:     make(chan struct{}),
		cancel: func() {
			stopParent()
			cancel()
		},
		defs: defs,
	}
	ready := make(chan error, 1)
	go w.serve(lifeCtx, options, ready)
	select {
	case err := <-ready:
		if err != nil {
			w.cancel()
			<-w.done
			return nil, err
		}
		return w, nil
	case <-ctx.Done():
		w.cancel()
		<-w.done
		return nil, ctx.Err()
	}
}

func (w *Worker) Definitions() []contracts.ToolDefinition {
	definitions := make([]contracts.ToolDefinition, len(w.defs))
	for i, definition := range w.defs {
		definitions[i] = definition
		definitions[i].InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
	}
	return definitions
}

func (w *Worker) Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !json.Valid(args) && len(args) != 0 {
		return nil, fmt.Errorf("tool %s arguments are not valid JSON", name)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if _, ok := w.definition(name); !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	request := callRequest{ctx: ctx, name: name, args: append(json.RawMessage(nil), args...), result: make(chan callResult, 1)}
	select {
	case w.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.done:
		return nil, w.closeErrOrClosed()
	}
	select {
	case result := <-request.result:
		return result.value, result.err
	case <-ctx.Done():
		w.cancel()
		select {
		case result := <-request.result:
			if result.err != nil {
				return nil, result.err
			}
			return result.value, nil
		case <-w.done:
			return nil, ctx.Err()
		}
	case <-w.done:
		select {
		case result := <-request.result:
			return result.value, result.err
		default:
			return nil, w.closeErrOrClosed()
		}
	}
}

func (w *Worker) Close() error {
	w.cancel()
	<-w.done
	return w.closeErr
}

func (w *Worker) serve(ctx context.Context, options Options, ready chan<- error) {
	defer close(w.done)
	owner := &vmOwner{
		ctx:            ctx,
		options:        options,
		files:          options.Snapshot.FrozenResources(),
		defs:           w.defs,
		active:         map[string]activeObservation{},
		callStarts:     map[string]contracts.Observation{},
		delegationEnds: map[string]delegationObservationEnd{},
		pending:        map[string]*quickjs.PromiseCapability{},
		agentPending:   map[string]*quickjs.PromiseCapability{},
		agentJobs:      map[string]*agentJob{},
		completeWake:   make(chan struct{}, 1),
	}
	if options.Host != nil {
		cloned := *options.Host
		if options.Workspace != "" {
			opened, err := host.New(options.Workspace)
			if err != nil {
				ready <- err
				return
			}
			cloned.Workspace = opened.Workspace
		}
		owner.host = &cloned
	} else {
		h, err := host.New(options.Workspace)
		if err != nil {
			ready <- err
			return
		}
		owner.host = h
	}
	if owner.host.Workspace == "" {
		configured, err := host.New(options.Workspace)
		if err != nil {
			ready <- err
			return
		}
		owner.host.Workspace = configured.Workspace
	}
	vm, err := quickjs.NewVM()
	if err != nil {
		ready <- err
		return
	}
	owner.vm = vm
	var setupErr error
	defer func() {
		owner.stopPending(ctx.Err())
		owner.jobs.Wait()
		for _, capability := range owner.pending {
			capability.Free()
		}
		for _, capability := range owner.agentPending {
			capability.Free()
		}
		vm.SetModuleLoader(nil, nil)
		setupErr = errors.Join(setupErr, vm.Close())
		if setupErr != nil {
			w.closeErr = setupErr
		}
	}()

	if err = owner.installBindings(); err != nil {
		setupErr = err
		ready <- err
		return
	}
	if err = owner.loadExtensions(); err != nil {
		setupErr = err
		ready <- err
		return
	}
	ready <- nil
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-w.requests:
			result, stop := owner.call(request)
			request.result <- result
			if stop {
				return
			}
		}
	}
}

func (o *vmOwner) installBindings() error {
	if err := o.vm.RegisterHostFunc("__start", func(args []any) (any, error) {
		values, err := stringArgs(args, 4)
		if err != nil {
			return nil, err
		}
		return o.start(values[0], values[1], values[2], json.RawMessage(values[3]))
	}); err != nil {
		return err
	}
	if err := o.vm.RegisterHostFunc("__end", func(args []any) (any, error) {
		values, err := stringArgs(args, 4)
		if err != nil {
			return nil, err
		}
		var resultErr error
		if values[3] != "" {
			resultErr = errors.New(values[3])
		}
		return nil, o.end(values[0], values[1], json.RawMessage(values[2]), resultErr)
	}); err != nil {
		return err
	}
	if err := o.vm.RegisterFunc("__host", func(name, parent, input string) quickjs.Value {
		return o.startHostValue(name, parent, input)
	}, false); err != nil {
		return err
	}
	return o.vm.RegisterFunc("__agent", func(parent, input string) quickjs.Value {
		return o.startAgentValue(parent, input)
	}, false)
}

func (o *vmOwner) loadExtensions() error {
	bootstrap, err := bootstrapSource()
	if err != nil {
		return err
	}
	if _, err = o.vm.Eval(bootstrap, quickjs.EvalGlobal); err != nil {
		return fmt.Errorf("initialize runtime bridge: %w", err)
	}
	o.files["__runtime_entry__.js"] = []byte(o.extensionEntry())
	o.vm.SetModuleLoader(func(_ *quickjs.VM, name string) (string, error) {
		resource := moduleResourcePath(name)
		contents, ok := o.files[resource]
		if !ok {
			return "", fmt.Errorf("captured JavaScript module not found: %s", name)
		}
		return string(contents), nil
	}, func(_ *quickjs.VM, base, name string) (string, error) {
		if path.IsAbs(name) {
			return path.Clean(name), nil
		}
		if base == "" || base == "<eval>" {
			return path.Clean(path.Join("/__extensions__", name)), nil
		}
		return path.Clean(path.Join(path.Dir(base), name)), nil
	})
	ctx := o.ctx
	stop := o.interrupt(ctx)
	entry, err := o.vm.EvalValue("import('/__extensions__/__runtime_entry__.js')", quickjs.EvalGlobal)
	if err == nil {
		err = o.awaitModule(ctx, entry)
		entry.Free()
	}
	stop()
	if err != nil {
		return fmt.Errorf("initialize extensions: %w", err)
	}
	return nil
}

func (o *vmOwner) extensionEntry() string {
	var builder strings.Builder
	moduleAliases := map[string]string{}
	modules := make([]string, 0, len(o.options.Snapshot.Tools))
	for _, tool := range o.options.Snapshot.Tools {
		if tool.Name == "run_js" {
			continue
		}
		module := extensionsResourcePath(tool.Module)
		if _, ok := moduleAliases[module]; !ok {
			alias := fmt.Sprintf("__extension_module_%d", len(moduleAliases))
			moduleAliases[module] = alias
			modules = append(modules, module)
		}
	}
	hookModule := o.options.Snapshot.HookModule()
	for _, module := range modules {
		fmt.Fprintf(&builder, "import * as %s from %s;\n", moduleAliases[module], jsString("/__extensions__/"+module))
	}
	if hookModule != "" {
		fmt.Fprintf(&builder, "import * as __extension_hooks from %s;\n", jsString("/__extensions__/"+extensionsResourcePath(hookModule)))
	}
	builder.WriteString("const __extension_entries = [\n")
	for _, tool := range o.options.Snapshot.Tools {
		if tool.Name == "run_js" {
			continue
		}
		alias := moduleAliases[extensionsResourcePath(tool.Module)]
		fmt.Fprintf(&builder, "  [%s, %s[%s]],\n", jsString(tool.Name), alias, jsString(tool.Export))
	}
	builder.WriteString("];\n")
	builder.WriteString("for (const [name, implementation] of __extension_entries) { if (typeof implementation !== 'function') throw new Error(`tool ${name} does not export its configured implementation`); }\n")
	builder.WriteString("globalThis.__extensionTools = Object.fromEntries(__extension_entries);\n")
	builder.WriteString("globalThis.__selectedToolNames = [")
	for index, definition := range o.defs {
		if index > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(jsString(definition.Name))
	}
	builder.WriteString("];\n")
	if hookModule != "" {
		builder.WriteString("globalThis.__extensionHooks = __extension_hooks;\n")
	} else {
		builder.WriteString("globalThis.__extensionHooks = {};\n")
	}
	return builder.String()
}

func (o *vmOwner) call(request callRequest) (callResult, bool) {
	input := append(json.RawMessage(nil), request.args...)
	parent := contracts.CallParent(request.ctx)
	if parent == "" {
		parent = o.options.ObserverParent
	}
	root, err := o.startCall(contracts.ModelToolCall(request.ctx), parent, request.name, input)
	if err != nil {
		return callResult{err: err}, false
	}
	callCtx, cancel := context.WithCancel(o.ctx)
	agentCtx, agentCancel := context.WithCancel(callCtx)
	stopRequest := context.AfterFunc(request.ctx, cancel)
	o.callCtx = callCtx
	o.agentCtx = agentCtx
	o.agentCancel = agentCancel
	o.agentClosed = false
	o.agentJobs = map[string]*agentJob{}
	o.callRoot = root
	o.callStarts = map[string]contracts.Observation{root: o.active[root].started}
	o.delegationEnds = map[string]delegationObservationEnd{}
	interruptDone := o.interrupt(callCtx)
	promise, err := o.vm.CallValue("__invokeDirect", request.name, string(request.args), root)
	var value json.RawMessage
	if err == nil {
		value, err = o.awaitCall(callCtx, promise)
		promise.Free()
	}
	interruptDone()
	stopRequest()
	if callCtx.Err() != nil {
		cancel()
		agentCancel()
		joinErr := o.joinAgents()
		o.jobs.Wait()
		callErr := errors.Join(callCtx.Err(), err)
		if joinErr != nil {
			callErr = errors.Join(callErr, joinErr)
		}
		o.recordCompletionsOnShutdown()
		o.stopPending(callErr)
		callErr = errors.Join(callErr, o.closeDelegationObservations())
		o.finishOpen("cancelled", callErr)
		o.clearCallScope()
		return callResult{err: callErr}, true
	}
	cancel()
	if err != nil {
		agentCancel()
		err = errors.Join(err, o.joinAgents())
		o.jobs.Wait()
		o.recordCompletionsOnShutdown()
		o.stopPending(err)
		err = errors.Join(err, o.closeDelegationObservations())
		o.finishOpen("failed", err)
		if _, active := o.active[root]; active {
			_ = o.end(root, "failed", json.RawMessage("null"), err)
		}
		o.clearCallScope()
		return callResult{err: err}, false
	}
	agentCancel()
	if joinErr := o.joinAgents(); joinErr != nil {
		joinErr = errors.Join(joinErr, o.closeDelegationObservations())
		o.finishOpen("failed", joinErr)
		if _, active := o.active[root]; active {
			_ = o.end(root, "failed", json.RawMessage("null"), joinErr)
		}
		o.clearCallScope()
		return callResult{err: joinErr}, false
	}
	if endErr := o.closeDelegationObservations(); endErr != nil {
		o.finishOpen("failed", endErr)
		if _, active := o.active[root]; active {
			_ = o.end(root, "failed", json.RawMessage("null"), endErr)
		}
		o.clearCallScope()
		return callResult{err: endErr}, false
	}
	if _, active := o.active[root]; active {
		if endErr := o.end(root, "completed", value, nil); endErr != nil {
			o.finishOpen("failed", endErr)
			o.clearCallScope()
			return callResult{err: endErr}, false
		}
	}
	o.clearCallScope()
	return callResult{value: value}, false
}

func (o *vmOwner) clearCallScope() {
	o.callCtx = nil
	o.agentCtx = nil
	o.agentCancel = nil
	o.agentClosed = false
	o.agentJobs = map[string]*agentJob{}
	o.callRoot = ""
	o.callStarts = map[string]contracts.Observation{}
	o.delegationEnds = map[string]delegationObservationEnd{}
}

func (o *vmOwner) awaitCall(ctx context.Context, promise quickjs.Value) (json.RawMessage, error) {
	var value json.RawMessage
	var settled bool
	var resultErr error
	then, err := promise.Then(func(v quickjs.Value) {
		b, marshalErr := v.MarshalJSON()
		if marshalErr != nil {
			resultErr = marshalErr
		} else {
			var encoded string
			if jsonErr := json.Unmarshal(b, &encoded); jsonErr != nil {
				resultErr = jsonErr
			} else if !json.Valid([]byte(encoded)) {
				resultErr = fmt.Errorf("JavaScript returned invalid JSON")
			} else {
				value = json.RawMessage(encoded)
			}
		}
		o.closeAgentScope()
		settled = true
	}, func(v quickjs.Value) {
		resultErr = quickjs.ErrorFromValue(v)
		o.closeAgentScope()
		settled = true
	})
	if err != nil {
		return nil, err
	}
	defer then.Free()
	for !settled {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if _, err = o.vm.ExecutePendingJobs(); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if settled {
			break
		}
		select {
		case <-o.completeWake:
			if err = o.resolveCompletions(); err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	agentErr := o.joinAgents()
	if resultErr != nil {
		return nil, errors.Join(resultErr, agentErr)
	}
	if agentErr != nil {
		return nil, agentErr
	}
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if _, err = o.vm.ExecutePendingJobs(); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		if len(o.pending) == 0 {
			break
		}
		select {
		case <-o.completeWake:
			if err = o.resolveCompletions(); err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return value, o.joinAgents()
}

func (o *vmOwner) awaitModule(ctx context.Context, promise quickjs.Value) error {
	var settled bool
	var resultErr error
	then, err := promise.Then(func(quickjs.Value) { settled = true }, func(v quickjs.Value) {
		resultErr = quickjs.ErrorFromValue(v)
		settled = true
	})
	if err != nil {
		return err
	}
	defer then.Free()
	for !settled {
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = o.vm.ExecutePendingJobs(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if settled {
			break
		}
		select {
		case <-o.completeWake:
			if err = o.resolveCompletions(); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return resultErr
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

func (o *vmOwner) startHost(name, parent string, input json.RawMessage) (any, error) {
	if o.callCtx == nil {
		return nil, errors.New("host operation called outside a Worker call")
	}
	if len(input) == 0 || !json.Valid(input) {
		return nil, fmt.Errorf("host operation %s arguments are not valid JSON", name)
	}
	id, err := o.start(parent, "host", name, input)
	if err != nil {
		return nil, err
	}
	capability, err := o.vm.NewPromiseCapability()
	if err != nil {
		_ = o.end(id, "failed", json.RawMessage("null"), err)
		return nil, err
	}
	o.pending[id] = capability
	o.jobs.Add(1)
	ctx := o.callCtx
	go func() {
		defer o.jobs.Done()
		value, callErr := o.host.Call(ctx, name, input)
		encoded, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			callErr = errors.Join(callErr, encodeErr)
		}
		o.queueCompletion(jobCompletion{id: id, value: encoded, err: callErr})
	}()
	return capability.Promise.Dup(), nil
}

func (o *vmOwner) startHostValue(name, parent, input string) quickjs.Value {
	result, err := o.startHost(name, parent, json.RawMessage(input))
	if err != nil {
		return o.rejectedPromise(err)
	}
	if promise, ok := result.(quickjs.Value); ok {
		return promise
	}
	return o.rejectedPromise(fmt.Errorf("host operation %s did not return a Promise", name))
}

func (o *vmOwner) startAgentValue(parent, input string) quickjs.Value {
	result, err := o.startAgent(parent, json.RawMessage(input))
	if err != nil {
		return o.rejectedPromise(err)
	}
	if promise, ok := result.(quickjs.Value); ok {
		return promise
	}
	return o.rejectedPromise(errors.New("agent operation did not return a Promise"))
}

func (o *vmOwner) startAgent(parent string, input json.RawMessage) (any, error) {
	if o.callCtx == nil || o.agentCtx == nil {
		return nil, errors.New("agent operation called outside a Worker call")
	}
	if len(input) == 0 || !json.Valid(input) {
		return nil, errors.New("agent operation arguments are not valid JSON")
	}
	var arguments struct {
		Task *string `json:"task"`
		Role string  `json:"role"`
	}
	if err := json.Unmarshal(input, &arguments); err != nil {
		return nil, fmt.Errorf("agent operation arguments are not valid: %w", err)
	}
	if arguments.Task == nil {
		return nil, errors.New("api.agents.run requires a string task field")
	}
	callID, err := o.start(parent, "agent", "agents.run", input)
	if err != nil {
		return nil, err
	}
	capability, err := o.vm.NewPromiseCapability()
	if err != nil {
		_ = o.end(callID, "failed", json.RawMessage("null"), err)
		return nil, err
	}
	o.agentPending[callID] = capability

	ctx, cancel := context.WithCancel(o.agentCtx)
	job := &agentJob{cancel: cancel, done: make(chan struct{})}
	o.agentJobs[callID] = job
	o.jobs.Add(1)
	request := contracts.AgentRunRequest{Task: *arguments.Task, Role: arguments.Role, CallID: callID}
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
	return capability.Promise.Dup(), nil
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
	promise, evalErr := o.vm.EvalValue("Promise.reject(new Error("+jsString(err.Error())+"))", quickjs.EvalGlobal)
	if evalErr != nil {
		panic(evalErr)
	}
	return promise
}

func (o *vmOwner) resolveHost(completion jobCompletion) error {
	capability := o.pending[completion.id]
	if capability == nil {
		return nil
	}
	phase := "completed"
	if completion.err != nil {
		phase = "failed"
		if errors.Is(completion.err, context.Canceled) || errors.Is(completion.err, context.DeadlineExceeded) {
			phase = "cancelled"
		}
	}
	endErr := o.end(completion.id, phase, completion.value, completion.err)
	if endErr != nil {
		completion.err = errors.Join(completion.err, endErr)
	}
	var promiseErr error
	if completion.err == nil {
		_, promiseErr = capability.Resolve.Call(quickjs.UndefinedValue, string(completion.value))
	} else {
		_, promiseErr = capability.Reject.Call(quickjs.UndefinedValue, completion.err.Error())
	}
	capability.Free()
	delete(o.pending, completion.id)
	return promiseErr
}

func (o *vmOwner) resolveAgent(completion jobCompletion) error {
	return o.settleAgent(completion, true)
}

func (o *vmOwner) settleAgent(completion jobCompletion, deliver bool) error {
	capability := o.agentPending[completion.id]
	if capability == nil {
		return nil
	}
	phase := "completed"
	if completion.err != nil {
		phase = "failed"
		if errors.Is(completion.err, context.Canceled) || errors.Is(completion.err, context.DeadlineExceeded) {
			phase = "cancelled"
		}
	}
	o.markDelegationAncestors(completion.id, phase, completion.err)
	endErr := o.end(completion.id, phase, completion.value, completion.err)
	if endErr != nil {
		completion.err = errors.Join(completion.err, endErr)
	}
	var promiseErr error
	if deliver {
		if completion.err == nil {
			_, promiseErr = capability.Resolve.Call(quickjs.UndefinedValue, string(completion.value))
		} else {
			_, promiseErr = capability.Reject.Call(quickjs.UndefinedValue, completion.err.Error())
		}
	}
	capability.Free()
	delete(o.agentPending, completion.id)
	return errors.Join(endErr, promiseErr)
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

func (o *vmOwner) queueCompletion(completion jobCompletion) {
	o.completeMu.Lock()
	o.completions = append(o.completions, completion)
	o.completeMu.Unlock()
	select {
	case o.completeWake <- struct{}{}:
	default:
	}
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
		phase := "completed"
		if completion.err != nil {
			phase = "failed"
			if errors.Is(completion.err, context.Canceled) || errors.Is(completion.err, context.DeadlineExceeded) {
				phase = "cancelled"
			}
		}
		_ = o.end(completion.id, phase, completion.value, completion.err)
		if capability := o.pending[completion.id]; capability != nil {
			capability.Free()
			delete(o.pending, completion.id)
		}
	}
}

func (o *vmOwner) finishOpen(phase string, err error) {
	open := make([]activeObservation, 0, len(o.active))
	for _, active := range o.active {
		open = append(open, active)
	}
	sort.Slice(open, func(i, j int) bool { return open[i].order > open[j].order })
	for _, active := range open {
		result := json.RawMessage("null")
		_ = o.end(active.started.ID, phase, result, err)
	}
}

func (o *vmOwner) stopPending(err error) {
	if err == nil {
		err = context.Canceled
	}
	for id, capability := range o.pending {
		_ = o.end(id, "cancelled", json.RawMessage("null"), err)
		capability.Free()
		delete(o.pending, id)
	}
}

func (o *vmOwner) interrupt(ctx context.Context) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-stop:
			return
		case <-ctx.Done():
		}
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			o.vm.Interrupt()
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func (w *Worker) definition(name string) (contracts.ToolDefinition, bool) {
	for _, def := range w.defs {
		if def.Name == name {
			return def, true
		}
	}
	return contracts.ToolDefinition{}, false
}

func (w *Worker) closeErrOrClosed() error {
	if w.closeErr != nil {
		return w.closeErr
	}
	return errors.New("Worker is closed")
}

func stringArgs(args []any, count int) ([]string, error) {
	if len(args) != count {
		return nil, fmt.Errorf("expected %d arguments, got %d", count, len(args))
	}
	values := make([]string, count)
	for i, arg := range args {
		value, ok := arg.(string)
		if !ok {
			return nil, fmt.Errorf("argument %d must be a string", i)
		}
		values[i] = value
	}
	return values, nil
}

func moduleResourcePath(name string) string {
	name = strings.TrimPrefix(path.Clean(name), "/__extensions__/")
	return strings.TrimPrefix(name, "/")
}

func extensionsResourcePath(name string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(name)), "/")
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
