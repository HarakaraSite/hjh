package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type GitInspectArgs struct {
	Op     string   `json:"op"`
	Args   []string `json:"args"`
	Offset int      `json:"offset"`
	Limit  *int     `json:"limit"`
}

type GitInspectResult struct {
	Op         string `json:"op"`
	Text       string `json:"text"`
	Offset     int    `json:"offset"`
	Total      *int   `json:"total,omitempty"`
	NextOffset *int   `json:"nextOffset,omitempty"`
}

// Git's full native output is temporary. Only the requested page is observed.
func (h *Host) gitInspect(ctx context.Context, args GitInspectArgs) (GitInspectResult, error) {
	limit := 100
	if args.Op == "log" {
		limit = 20
	}
	if args.Limit != nil {
		limit = *args.Limit
	}
	if args.Offset < 0 || limit < 1 {
		return GitInspectResult{}, errors.New("offset must be nonnegative and limit must be positive")
	}
	native, err := h.command(ctx, CommandArgs{Program: "git", Args: args.Args})
	if err != nil {
		return GitInspectResult{}, err
	}
	if native.ExitCode != 0 {
		return GitInspectResult{}, fmt.Errorf("git_inspect %s failed (%d): %s", args.Op, native.ExitCode, strings.TrimSpace(native.Stderr))
	}
	lines := []string{}
	if native.Stdout != "" {
		lines = strings.Split(strings.TrimSuffix(native.Stdout, "\n"), "\n")
	}
	result := GitInspectResult{Op: args.Op, Offset: args.Offset}
	if args.Op == "log" {
		result.Text = strings.Join(lines[:min(limit, len(lines))], "\n")
		if len(lines) > limit {
			next := args.Offset + limit
			result.NextOffset = &next
		}
		return result, nil
	}
	total := len(lines)
	result.Total = &total
	start := min(args.Offset, total)
	end := start + min(limit, total-start)
	result.Text = strings.Join(lines[start:end], "\n")
	if end < total {
		result.NextOffset = &end
	}
	return result, nil
}
