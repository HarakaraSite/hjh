package tui

// runNoticeTarget outlives activeRun so delayed results still belong to the
// displayed run while idle, but never become current again after the next run.
type runNoticeTarget struct {
	coreID    string
	sessionID string
	runID     string
}

func (m *model) setRunNoticeTarget(run *activeRun) {
	m.runNoticeTarget = runNoticeTarget{coreID: run.coreID, sessionID: run.session, runID: run.id}
	// A cancellation can begin before Core admits the first Session. Keep its
	// Session identity in sync with that same run's confirmed identity.
	if target := m.chainCancel; target != nil && target.coreID == run.coreID && target.runID == run.id {
		target.sessionID = run.session
	}
}

func (m *model) showRunNotice(coreID, sessionID, runID string) bool {
	return runID != "" && m.runNoticeTarget == (runNoticeTarget{coreID: coreID, sessionID: sessionID, runID: runID})
}
