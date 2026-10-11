package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/contracts"
)

type detailBackend struct {
	*fakeBackend
	selected chan api.ConversationDetailLocator
	result   api.Record
}

func (b *detailBackend) Record(_ context.Context, execution string, ordinal int64) (api.Record, error) {
	b.selected <- api.ConversationDetailLocator{ExecutionID: execution, Ordinal: ordinal}
	return b.result, nil
}

func TestToolCommandReadsOnlyTheSelectedSavedResult(t *testing.T) {
	const body = "READMEの詳細を読む。\n通常会話へは自動展開しない。"
	b := &detailBackend{fakeBackend: &fakeBackend{}, selected: make(chan api.ConversationDetailLocator, 1),
		result: api.Record{Payload: contracts.JSON(map[string]any{"result": map[string]string{"text": body}})}}
	ctx, cancel := context.WithCancel(context.Background())
	m := newModel(ctx, cancel, b, Options{})
	messages := make(chan tea.Msg, 1)
	m.send = func(msg tea.Msg) { messages <- msg }
	defer func() { cancel(); m.shutdown() }()
	for i, id := range []string{"outer", "read"} {
		entity := conversationEntity(id, "tool", "", int64(i+1))
		entity.Name = "read_file"
		entity.Preview = "README.md"
		entity.Detail = &api.ConversationDetailLocator{ExecutionID: "execution-1", Ordinal: int64(10 + i)}
		m.sessionEntities[id] = entity
	}
	m.handleCommand("/tools")
	if len(m.entries) != 1 || !strings.Contains(m.entries[0].text, "2 · read_file README.md") {
		t.Fatal("tool list does not identify the saved calls for detail selection")
	}
	m.handleCommand("/tool 2")
	select {
	case msg := <-messages:
		m.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("selected tool detail did not complete")
	}
	selected := <-b.selected
	if selected.ExecutionID != "execution-1" || selected.Ordinal != 11 {
		t.Fatalf("detail fetched another saved result: %+v", selected)
	}
	if len(m.entries) != 2 || m.entries[1].text != body {
		t.Fatalf("explicit tool detail did not show the complete readable result: %+v", m.entries)
	}
}
