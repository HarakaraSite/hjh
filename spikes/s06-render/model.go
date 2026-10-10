package main

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type sampleEvent struct {
	kind string
	text string
}

var sampleEvents = []sampleEvent{
	{kind: "assistant", text: "日本語の進捗を"},
	{kind: "assistant", text: "少しずつ表示して、安定行として残します。\n"},
	{kind: "tool", text: "ファイル一覧を取得中"},
	{kind: "assistant", text: "ツール結果を確認し、"},
	{kind: "assistant", text: "次の本文を更新します。\n"},
	{kind: "assistant", text: "最後の行は"},
	{kind: "assistant", text: " live tail のまま変化します。"},
}

type streamTickMsg struct{}

type model struct {
	input     textarea.Model
	focusCmd  tea.Cmd
	width     int
	nextEvent int
	liveTail  string
	finished  bool
	emitted   []string
}

func newModel() *model {
	input := textarea.New()
	input.Prompt = "❯ "
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 2
	input.MaxHeight = 5
	input.EndOfBufferCharacter = ' '
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	input.SetWidth(78)
	focusCmd := input.Focus()

	return &model{
		input:    input,
		focusCmd: focusCmd,
		width:    80,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.focusCmd, tea.Sequence(
		tea.Println("S06 normal-screen rendering spike"),
		tea.Println("Enter sends · Alt+Enter / Ctrl+J inserts a newline · Ctrl+C exits"),
		nextSampleTick(),
	))
}

func nextSampleTick() tea.Cmd {
	return tea.Tick(450*time.Millisecond, func(time.Time) tea.Msg { return streamTickMsg{} })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.input.SetWidth(max(1, m.width-2))
		return m, nil
	case streamTickMsg:
		return m, m.advanceSample()
	case tea.KeyPressMsg:
		if msg.Code == tea.KeyEnter && !msg.Mod.Contains(tea.ModAlt) &&
			!msg.Mod.Contains(tea.ModCtrl) && !msg.Mod.Contains(tea.ModShift) {
			return m, m.submit()
		}
		if msg.Code == 'c' && msg.Mod.Contains(tea.ModCtrl) {
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) advanceSample() tea.Cmd {
	if m.nextEvent >= len(sampleEvents) {
		m.finished = true
		return nil
	}

	event := sampleEvents[m.nextEvent]
	m.nextEvent++
	var cmds []tea.Cmd
	switch event.kind {
	case "assistant":
		cmds = append(cmds, m.appendAssistant(event.text)...)
	case "tool":
		cmds = append(cmds, m.printStable("[tool] "+event.text))
	}

	if m.nextEvent == len(sampleEvents) {
		m.finished = true
	} else {
		cmds = append(cmds, nextSampleTick())
	}
	return tea.Sequence(cmds...)
}

func (m *model) appendAssistant(text string) []tea.Cmd {
	var cmds []tea.Cmd
	for _, part := range strings.SplitAfter(text, "\n") {
		newline := strings.HasSuffix(part, "\n")
		piece := strings.TrimSuffix(part, "\n")
		if piece != "" {
			if m.liveTail == "" {
				m.liveTail = "assistant: "
			}
			m.liveTail += piece
		}
		if newline {
			if m.liveTail == "" {
				m.liveTail = "assistant: "
			}
			cmds = append(cmds, m.printStable(m.liveTail))
			m.liveTail = ""
		}
	}
	return cmds
}

func (m *model) printStable(line string) tea.Cmd {
	m.emitted = append(m.emitted, line)
	return tea.Println(line)
}

func (m *model) submit() tea.Cmd {
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		return nil
	}
	cmd := m.printStable("[you] " + value)
	m.input.Reset()
	return cmd
}

func (m *model) View() tea.View {
	tail := m.liveTail
	if tail == "" {
		tail = "(no unfinished assistant line)"
	}
	tail = ansi.Hardwrap(tail, m.width, true)

	content := strings.Join([]string{tail, m.input.View()}, "\n")
	return tea.NewView(content)
}
