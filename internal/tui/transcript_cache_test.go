package tui

import (
	"strings"
	"testing"

	"charm.land/bubbletea/v2"
	"hjh/internal/api"
)

func TestUnclosedMarkdownStaysInBoundedLiveBandUntilClosed(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		suffix string
		body   string
	}{
		{name: "inline emphasis", prefix: "**Hello ", suffix: "**", body: strings.Repeat("A", 3000)},
		{name: "fenced code", prefix: "```go\n", suffix: "\n```", body: strings.Repeat("fmt.Println(\"ok\")\n", 250)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
			m, _, cancel := testModel(backend, Options{Core: backend.core})
			output := attachWriter(m)
			defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()

			source := tc.prefix + tc.body
			entity := conversationEntity("stream", "assistant", source, 1)
			entity.Status = "streaming"
			m.sessionEntities[entity.ID] = entity
			m.View()
			if got := m.markdownSourceCache["entity:stream:body"].source; got != source {
				t.Fatal("the full unfinished Markdown source was not retained in the transcript")
			}
			bodyHeight := max(1, m.height-3-1-1)
			if len(m.lastBodyRows) > bodyHeight {
				t.Fatalf("unfinished body escaped the live terminal band: rows=%d height=%d", len(m.lastBodyRows), bodyHeight)
			}
			frame, _, _, _, _, _ := m.screenFrame([]string{"header one", "header two", "header three"})
			if len(frame.LiveRows) > m.height {
				t.Fatalf("live frame exceeds terminal height: rows=%d height=%d", len(frame.LiveRows), m.height)
			}
			bodyKey := "entity:stream:body"
			if got := m.receiptByPart[bodyKey]; got != 0 {
				t.Fatalf("unfinished Markdown source was committed at offset %d", got)
			}

			output.Reset()
			update(m, tea.WindowSizeMsg{Width: 44, Height: 24})
			if strings.Contains(strings.Join(m.pendingSealRows, "\n"), tc.body[:min(16, len(tc.body))]) {
				t.Fatal("resize sealed text from the unfinished Markdown tail")
			}
			if got := m.receiptByPart[bodyKey]; got != 0 {
				t.Fatalf("resize advanced receipt through unfinished Markdown: %d", got)
			}

			entity = m.sessionEntities[entity.ID]
			entity.Text += tc.suffix
			entity.Status = "completed"
			m.sessionEntities[entity.ID] = entity
			m.invalidateTranscript()
			output.Reset()
			m.View()
			if m.sessionEntities[entity.ID].Text != entity.Text || m.markdownSourceCache["entity:stream:body"].source != entity.Text {
				t.Fatal("completed rendering replaced the original Markdown source")
			}
			if !strings.Contains(m.transcript.plain, tc.body[:min(30, len(tc.body))]) {
				t.Fatal("completed rendering omitted the body content")
			}
			m.sealCurrentFrame()
			if got := m.receiptByPart[bodyKey]; got != len(entity.Text) {
				t.Fatalf("closed Markdown was not committed at its full source offset: got=%d want=%d", got, len(entity.Text))
			}
		})
	}
}

func TestTranscriptUpdateReusesUnchangedEntityAndEntryMarkdown(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()

	m.sessionEntities["changing"] = conversationEntity("changing", "assistant", "changing source", 1)
	m.sessionEntities["stable"] = conversationEntity("stable", "assistant", "stable body has a distinctive cached phrase", 2)
	m.entries = append(m.entries, displayEntry{role: "help", text: "static help text has another distinctive cached phrase"})
	m.invalidateTranscript()
	m.View()

	const stableKey = "entity:stable:body"
	const entryKey = "entry:0:body"
	oldStable := m.markdownSourceCache[stableKey].blocks
	oldEntry := m.markdownSourceCache[entryKey].blocks
	if len(oldStable) == 0 || len(oldEntry) == 0 {
		t.Fatal("initial transcript did not cache the stable entity and help entry")
	}
	var lineKey transcriptLineKey
	var oldRows []string
	for key, cached := range m.transcriptLineCache {
		if strings.Contains(key.plain, "stable body has a distinctive cached phrase") || strings.Contains(key.plain, "static help text has another distinctive cached phrase") {
			lineKey, oldRows = key, cached.rows
			if len(oldRows) > 0 {
				break
			}
		}
	}
	if len(oldRows) == 0 {
		t.Fatal("initial transcript did not cache a wrapped stable line")
	}

	changed := m.sessionEntities["changing"]
	changed.Text += " with a streamed suffix"
	m.sessionEntities["changing"] = changed
	m.invalidateTranscript()
	m.View()

	if got := m.markdownSourceCache[stableKey].blocks; len(got) == 0 || &got[0] != &oldStable[0] {
		t.Fatal("an unchanged conversation entity was parsed again")
	}
	if got := m.markdownSourceCache[entryKey].blocks; len(got) == 0 || &got[0] != &oldEntry[0] {
		t.Fatal("an unchanged help entry was parsed again")
	}
	if got := m.transcriptLineCache[lineKey].rows; len(got) == 0 || &got[0] != &oldRows[0] {
		t.Fatal("an unchanged transcript line was wrapped again")
	}
}
