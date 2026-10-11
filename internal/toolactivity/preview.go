// Package toolactivity projects saved tool arguments into short display text.
// The original arguments and results remain in history, outside this projection.
package toolactivity

import (
	"encoding/json"
	"strings"
)

func Preview(name string, input json.RawMessage) string {
	var args struct {
		Path    string   `json:"path"`
		Program string   `json:"program"`
		Args    []string `json:"args"`
		URL     string   `json:"url"`
		Role    string   `json:"role"`
		Task    string   `json:"task"`
	}
	_ = json.Unmarshal(input, &args)
	var text string
	switch name {
	case "read_file", "write_file", "edit_file":
		text = args.Path
	case "command":
		text = strings.Join(append([]string{args.Program}, args.Args...), " ")
	case "http":
		text = args.URL
	case "run_agent":
		text = args.Role
		if args.Task != "" {
			text += " · " + strings.SplitN(args.Task, "\n", 2)[0]
		}
	case "run_js":
		text = "JavaScript"
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
