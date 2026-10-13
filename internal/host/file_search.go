package host

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

type FindFilesArgs struct {
	Path    string   `json:"path"`
	Pattern string   `json:"pattern"`
	Type    string   `json:"type"`
	Exclude []string `json:"exclude"`
	Offset  int      `json:"offset"`
	Limit   *int     `json:"limit,omitempty"`
}

type FoundEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type FindFilesResult struct {
	Path       string       `json:"path"`
	Entries    []FoundEntry `json:"entries"`
	Offset     int          `json:"offset"`
	Total      int          `json:"total"`
	NextOffset *int         `json:"nextOffset,omitempty"`
}

// A slashless glob matches a basename at any depth; ** spans directories.
func matchGlob(pattern, name string) (bool, error) {
	if !strings.Contains(pattern, "/") {
		return filepath.Match(pattern, filepath.Base(name))
	}
	parts := strings.Split(pattern, "/")
	names := strings.Split(filepath.ToSlash(name), "/")
	var match func([]string, []string) (bool, error)
	match = func(p, n []string) (bool, error) {
		if len(p) == 0 {
			return len(n) == 0, nil
		}
		if p[0] == "**" {
			for i := 0; i <= len(n); i++ {
				ok, err := match(p[1:], n[i:])
				if err != nil || ok {
					return ok, err
				}
			}
			return false, nil
		}
		if len(n) == 0 {
			return false, nil
		}
		ok, err := filepath.Match(p[0], n[0])
		if err != nil || !ok {
			return ok, err
		}
		return match(p[1:], n[1:])
	}
	return match(parts, names)
}

func (h *Host) findFiles(ctx context.Context, args FindFilesArgs) (FindFilesResult, error) {
	if args.Path == "" {
		args.Path = "."
	}
	if args.Pattern == "" {
		args.Pattern = "*"
	}
	if args.Type == "" {
		args.Type = "any"
	}
	limit := 100
	if args.Limit != nil {
		limit = *args.Limit
	}
	if args.Offset < 0 || limit < 1 {
		return FindFilesResult{}, errors.New("find expects nonnegative offset and positive limit")
	}
	if args.Type != "any" && args.Type != "file" && args.Type != "directory" {
		return FindFilesResult{}, errors.New("find type must be any, file, or directory")
	}
	for _, pattern := range append([]string{args.Pattern}, args.Exclude...) {
		for _, part := range strings.Split(pattern, "/") {
			if _, err := filepath.Match(part, ""); err != nil {
				return FindFilesResult{}, err
			}
		}
	}
	root := h.resolvePath(args.Path)
	result := FindFilesResult{Path: args.Path, Offset: args.Offset, Entries: []FoundEntry{}}
	err := filepath.WalkDir(root, func(actual string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, actual)
		if err != nil {
			return err
		}
		if rel == "." && entry.IsDir() {
			return nil
		}
		if rel == "." {
			rel = entry.Name()
		}
		for _, pattern := range args.Exclude {
			matched, err := matchGlob(pattern, rel)
			if err != nil {
				return err
			}
			if matched {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		} else if entry.Type()&fs.ModeSymlink != 0 {
			kind = "symlink"
		} else if entry.Type() != 0 {
			kind = "other"
		}
		matched, err := matchGlob(args.Pattern, rel)
		if err != nil {
			return err
		}
		if !matched || (args.Type != "any" && args.Type != kind) {
			return nil
		}
		if result.Total >= args.Offset && len(result.Entries) < limit {
			result.Entries = append(result.Entries, FoundEntry{Path: filepath.ToSlash(actual), Type: kind})
		}
		result.Total++
		return nil
	})
	if err != nil {
		return FindFilesResult{}, err
	}
	if args.Offset < result.Total && len(result.Entries) < result.Total-args.Offset {
		next := args.Offset + len(result.Entries)
		result.NextOffset = &next
	}
	return result, nil
}
