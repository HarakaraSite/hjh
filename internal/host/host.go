// Package host provides context-aware operations used by extension modules.
// It has no dependency on the JavaScript runtime or history store.
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"hjh/internal/webfetch"
)

type Host struct {
	Workspace       string
	HTTPClient      *http.Client
	RequestProvider ProviderRequest
}

func New(workspace string) (*Host, error) {
	if workspace == "" {
		workspace = "."
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	return &Host{Workspace: abs}, nil
}

type HTTPArgs struct {
	URL string `json:"url"`
}

type HTTPResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

type WebFetchArgs struct {
	URL    string
	SaveTo string
}

type CommandArgs struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
	Stdin   string   `json:"stdin"`
}

type CommandResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

type ReadFileArgs struct {
	Path   string `json:"path"`
	Offset *int   `json:"offset,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
}

type ReadFileResult struct {
	Path       string `json:"path"`
	Text       string `json:"text"`
	Offset     int    `json:"offset"`
	Lines      int    `json:"lines"`
	TotalLines int    `json:"totalLines"`
	NextOffset *int   `json:"nextOffset,omitempty"`
}

type WriteFileArgs struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type WriteFileResult struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

func (h *Host) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case "resolveExecutable":
		var args struct {
			Program string `json:"program"`
		}
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		path, err := exec.LookPath(args.Program)
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return filepath.Abs(path)
	case "grepCommand":
		var args CommandArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.grepCommand(ctx, args)
	case "requestProvider":
		var args ProviderRequestArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		if h.RequestProvider == nil {
			return nil, errors.New("credential-aware requests are not configured")
		}
		return h.RequestProvider(ctx, args)
	case "http":
		var args HTTPArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.http(ctx, args)
	case "webFetch":
		args, err := parseWebFetchArgs(input)
		if err != nil {
			return nil, err
		}
		savePath := ""
		if args.SaveTo != "" {
			savePath = h.resolvePath(args.SaveTo)
		}
		return webfetch.Fetch(ctx, h.HTTPClient, args.URL, savePath, args.SaveTo)
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
		return h.readFile(args)
	case "listDirectory":
		var args ListDirectoryArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.listDirectory(ctx, args)
	case "countFiles":
		var args CountFilesArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.countFiles(ctx, args)
	case "findFiles":
		var args FindFilesArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.findFiles(ctx, args)
	case "writeFile":
		var args WriteFileArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		return h.writeFile(args)
	default:
		return nil, fmt.Errorf("unknown host operation: %s", name)
	}
}

func (h *Host) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(h.Workspace, filepath.FromSlash(path))
}

func parseWebFetchArgs(input json.RawMessage) (WebFetchArgs, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return WebFetchArgs{}, err
	}
	if fields == nil {
		return WebFetchArgs{}, errors.New("web_fetch expects an object")
	}
	for name := range fields {
		if name != "url" && name != "save_to" {
			return WebFetchArgs{}, errors.New("web_fetch expects url and optional save_to fields")
		}
	}
	urlValue, ok := fields["url"]
	if !ok {
		return WebFetchArgs{}, errors.New("web_fetch expects url and optional save_to fields")
	}
	var args WebFetchArgs
	if err := json.Unmarshal(urlValue, &args.URL); err != nil {
		return WebFetchArgs{}, err
	}
	if strings.TrimSpace(args.URL) == "" {
		return WebFetchArgs{}, errors.New("web_fetch URL must be a non-empty string")
	}
	if saveTo, ok := fields["save_to"]; ok {
		if err := json.Unmarshal(saveTo, &args.SaveTo); err != nil {
			return WebFetchArgs{}, err
		}
		if strings.TrimSpace(args.SaveTo) == "" {
			return WebFetchArgs{}, errors.New("web_fetch save_to must be a non-empty string")
		}
	}
	return args, nil
}

func (h *Host) readFile(args ReadFileArgs) (ReadFileResult, error) {
	start := 1
	if args.Offset != nil {
		start = *args.Offset
	}
	if start < 1 || (args.Limit != nil && *args.Limit < 1) {
		return ReadFileResult{}, errors.New("read_file offset and limit must be positive line counts")
	}
	b, err := os.ReadFile(h.resolvePath(args.Path))
	if err != nil {
		return ReadFileResult{}, err
	}
	lines := strings.SplitAfter(string(b), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	from := min(start-1, len(lines))
	to := len(lines)
	if args.Limit != nil {
		to = from + min(*args.Limit, len(lines)-from)
	}
	result := ReadFileResult{Path: args.Path, Text: strings.Join(lines[from:to], ""),
		Offset: start, Lines: to - from, TotalLines: len(lines)}
	if to < len(lines) {
		next := to + 1
		result.NextOffset = &next
	}
	return result, nil
}

func (h *Host) writeFile(args WriteFileArgs) (WriteFileResult, error) {
	b := []byte(args.Text)
	path := h.resolvePath(args.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return WriteFileResult{}, err
	}
	err := os.WriteFile(path, b, 0644)
	return WriteFileResult{Path: args.Path, Bytes: len(b)}, err
}

func (h *Host) http(ctx context.Context, args HTTPArgs) (HTTPResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args.URL, nil)
	if err != nil {
		return HTTPResult{}, err
	}
	client := h.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return HTTPResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return HTTPResult{Status: resp.StatusCode, Body: string(body)}, err
}

// command uses a Linux process group so cancellation stops the command and its
// children, then waits for the process before returning.
func (h *Host) command(ctx context.Context, args CommandArgs) (CommandResult, error) {
	cmd := exec.Command(args.Program, args.Args...)
	cmd.Dir = h.Workspace
	cmd.Env = make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "OPENROUTER_API_KEY=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = bytes.NewBufferString(args.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return CommandResult{}, err
	}
	var waitMu sync.Mutex
	waitFinished := false
	var killErr error
	stopWatch := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			waitMu.Lock()
			if !waitFinished {
				killErr = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				if errors.Is(killErr, syscall.ESRCH) {
					killErr = nil
				}
			}
			waitMu.Unlock()
		case <-stopWatch:
		}
	}()
	err := cmd.Wait()
	waitMu.Lock()
	waitFinished = true
	close(stopWatch)
	waitMu.Unlock()
	<-watchDone
	result := CommandResult{ExitCode: cmd.ProcessState.ExitCode(), Stdout: stdout.String(), Stderr: stderr.String()}
	if ctx.Err() != nil {
		return result, errors.Join(ctx.Err(), killErr)
	}
	if _, isExit := err.(*exec.ExitError); isExit {
		return result, nil
	}
	return result, err
}
