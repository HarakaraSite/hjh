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

func TestFooterUsesThreeRowsWithWorkspaceAndAdjacentEffort(t *testing.T) {
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
	if rows[1] != "/home/agent/projects/hjh" {
		t.Fatalf("workspace row = %q", rows[1])
	}
	divider := ansi.Strip(m.footerDivider(m.width))
	if !strings.HasPrefix(divider, "● ready") || !strings.Contains(divider, "README要約 · b71a29a6") {
		t.Fatalf("Session divider = %q", divider)
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
	if !strings.Contains(ansi.Strip(m.footerDivider(m.width)), "b71a29a6") || !strings.HasSuffix(rows[2], " / high") {
		t.Fatalf("narrow footer lost ID/effort: %q", rows)
	}
	m.width = 16
	if row := m.sessionFooter(); !strings.HasPrefix(row, "…") || !strings.HasSuffix(row, "/projects/hjh") || ansi.StringWidth(row) > m.width {
		t.Fatalf("narrow workspace row = %q", row)
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
	divider := ansi.Strip(m.footerDivider(m.width))
	if !strings.HasPrefix(divider, "⠋ working 01:05") || strings.Contains(rows[2], "next:") {
		t.Fatalf("run divider = %q, footer = %q", divider, rows)
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
	if cmd == nil || m.footerClockFrame != 1 || !strings.HasPrefix(ansi.Strip(m.footerDivider(m.width)), "⠙ working 01:05") {
		t.Fatal("working spinner did not advance on the divider")
	}
	m.activeRun.canceling = true
	if divider := ansi.Strip(m.footerDivider(m.width)); !strings.HasPrefix(divider, "⠙ cancelling 01:05") {
		t.Fatalf("cancelling divider = %q", divider)
	}
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
			if row := ansi.Strip(m.footerDivider(60)); !strings.HasSuffix(row, tc.want) {
				t.Fatalf("footer fragment = %q, want suffix %q", row, tc.want)
			}
		})
	}
}

func TestFooterDividerCentersSessionAndRightAlignsContext(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.width = 100
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706"
	m.sessionTitle = "README要約🐈"
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	m.setContextBudget(contextUsageBudget(2400, contextLimit(10000), "config/defaults"))
	identity := m.sessionTitle + " · b71a29a6"
	for _, width := range []int{100, 101} {
		row := ansi.Strip(m.footerDivider(width))
		index := strings.Index(row, identity)
		if index < 0 || ansi.StringWidth(row[:index]) != (width-ansi.StringWidth(identity))/2 {
			t.Fatalf("identity is not terminal-centered at width %d: %q", width, row)
		}
		if !strings.HasPrefix(row, "● ready") || !strings.HasSuffix(row, "ctx 24%") || !strings.Contains(row, "─") || ansi.StringWidth(row) != width {
			t.Fatalf("divider metadata/width = %q at width %d", row, width)
		}
	}
	if row := m.footerModel(m.width); row != "openrouter / model-name / high" {
		t.Fatalf("context displaced model selection: %q", row)
	}
	// Actual and next selections remain together below the editor.
	m.activeRun = &activeRun{jobID: 1, id: "run", coreID: "core",
		actualSelection:    contracts.ModelSelection{Provider: "openrouter", ModelID: "current-model", Effort: "high"},
		hasActualSelection: true}
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "next-model", Effort: "low"}
	if row := m.footerModel(m.width); row != "openrouter / current-model / high → next: openrouter / next-model / low" {
		t.Fatalf("actual/next selection row = %q", row)
	}
	if row := ansi.Strip(m.footerDivider(m.width)); !strings.HasSuffix(row, "ctx 24%") {
		t.Fatalf("running divider lost context: %q", row)
	}
}

func TestFooterDividerConstrainsIdentityBetweenSideLabels(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706"
	m.sessionTitle = strings.Repeat("長いタイトル🐈", 12)
	m.setContextBudget(contextUsageBudget(3040, nil, ""))
	row := ansi.Strip(m.footerDivider(48))
	if !strings.HasPrefix(row, "● ready") || !strings.Contains(row, "… · b71a29a6") || !strings.HasSuffix(row, "ctx 3.0k tok") || ansi.StringWidth(row) != 48 {
		t.Fatalf("long title displaced activity, ID, or context: %q", row)
	}
	m.sessionTitle = "README\n要約"
	row = ansi.Strip(m.footerDivider(60))
	if strings.ContainsAny(row, "\r\n") || !strings.Contains(row, "README 要約 · b71a29a6") {
		t.Fatalf("title did not stay on the separator row: %q", row)
	}
	// A long activity can shift a short identity away from terminal center.
	m.sessionTitle = "X"
	m.activeRun = &activeRun{startedAt: time.Now().Add(-65 * time.Second)}
	row = ansi.Strip(m.footerDivider(46))
	index := strings.Index(row, "X · b71a29a6")
	if index < 0 || !strings.HasPrefix(row, "⠋ working 01:05") || ansi.StringWidth(row[:index]) != ansi.StringWidth(m.footerActivity())+3 || !strings.HasSuffix(row, "ctx 3.0k tok") || ansi.StringWidth(row) != 46 {
		t.Fatalf("side labels overlap identity: %q", row)
	}
}

func TestFooterOmitsContextUsageWithoutMeasurement(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model-name", Effort: "high"}
	row := ansi.Strip(m.footerDivider(60))
	if strings.Contains(row, "ctx") || ansi.StringWidth(row) != 60 {
		t.Fatalf("divider without measurement = %q", row)
	}
	if row := m.footerModel(60); row != "openrouter / model-name / high" {
		t.Fatalf("model row changed without measurement: %q", row)
	}
}

func TestFooterDividerPrioritizesActivityAndIDOnNarrowRows(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706"
	m.sessionTitle = strings.Repeat("長いタイトル🐈", 12)
	m.setContextBudget(contextUsageBudget(2400, contextLimit(10000), "config/defaults"))
	// At 28 cells only activity, ID, and context fit; the title yields first.
	row := ansi.Strip(m.footerDivider(28))
	if !strings.HasPrefix(row, "● ready") || !strings.Contains(row, "b71a29a6") || !strings.HasSuffix(row, "ctx 24%") || strings.Contains(row, " · ") {
		t.Fatalf("minimal complete metadata = %q", row)
	}
	row = ansi.Strip(m.footerDivider(24))
	if !strings.HasPrefix(row, "● ready") || !strings.Contains(row, "b71a29a6") || strings.Contains(row, "ctx") {
		t.Fatalf("context displaced activity or ID: %q", row)
	}
	row = ansi.Strip(m.footerDivider(18))
	if !strings.HasPrefix(row, "● ready") || !strings.HasSuffix(row, "b71a29a6") {
		t.Fatalf("short ID was not preserved: %q", row)
	}
	row = ansi.Strip(m.footerDivider(16))
	if !strings.HasPrefix(row, "● ready") || strings.Contains(row, "b71a") || strings.Contains(row, "ctx") {
		t.Fatalf("activity did not take priority: %q", row)
	}
	for _, width := range []int{1, 6, 16, 18, 24, 28, 30, 48} {
		row := ansi.Strip(m.footerDivider(width))
		if strings.ContainsAny(row, "\r\n") || ansi.StringWidth(row) != width {
			t.Fatalf("divider wraps or loses width %d: %q", width, row)
		}
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
	if row := ansi.Strip(m.footerDivider(60)); !strings.HasSuffix(row, "ctx 3.0k tok") {
		t.Fatalf("resumed session footer = %q", row)
	}
	// A later run step overwrites the restored value with its own projection.
	m.activeRun = &activeRun{jobID: 1, id: "run-1", coreID: "core"}
	budget := contextUsageBudget(2400, contextLimit(10000), "config/defaults")
	update(m, runProgressMsg{jobID: 1, value: apiclient.RunEvent{Kind: "run.update", State: api.RunState{
		CoreID: "core", RunID: "run-1", Seq: 1, ContextBudget: &budget}}})
	if row := ansi.Strip(m.footerDivider(60)); !strings.HasSuffix(row, "ctx 24%") {
		t.Fatalf("run step did not overwrite the fragment: %q", row)
	}
	// A new conversation never inherits the previous Session's value.
	m.activeRun = nil
	m.handleCommand("/new")
	if m.contextBudgetSet || strings.Contains(ansi.Strip(m.footerDivider(60)), "ctx") {
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
