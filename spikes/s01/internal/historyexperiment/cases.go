package historyexperiment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hjh/spikes/s01/internal/history"
	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/jsruntime"
	"hjh/spikes/s01/internal/trace"
)

func (s *suite) success() (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, v, err := s.execute(ctx, "success", `const result = await tools.pipeline({url: input.url}); return {length: result.stdout.length};`, map[string]any{"url": s.server.URL}, &host.Host{}, nil)
	o := obs(r, v, "success-view.json")
	if err != nil {
		return o, err
	}
	aggregate, err := decode[struct{ Length int }](r.Value)
	if err != nil {
		return o, err
	}
	pipeline, ok := find(v, "tool", "pipeline")
	if !ok {
		return o, errors.New("pipeline absent")
	}
	http, okHTTP := find(v, "tool", "http")
	command, okCommand := find(v, "tool", "command")
	hostHTTP, okHostHTTP := find(v, "host", "http")
	hostCommand, okHostCommand := find(v, "host", "command")
	detail, err := decode[host.CommandResult](command.Result)
	if err != nil {
		return o, err
	}
	root, okRoot := find(v, "execution", "generated_js")
	if !okRoot {
		return o, errors.New("execution absent")
	}
	persisted, err := decode[struct{ Length int }](root.Result)
	if err != nil {
		return o, err
	}
	return o, check(v.Status == "completed" && aggregate.Length == 8 && persisted == aggregate && detail.Stdout == "RED\nBLUE" && okHTTP && okCommand && okHostHTTP && okHostCommand && http.Parent == pipeline.ID && command.Parent == pipeline.ID && hostHTTP.Parent == http.ID && hostCommand.Parent == command.ID && pipeline.Parent == root.ID, "aggregate, internal result, or parent relationship mismatch")
}

func (s *suite) failure() (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	file := s.runID("failure") + ".effect.txt"
	path := filepath.Join(s.out, file)
	r, v, runErr := s.execute(ctx, "failure", `await tools.command({program:"sh",args:["-c",'printf persisted > "$1"',"effect",input.path]}); throw new Error("failure after effect");`, map[string]any{"path": path}, &host.Host{}, nil)
	o := obs(r, v, "failure-view.json")
	if runErr == nil {
		return o, errors.New("missing parent failure")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return o, err
	}
	o["sideEffectReadback"] = string(content)
	o["sideEffectFile"] = file
	command, ok := find(v, "tool", "command")
	detail, err := decode[host.CommandResult](command.Result)
	if err != nil {
		return o, err
	}
	return o, check(strings.Contains(runErr.Error(), "failure after effect") && v.Status == "failed" && ok && command.Status == "completed" && detail.ExitCode == 0 && string(content) == "persisted", "parent failure lost the successful operation or effect")
}

func (s *suite) commandFailure() (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, v, runErr := s.execute(ctx, "command_failure", `return tools.command({program:"sh",args:["-c","printf before; printf bad >&2; exit 7"]});`, nil, &host.Host{}, nil)
	o := obs(r, v, "command_failure-view.json")
	if runErr == nil {
		return o, errors.New("missing command failure")
	}
	goCall, ok := find(v, "host", "command")
	tool, okTool := find(v, "tool", "command")
	detail, err := decode[host.CommandResult](goCall.Result)
	if err != nil {
		return o, err
	}
	return o, check(v.Status == "failed" && ok && okTool && tool.Status == "failed" && goCall.Status == "completed" && detail.ExitCode == 7 && detail.Stdout == "before" && detail.Stderr == "bad", "failed command details or tool outcome missing")
}

func (s *suite) cancel() (map[string]any, error) {
	file := s.runID("cancel") + ".effect.txt"
	path := filepath.Join(s.out, file)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var pid atomic.Int64
	h := &host.Host{Started: func(value int) { pid.Store(int64(value)) }}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				content, err := os.ReadFile(path)
				if err != nil || string(content) != "before-cancel" {
					continue
				}
				time.Sleep(50 * time.Millisecond)
				cancel()
				return
			}
		}
	}()
	r, v, runErr := s.execute(ctx, "cancel", `return tools.command({program:"sh",args:["-c",'printf before-cancel > "$1"; printf partial-output; exec sleep 60',"effect",input.path]});`, map[string]any{"path": path}, h, nil)
	<-watchDone
	o := obs(r, v, "cancel-view.json")
	if !errors.Is(runErr, context.Canceled) {
		return o, fmt.Errorf("expected cancel, got %v", runErr)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return o, err
	}
	o["sideEffectReadback"] = string(content)
	o["sideEffectFile"] = file
	goCall, ok := find(v, "host", "command")
	detail, err := decode[host.CommandResult](goCall.Result)
	if err != nil {
		return o, err
	}
	_, processErr := os.Stat(fmt.Sprintf("/proc/%d", pid.Load()))
	stopped := pid.Load() > 0 && os.IsNotExist(processErr)
	o["processAbsentAfterReturn"] = stopped
	return o, check(v.Status == "cancelled" && ok && goCall.Status == "cancelled" && detail.Stdout == "partial-output" && string(content) == "before-cancel" && r.HostOperationsAfterReturn == 0 && stopped, "cancel lost partial output/effect or left process running")
}

func (s *suite) parallel() (map[string]any, error) {
	store, err := history.Open(s.db)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	runs := []string{s.runID("parallel-A"), s.runID("parallel-B")}
	for _, run := range runs {
		if err = store.Register(run); err != nil {
			store.Close()
			return nil, err
		}
	}
	results := make([]jsruntime.Result, 2)
	errs := make([]error, 2)
	for i, label := range []string{"a", "b"} {
		go func(i int, label string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			rec := trace.NewWithSink(store.Sink(runs[i], nil))
			results[i], errs[i] = jsruntime.Run(ctx, s.modules, `return (await tools.command({program:"tr",args:["a-z","A-Z"],stdin:input.label})).stdout;`, map[string]any{"label": label}, &host.Host{}, rec)
			errs[i] = errors.Join(errs[i], rec.Close())
		}(i, label)
	}
	wg.Wait()
	err = errors.Join(errs[0], errs[1], store.Close())
	if err != nil {
		return nil, err
	}
	views := make([]history.View, 2)
	for i, run := range runs {
		views[i], err = s.inspect(run, fmt.Sprintf("parallel-%d-view.json", i))
		if err != nil {
			return nil, err
		}
	}
	a, err := decode[string](results[0].Value)
	if err != nil {
		return nil, err
	}
	b, err := decode[string](results[1].Value)
	if err != nil {
		return nil, err
	}
	return map[string]any{"runs": runs, "workers": results, "statuses": []string{views[0].Status, views[1].Status}, "readbackFiles": []string{"parallel-0-view.json", "parallel-1-view.json"}}, check(a == "A" && b == "B" && views[0].Root == "call-1" && views[1].Root == "call-1" && views[0].Status == "completed" && views[1].Status == "completed" && views[0].Run != views[1].Run, "parallel run identities/results mixed")
}
