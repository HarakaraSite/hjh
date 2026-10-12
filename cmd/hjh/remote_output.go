package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/sessionid"
)

// remoteRunOutput owns only display metadata for one Core-owned run. The Core
// remains the owner of execution state and conversation contents.
type remoteRunOutput struct {
	stdout          io.Writer
	stderr          io.Writer
	stream          bool
	knownSessionIDs []string

	sessionPrinted   bool
	executionPrinted bool
	rolePrinted      bool
	phase            string

	assistantBodies map[string]remoteBodyOffset
	lastAssistantID string
	progress        map[string]remoteProgress
	lineOpen        bool
}

type remoteBodyOffset struct {
	length int
	prefix [32]byte
}

type remoteProgress struct {
	kind       string
	status     string
	name       string
	outcome    string
	execution  string
	parentExec string
}

func (out *remoteRunOutput) observe(event apiclient.RunEvent) error {
	if err := out.observeIdentity(event.State); err != nil {
		return err
	}
	if event.State.Phase != "terminal" {
		if err := out.observePhase(event.State.Phase); err != nil {
			return err
		}
	}
	if event.Conversation != nil {
		for _, id := range event.Conversation.Order {
			entity, ok := event.Conversation.Entities[id]
			if !ok {
				continue
			}
			if err := out.observeEntity(event.State.ExecutionID, entity); err != nil {
				return err
			}
		}
	} else if event.Update != nil {
		// Conversation updates are already ordered by entity position by the
		// Core. Keeping their order preserves the user's visible run sequence.
		for _, entity := range event.Update.Upserts {
			if err := out.observeEntity(event.State.ExecutionID, entity); err != nil {
				return err
			}
		}
	}
	if event.State.Phase == "terminal" {
		return out.observePhase(event.State.Phase)
	}
	return nil
}

func (out *remoteRunOutput) observeIdentity(state api.RunState) error {
	var lines []string
	if state.SessionID != "" && !out.sessionPrinted {
		lines = append(lines, "Session: "+sessionid.Short(state.SessionID, out.knownSessionIDs...))
	}
	if state.ExecutionID != "" && !out.executionPrinted {
		lines = append(lines, "Execution: "+state.ExecutionID)
	}
	if state.ActualRole != "" && !out.rolePrinted {
		lines = append(lines, "Role: "+state.ActualRole)
	}
	if len(lines) == 0 {
		return nil
	}
	if err := out.endLine(); err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(out.stderr, line); err != nil {
			return err
		}
	}
	if state.SessionID != "" {
		out.sessionPrinted = true
	}
	if state.ExecutionID != "" {
		out.executionPrinted = true
	}
	if state.ActualRole != "" {
		out.rolePrinted = true
	}
	return nil
}

func (out *remoteRunOutput) observePhase(phase string) error {
	if phase == "" || phase == out.phase {
		return nil
	}
	if err := out.writeProgress("Run phase: %s\n", phase); err != nil {
		return err
	}
	out.phase = phase
	return nil
}

func (out *remoteRunOutput) observeEntity(rootExecutionID string, entity api.ConversationEntity) error {
	isRoot := rootExecutionID != "" && entity.ExecutionID == rootExecutionID
	if out.stream && isRoot && entity.Kind == "assistant" {
		if err := out.streamAssistant(entity); err != nil {
			return err
		}
	}
	if entity.Kind == "tool" || (entity.Kind == "execution" && entity.ParentExecutionID != "") {
		return out.observeProgress(entity)
	}
	return nil
}

func (out *remoteRunOutput) streamAssistant(entity api.ConversationEntity) error {
	if out.assistantBodies == nil {
		out.assistantBodies = make(map[string]remoteBodyOffset)
	}
	previous, seen := out.assistantBodies[entity.ID]
	start := 0
	if seen {
		// The saved conversation may extend a streamed prefix. If it changed
		// bytes already sent to stdout, those bytes cannot be retracted; keep
		// the visible prefix and do not duplicate the replacement body.
		if len(entity.Text) < previous.length || sha256.Sum256([]byte(entity.Text[:previous.length])) != previous.prefix {
			return nil
		}
		start = previous.length
	}
	if start == len(entity.Text) {
		return nil
	}
	if out.lastAssistantID != "" && out.lastAssistantID != entity.ID {
		if err := out.endLine(); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(out.stdout, entity.Text[start:]); err != nil {
		return err
	}
	out.lineOpen = !strings.HasSuffix(entity.Text, "\n")
	out.assistantBodies[entity.ID] = remoteBodyOffset{length: len(entity.Text), prefix: sha256.Sum256([]byte(entity.Text))}
	out.lastAssistantID = entity.ID
	return nil
}

func (out *remoteRunOutput) observeProgress(entity api.ConversationEntity) error {
	progress := remoteProgress{kind: entity.Kind, status: entity.Status, name: entity.Name,
		outcome: entity.Outcome, execution: entity.ExecutionID, parentExec: entity.ParentExecutionID}
	if out.progress == nil {
		out.progress = make(map[string]remoteProgress)
	}
	if previous, ok := out.progress[entity.ID]; ok && previous == progress {
		return nil
	}
	var line string
	switch entity.Kind {
	case "tool":
		line = "[tool"
		if entity.Name != "" {
			line += " " + entity.Name
		}
		line += "] " + entity.Status
		if entity.Outcome != "" {
			line += " (" + entity.Outcome + ")"
		}
	case "execution":
		line = fmt.Sprintf("[child execution=%s] %s", entity.ExecutionID, entity.Status)
	}
	if line == "" {
		return nil
	}
	if err := out.writeProgress("%s\n", line); err != nil {
		return err
	}
	out.progress[entity.ID] = progress
	return nil
}

func (out *remoteRunOutput) writeProgress(format string, args ...any) error {
	if err := out.endLine(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out.stderr, format, args...)
	return err
}

func (out *remoteRunOutput) endLine() error {
	if !out.lineOpen {
		return nil
	}
	if _, err := io.WriteString(out.stdout, "\n"); err != nil {
		return err
	}
	out.lineOpen = false
	return nil
}

func (out *remoteRunOutput) finish(finalText string) error {
	if out.stream {
		return out.endLine()
	}
	_, err := fmt.Fprintln(out.stdout, finalText)
	return err
}
