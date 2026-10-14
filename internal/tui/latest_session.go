package tui

import (
	"context"
	"path/filepath"

	"hjh/internal/api"
	"hjh/internal/sessionid"
)

func resumeWorkspace(workspace, coreWorkspace string) string {
	if workspace == "" {
		workspace = coreWorkspace
	} else if !filepath.IsAbs(workspace) {
		workspace = filepath.Join(coreWorkspace, workspace)
	}
	return filepath.Clean(workspace)
}

func latestSessionID(sessions []api.Session, workspace, coreWorkspace string) (string, bool) {
	workspace = resumeWorkspace(workspace, coreWorkspace)
	candidates := make([]sessionid.Candidate, len(sessions))
	for i, session := range sessions {
		candidates[i] = sessionid.Candidate{ID: session.ID, WorkspaceRoot: session.WorkspaceRoot, UpdatedAt: session.UpdatedAt}
	}
	return sessionid.Latest(candidates, workspace)
}

// Resolve the latest ID before entering the existing restore/attach path.
func resolveStartupSession(ctx context.Context, backend Backend, options *Options, sessions []api.Session) error {
	if options.Continue {
		options.Workspace = resumeWorkspace(options.Workspace, options.Core.Workspace)
		options.SessionID, _ = latestSessionID(sessions, options.Workspace, options.Core.Workspace)
	}
	if options.SessionID == "" {
		return nil
	}
	session, err := backend.ResolveSession(ctx, options.SessionID)
	if err != nil {
		return err
	}
	options.SessionID = session.ID
	return nil
}
