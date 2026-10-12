package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"hjh/internal/contracts"
	"hjh/internal/sessionid"
)

// runOutput owns only the CLI's display state. app serializes observations and
// joins its observers before returning; no provider or history state lives here.
type runOutput struct {
	stdout          io.Writer
	stderr          io.Writer
	stream          bool
	lastText        string
	lineOpen        bool
	knownSessionIDs []string
}

func (out *runOutput) observe(o contracts.Observation) error {
	if o.ParentExecutionID != "" {
		return out.observeChild(o)
	}
	if o.Kind == "assistant_text" {
		if !out.stream || o.Text == "" {
			// A public-thinking update has no body text and must not reset
			// the already streamed assistant prefix.
			return nil
		}
		// The provider publishes the cumulative text of the current request.
		delta := o.Text[len(out.lastText):]
		if _, err := io.WriteString(out.stdout, delta); err != nil {
			return err
		}
		if delta != "" {
			out.lineOpen = !strings.HasSuffix(delta, "\n")
		}
		out.lastText = o.Text
		return nil
	}
	if out.stream {
		if err := out.endLine(); err != nil {
			return err
		}
		if o.Kind == "model_request" && (o.Phase == "start" || o.Phase == "started") {
			out.lastText = ""
		}
	}
	if o.Kind == "execution" {
		if _, err := fmt.Fprintf(out.stderr, "Session: %s\nExecution: %s\n", sessionid.Short(extractSession(o.Input), out.knownSessionIDs...), o.ID); err != nil {
			return err
		}
		if role := startedRole(o); role != "" {
			if _, err := fmt.Fprintf(out.stderr, "Role: %s\n", role); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(out.stderr, "[%s] %s %s\n", o.Kind, o.Name, o.Phase)
	return err
}

func (out *runOutput) observeChild(o contracts.Observation) error {
	if _, err := fmt.Fprintf(out.stderr, "[child execution=%s parent=%s] %s", o.ExecutionID, o.ParentExecutionID, o.Kind); err != nil {
		return err
	}
	if o.Name != "" {
		if _, err := fmt.Fprintf(out.stderr, " %s", o.Name); err != nil {
			return err
		}
	}
	if o.Phase != "" {
		if _, err := fmt.Fprintf(out.stderr, " %s", o.Phase); err != nil {
			return err
		}
	}
	if role := startedRole(o); role != "" {
		if _, err := fmt.Fprintf(out.stderr, " · role %s", role); err != nil {
			return err
		}
	}
	if o.ModelStep != 0 {
		if _, err := fmt.Fprintf(out.stderr, " · step %d", o.ModelStep); err != nil {
			return err
		}
	}
	if o.Error != "" {
		if _, err := fmt.Fprintf(out.stderr, " · %s", o.Error); err != nil {
			return err
		}
	}
	_, err := io.WriteString(out.stderr, "\n")
	return err
}

func startedRole(o contracts.Observation) string {
	if o.Kind != "execution" || o.Name != "run" || (o.Phase != "started" && o.Phase != "start") {
		return ""
	}
	var input struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(o.Input, &input); err != nil {
		return ""
	}
	return input.Role
}

func (out *runOutput) endLine() error {
	if !out.lineOpen {
		return nil
	}
	_, err := io.WriteString(out.stdout, "\n")
	if err == nil {
		out.lineOpen = false
	}
	return err
}

func (out *runOutput) finish(finalText string) error {
	if out.stream {
		return out.endLine()
	}
	_, err := fmt.Fprintln(out.stdout, finalText)
	return err
}
