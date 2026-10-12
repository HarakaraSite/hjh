package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/contracts"
)

func (m *model) orientationRequest() api.OrientationRequest {
	request := api.OrientationRequest{SessionID: m.sessionID, Workspace: m.workspace}
	if m.roleSet {
		request.Role = m.roleID
	}
	return request
}

func (m *model) requestHeaderOrientation() {
	if m.headerPrinted {
		return
	}
	if m.activeRun != nil && m.activeRun.actualContext != nil {
		m.adoptActualOrientation(*m.activeRun.actualContext)
		return
	}
	if run := m.activeRun; run != nil && run.attached && run.phase != "terminal" {
		if m.headerOrientationJobID != 0 {
			m.cancelJob(m.headerOrientationJobID)
			m.headerOrientationJobID = 0
		}
		m.headerOrientation = api.Orientation{}
		m.headerOrientationSet = false
		m.deferTranscriptUntilHeader = true
		m.headerOrientationPending = true
		m.headerOrientationFailed = false
		return
	}
	if m.headerOrientationJobID != 0 {
		m.cancelJob(m.headerOrientationJobID)
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	request, generation, coreID := m.orientationRequest(), m.sessionGeneration, m.core.CoreID
	m.headerOrientationJobID = job.id
	m.deferTranscriptUntilHeader = true
	m.headerOrientationPending = true
	m.headerOrientationFailed = false
	m.launch(job, func() tea.Msg {
		value, err := m.backend.Orientation(ctx, request)
		return orientationLoadedMsg{
			jobID: job.id, generation: generation, coreID: coreID,
			request: request, value: value, err: err,
		}
	})
}

func (m *model) releaseAttachedOrientationWait(run *activeRun) {
	if run == nil || !run.attached || run.phase != "terminal" || run.actualContext != nil ||
		m.headerPrinted || !m.deferTranscriptUntilHeader || !m.headerOrientationPending || m.headerOrientationJobID != 0 {
		return
	}
	m.headerOrientationPending = false
	m.requestHeaderOrientation()
}

func (m *model) resetHeaderOrientation() {
	if m.headerOrientationJobID != 0 {
		m.cancelJob(m.headerOrientationJobID)
	}
	m.headerOrientationJobID = 0
	m.headerOrientation = api.Orientation{}
	m.headerOrientationSet = false
	m.deferTranscriptUntilHeader = true
	m.headerOrientationPending = false
	m.headerOrientationFailed = false
}

// sealOpeningHeaderForSessionSwitch keeps a deferred transcript attached to
// the opening header that described it before applySessionAction clears the
// current transcript.
func (m *model) sealOpeningHeaderForSessionSwitch() {
	if m.headerPrinted || !m.deferTranscriptUntilHeader {
		return
	}
	doc := m.ensureTranscript()
	if doc.sourceLength == 0 {
		return
	}
	if m.headerOrientationPending {
		m.headerOrientation = api.Orientation{}
		m.headerOrientationSet = false
		m.headerOrientationPending = false
		m.headerOrientationFailed = true
	}
	m.pendingSealRows = append(m.pendingSealRows, m.headerRows()...)
	m.pendingSealRows = append(m.pendingSealRows, doc.rows[doc.startRow:]...)
	doc.startRow = len(doc.rows)
	m.lastBodyRows = nil
	m.lastBodyStartRow = doc.startRow
	m.headerPrinted = true
	m.deferTranscriptUntilHeader = false
}

func (m *model) acceptHeaderOrientation(msg orientationLoadedMsg) {
	if m.headerPrinted || msg.jobID == 0 || msg.jobID != m.headerOrientationJobID ||
		msg.generation != m.sessionGeneration || msg.coreID != m.core.CoreID ||
		msg.request != m.orientationRequest() {
		return
	}
	m.headerOrientationJobID = 0
	m.headerOrientationPending = false
	if msg.err != nil {
		m.headerOrientation = api.Orientation{}
		m.headerOrientationSet = false
		m.headerOrientationFailed = true
		m.refreshViewport(false)
		return
	}
	value := msg.value.Clone()
	value.Mode = "preview"
	m.headerOrientation = value
	m.headerOrientationSet = true
	m.headerOrientationFailed = false
	m.refreshViewport(false)
}

func (m *model) adoptActualOrientation(value contracts.Orientation) {
	if m.headerPrinted {
		return
	}
	if m.headerOrientationJobID != 0 {
		m.cancelJob(m.headerOrientationJobID)
	}
	m.headerOrientationJobID = 0
	m.headerOrientationPending = false
	m.headerOrientationFailed = false
	value = value.Clone()
	value.Mode = "actual"
	m.headerOrientation = api.Orientation(value)
	m.headerOrientationSet = true
}

func orientationMode(value api.Orientation, fallback string) string {
	if value.Mode != "" {
		return value.Mode
	}
	return fallback
}

func orientationLabel(mode string) string {
	switch mode {
	case "actual":
		return "actual"
	case "preview":
		return "preview"
	default:
		return "unconfirmed"
	}
}

func formatBaseInstruction(base contracts.BaseInstruction) string {
	builtin := "built-in"
	if base.BuiltinVersion != "" {
		builtin += " v" + base.BuiltinVersion
	}
	if !base.HasSystemInstruction {
		return builtin + " · config system instruction absent"
	}
	config := base.ConfigPath
	if config == "" {
		config = "config path unavailable"
	}
	return builtin + " · config system instruction present · " + config
}

func formatOrientationDetails(value api.Orientation, fallback string) string {
	mode := orientationLabel(orientationMode(value, fallback))
	var b strings.Builder
	fmt.Fprintf(&b, "%s · workspace %s · role %s", mode, displayOr(value.Workspace, "current"), displayOr(value.Role, "default"))
	fmt.Fprintf(&b, "\nBase instructions · %s", formatBaseInstruction(value.Base))
	fmt.Fprintf(&b, "\nAGENTS.md sources · %s", instructionPaths(value.Instructions))
	fmt.Fprintf(&b, "\nModel-presented skills · %s", skillNames(value.Skills))
	for _, diagnostic := range value.Diagnostics {
		fmt.Fprintf(&b, "\nContext diagnostic · %s · %s", diagnostic.Path, diagnostic.Message)
	}
	return b.String()
}

func displayOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func instructionPaths(instructions []contracts.InstructionSource) string {
	if len(instructions) == 0 {
		return "none found"
	}
	paths := make([]string, 0, len(instructions))
	for _, instruction := range instructions {
		if instruction.Path != "" {
			paths = append(paths, instruction.Path)
		}
	}
	if len(paths) == 0 {
		return "none found"
	}
	return strings.Join(paths, ", ")
}

func skillNames(skills []contracts.SkillSummary) string {
	if len(skills) == 0 {
		return "none presented"
	}
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		if skill.Name != "" {
			names = append(names, skill.Name)
		}
	}
	if len(names) == 0 {
		return "none presented"
	}
	return strings.Join(names, ", ")
}

func (m *model) headerOrientationValues() (string, string, string) {
	if m.headerOrientationPending {
		return "loading", "loading", "loading"
	}
	if m.headerOrientationFailed || !m.headerOrientationSet {
		return "unconfirmed", "unconfirmed", "unconfirmed"
	}
	mode := orientationLabel(orientationMode(m.headerOrientation, "preview"))
	base := mode + " · " + formatBaseInstruction(m.headerOrientation.Base)
	context := mode + " · " + instructionPaths(m.headerOrientation.Instructions)
	skills := mode + " · " + skillNames(m.headerOrientation.Skills)
	return base, context, skills
}

func (m *model) acceptContextResult(msg contextLoadedMsg) {
	if msg.requestID != m.contextRequestID || msg.jobID != m.contextJobID ||
		msg.generation != m.sessionGeneration || msg.coreID != m.core.CoreID ||
		msg.request != m.orientationRequest() || msg.sessionID != m.sessionID ||
		msg.runID != currentRunID(m.activeRun) {
		return
	}
	m.contextJobID = 0
	var b strings.Builder
	if m.activeRun != nil {
		if m.activeRun.actualContext != nil {
			b.WriteString("Run actual context\n")
			b.WriteString(formatOrientationDetails(api.Orientation(*m.activeRun.actualContext), "actual"))
		} else {
			fmt.Fprintf(&b, "Run actual context · waiting for metadata from run %s", m.activeRun.id)
		}
		b.WriteString("\n\n")
	}
	if msg.orientErr != nil {
		fmt.Fprintf(&b, "Current orientation preview · unavailable: %v", msg.orientErr)
	} else {
		if m.activeRun == nil {
			b.WriteString("Current orientation\n")
		} else {
			b.WriteString("Current candidate preview\n")
		}
		b.WriteString(formatOrientationDetails(msg.orientation, "preview"))
	}
	if msg.sessionID != "" {
		b.WriteString("\n\n")
		if msg.stateErr != nil {
			fmt.Fprintf(&b, "Session context · unavailable: %v", msg.stateErr)
		} else {
			b.WriteString(formatSessionContext(msg.state, m.knownSessionIDs()...))
		}
		if msg.selectErr != nil {
			fmt.Fprintf(&b, "\nNext selection at query · unresolved: %v", msg.selectErr)
		} else {
			fmt.Fprintf(&b, "\nNext selection at query · %s / %s · effort %s · role %s",
				msg.selection.Selection.Provider, msg.selection.Selection.ModelID,
				msg.selection.Selection.Effort, msg.selection.Role)
		}
	} else {
		b.WriteString("\n\nSession context · unavailable until the first run creates a Session")
	}
	m.appendEntry("context", b.String())
	if msg.orientErr != nil && m.activeRun == nil {
		m.status = "context loaded · orientation preview unavailable"
	} else {
		m.status = "context loaded"
	}
}

func currentRunID(run *activeRun) string {
	if run == nil {
		return ""
	}
	return run.id
}
