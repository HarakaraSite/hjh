package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
)

func TestFooterUsesThreeRowsWithAdjacentEffortAndCurrentSession(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{Workspace: "/home/agent/projects/hjh"})
	defer func() { cancel(); m.shutdown() }()
	m.width = 100
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706"
	m.sessionTitle = "README要約"
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	rows := m.footerRows()
	for i := range rows {
		rows[i] = ansi.Strip(rows[i])
	}
	if len(rows) != 3 || rows[2] != "openrouter / model-name / high" {
		t.Fatalf("footer rows = %q", rows)
	}
	if !strings.HasPrefix(rows[1], "● ready   /home/agent/projects/hjh") || !strings.HasSuffix(rows[1], "README要約 · b71a29a6") {
		t.Fatalf("Session row = %q", rows[1])
	}
	if strings.Contains(rows[0], "F1") || !strings.Contains(rows[0], "Enter send") {
		t.Fatalf("idle controls = %q", rows[0])
	}
	m.width = 30
	m.sessionTitle = strings.Repeat("長いタイトル🐈", 12)
	rows = m.footerRows()
	for i := range rows {
		rows[i] = ansi.Strip(rows[i])
	}
	for _, row := range rows {
		if ansi.StringWidth(row) > m.width {
			t.Fatalf("footer overflows: %q", row)
		}
	}
	if !strings.HasSuffix(rows[1], "b71a29a6") || !strings.HasSuffix(rows[2], " / high") {
		t.Fatalf("narrow footer lost ID/effort: %q", rows)
	}
}

func TestFooterRunUsesCoreStartTimeAndAvailableControls(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.width = 120
	selection := contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	m.footerClockJobID = 4
	m.activeRun = &activeRun{jobID: 4, id: "run", coreID: "core", startedAt: time.Now().Add(-65 * time.Second), actualSelection: selection,
		hasActualSelection: true, inputAcceptance: api.InputAcceptance{FollowUp: true, Steering: true}}
	m.footerSelectionSet = true
	m.footerSelection.Selection = selection
	rows := m.footerRows()
	for i := range rows {
		rows[i] = ansi.Strip(rows[i])
	}
	if !strings.Contains(rows[1], "working 01:05") || strings.Contains(rows[2], "next:") {
		t.Fatalf("run footer = %q", rows)
	}
	for _, key := range []string{"F1 cancel", "F2 queue", "F3 steer"} {
		if !strings.Contains(rows[0], key) {
			t.Fatalf("missing run operation %q: %q", key, rows[0])
		}
	}
	if strings.Contains(rows[0], "Enter send") {
		t.Fatalf("running footer offers unavailable submit: %q", rows[0])
	}
	cmd := update(m, footerTickMsg{jobID: 4})
	if cmd == nil || m.footerClockFrame != 1 {
		t.Fatal("working spinner did not advance")
	}
	m.activeRun.canceling = true
	if controls := ansi.Strip(m.footerControls(120)); strings.Contains(controls, "F1") || strings.Contains(controls, "F2") || strings.Contains(controls, "F3") {
		t.Fatalf("cancelling controls = %q", controls)
	}
	m.activeRun = nil
	if cmd := update(m, footerTickMsg{jobID: 4}); cmd != nil {
		t.Fatal("idle footer clock kept running")
	}
	update(m, tea.KeyPressMsg{Code: '/', Text: "/"})
	if !strings.Contains(ansi.Strip(m.footerControls(120)), "Enter choose") || !strings.Contains(ansi.Strip(m.footerControls(120)), "Tab complete") {
		t.Fatal("slash draft did not change controls")
	}
}

func TestFooterSelectionReadsCorePreviewAndRejectsPreviousSelection(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core"}, selection: api.SelectionPreview{
		Selection: contracts.ModelSelection{Provider: "openrouter", ModelID: "from-config", Effort: "high"},
	}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.footerInitialized = true
	m.requestFooterSelection()
	first := receive(t, messages).(footerSelectionLoadedMsg)
	update(m, first)
	if got := m.footerModel(120); got != "openrouter / from-config / high" {
		t.Fatalf("resolved config footer = %q", got)
	}
	m.setProvider("another")
	second := receive(t, messages).(footerSelectionLoadedMsg)
	update(m, first)
	if m.footerSelectionSet {
		t.Fatal("old selection restored after provider change")
	}
	second.value.Selection = contracts.ModelSelection{Provider: "another", ModelID: "new-default", Effort: "low"}
	update(m, second)
	if got := m.footerModel(120); got != "another / new-default / low" {
		t.Fatalf("new route footer = %q", got)
	}
}

func contextLimit(value int64) *int64 { return &value }

func contextUsageBudget(estimated int64, limit *int64, source string) contracts.ContextBudgetStatus {
	return contracts.ContextBudgetStatus{Summary: contracts.ContextBudgetSummary{
		EstimatedTokens: estimated,
		Budget:          contracts.ContextBudgetValues{Limit: limit, LimitSource: source},
	}}
}

func TestFooterContextUsageFormats(t *testing.T) {
	cases := []struct {
		name   string
		budget contracts.ContextBudgetStatus
		want   string
	}{
		{"config limit percent", contextUsageBudget(2400, contextLimit(10000), "config/defaults"), "ctx 24%"},
		{"hook limit marks the budget basis", contextUsageBudget(38000, contextLimit(100000), "hook"), "ctx 38%*"},
		{"unknown limit shows tokens", contextUsageBudget(3040, nil, ""), "ctx 3.0k tok"},
		{"percent truncates", contextUsageBudget(9, contextLimit(100), "config/defaults"), "ctx 9%"},
		{"tokens truncate to one decimal", contextUsageBudget(12400, nil, ""), "ctx 12.4k tok"},
		{"large tokens keep k scaling", contextUsageBudget(123900, nil, ""), "ctx 123.9k tok"},
		{"small tokens stay integer", contextUsageBudget(700, nil, ""), "ctx 700 tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := contextUsageLabel(tc.budget); got != tc.want {
				t.Fatalf("context usage label = %q, want %q", got, tc.want)
			}
			m, _, cancel := testModel(&fakeBackend{}, Options{})
			defer func() { cancel(); m.shutdown() }()
			m.footerSelectionSet = true
			m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
			m.setContextBudget(tc.budget)
			if row := ansi.Strip(m.footerModel(60)); !strings.HasSuffix(row, tc.want) {
				t.Fatalf("footer fragment = %q, want suffix %q", row, tc.want)
			}
		})
	}
}

func TestFooterShowsContextUsageRightOfSelection(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.width = 60
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	m.setContextBudget(contextUsageBudget(2400, contextLimit(10000), "config/defaults"))
	row := ansi.Strip(m.footerRows()[2])
	if !strings.HasPrefix(row, "openrouter / model-name / high") || !strings.HasSuffix(row, "ctx 24%") {
		t.Fatalf("selection is not left of the right-aligned fragment: %q", row)
	}
	if ansi.StringWidth(row) != m.width {
		t.Fatalf("fragment row width = %d, want %d: %q", ansi.StringWidth(row), m.width, row)
	}
	// The → next: dual label keeps the fragment on the right edge.
	m.activeRun = &activeRun{jobID: 1, id: "run", coreID: "core",
		actualSelection:    contracts.ModelSelection{Provider: "openrouter", ModelID: "current-model", Effort: "high"},
		hasActualSelection: true}
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "next-model", Effort: "low"}
	row = ansi.Strip(m.footerModel(m.width))
	if !strings.Contains(row, "→ next:") || !strings.HasSuffix(row, "ctx 24%") || ansi.StringWidth(row) != m.width {
		t.Fatalf("next selection row lost the fragment: %q", row)
	}
}

func TestFooterOmitsContextUsageWithoutMeasurement(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.width = 60
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	row := ansi.Strip(m.footerModel(m.width))
	if strings.Contains(row, "ctx") {
		t.Fatalf("usage fragment shown without measurement: %q", row)
	}
	if row != "openrouter / model-name / high" {
		t.Fatalf("model row changed without measurement: %q", row)
	}
}

func TestFooterDropsContextUsageOnNarrowModelRow(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	selection := contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	m.footerSelectionSet = true
	m.footerSelection.Selection = selection
	m.setContextBudget(contextUsageBudget(2400, contextLimit(10000), "config/defaults"))
	row := ansi.Strip(m.footerModel(16))
	if strings.Contains(row, "ctx") {
		t.Fatalf("narrow row kept the fragment: %q", row)
	}
	if row != ansi.Strip(footerSelectionLabel(selection, 16)) {
		t.Fatalf("narrow row did not return the width to selection: %q", row)
	}
}

type contextUsageBackend struct {
	*fakeBackend
	latestBudget *contracts.ContextBudgetStatus
}

func (b *contextUsageBackend) SessionContext(_ context.Context, coreID, sessionID string) (api.SessionContext, error) {
	return api.SessionContext{CoreID: coreID, SessionID: sessionID, LatestBudget: b.latestBudget}, nil
}

func TestFooterContextUsageSeedsFromSessionLoadAndRunEvents(t *testing.T) {
	seed := contextUsageBudget(3040, nil, "")
	backend := &contextUsageBackend{
		fakeBackend:  &fakeBackend{core: api.Core{CoreID: "core"}, session: api.Session{ID: "session-1"}},
		latestBudget: &seed,
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.changeSession(sessionAction{resumeID: "session-1"})
	updateUntil(t, m, messages, func(msg tea.Msg) bool { _, ok := msg.(sessionLoadedMsg); return ok })
	if !m.contextBudgetSet || m.contextBudget.Summary.EstimatedTokens != 3040 {
		t.Fatalf("resume did not seed the latest budget: set=%t %+v", m.contextBudgetSet, m.contextBudget)
	}
	if row := ansi.Strip(m.footerModel(60)); !strings.HasSuffix(row, "ctx 3.0k tok") {
		t.Fatalf("resumed session footer = %q", row)
	}
	// A later run step overwrites the restored value with its own projection.
	m.activeRun = &activeRun{jobID: 1, id: "run-1", coreID: "core"}
	budget := contextUsageBudget(2400, contextLimit(10000), "config/defaults")
	update(m, runProgressMsg{jobID: 1, value: apiclient.RunEvent{Kind: "run.update", State: api.RunState{
		CoreID: "core", RunID: "run-1", Seq: 1, ContextBudget: &budget}}})
	if row := ansi.Strip(m.footerModel(60)); !strings.HasSuffix(row, "ctx 24%") {
		t.Fatalf("run step did not overwrite the fragment: %q", row)
	}
	// A new conversation never inherits the previous Session's value.
	m.activeRun = nil
	m.handleCommand("/new")
	if m.contextBudgetSet || strings.Contains(ansi.Strip(m.footerModel(60)), "ctx") {
		t.Fatalf("new conversation kept the previous usage: set=%t", m.contextBudgetSet)
	}
}

func TestContextDetailsExplainHookBudgetMarker(t *testing.T) {
	status := contextUsageBudget(2400, contextLimit(10000), "hook")
	for _, detailed := range []bool{false, true} {
		status.DetailAvailable = detailed
		status.Fact = contracts.JSON(map[string]any{"budget": status.Summary.Budget})
		text := formatCoreBudget(status)
		if !strings.Contains(text, "footer * means hook policy, not model context capacity") {
			t.Fatalf("hook basis unexplained (detailed=%t): %s", detailed, text)
		}
	}
}
