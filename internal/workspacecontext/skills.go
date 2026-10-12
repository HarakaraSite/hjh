package workspacecontext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"hjh/internal/contracts"
)

type skillDiscovery struct {
	ctx         context.Context
	diagnostics *[]contracts.ContextDiagnostic
	visitedDirs map[string]struct{}
	seenFiles   []os.FileInfo
	candidates  []Skill
}

func (d *skillDiscovery) walk(path, source string) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
			Path: path, Message: fmt.Sprintf("cannot inspect skills directory: %v", err),
		})
		return nil
	}
	if !info.IsDir() {
		return nil
	}

	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		canonical = filepath.Clean(path)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		canonical = filepath.Clean(path)
	}
	if d.visitedDirs == nil {
		d.visitedDirs = make(map[string]struct{})
	}
	if _, seen := d.visitedDirs[canonical]; seen {
		return nil
	}
	d.visitedDirs[canonical] = struct{}{}

	entries, err := os.ReadDir(path)
	if err != nil {
		*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
			Path: path, Message: fmt.Sprintf("cannot read skills directory: %v", err),
		})
		return nil
	}
	for _, entry := range entries {
		if err := d.ctx.Err(); err != nil {
			return err
		}
		child := filepath.Join(path, entry.Name())
		if entry.Name() == "SKILL.md" {
			if err := d.readSkill(child, source); err != nil {
				return err
			}
			continue
		}

		childInfo, err := os.Stat(child)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
				Path: child, Message: fmt.Sprintf("cannot inspect skills entry: %v", err),
			})
			continue
		}
		if childInfo.IsDir() {
			if err := d.walk(child, source); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *skillDiscovery) readSkill(path, source string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
			Path: path, Message: fmt.Sprintf("cannot inspect skill: %v", err),
		})
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	for _, previous := range d.seenFiles {
		if os.SameFile(previous, info) {
			return nil
		}
	}
	d.seenFiles = append(d.seenFiles, info)

	data, err := os.ReadFile(path)
	if err != nil {
		*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
			Path: path, Message: fmt.Sprintf("cannot read skill metadata: %v", err),
		})
		return nil
	}
	metadata, err := parseSkillMetadata(data, filepath.Base(filepath.Dir(path)))
	if err != nil {
		*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
			Path: path, Message: fmt.Sprintf("invalid skill metadata: %v", err),
		})
		return nil
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
			Path: path, Message: fmt.Sprintf("cannot resolve skill path: %v", err),
		})
		return nil
	}
	d.candidates = append(d.candidates, Skill{
		Name:                   metadata.Name,
		Description:            metadata.Description,
		Path:                   filepath.Clean(absolutePath),
		Directory:              filepath.Dir(filepath.Clean(absolutePath)),
		Source:                 source,
		DisableModelInvocation: metadata.DisableModelInvocation,
	})
	return nil
}

func (d *skillDiscovery) finish() []Skill {
	firstByName := make(map[string]int, len(d.candidates))
	indicesByName := make(map[string][]int, len(d.candidates))
	for i, skill := range d.candidates {
		if _, exists := firstByName[skill.Name]; !exists {
			firstByName[skill.Name] = i
		}
		indicesByName[skill.Name] = append(indicesByName[skill.Name], i)
	}

	for i, skill := range d.candidates {
		if firstByName[skill.Name] != i {
			continue
		}
		indices := indicesByName[skill.Name]
		if len(indices) < 2 {
			continue
		}
		winner := d.candidates[indices[0]]
		shadowed := make([]string, 0, len(indices)-1)
		for _, i := range indices[1:] {
			shadowed = append(shadowed, d.candidates[i].Path)
		}
		*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
			Path:    winner.Path,
			Message: fmt.Sprintf("skill %q selected from %s and shadows duplicate(s): %s", skill.Name, winner.Source, strings.Join(shadowed, ", ")),
		})
		for _, i := range indices[1:] {
			loser := d.candidates[i]
			*d.diagnostics = append(*d.diagnostics, contracts.ContextDiagnostic{
				Path:    loser.Path,
				Message: fmt.Sprintf("skill %q is shadowed by %s", skill.Name, winner.Path),
			})
		}
	}

	winners := make([]Skill, 0, len(firstByName))
	for i, skill := range d.candidates {
		if firstByName[skill.Name] == i {
			winners = append(winners, skill)
		}
	}
	return winners
}

type skillFrontmatter struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
}

func parseSkillMetadata(data []byte, parentName string) (skillFrontmatter, error) {
	openingEnd := bytes.IndexByte(data, '\n')
	if openingEnd < 0 {
		return skillFrontmatter{}, fmt.Errorf("missing YAML frontmatter")
	}
	opening := strings.TrimSuffix(string(data[:openingEnd]), "\r")
	if strings.TrimSpace(opening) != "---" {
		return skillFrontmatter{}, fmt.Errorf("missing YAML frontmatter")
	}

	remaining := data[openingEnd+1:]
	offset := 0
	closingStart := -1
	for offset <= len(remaining) {
		lineEnd := bytes.IndexByte(remaining[offset:], '\n')
		lineStart := offset
		if lineEnd < 0 {
			lineEnd = len(remaining)
		} else {
			lineEnd += offset
		}
		line := strings.TrimSuffix(string(remaining[lineStart:lineEnd]), "\r")
		if strings.TrimSpace(line) == "---" {
			closingStart = lineStart
			break
		}
		if lineEnd == len(remaining) {
			break
		}
		offset = lineEnd + 1
	}
	if closingStart < 0 {
		return skillFrontmatter{}, fmt.Errorf("unterminated YAML frontmatter")
	}

	var metadata skillFrontmatter
	if err := yaml.Unmarshal(remaining[:closingStart], &metadata); err != nil {
		return skillFrontmatter{}, fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	if strings.TrimSpace(metadata.Name) == "" {
		metadata.Name = parentName
	} else {
		metadata.Name = strings.TrimSpace(metadata.Name)
	}
	metadata.Description = strings.TrimSpace(metadata.Description)
	if metadata.Description == "" {
		return skillFrontmatter{}, fmt.Errorf("description is required")
	}
	return metadata, nil
}
