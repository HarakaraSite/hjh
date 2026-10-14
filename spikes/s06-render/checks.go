package main

import (
	"fmt"
	"reflect"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func runChecks() error {
	if err := checkNormalScreenAndStreamSeparation(); err != nil {
		return err
	}
	if err := checkJapaneseEditorResizeAndNewlines(); err != nil {
		return err
	}
	return nil
}

func checkNormalScreenAndStreamSeparation() error {
	m := newModel()
	if m.View().AltScreen {
		return fmt.Errorf("normal screen unexpectedly enabled AltScreen")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(streamTickMsg{})
	if !strings.Contains(m.View().Content, "assistant: 日本語の進捗を") {
		return fmt.Errorf("first Japanese chunk is not visible in the live tail")
	}

	_, cmd := m.Update(streamTickMsg{})
	first := "assistant: 日本語の進捗を少しずつ表示して、安定行として残します。"
	if cmd == nil || !reflect.DeepEqual(m.emitted, []string{first}) {
		return fmt.Errorf("completed assistant line was not queued once: emitted=%q cmd=%v", m.emitted, cmd != nil)
	}
	if strings.Contains(m.View().Content, first) {
		return fmt.Errorf("stable assistant line remains in the live view after Println was queued")
	}

	_, cmd = m.Update(streamTickMsg{})
	tool := "[tool] ファイル一覧を取得中"
	if cmd == nil || !reflect.DeepEqual(m.emitted, []string{first, tool}) {
		return fmt.Errorf("tool marker is not ordered after the assistant line: %q", m.emitted)
	}
	if strings.Contains(m.View().Content, tool) {
		return fmt.Errorf("stable tool marker is duplicated in the live view")
	}

	m.Update(streamTickMsg{})
	if !strings.Contains(m.View().Content, "assistant: ツール結果を確認し、") {
		return fmt.Errorf("post-tool assistant tail is not visible")
	}
	_, cmd = m.Update(streamTickMsg{})
	second := "assistant: ツール結果を確認し、次の本文を更新します。"
	if cmd == nil || !reflect.DeepEqual(m.emitted, []string{first, tool, second}) {
		return fmt.Errorf("second completed line is not ordered after the tool marker: %q", m.emitted)
	}

	m.Update(streamTickMsg{})
	m.Update(streamTickMsg{})
	finalTail := "assistant: 最後の行は live tail のまま変化します。"
	if !m.finished || !strings.Contains(m.View().Content, finalTail) {
		return fmt.Errorf("final unfinished assistant tail did not remain live after the sample")
	}
	m.Update(tea.WindowSizeMsg{Width: 24, Height: 24})
	wrappedTail := ansi.Hardwrap(finalTail, 24, true)
	if !strings.HasPrefix(m.View().Content, wrappedTail+"\n") {
		return fmt.Errorf("live tail did not reflow when the terminal narrowed: %q", m.View().Content)
	}
	for _, line := range strings.Split(wrappedTail, "\n") {
		if width := ansi.StringWidth(line); width > 24 {
			return fmt.Errorf("live tail line exceeds resized terminal width: width=%d line=%q", width, line)
		}
	}
	return nil
}

func checkJapaneseEditorResizeAndNewlines() error {
	m := newModel()
	m.Update(tea.WindowSizeMsg{Width: 28, Height: 24})
	m.input.SetValue("日本語の文章を幅に合わせて折り返し、terminal resizeを確認します。")
	narrowHeight := m.input.Height()
	narrowWidth := m.input.Width()

	m.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	wideHeight := m.input.Height()
	if narrowWidth >= m.input.Width() || narrowHeight <= wideHeight {
		return fmt.Errorf("textarea did not reflow Japanese text on resize: widths=%d->%d heights=%d->%d", narrowWidth, m.input.Width(), narrowHeight, wideHeight)
	}

	m.input.SetValue("日本語")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	if got := m.input.Value(); got != "日本語\n" {
		return fmt.Errorf("Alt+Enter did not insert a newline: %q", got)
	}
	m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if got := m.input.Value(); got != "日本語\n\n" {
		return fmt.Errorf("Ctrl+J did not insert a newline: %q", got)
	}

	m.input.SetValue("依頼の1行目\n依頼の2行目")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil || m.input.Value() != "" || len(m.emitted) == 0 ||
		m.emitted[len(m.emitted)-1] != "[you] 依頼の1行目\n依頼の2行目" {
		return fmt.Errorf("plain Enter did not submit and clear the multiline draft")
	}
	return nil
}
