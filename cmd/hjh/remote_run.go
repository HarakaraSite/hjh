package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

func runTask(ctx context.Context, args []string, stdin io.ReadCloser) (string, error) {
	if len(args) > 0 {
		task := strings.Join(args, " ")
		if task == "" {
			return "", fmt.Errorf("supply a task as arguments or stdin")
		}
		return task, nil
	}
	data, err := readInput(ctx, stdin)
	if err != nil {
		return "", err
	}
	task := strings.TrimSpace(string(data))
	if task == "" {
		return "", fmt.Errorf("supply a task as arguments or stdin")
	}
	return task, nil
}

// runConnected owns CLI communication only. The existing Core owns the run,
// its Session, cancellation, conversation state and final persistence.
func runConnected(ctx context.Context, endpoint string, request api.StartRunRequest, stream bool, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	client, err := apiclient.New(endpoint)
	if err != nil {
		return fail(err)
	}
	defer client.Close()
	core, err := client.Core(ctx)
	if err != nil {
		return fail(err)
	}
	request.CoreID, request.RunID = core.CoreID, uuid.NewString()
	if request.Workspace != "" && !filepath.IsAbs(request.Workspace) {
		request.Workspace = filepath.Join(core.Workspace, request.Workspace)
	}
	if request.SessionID != "" {
		session, err := client.ResolveSession(ctx, request.SessionID)
		if err != nil {
			return fail(err)
		}
		request.SessionID = session.ID
	}
	sessions, err := client.Sessions(ctx)
	if err != nil {
		return fail(err)
	}
	if _, err := fmt.Fprintf(stderr, "Core: %s\nRun: %s\n", request.CoreID, request.RunID); err != nil {
		return fail(err)
	}
	output := remoteRunOutput{stdout: stdout, stderr: stderr, stream: stream, knownSessionIDs: apiSessionIDs(sessions)}
	var outputErr error
	handle := client.StartRun(request, func(event apiclient.RunEvent) {
		if outputErr == nil {
			outputErr = output.observe(event)
			if outputErr != nil {
				// Losing the CLI output does not cancel the Core-owned work.
				_ = client.Close()
			}
		}
	})
	type completion struct {
		state api.RunState
		err   error
	}
	done := make(chan completion, 1)
	go func() {
		state, err := handle.Wait()
		done <- completion{state, err}
	}()
	var result completion
	select {
	case result = <-done:
	case <-ctx.Done():
		// Keep the HTTP client alive while explicit cancellation and the final
		// saved conversation are delivered. Client.Close alone only detaches.
		cancelErr := handle.Cancel()
		if cancelErr != nil {
			_ = client.Close()
		}
		result = <-done
		result.err = errors.Join(result.err, cancelErr)
	}
	if outputErr != nil {
		return fail(outputErr)
	}
	if result.err != nil {
		if stream {
			result.err = errors.Join(result.err, output.endLine())
		}
		return fail(result.err)
	}
	state := result.state
	if state.Result == nil {
		return fail(fmt.Errorf("Core returned no result for run %s", request.RunID))
	}
	if !state.Result.OK {
		if stream {
			if err := output.endLine(); err != nil {
				return fail(err)
			}
		}
		message := "run failed"
		cancelled := false
		if detail := state.Result.Error; detail != nil {
			message = detail.Message
			cancelled = detail.Code == "run_cancelled"
		} else if agent := state.Result.AgentResult; agent != nil && agent.Error != "" {
			message = agent.Error
		}
		if agent := state.Result.AgentResult; agent != nil {
			cancelled = cancelled || agent.Outcome == "cancelled"
		}
		code := fail(errors.New(message))
		if cancelled {
			return 130
		}
		return code
	}
	if ctx.Err() != nil {
		return 130
	}
	if state.Result.AgentResult == nil {
		return fail(fmt.Errorf("Core returned no answer for run %s", request.RunID))
	}
	if err := output.finish(state.Result.AgentResult.FinalText); err != nil {
		return fail(err)
	}
	return 0
}
