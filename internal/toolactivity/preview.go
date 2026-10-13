// Package toolactivity projects saved tool arguments into short display text.
// The original arguments and results remain in history, outside this projection.
package toolactivity

import (
	"encoding/json"
	"strings"
)

func Preview(name string, input json.RawMessage) string {
	var args struct {
		Path        string   `json:"path"`
		Program     string   `json:"program"`
		Args        []string `json:"args"`
		URL         string   `json:"url"`
		Query       string   `json:"query"`
		Role        string   `json:"role"`
		Task        string   `json:"task"`
		Text        string   `json:"text"`
		Code        string   `json:"code"`
		ExecutionID string   `json:"executionId"`
		Pattern     string   `json:"pattern"`
		Op          string   `json:"op"`
		Paths       []string `json:"paths"`
	}
	_ = json.Unmarshal(input, &args)
	var text string
	switch name {
	case "read_file", "write_file", "edit_file":
		text = args.Path
	case "ls":
		text = args.Path
		if text == "" {
			text = "."
		}
	case "find", "grep":
		if args.Path == "" {
			args.Path = "."
		}
		if name == "find" && args.Pattern == "" {
			args.Pattern = "*"
		}
		text = strings.TrimSpace(args.Pattern + " · " + args.Path)
	case "wc":
		text = strings.Join(args.Paths, ", ")
	case "git_inspect":
		text = strings.TrimSpace(args.Op + " " + strings.Join(args.Paths, " "))
	case "command":
		text = strings.Join(append([]string{args.Program}, args.Args...), " ")
	case "http":
		text = args.URL
	case "web_search":
		text = args.Query
	case "run_agent", "spawn_agent":
		text = args.Role
		if args.Task != "" {
			text += " · " + strings.SplitN(args.Task, "\n", 2)[0]
		}
	case "agent_status", "collect_agent", "cancel_agent":
		text = args.ExecutionID
	case "steer_agent":
		text = args.ExecutionID
		if args.Text != "" {
			text += " · " + strings.SplitN(args.Text, "\n", 2)[0]
		}
	case "run_js":
		line, _, _ := strings.Cut(strings.TrimSpace(args.Code), "\n")
		if strings.HasPrefix(line, "//") {
			text = strings.TrimSpace(strings.TrimPrefix(line, "//"))
		}
	}
	return Short(text)
}

// Short limits only the single-line activity preview; it does not limit or
// modify tool input, output, user messages, or assistant responses.
func Short(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 96 {
		text = string(runes[:96]) + "…"
	}
	return text
}
