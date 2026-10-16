package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/go-quickjs/go-quickjs"
	"github.com/go-quickjs/go-quickjs/stdlib"
	"hjh/internal/contracts"
	"hjh/internal/host"
)

type jobCompletion struct {
	id          string
	value       json.RawMessage
	observation json.RawMessage
	err         error
	agent       bool
}

func (c jobCompletion) observationValue() json.RawMessage {
	if c.observation != nil {
		return c.observation
	}
	return c.value
}

type agentJob struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// vmOwner is the only owner of the Runtime and all values that belong to it.
// Host and AgentRunner goroutines exchange only Go values through completions.
type vmOwner struct {
	rt               *quickjs.Runtime
	loop             *stdlib.Loop
	stopLoopLifetime func() bool
	pumpCancel       context.CancelFunc
	completionErr    error
	ctx              context.Context
	options          Options
	host             *host.Host
	files            map[string][]byte
	defs             []contracts.ToolDefinition
	hookHandlers     []string

	active         map[string]activeObservation
	callRoot       string
	callStarts     map[string]contracts.Observation
	delegationEnds map[string]delegationObservationEnd
	sequence       int

	pending     map[string]*quickjs.Promise
	completeMu  sync.Mutex
	completions []jobCompletion
	jobs        sync.WaitGroup

	callCtx        context.Context
	requestContext contracts.RequestContext
	agentCtx       context.Context
	agentCancel    context.CancelFunc
	agentClosed    bool
	agentPending   map[string]*quickjs.Promise
	agentJobs      map[string]*agentJob
}

func newVMOwner(ctx context.Context, options Options, defs []contracts.ToolDefinition) *vmOwner {
	return &vmOwner{
		ctx:            ctx,
		options:        options,
		files:          options.Snapshot.FrozenResources(),
		defs:           defs,
		active:         map[string]activeObservation{},
		callStarts:     map[string]contracts.Observation{},
		delegationEnds: map[string]delegationObservationEnd{},
		pending:        map[string]*quickjs.Promise{},
		agentPending:   map[string]*quickjs.Promise{},
		agentJobs:      map[string]*agentJob{},
	}
}

func (o *vmOwner) open() error {
	if err := o.openRuntime(); err != nil {
		return err
	}
	return o.loadExtensions()
}

func (o *vmOwner) openRuntime() error {
	if o.options.Host != nil {
		cloned := *o.options.Host
		if o.options.Workspace != "" {
			opened, err := host.New(o.options.Workspace)
			if err != nil {
				return err
			}
			cloned.Workspace = opened.Workspace
		}
		o.host = &cloned
	} else {
		h, err := host.New(o.options.Workspace)
		if err != nil {
			return err
		}
		o.host = h
	}
	if o.host.Workspace == "" {
		configured, err := host.New(o.options.Workspace)
		if err != nil {
			return err
		}
		o.host.Workspace = configured.Workspace
	}

	o.rt = quickjs.New()
	o.loop = stdlib.NewLoop(o.rt)
	o.stopLoopLifetime = context.AfterFunc(o.ctx, o.loop.Close)
	o.host.CommandEnv = commandEnvironment(o.options.CredentialEnvNames)
	if err := o.installStdlib(); err != nil {
		return err
	}
	if err := o.installBindings(); err != nil {
		return err
	}
	return nil
}

func (o *vmOwner) close(err error) error {
	if o.rt == nil {
		return nil
	}
	if err == nil {
		err = context.Canceled
	}
	if o.stopLoopLifetime != nil {
		o.stopLoopLifetime()
	}
	if o.loop != nil {
		o.loop.Close()
	}
	o.stopPending(err)
	o.jobs.Wait()
	o.recordCompletionsOnShutdown()
	o.finishOpen("cancelled", err)
	o.rt.SetModuleLoader(nil)
	closeErr := o.rt.Close()
	o.rt = nil
	o.completionErr = nil
	return closeErr
}

func (o *vmOwner) installBindings() error {
	if err := o.rt.Set("__start", func(parent, kind, name, input string) (string, error) {
		return o.start(parent, kind, name, json.RawMessage(input))
	}); err != nil {
		return err
	}
	if err := o.rt.Set("__end", func(id, phase, result, message string) error {
		var resultErr error
		if message != "" {
			resultErr = errors.New(message)
		}
		return o.end(id, phase, json.RawMessage(result), resultErr)
	}); err != nil {
		return err
	}
	if err := o.rt.Set("__host", func(name, parent, input string) quickjs.Value {
		return o.startHostValue(name, parent, input)
	}); err != nil {
		return err
	}
	if err := o.rt.Set("__agent", func(parent, input string) quickjs.Value {
		return o.startAgentValue(parent, input)
	}); err != nil {
		return err
	}
	if err := o.rt.Set("__context", func(operation, parent, input string) quickjs.Value {
		return o.startContextValue(operation, parent, input)
	}); err != nil {
		return err
	}
	return o.rt.Set("__agentControl", func(operation, parent, input string) quickjs.Value {
		return o.startAgentControlValue(operation, parent, input)
	})
}

func (o *vmOwner) loadExtensions() error {
	bootstrap, err := bootstrapSource()
	if err != nil {
		return err
	}
	if _, err = o.rt.EvalContext(o.ctx, bootstrap); err != nil {
		return fmt.Errorf("initialize runtime bridge: %w", captureJavaScriptError(err))
	}
	// Keep the generated entry and source bytes from the initial capture when
	// a failed call retires the VM. Never reload edited files on a reset.
	if _, captured := o.files["__runtime_entry__.js"]; !captured {
		o.files["__runtime_entry__.js"] = []byte(o.extensionEntry())
	}
	o.rt.SetModuleLoader(func(specifier, referrer string) (string, string, error) {
		resolved := resolveModulePath(referrer, specifier)
		resource := moduleResourcePath(resolved)
		contents, ok := o.files[resource]
		if !ok {
			return "", "", fmt.Errorf("captured JavaScript module not found: %s", specifier)
		}
		return string(contents), resolved, nil
	})

	entry, err := o.rt.EvalContext(o.ctx, "import("+jsString("/__extensions__/__runtime_entry__.js")+")")
	if err != nil {
		return fmt.Errorf("initialize extensions: %w", captureJavaScriptError(err))
	}
	if err = o.awaitModule(o.ctx, entry); err != nil {
		return fmt.Errorf("initialize extensions: %w", captureJavaScriptError(err))
	}
	handlerNames, err := o.rt.Get("__getExtensionHookHandlers")
	if err != nil {
		return fmt.Errorf("read extension hook handlers: %w", captureJavaScriptError(err))
	}
	encodedHandlers, err := handlerNames.CallContext(o.ctx)
	if err != nil {
		return fmt.Errorf("read extension hook handlers: %w", captureJavaScriptError(err))
	}
	if !encodedHandlers.IsString() {
		return fmt.Errorf("read extension hook handlers: JavaScript returned a non-string value")
	}
	if err = json.Unmarshal([]byte(encodedHandlers.String()), &o.hookHandlers); err != nil {
		return fmt.Errorf("read extension hook handlers: %w", err)
	}
	return nil
}

func (o *vmOwner) extensionEntry() string {
	help := make(map[string]any, len(o.options.Snapshot.Tools))
	for _, tool := range o.options.Snapshot.Tools {
		result, err := o.options.Snapshot.ToolHelp(tool)
		if err != nil {
			help[tool.Name] = map[string]string{"error": err.Error()}
			continue
		}
		help[tool.Name] = result
	}
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
	fmt.Fprintf(&builder, "globalThis.__extensionHelp = %s;\n", contracts.JSON(help))
	if hookModule != "" {
		builder.WriteString("globalThis.__extensionHooks = __extension_hooks;\n")
	} else {
		builder.WriteString("globalThis.__extensionHooks = {};\n")
	}
	builder.WriteString("globalThis.__getExtensionHookHandlers = () => JSON.stringify(['runtimeStart', 'beforeTurn', 'projectRequest', 'beforeTool', 'afterTool', 'afterTurn', 'runtimeStop'].filter(name => typeof globalThis.__extensionHooks?.[name] === 'function'));\n")
	return builder.String()
}

type capturedJavaScriptError struct {
	message string
	stack   string
	cause   error
}

func (e *capturedJavaScriptError) Error() string {
	if e.stack == "" || e.stack == e.message {
		return e.message
	}
	if strings.HasPrefix(e.stack, e.message) {
		return e.stack
	}
	return e.message + "\n" + e.stack
}

func (e *capturedJavaScriptError) Unwrap() error { return e.cause }

func captureJavaScriptError(err error) error {
	var javascriptError *quickjs.Error
	if err == nil || !errors.As(err, &javascriptError) {
		return err
	}
	return &capturedJavaScriptError{
		message: javascriptError.Error(),
		stack:   javascriptError.Stack(),
		cause:   javascriptError.Unwrap(),
	}
}

// Runtime.Throw returns an internal thrown value rather than *quickjs.Error.
// Capture a rejected Promise's own stack while its value still belongs to the
// open runtime, and pass only Go strings and any original Go cause outward.
func (o *vmOwner) capturePromiseRejection(value quickjs.Value) error {
	err := o.rt.Throw(value)
	captured := &capturedJavaScriptError{message: err.Error()}
	if stack, stackErr := value.Get("stack"); stackErr == nil && stack.IsString() {
		captured.stack = stack.String()
	}
	var caused interface{ Cause() error }
	if errors.As(err, &caused) {
		captured.cause = caused.Cause()
	}
	return captured
}

func resolveModulePath(referrer, specifier string) string {
	if path.IsAbs(specifier) {
		return path.Clean(specifier)
	}
	if referrer == "" || referrer == "<eval>" {
		return path.Clean(path.Join("/__extensions__", specifier))
	}
	return path.Clean(path.Join(path.Dir(referrer), specifier))
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
