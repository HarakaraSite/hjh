package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/tui/renderer"
)

func TestUserLabelMatchesAssistantBackground(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	task := conversationEntity("task", "task", "request", 1)
	m.sessionEntities[task.ID] = task
	wantLabel := yellowSGR + "user:" + resetSGR + "\n"
	for _, rendered := range []string{m.renderTranscript(), strings.Join(m.ensureTranscript().rows, "\n")} {
		if !strings.HasPrefix(rendered, wantLabel) {
			t.Fatalf("user label must use assistant's default background: %q", rendered)
		}
		if !strings.Contains(rendered, solarizedMarkdownBackgroundSGR+task.Text) {
			t.Fatalf("user body must use assistant body background: %q", rendered)
		}
	}
	if entityLabelStyle(task) != strings.Replace(entityLabelStyle(api.ConversationEntity{Kind: "assistant"}), "assistant:", "user:", 1) {
		t.Fatal("user and assistant label styles differ")
	}
}

func TestUserBodyMatchesAssistantBackgroundWithoutRenderingMarkdown(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	m.width = 24
	source := "**原文のまま**\n\n" + strings.Repeat("日本語", 12) + "\n"
	blocks, _ := m.bodyBlocksForSource("user-body", source, "task")
	var rendered strings.Builder
	for _, block := range blocks {
		rendered.WriteString(block.render)
	}
	if !strings.Contains(ansi.Strip(rendered.String()), "**原文のまま**") {
		t.Fatal("user body must remain literal text")
	}
	for _, row := range renderer.WrapText(rendered.String(), m.width) {
		if row == "" {
			continue // Final line terminator.
		}
		if ansi.StringWidth(row) != m.width {
			t.Fatalf("user background did not fill the row: %q", row)
		}
		assertMarkdownCellsUseBase03(t, row)
	}
}

func TestConversationThemeKeepsThinkingAndToolContentsDimDefault(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{})
	defer func() { cancel(); m.shutdown() }()
	thinking := conversationEntity("thinking", "thinking", "考察 **原文**\n続きです", 1)
	thinking.Status = "completed"
	tool := conversationEntity("tool", "tool", "", 2)
	tool.Name, tool.Preview, tool.Status = "read_file", "path=docs/README.md", "completed"
	m.sessionEntities[thinking.ID], m.sessionEntities[tool.ID] = thinking, tool
	styled := m.renderTranscript()
	for _, span := range []string{
		yellowDimSGR + "thinking:", defaultDimSGR + "考察 **原文**",
		defaultDimSGR + "続きです", yellowDimSGR + "tool / read_file",
		defaultDimSGR + " · path=docs/README.md",
	} {
		if !strings.Contains(styled, span) {
			t.Fatalf("missing requested color for %q: %q", span, styled)
		}
	}
	if renderer.ReceiptText(styled) != "thinking:\n考察 **原文**\n続きです\ntool / read_file · path=docs/README.md ✓\n" {
		t.Fatalf("coloring changed thinking/tool content: %q", renderer.ReceiptText(styled))
	}
}

func TestThemeShowsNotificationsFailuresAndOrdinaryFooterMetadata(t *testing.T) {
	m, _, cancel := testModel(&fakeBackend{}, Options{Workspace: "/work/hjh"})
	defer func() { cancel(); m.shutdown() }()
	m.entries = []displayEntry{{role: "help", text: "help contents"}, {role: "run ended", text: "provider returned an error"}}
	text := m.renderTranscript()
	if !strings.Contains(text, magentaDimSGR+"help:") || !strings.Contains(text, redSGR+"provider returned an error") {
		t.Fatalf("notification/failure colors missing: %q", text)
	}
	m.width = 100
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706"
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model", Effort: "high"}
	m.sessionTitle = "README要約"
	m.setContextBudget(contextUsageBudget(2400, contextLimit(10000), "config/defaults"))
	if row := m.sessionFooter(); !strings.HasPrefix(row, "/work/hjh   ") || !strings.HasSuffix(row, "openrouter / model / high") || ansi.StringWidth(row) != m.width || strings.Contains(row, "\x1b[") {
		t.Fatalf("workspace/selection were not right-aligned ordinary text: %q", row)
	}
	row := m.footerDivider(100)
	if !strings.HasPrefix(row, blueSGR+"● ready"+resetSGR) || !strings.Contains(row, "README要約 · b71a29a6") || !strings.HasSuffix(row, "ctx 24%") || strings.Count(row, "\x1b[") != 2 {
		t.Fatalf("divider identity/context were not ordinary text: %q", row)
	}
	m.activeRun = &activeRun{}
	if row := m.footerDivider(100); !strings.HasPrefix(row, yellowSGR+"⠋ working"+resetSGR) {
		t.Fatalf("working activity lost its style: %q", row)
	}
	m.activeRun = nil
	if row := m.footerModelSelection(120); row != "openrouter / model / high" {
		t.Fatalf("provider/model/effort were styled: %q", row)
	}
	tool := api.ConversationEntity{Kind: "tool", Name: "write_file", Error: "failed"}
	if !strings.Contains(toolDisplayLine(tool), redSGR+"✗") {
		t.Fatal("failed tool was not marked red")
	}
}
