package experiment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/trace"
)

func (s *suite) chain() (map[string]any, error) {
	r, events, err := s.ordinary("result_chain", `
const response = await tools.http({url: input.url});
const values = JSON.parse(response.body).values;
const result = await tools.command({program: "tr", args: ["a-z", "A-Z"], stdin: values.join("\n")});
return {text: result.stdout, exitCode: result.exitCode};`, map[string]any{"url": s.server.URL + "/data"})
	obs := observed(r, events)
	if err != nil {
		return obs, err
	}
	v, e := decode[struct {
		Text     string
		ExitCode int
	}](r)
	if e != nil {
		return obs, e
	}
	return obs, require(v.Text == "RED\nBLUE" && v.ExitCode == 0, "HTTP → JS processing → command result mismatch")
}

func (s *suite) nested() (map[string]any, error) {
	r, events, err := s.ordinary("nested_tools", `return tools.pipeline({url: input.url});`, map[string]any{"url": s.server.URL + "/data"})
	obs := observed(r, events)
	if err != nil {
		return obs, err
	}
	v, e := decode[host.CommandResult](r)
	if e != nil {
		return obs, e
	}
	parents := map[string]string{}
	pipeline := ""
	for _, event := range events {
		if event.Phase == "started" && event.Kind == "tool" {
			parents[event.Name] = event.Parent
			if event.Name == "pipeline" {
				pipeline = event.ID
			}
		}
	}
	return obs, require(v.Stdout == "RED\nBLUE" && parents["http"] == pipeline && parents["command"] == pipeline, "nested call result or parent relationship mismatch")
}

func (s *suite) parallel() (map[string]any, error) {
	r, events, err := s.ordinary("parallel_http", `const replies = await Promise.all([tools.http({url: input.url}), tools.http({url: input.url})]); return replies.map(r => r.body);`, map[string]any{"url": s.server.URL + "/paired?group=tools"})
	obs := observed(r, events)
	if err != nil {
		return obs, err
	}
	v, e := decode[[]string](r)
	if e != nil {
		return obs, e
	}
	max := s.maxParallel("tools")
	obs["maxConcurrentHTTPHandlers"] = max
	return obs, require(len(v) == 2 && v[0] == "paired" && v[1] == "paired" && max == 2, "Promise.all did not overlap two HTTP requests")
}

func (s *suite) jsException() (map[string]any, error) {
	r, events, err := s.ordinary("js_exception", `throw new Error("generated JS failure");`, nil)
	return expectError(r, events, err, "generated JS failure")
}
func (s *suite) toolException() (map[string]any, error) {
	r, events, err := s.ordinary("tool_exception", `return tools.fail();`, nil)
	return expectError(r, events, err, "external tool failure")
}

func (s *suite) httpStatus() (map[string]any, error) {
	r, events, err := s.ordinary("http_status_failure", `try { await tools.http({url: input.url}); } catch (e) { return e.message; } throw new Error("missing HTTP failure");`, map[string]any{"url": s.server.URL + "/status"})
	obs := observed(r, events)
	if err != nil {
		return obs, err
	}
	v, e := decode[string](r)
	if e != nil {
		return obs, e
	}
	return obs, require(strings.Contains(v, "503") && strings.Contains(v, "service unavailable"), "HTTP status failure was not available to JS catch")
}

func (s *suite) httpTransport() (map[string]any, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	url := "http://" + listener.Addr().String()
	if err = listener.Close(); err != nil {
		return nil, err
	}
	r, events, err := s.ordinary("http_transport_failure", `try { await tools.http({url: input.url}); } catch (e) { return e.message; } throw new Error("missing transport failure");`, map[string]any{"url": url})
	obs := observed(r, events)
	if err != nil {
		return obs, err
	}
	v, e := decode[string](r)
	if e != nil {
		return obs, e
	}
	return obs, require(strings.Contains(v, "connection refused"), "connection failure was not available to JS catch")
}

func (s *suite) commandFailure() (map[string]any, error) {
	r, events, err := s.ordinary("command_failure", `try { await tools.command({program: "sh", args: ["-c", "printf bad >&2; exit 7"]}); } catch (e) { return e.message; } throw new Error("missing command failure");`, nil)
	obs := observed(r, events)
	if err != nil {
		return obs, err
	}
	v, e := decode[string](r)
	if e != nil {
		return obs, e
	}
	return obs, require(strings.Contains(v, "exit 7") && strings.Contains(v, "bad"), "command exit/stderr was not available to JS catch")
}

func (s *suite) partialFailure() (map[string]any, error) {
	dir, err := os.MkdirTemp("", "hjh-s01-effect-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "effect.txt")
	r, events, runErr := s.ordinary("partial_failure_evidence", `await tools.command({program: "sh", args: ["-c", 'printf persisted > "$1"', "effect", input.path]}); throw new Error("failure after effect");`, map[string]any{"path": path})
	obs, err := expectError(r, events, runErr, "failure after effect")
	if err != nil {
		return obs, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return obs, err
	}
	obs["sideEffectReadback"] = string(content)
	commandSaved := false
	for _, e := range events {
		if e.Kind == "tool" && e.Name == "command" && e.Phase == "completed" {
			commandSaved = true
		}
	}
	return obs, require(string(content) == "persisted" && commandSaved, "successful internal operation/effect missing after parent failure")
}

func (s *suite) cancelJS(async bool) (map[string]any, error) {
	name := "cancel_sync_js"
	body := `__mark("busy"); while (true) {}`
	if async {
		name = "cancel_async_js"
		body = `await tools.http({url: input.url}); __mark("busy"); while (true) {}`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var started atomic.Bool
	observe := func(e trace.Event) {
		if e.Kind == "marker" && e.Name == "busy" && e.Phase == "started" {
			started.Store(true)
			time.AfterFunc(50*time.Millisecond, cancel)
		}
	}
	r, events, err := s.worker(ctx, name, s.modules, body, map[string]any{"url": s.server.URL + "/data"}, &host.Host{}, observe)
	obs, checkErr := expectError(r, events, err, "context canceled")
	obs["jsLoopStarted"] = started.Load()
	if checkErr != nil {
		return obs, checkErr
	}
	return obs, require(started.Load() && r.HostOperationsAfterReturn == 0 && r.DurationMS < 2000, "JS cancellation/cleanup did not finish within observation window")
}

func (s *suite) cancelHTTP() (map[string]any, error) {
	s.mu.Lock()
	s.waitStarted = make(chan struct{})
	s.waitStopped = make(chan struct{})
	started, stopped := s.waitStarted, s.waitStopped
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-started:
			time.Sleep(50 * time.Millisecond)
			cancel()
		case <-ctx.Done():
		}
	}()
	r, events, err := s.worker(ctx, "cancel_http", s.modules, `return tools.http({url: input.url});`, map[string]any{"url": s.server.URL + "/wait"}, &host.Host{}, nil)
	<-watchDone
	obs, checkErr := expectError(r, events, err, "context canceled")
	if checkErr != nil {
		return obs, checkErr
	}
	select {
	case <-stopped:
		obs["serverRequestContextEnded"] = true
	case <-time.After(time.Second):
		return obs, errors.New("server did not observe request cancellation")
	}
	return obs, require(r.HostOperationsAfterReturn == 0 && r.DurationMS < 2000, "HTTP operation remains after Worker return")
}

func (s *suite) cancelCommand() (map[string]any, error) {
	dir, err := os.MkdirTemp("", "hjh-s01-process-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var parentPID, childPID atomic.Int64
	h := &host.Host{Started: func(pid int) { parentPID.Store(int64(pid)) }}
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
				b, e := os.ReadFile(path)
				if e != nil {
					continue
				}
				pid, e := strconv.Atoi(string(b))
				if e != nil {
					continue
				}
				childPID.Store(int64(pid))
				time.Sleep(50 * time.Millisecond)
				cancel()
				return
			}
		}
	}()
	r, events, runErr := s.worker(ctx, "cancel_process_group", s.modules, `return tools.command({program: "sh", args: ["-c", 'sleep 60 & child=$!; printf "%s" "$child" > "$1"; wait', "parent", input.path]});`, map[string]any{"path": path}, h, nil)
	<-watchDone
	obs, checkErr := expectError(r, events, runErr, "context canceled")
	if checkErr != nil {
		return obs, checkErr
	}
	parentState := processState(int(parentPID.Load()))
	childState := processState(int(childPID.Load()))
	obs["parentPID"] = parentPID.Load()
	obs["childPID"] = childPID.Load()
	obs["parentStateAfterReturn"] = parentState
	obs["childStateAfterReturn"] = childState
	terminated := func(state string) bool { return state == "absent" || strings.HasPrefix(state, "Z") }
	return obs, require(parentPID.Load() > 0 && childPID.Load() > 0 && terminated(parentState) && terminated(childState) && r.HostOperationsAfterReturn == 0 && r.DurationMS < 2000, "process/group cancellation left a running process")
}

func processState(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if os.IsNotExist(err) {
		return "absent"
	}
	if err != nil {
		return err.Error()
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "State:"))
		}
	}
	return "unknown"
}

func (s *suite) edit() (map[string]any, error) {
	dir, err := os.MkdirTemp("", "hjh-s01-modules-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	data, err := os.ReadFile(filepath.Join(s.modules, "index.js"))
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "index.js")
	if err = os.WriteFile(path, data, 0644); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, firstEvents, err := s.worker(ctx, "external_edit_before", dir, `return tools.revision();`, nil, &host.Host{}, nil)
	if err != nil {
		return observed(first, firstEvents), err
	}
	if err = os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "revision-one", "revision-two")), 0644); err != nil {
		return nil, err
	}
	second, secondEvents, err := s.worker(ctx, "external_edit_after", dir, `return tools.revision();`, nil, &host.Host{}, nil)
	if err != nil {
		return observed(second, secondEvents), err
	}
	a, err := decode[string](first)
	if err != nil {
		return nil, err
	}
	b, err := decode[string](second)
	if err != nil {
		return nil, err
	}
	return map[string]any{"firstWorker": first, "nextWorker": second, "sameExecutable": true}, require(a == "revision-one" && b == "revision-two", "source edit was not reflected by next Worker")
}

func (s *suite) shared() (map[string]any, error) {
	r, events, err := s.ordinary("vm_shared_state", `return [await tools.counter(), await tools.counter()];`, nil)
	obs := observed(r, events)
	if err != nil {
		return obs, err
	}
	v, e := decode[[]int](r)
	if e != nil {
		return obs, e
	}
	return obs, require(len(v) == 2 && v[0] == 1 && v[1] == 2, "module state was not shared inside one Worker")
}

func (s *suite) isolation() (map[string]any, error) {
	var wg sync.WaitGroup
	wg.Add(2)
	observations := make([]map[string]any, 2)
	errs := make([]error, 2)
	for i, label := range []string{"worker-A", "worker-B"} {
		go func(i int, label string) {
			defer wg.Done()
			r, events, err := s.ordinary("vm_isolation_"+label, `globalThis.workerLabel = input.label; await tools.http({url: input.url}); return {label: workerLabel, count: await tools.counter()};`, map[string]any{"label": label, "url": s.server.URL + "/paired?group=workers"})
			observations[i] = observed(r, events)
			if err != nil {
				errs[i] = err
				return
			}
			v, e := decode[struct {
				Label string
				Count int
			}](r)
			if e != nil {
				errs[i] = e
				return
			}
			errs[i] = require(v.Label == label && v.Count == 1, "independent Workers shared VM state")
		}(i, label)
	}
	wg.Wait()
	max := s.maxParallel("workers")
	return map[string]any{"workers": observations, "maxConcurrentHTTPHandlers": max}, errors.Join(errs[0], errs[1], require(max == 2, "independent Workers did not execute concurrently"))
}
