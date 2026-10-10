// Package jsruntime owns each VM and connects async host work to its Promise jobs.
package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/trace"
	"modernc.org/quickjs"
)

type Result struct {
	Execution                 string          `json:"execution"`
	Value                     json.RawMessage `json:"value,omitempty"`
	Error                     string          `json:"error,omitempty"`
	DurationMS                int64           `json:"durationMS"`
	HostOperationsAfterReturn int64           `json:"hostOperationsAfterReturn"`
}

type completion struct {
	id    string
	value any
	err   error
}

// Run is one fresh Worker, in the caller goroutine. Only Interrupt uses another
// goroutine; host workers never touch VM values or resolve Promises themselves.
func Run(parent context.Context, moduleDir, body string, input any, h *host.Host, rec *trace.Recorder) (result Result, err error) {
	return RunWithOptions(parent, moduleDir, body, input, h, rec, Options{})
}

func RunWithOptions(parent context.Context, moduleDir, body string, input any, h *host.Host, rec *trace.Recorder, options Options) (result Result, err error) {
	start := time.Now()
	source, _ := json.Marshal(map[string]any{"body": body, "input": input})
	result.Execution = rec.Start("", "execution", "generated_js", source)
	ctx, cancel := context.WithCancel(parent)
	vm, err := quickjs.NewVM()
	if err != nil {
		cancel()
		rec.End(result.Execution, "failed", nil, err)
		return result, err
	}
	completed := make(chan completion)
	pending := map[string]*quickjs.PromiseCapability{}
	var jobs sync.WaitGroup
	stopWatch, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-stopWatch:
			return
		case <-ctx.Done():
		}
		// Eval/Call reset the binding's interrupt flag on entry. Repeat after
		// cancellation to cover a concurrent entry or an async continuation.
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			vm.Interrupt()
			select {
			case <-stopWatch:
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() {
		cancel()
		close(stopWatch)
		<-watchDone // No Interrupt after VM.Close.
		jobs.Wait() // No host operation remains when the Worker returns.
		for _, pc := range pending {
			pc.Free()
		}
		if closeErr := vm.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		if parent.Err() != nil {
			err = errors.Join(parent.Err(), err)
		}
		phase := "completed"
		if err != nil {
			phase = "failed"
			if parent.Err() != nil {
				phase = "cancelled"
			}
			result.Error = err.Error()
		}
		unfinishedErr := err
		if unfinishedErr == nil {
			unfinishedErr = errors.New("Worker ended before the internal call settled")
		}
		rec.Settle(result.Execution, "cancelled", unfinishedErr)
		rec.End(result.Execution, phase, result.Value, err)
		result.DurationMS = time.Since(start).Milliseconds()
		result.HostOperationsAfterReturn = h.Active.Load()
	}()

	vm.SetModuleLoader(func(_ *quickjs.VM, name string) (string, error) { b, e := os.ReadFile(name); return string(b), e },
		func(_ *quickjs.VM, base, name string) (string, error) {
			if filepath.IsAbs(name) {
				return filepath.Clean(name), nil
			}
			dir := moduleDir
			if filepath.IsAbs(base) {
				dir = filepath.Dir(base)
			}
			return filepath.Join(dir, name), nil
		})
	if err = vm.RegisterFunc("__start", func(parent, kind, name, args string) string {
		return rec.Start(parent, kind, name, json.RawMessage(args))
	}, false); err != nil {
		return result, err
	}
	if err = vm.RegisterFunc("__end", func(id, phase, value, message string) {
		var e error
		if message != "" {
			e = errors.New(message)
		}
		rec.End(id, phase, json.RawMessage(value), e)
	}, false); err != nil {
		return result, err
	}
	if err = vm.RegisterFunc("__mark", func(name string) {
		id := rec.Start(result.Execution, "marker", name, nil)
		rec.End(id, "completed", nil, nil)
	}, false); err != nil {
		return result, err
	}
	if err = vm.RegisterFunc("__host", func(name, parent, args string) (quickjs.Value, error) {
		if e := ctx.Err(); e != nil {
			return quickjs.UndefinedValue, e
		}
		pc, e := vm.NewPromiseCapability()
		if e != nil {
			return quickjs.UndefinedValue, e
		}
		id := rec.Start(parent, "host", name, json.RawMessage(args))
		pending[id] = pc
		jobs.Add(1)
		go func() {
			defer jobs.Done()
			value, e := h.Call(ctx, name, json.RawMessage(args))
			encoded, encodeErr := json.Marshal(value)
			if encodeErr != nil {
				e = errors.Join(e, encodeErr)
			}
			phase := "completed"
			if e != nil {
				phase = "failed"
				if ctx.Err() != nil {
					phase = "cancelled"
				}
			}
			rec.End(id, phase, encoded, e)
			select {
			case completed <- completion{id, value, e}:
			case <-ctx.Done():
			}
		}()
		return pc.Promise.Dup(), nil
	}, false); err != nil {
		return result, err
	}

	if _, err = vm.Eval(bootstrap(moduleDir, result.Execution, options), quickjs.EvalModule); err != nil {
		return result, err
	}
	if _, err = vm.ExecutePendingJobs(); err != nil {
		return result, err
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	if _, err = vm.Eval("globalThis.__run = async function(input) { const tools = __tools;\n"+body+"\n};", quickjs.EvalGlobal); err != nil {
		return result, err
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	promise, err := vm.CallValue("__run", input)
	if err != nil {
		return result, err
	}
	defer promise.Free()
	settled := false
	var promiseErr error
	then, err := promise.Then(func(v quickjs.Value) { result.Value, promiseErr = v.MarshalJSON(); settled = true },
		func(v quickjs.Value) { promiseErr = quickjs.ErrorFromValue(v); settled = true })
	if err != nil {
		return result, err
	}
	defer then.Free()
	for {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		if _, err = vm.ExecutePendingJobs(); err != nil {
			return result, err
		}
		if settled {
			return result, promiseErr
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case done := <-completed:
			pc := pending[done.id]
			var e error
			if done.err != nil {
				_, e = pc.Reject.Call(quickjs.UndefinedValue, done.err.Error())
			} else {
				b, marshalErr := json.Marshal(done.value)
				if marshalErr != nil {
					return result, marshalErr
				}
				_, e = pc.Resolve.Call(quickjs.UndefinedValue, string(b))
			}
			pc.Free()
			delete(pending, done.id)
			if e != nil {
				return result, e
			}
		}
	}
}
