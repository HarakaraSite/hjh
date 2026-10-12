package tui

import (
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/tui/renderer"
)

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
	m.sessionID = "b71a29a6-7552-485d-9922-a1d7ae426706"
	m.footerSelectionSet = true
	m.footerSelection.Selection = contracts.ModelSelection{Provider: "openrouter", ModelID: "model", Effort: "high"}
	row := m.sessionFooter()
	if !strings.Contains(row, blueSGR+"● ready"+resetSGR+"   /work/hjh") || !strings.HasSuffix(row, " · b71a29a6") {
		t.Fatalf("workspace/short ID were not ordinary text: %q", row)
	}
	if row := m.footerModel(120); row != "openrouter / model / high" {
		t.Fatalf("provider/model/effort were styled: %q", row)
	}
	tool := api.ConversationEntity{Kind: "tool", Name: "write_file", Error: "failed"}
	if !strings.Contains(toolDisplayLine(tool), redSGR+"✗") {
		t.Fatal("failed tool was not marked red")
	}
}
