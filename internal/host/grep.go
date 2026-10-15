package host

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type GrepCommandArgs struct {
	CommandArgs
	Backend string `json:"backend"`
	Output  string `json:"output"`
	Offset  int    `json:"offset"`
	Limit   *int   `json:"limit"`
}

// Only the requested page crosses the observed host boundary. Native output is
// transient; totals still describe the entire search, including context lines.
type GrepCommandResult struct {
	Items      []any  `json:"items"`
	Matches    int    `json:"matches"`
	Offset     int    `json:"offset"`
	Total      int    `json:"total"`
	NextOffset *int   `json:"nextOffset,omitempty"`
	Warnings   string `json:"warnings,omitempty"`
}

type grepPage struct {
	GrepCommandResult
	output string
	limit  int
	seen   map[string]bool
}

func (p *grepPage) add(line GrepLine) {
	if line.Match {
		p.Matches++
	}
	var item any = line
	if p.output == "files" {
		if !line.Match {
			return
		}
		key, _ := json.Marshal(line.Path)
		if p.seen[string(key)] {
			return
		}
		p.seen[string(key)] = true
		item = line.Path
	}
	if p.Total >= p.Offset && len(p.Items) < p.limit {
		p.Items = append(p.Items, item)
	}
	p.Total++
}

func (h *Host) grepCommand(ctx context.Context, args GrepCommandArgs) (GrepCommandResult, error) {
	if args.Output == "" {
		args.Output = "lines"
	}
	if args.Output != "lines" && args.Output != "files" {
		return GrepCommandResult{}, errors.New("grep output must be lines or files")
	}
	limit := 100
	if args.Limit != nil {
		limit = *args.Limit
	}
	if args.Offset < 0 || limit < 1 {
		return GrepCommandResult{}, errors.New("offset must be nonnegative and limit must be positive")
	}
	result, err := h.command(ctx, args.CommandArgs)
	if err != nil {
		return GrepCommandResult{}, err
	}
	if result.ExitCode > 1 {
		return GrepCommandResult{}, fmt.Errorf("grep failed (%d): %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	parsed := grepPage{GrepCommandResult: GrepCommandResult{Items: []any{}, Offset: args.Offset, Warnings: result.Stderr},
		output: args.Output, limit: limit, seen: map[string]bool{}}
	switch args.Backend {
	case "rg":
		err = parseRG(result.Stdout, parsed.add)
	case "grep":
		err = parseGNU([]byte(result.Stdout), parsed.add)
	default:
		err = fmt.Errorf("unknown grep backend %q", args.Backend)
	}
	if err != nil {
		return GrepCommandResult{}, err
	}
	if next := parsed.Offset + len(parsed.Items); next < parsed.Total {
		parsed.NextOffset = &next
	}
	return parsed.GrepCommandResult, nil
}

// rg's JSON format preserves non-UTF-8 bytes as base64 for both names and text.
type rgText struct {
	Text  *string `json:"text"`
	Bytes string  `json:"bytes"`
}

func (v rgText) value() any {
	if v.Text != nil {
		return *v.Text
	}
	return map[string]string{"base64": v.Bytes}
}

func parseRG(output string, add func(GrepLine)) error {
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var event struct {
			Type string `json:"type"`
			Data struct {
				Path       rgText `json:"path"`
				Lines      rgText `json:"lines"`
				LineNumber int    `json:"line_number"`
			} `json:"data"`
		}
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if event.Type == "match" || event.Type == "context" {
			add(GrepLine{Path: event.Data.Path.value(), Text: event.Data.Lines.value(), Line: event.Data.LineNumber, Match: event.Type == "match"})
		}
	}
}

// GNU grep -H -n -Z separates filenames with NUL. Parse before JSON encoding
// so embedded newlines and non-UTF-8 filenames retain their original bytes.
func parseGNU(remaining []byte, add func(GrepLine)) error {
	for len(remaining) > 0 {
		endPath := bytes.IndexByte(remaining, 0)
		if endPath < 0 {
			return errors.New("GNU grep output has no filename separator")
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
			return errors.New("GNU grep output has no line number separator")
		}
		number, err := strconv.Atoi(string(line[:i]))
		if err != nil {
			return err
		}
		add(GrepLine{Path: grepText(path), Line: number, Text: grepText(line[i+1:]), Match: line[i] == ':'})
	}
	return nil
}

func grepText(value []byte) any {
	if utf8.Valid(value) {
		return string(value)
	}
	return map[string]string{"base64": base64.StdEncoding.EncodeToString(value)}
}
