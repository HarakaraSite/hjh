// Package host provides actual HTTP and process operations, independent of the JS VM.
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync/atomic"
	"syscall"
)

type Host struct {
	Active  atomic.Int64
	Started func(int)
}

type HTTPArgs struct {
	URL string `json:"url"`
}
type HTTPResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}
type CommandArgs struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
	Stdin   string   `json:"stdin"`
}
type CommandResult struct {
	PID      int    `json:"pid"`
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func (h *Host) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	h.Active.Add(1)
	defer h.Active.Add(-1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case "http":
		var args HTTPArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.http(ctx, args)
	case "command":
		var args CommandArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.command(ctx, args)
	case "readFile":
		var args ReadFileArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return readFile(args)
	case "writeFile":
		var args WriteFileArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return writeFile(args)
	default:
		return nil, fmt.Errorf("unknown host operation: %s", name)
	}
}

func (h *Host) http(ctx context.Context, args HTTPArgs) (HTTPResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return HTTPResult{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return HTTPResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	result := HTTPResult{Status: resp.StatusCode, Body: string(body)}
	if err != nil {
		return result, err
	}
	// HTTP status is data; the external JS tool decides how to handle it.
	return result, nil
}

// This spike runs on Linux. A process group connects cancellation to shell children.
func (h *Host) command(ctx context.Context, args CommandArgs) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, args.Program, args.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = bytes.NewBufferString(args.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return CommandResult{}, err
	}
	if h.Started != nil {
		h.Started(cmd.Process.Pid)
	}
	err := cmd.Wait()
	result := CommandResult{PID: cmd.Process.Pid, ExitCode: cmd.ProcessState.ExitCode(), Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	// Nonzero exit is structured data for the external tool, rather than lost output.
	if _, ok := err.(*exec.ExitError); ok {
		return result, nil
	}
	return result, err
}
