package host

import "os"

type ReadFileArgs struct {
	Path string `json:"path"`
}
type WriteFileArgs struct {
	Path string `json:"path"`
	Text string `json:"text"`
}
type ReadFileResult struct {
	Path string `json:"path"`
	Text string `json:"text"`
}
type WriteFileResult struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

func readFile(args ReadFileArgs) (ReadFileResult, error) {
	b, err := os.ReadFile(args.Path)
	return ReadFileResult{Path: args.Path, Text: string(b)}, err
}

func writeFile(args WriteFileArgs) (WriteFileResult, error) {
	err := os.WriteFile(args.Path, []byte(args.Text), 0644)
	return WriteFileResult{Path: args.Path, Bytes: len([]byte(args.Text))}, err
}
