package workspacecontext

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hjh/internal/contracts"
)

// Instruction is one AGENTS.md file read during workspace discovery.
type Instruction struct {
	Path   string
	Bytes  []byte
	Digest string
}

// Skill contains the metadata needed to present a skill to the model. Its
// instruction body is deliberately not retained in the snapshot.
type Skill struct {
	Name                   string
	Description            string
	Path                   string
	Directory              string
	Source                 string
	DisableModelInvocation bool
}

// Snapshot freezes the workspace instructions and skill metadata discovered
// for one run or orientation preview.
type Snapshot struct {
	Instructions []Instruction
	Skills       []Skill
	Diagnostics  []contracts.ContextDiagnostic
}

type skillRoot struct {
	path   string
	source string
}

// Discover reads user and workspace AGENTS.md files, then discovers skills in
// the configured priority order. Missing files and directories are ordinary;
// unreadable or invalid sources are returned as diagnostics so other context
// remains available.
func Discover(ctx context.Context, workspace string) (Snapshot, error) {
	workspacePath, err := filepath.Abs(workspace)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve workspace path: %w", err)
	}
	workspacePath = filepath.Clean(workspacePath)

	configDir, err := os.UserConfigDir()
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve user config directory: %w", err)
	}
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve user config directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve user home directory: %w", err)
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve user home directory: %w", err)
	}

	snapshot := Snapshot{}
	if err := discoverInstructions(ctx, workspacePath, filepath.Join(configDir, "hjh", "AGENTS.md"), &snapshot); err != nil {
		return Snapshot{}, err
	}

	roots := []skillRoot{
		{path: filepath.Join(workspacePath, ".hjh", "skills"), source: "project-hjh"},
		{path: filepath.Join(workspacePath, ".agents", "skills"), source: "project-agents"},
		{path: filepath.Join(configDir, "hjh", "skills"), source: "user-hjh"},
		{path: filepath.Join(homeDir, ".agents", "skills"), source: "user-agents"},
	}
	discovery := skillDiscovery{ctx: ctx, diagnostics: &snapshot.Diagnostics}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if err := discovery.walk(root.path, root.source); err != nil {
			return Snapshot{}, err
		}
	}
	snapshot.Skills = discovery.finish()
	return snapshot, nil
}

func discoverInstructions(ctx context.Context, workspace, userPath string, snapshot *Snapshot) error {
	seenPaths := make(map[string]struct{})
	read := func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		path = filepath.Clean(path)
		if _, exists := seenPaths[path]; exists {
			return nil
		}
		seenPaths[path] = struct{}{}

		if _, err := os.Lstat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			snapshot.Diagnostics = append(snapshot.Diagnostics, contracts.ContextDiagnostic{
				Path: path, Message: fmt.Sprintf("cannot inspect instructions: %v", err),
			})
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			snapshot.Diagnostics = append(snapshot.Diagnostics, contracts.ContextDiagnostic{
				Path: path, Message: fmt.Sprintf("cannot read instructions: %v", err),
			})
			return nil
		}
		digest := sha256.Sum256(data)
		snapshot.Instructions = append(snapshot.Instructions, Instruction{
			Path: path, Bytes: data, Digest: "sha256:" + hex.EncodeToString(digest[:]),
		})
		return nil
	}

	if err := read(userPath); err != nil {
		return err
	}

	var directories []string
	for dir := workspace; ; dir = filepath.Dir(dir) {
		directories = append(directories, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	for i := len(directories) - 1; i >= 0; i-- {
		if err := read(filepath.Join(directories[i], "AGENTS.md")); err != nil {
			return err
		}
	}
	return nil
}

func (s Snapshot) Instruction(readFile bool) string {
	var prompt strings.Builder
	if len(s.Instructions) > 0 {
		prompt.WriteString("Workspace instructions from AGENTS.md follow. Read and apply every applicable source. When instructions conflict, a source closer to the workspace takes precedence over a more distant source.\n\n")
		for _, instruction := range s.Instructions {
			prompt.WriteString("Instructions from ")
			prompt.WriteString(instruction.Path)
			prompt.WriteString(":\n")
			prompt.Write(instruction.Bytes)
			if len(instruction.Bytes) == 0 || instruction.Bytes[len(instruction.Bytes)-1] != '\n' {
				prompt.WriteByte('\n')
			}
			prompt.WriteByte('\n')
		}
	}

	if !readFile {
		return prompt.String()
	}
	automatic := make([]Skill, 0, len(s.Skills))
	for _, skill := range s.Skills {
		if !skill.DisableModelInvocation {
			automatic = append(automatic, skill)
		}
	}
	if len(automatic) == 0 {
		return prompt.String()
	}

	prompt.WriteString("\nAvailable skills (metadata only; skill bodies are read when needed):\n")
	for _, skill := range automatic {
		prompt.WriteString("- ")
		prompt.WriteString(skill.Name)
		prompt.WriteString(": ")
		prompt.WriteString(skill.Description)
		prompt.WriteString("\n  SKILL.md: ")
		prompt.WriteString(skill.Path)
		prompt.WriteString("\n  Relative references are resolved from: ")
		prompt.WriteString(skill.Directory)
		prompt.WriteByte('\n')
	}
	prompt.WriteString("When a task explicitly names a skill or matches its purpose, read that skill's SKILL.md with read_file before acting. If you call read_file from run_js, return its body from the outer script, for example: return await tools.read_file({path: absoluteSkillPath});. The inner tool observation alone does not put the body in the conversation. Resolve relative references using the skill directory shown above.\n")
	return prompt.String()
}

func (s Snapshot) Orientation(base contracts.BaseInstruction, workspace, role, mode string, readFile bool) contracts.Orientation {
	resolvedWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		resolvedWorkspace = filepath.Clean(workspace)
	}

	orientation := contracts.Orientation{
		Mode:             mode,
		Workspace:        resolvedWorkspace,
		Role:             role,
		Base:             base,
		Instructions:     make([]contracts.InstructionSource, 0, len(s.Instructions)),
		Skills:           []contracts.SkillSummary{},
		DiscoveredSkills: make([]contracts.SkillSummary, 0, len(s.Skills)),
		Diagnostics:      append([]contracts.ContextDiagnostic{}, s.Diagnostics...),
	}
	for _, instruction := range s.Instructions {
		orientation.Instructions = append(orientation.Instructions, contracts.InstructionSource{
			Path: instruction.Path, Digest: instruction.Digest,
		})
	}
	for _, skill := range s.Skills {
		summary := skillSummary(skill)
		orientation.DiscoveredSkills = append(orientation.DiscoveredSkills, summary)
		if readFile && !skill.DisableModelInvocation {
			orientation.Skills = append(orientation.Skills, summary)
		}
	}
	return orientation
}

func skillSummary(skill Skill) contracts.SkillSummary {
	return contracts.SkillSummary{
		Name: skill.Name, Description: skill.Description, Path: skill.Path,
		Directory: skill.Directory, Source: skill.Source,
	}
}
