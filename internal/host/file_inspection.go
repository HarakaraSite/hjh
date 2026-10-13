package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ListDirectoryArgs struct {
	Path   string `json:"path"`
	Tree   bool   `json:"tree"`
	Depth  *int   `json:"depth,omitempty"`
	Offset int    `json:"offset"`
	Limit  *int   `json:"limit,omitempty"`
}

// Tree entries are flat, ordered by traversal and carry depth for display.
// This makes paging continue at one entry rather than splitting nested objects.
type DirectoryEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Bytes      int64  `json:"bytes"`
	ModifiedAt string `json:"modifiedAt"`
	Depth      int    `json:"depth"`
}

type ListDirectoryResult struct {
	Path       string           `json:"path"`
	Tree       bool             `json:"tree"`
	Entries    []DirectoryEntry `json:"entries"`
	Offset     int              `json:"offset"`
	Total      int              `json:"total"`
	NextOffset *int             `json:"nextOffset,omitempty"`
}

func (h *Host) listDirectory(ctx context.Context, args ListDirectoryArgs) (ListDirectoryResult, error) {
	if args.Path == "" {
		args.Path = "."
	}
	depth, limit := 3, 100
	if args.Depth != nil {
		depth = *args.Depth
	}
	if args.Limit != nil {
		limit = *args.Limit
	}
	if args.Offset < 0 || depth < 1 || limit < 1 {
		return ListDirectoryResult{}, errors.New("ls expects nonnegative offset and positive depth and limit")
	}
	result := ListDirectoryResult{Path: args.Path, Tree: args.Tree, Offset: args.Offset, Entries: []DirectoryEntry{}}
	var visit func(string, string, int) error
	visit = func(actual, display string, level int) error {
		entries, err := os.ReadDir(actual)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			kind := "file"
			if entry.Type()&os.ModeSymlink != 0 {
				kind = "symlink"
			} else if entry.IsDir() {
				kind = "directory"
			} else if !info.Mode().IsRegular() {
				kind = "other"
			}
			item := DirectoryEntry{Name: entry.Name(), Path: filepath.ToSlash(filepath.Join(display, entry.Name())),
				Type: kind, Bytes: info.Size(), ModifiedAt: info.ModTime().Format(time.RFC3339Nano), Depth: level}
			if result.Total >= args.Offset && len(result.Entries) < limit {
				result.Entries = append(result.Entries, item)
			}
			result.Total++
			if args.Tree && entry.IsDir() && level < depth {
				if err = visit(filepath.Join(actual, entry.Name()), item.Path, level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(h.resolvePath(args.Path), args.Path, 1); err != nil {
		return ListDirectoryResult{}, err
	}
	if args.Offset < result.Total && len(result.Entries) < result.Total-args.Offset {
		next := args.Offset + len(result.Entries)
		result.NextOffset = &next
	}
	return result, nil
}

type CountFilesArgs struct {
	Paths []string `json:"paths"`
}

type FileCounts struct {
	Path  string `json:"path,omitempty"`
	Lines int64  `json:"lines"`
	Words int64  `json:"words"`
	Bytes int64  `json:"bytes"`
}

type CountFilesResult struct {
	Files []FileCounts `json:"files"`
	Total FileCounts   `json:"total"`
}

func (h *Host) countFiles(ctx context.Context, args CountFilesArgs) (CountFilesResult, error) {
	result := CountFilesResult{Files: []FileCounts{}}
	for _, path := range args.Paths {
		if err := ctx.Err(); err != nil {
			return CountFilesResult{}, err
		}
		b, err := os.ReadFile(h.resolvePath(path))
		if err != nil {
			return CountFilesResult{}, err
		}
		text := string(b)
		counts := FileCounts{Path: path, Lines: int64(strings.Count(text, "\n")),
			Words: int64(len(strings.Fields(text))), Bytes: int64(len(b))}
		result.Files = append(result.Files, counts)
		result.Total.Lines += counts.Lines
		result.Total.Words += counts.Words
		result.Total.Bytes += counts.Bytes
	}
	return result, nil
}
