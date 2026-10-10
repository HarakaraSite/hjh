// Package session owns the S-04 execution and adoption flow, not an Agent loop.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"hjh/spikes/s01/internal/configsnapshot"
	"hjh/spikes/s01/internal/history"
	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/jsruntime"
	"hjh/spikes/s01/internal/trace"
)

type Prepared struct {
	Configuration string                    `json:"configuration"`
	Descriptor    configsnapshot.Descriptor `json:"descriptor"`
	Modules       string                    `json:"modules"`
}

func Prepare(store *history.Store, settingsPath, exe, workdir string) (Prepared, error) {
	var p Prepared
	snapshot, err := configsnapshot.Capture(settingsPath, exe)
	if err != nil {
		return p, err
	}
	if err = store.SaveConfiguration(snapshot); err != nil {
		return p, err
	}
	// Use the just-persisted source bytes. The original tree is not read by this Worker.
	modules, err := snapshot.Materialize(workdir)
	if err != nil {
		return p, err
	}
	p = Prepared{Configuration: snapshot.ID, Descriptor: snapshot.Descriptor, Modules: modules}
	return p, nil
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type ModelInput struct {
	Model       string    `json:"model"`
	Instruction string    `json:"instruction"`
	Messages    []Message `json:"messages"`
}

func Input(settings configsnapshot.Settings, window history.Window) ModelInput {
	v := ModelInput{Model: settings.Model, Instruction: settings.Instruction, Messages: []Message{}}
	for _, pair := range window.Pairs {
		v.Messages = append(v.Messages, Message{Role: "user", Content: pair.Request}, Message{Role: "assistant", Content: pair.Response})
	}
	return v
}

const GeneratedJS = `const w = await tools.workflow(input); return {report:w.report,savedBytes:w.savedBytes,previous:input.history.at(-1)?.content ?? null,historyMessages:input.history.length,instruction:input.configuration.instruction};`

type Result struct {
	Turn          string           `json:"turn"`
	Run           string           `json:"run"`
	Configuration string           `json:"configuration"`
	Worker        jsruntime.Result `json:"worker"`
	Input         ModelInput       `json:"modelInputCandidate"`
	Adopted       bool             `json:"adopted"`
}

type Request struct {
	SessionID, TurnID, Text, Body string
	Args                          map[string]any
	ContextPairs                  int
}

// Execute saves the execution and response draft. Adoption is a later explicit operation.
func Execute(ctx context.Context, store *history.Store, prepared Prepared, request Request, h *host.Host) (Result, error) {
	r := Result{Turn: request.TurnID, Run: request.TurnID + "-run", Configuration: prepared.Configuration}
	window, err := store.Conversation(request.SessionID, 0, request.ContextPairs)
	if err != nil {
		return r, err
	}
	r.Input = Input(prepared.Descriptor.Settings, window)
	input := map[string]any{}
	for k, v := range request.Args {
		input[k] = v
	}
	input["history"] = r.Input.Messages
	input["configuration"] = map[string]string{"model": r.Input.Model, "instruction": r.Input.Instruction}
	input["hookConfigPath"] = filepath.Join(prepared.Modules, "hook-config.yaml")
	if err = store.Register(r.Run); err != nil {
		return r, err
	}
	if _, err = store.StartTurn(request.SessionID, request.TurnID, r.Run, prepared.Configuration, request.Text); err != nil {
		return r, err
	}
	rec := trace.NewWithSink(store.Sink(r.Run, nil))
	hooks := ""
	if prepared.Descriptor.Settings.Hooks != "" {
		hooks = filepath.Join(prepared.Modules, prepared.Descriptor.Settings.Hooks)
	}
	r.Worker, err = jsruntime.RunWithOptions(ctx, prepared.Modules, request.Body, input, h, rec, jsruntime.Options{Hooks: hooks})
	if saveErr := rec.Close(); saveErr != nil {
		return r, fmt.Errorf("persist events: %w", saveErr)
	}
	outcome, response, message := "completed", "", ""
	if err != nil {
		outcome = "failed"
		message = err.Error()
		if ctx.Err() != nil {
			outcome = "cancelled"
		}
	} else {
		var value struct {
			Report struct {
				Compatible []struct {
					Name string `json:"name"`
				} `json:"compatible"`
				ToolRevision string `json:"toolRevision"`
				HookRevision string `json:"hookRevision"`
			} `json:"report"`
		}
		if e := json.Unmarshal(r.Worker.Value, &value); e != nil {
			return r, e
		}
		var names []string
		for _, dep := range value.Report.Compatible {
			names = append(names, dep.Name)
		}
		response = fmt.Sprintf("%s: candidates=%v; %s/%s", request.Text, names, value.Report.ToolRevision, value.Report.HookRevision)
	}
	if e := store.FinishTurn(request.TurnID, outcome, response, r.Worker.Value, message); e != nil {
		return r, fmt.Errorf("persist turn: %w", e)
	}
	return r, err
}

func Adopt(store *history.Store, sessionID string, result *Result, beforeCommit func()) error {
	if err := store.Adopt(sessionID, result.Turn, beforeCommit); err != nil {
		return err
	}
	result.Adopted = true
	return nil
}
