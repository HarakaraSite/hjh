package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/tui/renderer"
)

const (
	probeToken      = "Q"
	probeTokenCount = 1000
)

type terminalSink struct{ fd uintptr }

func (terminalSink) Write(data []byte) (int, error) { return len(data), nil }
func (terminalSink) Read([]byte) (int, error)       { return 0, io.EOF }
func (terminalSink) Close() error                   { return nil }
func (sink terminalSink) Fd() uintptr               { return sink.fd }

type writerTickMsg struct{}

func runIndependentWriterDemo(out io.Writer, receipt bool) error {
	file, ok := out.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("independent writer demo requires a terminal output file")
	}
	writer := renderer.New(out)
	if err := writer.Start(); err != nil {
		return err
	}
	model := &writerDemoModel{
		writer: writer, columns: 80, rows: 24, receipt: receipt,
	}
	program := tea.NewProgram(model, tea.WithOutput(terminalSink{fd: file.Fd()}))
	_, runErr := program.Run()
	return errors.Join(runErr, writer.Close())
}

func runDefaultWriterDemo(out io.Writer) error {
	file, ok := out.(interface {
		io.Writer
		Fd() uintptr
	})
	if !ok {
		return errors.New("default writer demo requires a terminal output file")
	}
	program := tea.NewProgram(&defaultWriterDemoModel{}, tea.WithOutput(file))
	_, err := program.Run()
	return err
}

type writerDemoModel struct {
	writer        *renderer.Writer
	columns       int
	rows          int
	tick          int
	receipt       bool
	receiptOffset int
	stablePending bool
	lastSize      renderer.Size
	lastBodyRows  []string
	sealedRows    []string
}

func (m *writerDemoModel) Init() tea.Cmd { return writerTick() }

func (m *writerDemoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.columns, m.rows = max(1, msg.Width), max(1, msg.Height)
		if m.receipt && m.lastSize.Columns > 0 && m.lastSize != (renderer.Size{Columns: m.columns, Rows: m.rows}) {
			m.sealedRows = append(m.sealedRows, m.lastBodyRows...)
			m.lastBodyRows = nil
			m.receiptOffset = len(probeText(m.tick))
		}
		if err := m.render(); err != nil {
			return m, tea.Quit
		}
		return m, nil
	case writerTickMsg:
		m.tick++
		if err := m.render(); err != nil {
			return m, tea.Quit
		}
		if m.tick < 100 {
			return m, writerTick()
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *writerDemoModel) View() tea.View { return tea.NewView("") }

func (m *writerDemoModel) render() error {
	text := probeText(m.tick)
	committed := []string(nil)
	visibleText := text
	if m.receipt {
		bodyRows := max(1, m.rows-4)
		desiredOffset := suffixOffsetByCells(text, m.columns*bodyRows)
		desiredOffset = max(desiredOffset, m.receiptOffset)
		if desiredOffset > m.receiptOffset {
			committed = renderer.WrapText(text[m.receiptOffset:desiredOffset], m.columns)
			m.receiptOffset = desiredOffset
		}
		visibleText = text[m.receiptOffset:]
	}
	rows := renderer.WrapText(strings.Join([]string{
		visibleText,
		fmt.Sprintf("live update: %d · Ctrl+C exits", m.tick),
	}, "\n"), m.columns)
	if len(rows) == 0 {
		rows = []string{""}
	}
	cursor := renderer.Cursor{Row: len(rows) - 1, Cell: ansi.StringWidth(rows[len(rows)-1])}
	if m.stablePending {
		committed = append([]string{
			"S06 independent Go writer",
			"stable: 日本語の確定行は一度だけ記録されます",
		}, committed...)
		m.stablePending = false
	}
	err := m.writer.Render(renderer.Frame{
		CommittedRows: committed,
		SealedRows:    m.sealedRows,
		LiveRows:      rows,
		Cursor:        cursor,
		Size:          renderer.Size{Columns: m.columns, Rows: m.rows},
	})
	if err == nil {
		m.lastSize = renderer.Size{Columns: m.columns, Rows: m.rows}
		m.sealedRows = nil
		m.lastBodyRows = nil
		if m.receipt && visibleText != "" {
			m.lastBodyRows = renderer.WrapText(visibleText, m.columns)
		}
	}
	return err
}

func probeText(tick int) string {
	base := "assistant: " + strings.TrimSuffix(strings.Repeat(probeToken+" ", probeTokenCount), " ")
	if tick > 0 {
		base += fmt.Sprintf(" stream update %d", tick)
	}
	return base
}

func suffixOffsetByCells(text string, maxCells int) int {
	if maxCells <= 0 {
		return len(text)
	}
	cells := 0
	offset := len(text)
	for offset > 0 {
		_, size := utf8.DecodeLastRuneInString(text[:offset])
		if size == 0 {
			break
		}
		width := ansi.StringWidth(text[offset-size : offset])
		if cells+width > maxCells {
			break
		}
		cells += width
		offset -= size
	}
	for offset > 0 && text[offset] != ' ' {
		offset--
	}
	return offset
}

type defaultWriterDemoModel struct {
	columns int
	tick    int
}

func (m *defaultWriterDemoModel) Init() tea.Cmd { return writerTick() }

func (m *defaultWriterDemoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.columns = max(1, msg.Width)
	case writerTickMsg:
		m.tick++
		if m.tick < 100 {
			return m, writerTick()
		}
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *defaultWriterDemoModel) View() tea.View {
	text := probeText(m.tick)
	content := strings.Join([]string{text, fmt.Sprintf("live update: %d · Ctrl+C exits", m.tick)}, "\n")
	return tea.NewView(ansi.Hardwrap(content, max(1, m.columns), true))
}

func writerTick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return writerTickMsg{} })
}
