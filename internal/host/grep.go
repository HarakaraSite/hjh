package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type GrepLine struct {
	Path  any  `json:"path"`
	Line  int  `json:"line"`
	Text  any  `json:"text"`
	Match bool `json:"match"`
}

type GrepCommandResult struct {
	Lines    []GrepLine `json:"lines"`
	Files    []any      `json:"files"`
	Matches  int        `json:"matches"`
	Warnings string     `json:"warnings,omitempty"`
}

// GNU grep -H -n -Z separates filenames with NUL. Parse before JSON encoding
// so embedded newlines and non-UTF-8 filenames retain their original bytes.
func (h *Host) grepCommand(ctx context.Context, args CommandArgs) (GrepCommandResult, error) {
	result, err := h.command(ctx, args)
	if err != nil {
		return GrepCommandResult{}, err
	}
	if result.ExitCode > 1 {
		return GrepCommandResult{}, fmt.Errorf("grep failed (%d): %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	parsed := GrepCommandResult{Lines: []GrepLine{}, Files: []any{}, Warnings: result.Stderr}
	seen := map[string]bool{}
	remaining := []byte(result.Stdout)
	for len(remaining) > 0 {
		endPath := bytes.IndexByte(remaining, 0)
		if endPath < 0 {
			return GrepCommandResult{}, errors.New("GNU grep output has no filename separator")
		}
		path := remaining[:endPath]
		remaining = remaining[endPath+1:]
		endLine := bytes.IndexByte(remaining, '\n')
		if endLine < 0 {
			endLine = len(remaining)
		} else {
			endLine++
		}
		line := remaining[:endLine]
		remaining = remaining[endLine:]
		i := 0
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			i++
		}
		if i == 0 || i == len(line) || (line[i] != ':' && line[i] != '-') {
			return GrepCommandResult{}, errors.New("GNU grep output has no line number separator")
		}
		number, err := strconv.Atoi(string(line[:i]))
		if err != nil {
			return GrepCommandResult{}, err
		}
		matched := line[i] == ':'
		parsed.Lines = append(parsed.Lines, GrepLine{Path: grepText(path), Line: number, Text: grepText(line[i+1:]), Match: matched})
		if matched {
			parsed.Matches++
			if !seen[string(path)] {
				seen[string(path)] = true
				parsed.Files = append(parsed.Files, grepText(path))
			}
		}
	}
	return parsed, nil
}

func grepText(value []byte) any {
	if utf8.Valid(value) {
		return string(value)
	}
	return map[string]string{"base64": base64.StdEncoding.EncodeToString(value)}
}
