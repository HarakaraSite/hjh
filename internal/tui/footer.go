package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/contracts"
)

// Footer selection is a Core preview, never a second implementation of model
// resolution. The running selection remains bound to the root run.
type footerSelectionLoadedMsg struct {
	jobID, generation uint64
	coreID            string
	request           api.SelectionRequest
	value             api.SelectionPreview
	err               error
}

func (m *model) resetFooterSelection() {
	m.cancelJob(m.footerSelectionJobID)
	m.footerSelectionJobID = 0
	m.footerSelectionSet = false
	m.footerSelectionError = nil
}

func (m *model) requestFooterSelection() {
	m.resetFooterSelection()
	if !m.footerInitialized || m.sessionLoading || m.resumePending {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.footerSelectionJobID = job.id
	request, generation, coreID := m.selectionRequest(), m.selectionGeneration, m.core.CoreID
	saveTail := m.selectionSaveTail
	m.launch(job, func() tea.Msg {
		if err := waitForSelectionSave(ctx, saveTail); err != nil {
			return footerSelectionLoadedMsg{job.id, generation, coreID, request, api.SelectionPreview{}, err}
		}
		value, err := m.backend.Selection(ctx, request)
		return footerSelectionLoadedMsg{job.id, generation, coreID, request, value, err}
	})
}

func (m *model) acceptFooterSelection(msg footerSelectionLoadedMsg) {
	if msg.jobID == 0 || msg.jobID != m.footerSelectionJobID ||
		msg.generation != m.selectionGeneration || msg.coreID != m.core.CoreID || msg.request != m.selectionRequest() {
		return
	}
	m.footerSelectionJobID = 0
	m.footerSelectionError = msg.err
	m.footerSelectionSet = msg.err == nil
	m.footerSelection = msg.value
}

type footerTickMsg struct{ jobID uint64 }

func (m *model) footerTick() tea.Cmd {
	jobID := m.activeRun.jobID
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return footerTickMsg{jobID} })
}

func (m *model) footerRows() []string {
	width := max(1, m.width)
	return []string{m.footerControls(width), m.sessionFooter(), m.footerModel(width)}
}

func (m *model) footerControls(width int) string {
	var controls []string
	switch {
	case m.authUI != nil && m.authUI.open:
		return styleFooterControls(ansi.Truncate(m.authHelp(), width, "…"), "")
	case m.connectionPicker != nil && m.connectionPicker.kind == modelPicker:
		return styleFooterControls(ansi.Truncate("↑/↓ select · type search · Enter apply · Tab favorite · Esc close", width, "…"), "")
	case m.connectionPicker != nil:
		return styleFooterControls(ansi.Truncate("↑/↓ select · Enter applies to next run · Esc close", width, "…"), "")
	case m.pickerOpen && m.sessionDeleteJobID != 0:
		return styleFooterControls(ansi.Truncate("Deleting Session · Esc close", width, "…"), "")
	case m.pickerOpen && m.sessionDeleteTarget != "":
		return styleFooterControls(ansi.Truncate("Enter delete Session and history · Esc cancel", width, "…"), "")
	case m.pickerOpen:
		return styleFooterControls(ansi.Truncate("↑/↓ select · Enter resumes · Del delete · Esc close", width, "…"), "")
	}
	if !m.shutdownPending && !m.detachPending {
		if m.commandMenuVisible() {
			controls = append(controls, "↑/↓ select", "Enter choose", "Tab complete", "Esc close")
		} else if strings.HasPrefix(strings.TrimSpace(m.textarea.Value()), "/") {
			controls = append(controls, "Enter command")
		} else if m.activeRun == nil && !m.sessionLoading && !m.resumePending {
			controls = append(controls, "Enter send")
		}
		if run := m.activeRun; run != nil && !run.canceling && !run.chainCancelRequested {
			controls = append(controls, "F1 cancel")
			if run.inputAcceptance.FollowUp {
				controls = append(controls, "F2 queue")
			}
			if run.inputAcceptance.Steering {
				controls = append(controls, "F3 steer")
			}
		}
		controls = append(controls, "F4 sessions", "/ commands", "Alt+Enter newline")
	}
	notice := m.footerNotice()
	if notice != "" {
		controls = append([]string{notice}, controls...)
	}
	for len(controls) > 1 && ansi.StringWidth(strings.Join(controls, " · ")) > width {
		controls = controls[:len(controls)-1]
	}
	return styleFooterControls(ansi.Truncate(strings.Join(controls, " · "), width, "…"), notice)
}

var footerControlKey = regexp.MustCompile(`^(Enter|Esc|Tab|Alt\+Enter|Ctrl\+[A-Za-z]|F[1-4]|↑/↓|←/→|/)( |$)`)

func styleFooterControls(text, notice string) string {
	parts := strings.Split(text, " · ")
	for index, part := range parts {
		if key := footerControlKey.FindStringSubmatch(part); len(key) > 1 {
			parts[index] = key[1] + styledText(part[len(key[1]):], defaultDimSGR)
		} else {
			style := magentaDimSGR
			if strings.Contains(notice, "failed") || strings.Contains(notice, "unconfirmed") || strings.Contains(notice, "unavailable") || strings.Contains(notice, "not accepted") {
				style = redSGR
			}
			parts[index] = styledText(part, style)
		}
	}
	return strings.Join(parts, styledText(" · ", defaultDimSGR))
}

func (m *model) footerNotice() string {
	notice := m.status
	switch {
	case notice == "ready", strings.HasPrefix(notice, "run preparing"),
		strings.HasPrefix(notice, "run running"), strings.HasPrefix(notice, "run settling"),
		strings.HasPrefix(notice, "run terminal"), strings.HasPrefix(notice, "starting run"),
		strings.HasPrefix(notice, "attaching to run"), strings.HasPrefix(notice, "following up"),
		strings.HasPrefix(notice, "resumed ·"), strings.HasPrefix(notice, "new conversation ·"),
		strings.HasPrefix(notice, "loading Session"), strings.HasPrefix(notice, "cancelling run input chain"),
		strings.HasPrefix(notice, "cancel accepted"):
		notice = ""
	}
	if m.runtimeLatestStatus != "" && !strings.Contains(notice, m.runtimeLatestStatus) {
		if notice != "" {
			notice += " · "
		}
		notice += m.runtimeLatestStatus
	}
	if m.footerSelectionError != nil && m.activeRun == nil {
		if notice != "" {
			notice += " · "
		}
		notice += "selection unavailable: " + m.footerSelectionError.Error()
	}
	return strings.Join(strings.Fields(notice), " ")
}

func (m *model) footerActivity() string {
	if run := m.activeRun; run != nil {
		activity := "working"
		if run.canceling || run.chainCancelRequested || m.shutdownPending {
			activity = "cancelling"
		} else if strings.HasPrefix(m.status, "connection interrupted") {
			activity = "reconnecting"
		} else if run.phase == "terminal" || run.phase == "settling" {
			activity = "settling"
		}
		spinner := [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		label := spinner[m.footerClockFrame%uint64(len(spinner))] + " " + activity
		if !run.startedAt.IsZero() {
			total := max(0, int(time.Since(run.startedAt).Seconds()))
			if total >= 3600 {
				label += fmt.Sprintf(" %d:%02d:%02d", total/3600, total/60%60, total%60)
			} else {
				label += fmt.Sprintf(" %02d:%02d", total/60, total%60)
			}
		}
		return label
	}
	if m.sessionLoading || m.resumePending {
		return "loading"
	}
	if m.shutdownPending {
		return "stopping Core"
	}
	if m.chainCancel != nil && !m.chainCancelSettled {
		return "cancelling"
	}
	return "● ready"
}

func (m *model) sessionFooter() string {
	width := max(1, m.width)
	activity := m.footerActivity()
	activityStyle := yellowSGR
	if activity == "● ready" {
		activityStyle = blueSGR
	}
	activity = styledText(activity, activityStyle)
	id := displayOr(m.shortSessionID(m.sessionID), "new")
	title := displayOr(m.sessionTitle, "(untitled)")
	if m.sessionID == "" {
		title = "new conversation"
	}
	// Preserve the state and short ID before spending cells on title/path.
	if ansi.StringWidth(activity)+ansi.StringWidth(id)+3 > width {
		return ansi.Truncate(activity, width, "…")
	}
	available := width - ansi.StringWidth(activity) - ansi.StringWidth(id) - 6
	right := id
	if available > 0 {
		right = ansi.Truncate(title, available, "…") + " · " + id
	}
	left := activity
	pathWidth := width - ansi.StringWidth(left) - ansi.StringWidth(right) - 6
	if pathWidth > 0 && m.workspace != "" {
		path := m.workspace
		if ansi.StringWidth(path) > pathWidth {
			path = ansi.TruncateLeft(path, ansi.StringWidth(path)-pathWidth+1, "…")
		}
		left += "   " + path
	}
	return left + strings.Repeat(" ", max(3, width-ansi.StringWidth(left)-ansi.StringWidth(right))) + right
}

func (m *model) footerModel(width int) string {
	var selection contracts.ModelSelection
	if run := m.activeRun; run != nil {
		if !run.hasActualSelection {
			return ansi.Truncate("resolving selection", width, "…")
		}
		selection = run.actualSelection
	} else if m.footerSelectionSet {
		selection = m.footerSelection.Selection
	} else {
		label := "resolving selection"
		if m.footerSelectionError != nil {
			label = "selection unavailable"
		}
		return ansi.Truncate(label, width, "…")
	}
	if m.activeRun != nil && m.footerSelectionSet {
		next := m.footerSelection.Selection
		if next.Provider != selection.Provider || next.ModelID != selection.ModelID || next.Effort != selection.Effort {
			separator := " → next: "
			available := width - ansi.StringWidth(separator)
			if available >= 2 {
				nextWidth := min(ansi.StringWidth(footerSelectionLabel(next, width)), available/2)
				return footerSelectionLabel(selection, available-nextWidth) + separator + footerSelectionLabel(next, nextWidth)
			}
		}
	}
	return footerSelectionLabel(selection, width)
}

func footerSelectionLabel(selection contracts.ModelSelection, width int) string {
	provider := displayOr(selection.Provider, "—")
	effort := displayOr(selection.Effort, "default")
	model := displayOr(selection.ModelID, "—")
	available := width - ansi.StringWidth(provider) - ansi.StringWidth(effort) - 6
	if available > 0 {
		model = ansi.Truncate(model, available, "…")
	}
	return ansi.Truncate(provider+" / "+model+" / "+effort, width, "…")
}
