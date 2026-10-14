package tui

import (
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/tui/renderer"
)

func TestFailureNoticesUseAdoptedReasonsAndRecallHints(t *testing.T) {
	for _, tc := range []struct {
		code, reason string
		recall       bool
	}{
		{contracts.FailureResponse, "provider response invalid", true},
		{contracts.FailureTransport, "provider connection failed", true},
		{contracts.FailureProviderTimeout, "provider deadline exceeded", true},
		{contracts.FailureHTTP, "provider request failed", false},
		{contracts.FailureMissingCredential, "credential unavailable", false},
		{contracts.FailureContextBudget, "context budget exceeded", false},
		{contracts.FailureCommit, "session save failed", false},
		{contracts.FailureCancelled, "cancelled", false},
		{contracts.FailureStepLimit, "step limit reached", false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			diagnostic := &contracts.FailureDiagnostic{Code: tc.code}
			text := failureNotice(diagnostic, "failed", "private long detail", true)
			if !strings.Contains(text, tc.reason) || strings.Contains(text, "private") || strings.Contains(text, "try /recall") != tc.recall {
				t.Fatalf("notice = %q", text)
			}
			if strings.Contains(failureNotice(diagnostic, "failed", "detail", false), "/recall") {
				t.Fatal("pre-admission failure advertised a missing execution")
			}
		})
	}
	if got := failureNotice(nil, "failed", "unknown detail", true); got != "unknown detail" {
		t.Fatalf("unknown error changed: %q", got)
	}
}

func TestRootFailureNoticeSurvivesReloadAndIsNotDuplicatedAtFinish(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	diagnostic := &contracts.FailureDiagnostic{Code: contracts.FailureResponse, ParseReason: "invalid_tool_arguments"}
	entity := conversationEntity("root", "execution", "", 1)
	entity.ExecutionID, entity.Status, entity.Outcome, entity.Error = "a7e6a9f4-1234", "incomplete", "failed", "detailed invalid arguments"
	entity.Diagnostic = diagnostic
	m.sessionID = "session"
	m.activeRun = &activeRun{jobID: 887766, session: "session", entities: map[string]api.ConversationEntity{entity.ID: entity}}
	m.finishRun(runDoneMsg{jobID: 887766, value: api.RunState{SessionID: "session", ExecutionID: entity.ExecutionID,
		Result: &api.RunResult{Error: &api.Error{Message: entity.Error, Diagnostic: diagnostic}}}})
	const notice = "FAILED · provider response invalid · try /recall"
	for _, rendered := range []string{m.renderTranscript(), m.renderFullTranscript(), m.ensureTranscript().styled} {
		plain := renderer.ReceiptText(rendered)
		if strings.Count(plain, notice) != 1 || strings.Contains(plain, entity.Error) || strings.Contains(plain, entity.ExecutionID) {
			t.Fatalf("live/reloaded notice = %q", plain)
		}
	}
	if m.sessionEntities[entity.ID].Error != entity.Error {
		t.Fatal("presentation discarded inspect detail")
	}
	// A later save failure differs from the saved provider error and must remain visible.
	m.appendRunFailure(entity.ExecutionID, "failed", "save detail", &contracts.FailureDiagnostic{Code: contracts.FailureCommit})
	if !strings.Contains(renderer.ReceiptText(m.renderFullTranscript()), "FAILED · session save failed") {
		t.Fatal("Core finalization failure was suppressed")
	}
}

func TestChildFailureDoesNotAdvertiseRootRecall(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	entity := api.ConversationEntity{Kind: "execution", ExecutionID: "child-12345678", ParentExecutionID: "root", Status: "incomplete", Error: "long provider detail",
		Diagnostic: &contracts.FailureDiagnostic{Code: contracts.FailureTransport}}
	text := m.childSummaryLines(entity)[1].text
	if !strings.Contains(text, "provider connection failed") || strings.Contains(text, "long provider") || strings.Contains(text, "/recall") {
		t.Fatalf("child notice = %q", text)
	}
}
