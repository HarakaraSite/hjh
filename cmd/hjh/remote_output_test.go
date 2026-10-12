package main

import (
	"bytes"
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
)

func TestRemoteRunOutputStreamsRootAssistantAndCoalescedProgressOnce(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := remoteRunOutput{stdout: &stdout, stderr: &stderr, stream: true, knownSessionIDs: []string{"session-1", "session-2"}}
	state := api.RunState{CoreID: "core-1", RunID: "run-1", Phase: "running", SessionID: "session-1",
		ExecutionID: "root-1", ActualRole: "builder"}
	rootAnswer := api.ConversationEntity{ID: "answer-1", Kind: "assistant", ExecutionID: "root-1", Text: "Hel", Status: "streaming"}
	rootThinking := api.ConversationEntity{ID: "thinking-1", Kind: "thinking", ExecutionID: "root-1", Text: "private reasoning", Status: "streaming"}
	rootTool := api.ConversationEntity{ID: "tool-1", Kind: "tool", ExecutionID: "root-1", Name: "read_file", Status: "streaming"}
	childExecution := api.ConversationEntity{ID: "child-entity", Kind: "execution", ExecutionID: "child-1",
		ParentExecutionID: "root-1", Name: "delegate", Status: "streaming"}
	childAnswer := api.ConversationEntity{ID: "child-answer", Kind: "assistant", ExecutionID: "child-1",
		ParentExecutionID: "root-1", Text: "child text must stay off stdout", Status: "completed"}
	first := api.ConversationSnapshot{ViewID: "view-1", Order: []string{
		rootThinking.ID, rootAnswer.ID, rootTool.ID, childExecution.ID, childAnswer.ID,
	}, Entities: map[string]api.ConversationEntity{
		rootThinking.ID: rootThinking, rootAnswer.ID: rootAnswer, rootTool.ID: rootTool,
		childExecution.ID: childExecution, childAnswer.ID: childAnswer,
	}}
	if err := output.observe(apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &first}); err != nil {
		t.Fatal(err)
	}

	// A coalesced update extends the same parent entity and changes two
	// progress entities. Children and public thinking remain off stdout.
	rootAnswer.Text = "Hello"
	rootTool.Status = "incomplete"
	rootTool.Outcome = "failed"
	childExecution.Status = "completed"
	childExecution.Outcome = "success"
	update := api.ConversationUpdate{ViewID: "view-1", Upserts: []api.ConversationEntity{
		rootAnswer, rootTool, childExecution,
	}}
	if err := output.observe(apiclient.RunEvent{Kind: "run.update", State: state, Update: &update}); err != nil {
		t.Fatal(err)
	}
	// Reconnect and final snapshots repeat existing bodies and progress.
	final := first
	final.Entities = map[string]api.ConversationEntity{
		rootThinking.ID: rootThinking, rootAnswer.ID: rootAnswer, rootTool.ID: rootTool,
		childExecution.ID: childExecution, childAnswer.ID: childAnswer,
	}
	final.Order = append([]string(nil), first.Order...)
	terminal := state
	terminal.Phase = "terminal"
	if err := output.observe(apiclient.RunEvent{Kind: "run.snapshot", State: terminal, Conversation: &final}); err != nil {
		t.Fatal(err)
	}
	if err := output.finish("Hello"); err != nil {
		t.Fatal(err)
	}

	if got, want := stdout.String(), "Hel\nlo\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "Session: session-1\nExecution: root-1\nRole: builder\nRun phase: running\n[tool read_file] streaming\n[child execution=child-1] streaming\n[tool read_file] incomplete (failed)\n[child execution=child-1] completed\nRun phase: terminal\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestLifecycleNoticeStaysOnStderrAcrossReconnect(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := remoteRunOutput{stdout: &stdout, stderr: &stderr}
	state := api.RunState{ExecutionID: "root-1", Phase: "running"}
	notice := api.ConversationEntity{ID: "notice-1", Kind: "notice", ExecutionID: "root-1", Name: "hook/afterTurn",
		Text: "Hook afterTurn: cleanup failed", Status: "failed"}
	update := apiclient.RunEvent{State: state, Update: &api.ConversationUpdate{Upserts: []api.ConversationEntity{notice}}}
	if err := output.observe(update); err != nil {
		t.Fatal(err)
	}
	if err := output.observe(apiclient.RunEvent{State: state, Conversation: &api.ConversationSnapshot{Order: []string{notice.ID}, Entities: map[string]api.ConversationEntity{notice.ID: notice}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(stderr.String(), "Hook afterTurn: cleanup failed") != 1 || stdout.Len() != 0 {
		t.Fatalf("notice duplicated or leaked to final stdout: out=%q err=%q", stdout.String(), stderr.String())
	}
	var directOut, directErr bytes.Buffer
	direct := runOutput{stdout: &directOut, stderr: &directErr}
	if err := direct.observe(contracts.Observation{Kind: "context", Name: "hook/afterTurn", Phase: "failed", Preview: notice.Text}); err != nil {
		t.Fatal(err)
	}
	if directOut.Len() != 0 || !strings.Contains(directErr.String(), notice.Text) {
		t.Fatal("direct CLI lost the hook notice or printed it to answer stdout")
	}
}

func TestRemoteRunOutputKeepsSameTextFromDistinctRootSteps(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := remoteRunOutput{stdout: &stdout, stderr: &stderr, stream: true}
	state := api.RunState{ExecutionID: "root-1", Phase: "running"}
	snapshot := api.ConversationSnapshot{Order: []string{"step-1", "step-2"}, Entities: map[string]api.ConversationEntity{
		"step-1": {ID: "step-1", Kind: "assistant", ExecutionID: "root-1", Text: "same", Status: "completed"},
		"step-2": {ID: "step-2", Kind: "assistant", ExecutionID: "root-1", Text: "same", Status: "completed"},
	}}
	if err := output.observe(apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := output.observe(apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := output.finish("same"); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "same\nsame\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRemoteRunOutputWithoutStreamPrintsOnlyFinalText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := remoteRunOutput{stdout: &stdout, stderr: &stderr}
	state := api.RunState{ExecutionID: "root-1", Phase: "running"}
	snapshot := api.ConversationSnapshot{Order: []string{"answer"}, Entities: map[string]api.ConversationEntity{
		"answer": {ID: "answer", Kind: "assistant", ExecutionID: "root-1", Text: "partial answer", Status: "streaming"},
	}}
	if err := output.observe(apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &snapshot}); err != nil {
		t.Fatal(err)
	}
	if err := output.finish("saved final answer"); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "saved final answer\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}
