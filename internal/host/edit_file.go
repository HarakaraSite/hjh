package host

import (
	"os"
	"strings"
)

type FileEdit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type EditFileArgs struct {
	Path  string     `json:"path"`
	Edits []FileEdit `json:"edits"`
}

type EditCount struct {
	Replacements int `json:"replacements"`
}

type EditFileResult struct {
	Path         string      `json:"path"`
	Replacements int         `json:"replacements"`
	Edits        []EditCount `json:"edits"`
	Bytes        int         `json:"bytes"`
}

// Reading, replacing, and writing are one observed operation, rather than
// recording the complete old file and complete replacement file separately.
func (h *Host) editFile(args EditFileArgs) (EditFileResult, error) {
	raw, err := os.ReadFile(h.resolvePath(args.Path))
	if err != nil {
		return EditFileResult{}, err
	}
	text := string(raw)
	result := EditFileResult{Path: args.Path, Edits: []EditCount{}}
	for _, edit := range args.Edits {
		count := 0
		if edit.OldText != "" {
			count = strings.Count(text, edit.OldText)
			if count > 0 {
				text = strings.ReplaceAll(text, edit.OldText, edit.NewText)
			}
		}
		result.Edits = append(result.Edits, EditCount{Replacements: count})
		result.Replacements += count
	}
	saved, err := h.writeFile(WriteFileArgs{Path: args.Path, Text: text})
	result.Bytes = saved.Bytes
	return result, err
}
