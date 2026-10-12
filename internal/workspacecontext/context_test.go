package workspacecontext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestDiscoverReadsInstructionSourcesInScopeOrder(t *testing.T) {
	home, config := setUserDirectories(t)
	_ = home
	root := t.TempDir()
	workspace := filepath.Join(root, "project", "nested")
	for _, dir := range []string{workspace, filepath.Join(config, "hjh")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	sources := []struct {
		path string
		body []byte
	}{
		{filepath.Join(config, "hjh", "AGENTS.md"), []byte("user\nexact bytes")},
		{filepath.Join(root, "AGENTS.md"), []byte("filesystem root scope\n")},
		{filepath.Join(root, "project", "AGENTS.md"), []byte("project scope\nwith second line")},
		{filepath.Join(workspace, "AGENTS.md"), []byte("workspace scope\n")},
	}
	for _, source := range sources {
		if err := os.WriteFile(source.path, source.body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := Discover(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Instructions) != len(sources) {
		t.Fatalf("got %d instruction sources, want %d", len(snapshot.Instructions), len(sources))
	}
	prompt := snapshot.Instruction(false)
	previous := -1
	for i, source := range sources {
		instruction := snapshot.Instructions[i]
		if instruction.Path != source.path {
			t.Errorf("source %d path = %q, want %q", i, instruction.Path, source.path)
		}
		if !bytes.Equal(instruction.Bytes, source.body) {
			t.Errorf("source %d bytes changed: got %q, want %q", i, instruction.Bytes, source.body)
		}
		digest := sha256.Sum256(source.body)
		wantDigest := "sha256:" + hex.EncodeToString(digest[:])
		if instruction.Digest != wantDigest {
			t.Errorf("source %d digest = %q, want %q", i, instruction.Digest, wantDigest)
		}
		position := strings.Index(prompt, string(source.body))
		if position < 0 {
			t.Errorf("prompt does not contain exact source bytes %q", source.body)
		}
		if position <= previous {
			t.Errorf("prompt source %d is out of scope order", i)
		}
		previous = position
	}
	if !strings.Contains(prompt, "closer to the workspace takes precedence") {
		t.Error("prompt does not explain that nearer workspace instructions take precedence")
	}
}

func TestDiscoverSkillsUsesPrioritySymlinksAndYAMLMetadata(t *testing.T) {
	home, config := setUserDirectories(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	projectHJH := filepath.Join(workspace, ".hjh", "skills")
	projectAgents := filepath.Join(workspace, ".agents", "skills")
	userHJH := filepath.Join(config, "hjh", "skills")
	userAgents := filepath.Join(home, ".agents", "skills")
	for _, dir := range []string{projectHJH, projectAgents, userHJH, userAgents} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeSkill(t, filepath.Join(projectHJH, "priority"), "name: priority\ndescription: first root wins\n---\n")
	writeSkill(t, filepath.Join(projectAgents, "priority-copy"), "name: priority\ndescription: second root loses\n---\n")
	writeSkill(t, filepath.Join(userHJH, "priority"), "name: priority\ndescription: third root loses\n---\n")
	writeSkill(t, filepath.Join(userAgents, "priority"), "name: priority\ndescription: fourth root loses\n---\n")

	realDir := filepath.Join(t.TempDir(), "symlink-target", "linked")
	writeSkill(t, realDir, "name: linked\ndescription: reached through a directory symlink\n---\nBODY_MARKER_9d3e7a")
	if err := os.Symlink(realDir, filepath.Join(projectHJH, "linked-alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(projectAgents, "same-file-alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(projectHJH, filepath.Join(projectHJH, "cycle")); err != nil {
		t.Fatal(err)
	}

	writeSkill(t, filepath.Join(projectHJH, "multi"), "name: multi\ndescription: |\n  first description line\n  second description line\ndisable-model-invocation: false\n---\nBODY_MARKER_9d3e7a")
	writeSkill(t, filepath.Join(projectAgents, "private"), "description: |\n  Manual only.\n  Do not auto select.\ndisable-model-invocation: true\n---\nBODY_MARKER_9d3e7a")
	if err := os.MkdirAll(filepath.Join(projectHJH, "invalid"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectHJH, "invalid", "SKILL.md"), []byte("description is missing frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Discover(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	winners := make(map[string]Skill, len(snapshot.Skills))
	for _, skill := range snapshot.Skills {
		winners[skill.Name] = skill
	}
	priority, ok := winners["priority"]
	if !ok || priority.Source != "project-hjh" || priority.Description != "first root wins" {
		t.Fatalf("priority winner = %#v, want first project root", priority)
	}
	if _, ok := winners["linked"]; !ok {
		t.Fatal("skill in symlinked directory was not discovered")
	}
	if _, ok := winners["multi"]; !ok {
		t.Fatal("skill with YAML block description was not discovered")
	}
	private, ok := winners["private"]
	if !ok || !private.DisableModelInvocation || private.Name != "private" {
		t.Fatalf("omitted name or disable metadata not parsed: %#v", private)
	}
	if private.Description != "Manual only.\nDo not auto select." {
		t.Errorf("multiline description = %q", private.Description)
	}

	var priorityDiagnostics int
	for _, diagnostic := range snapshot.Diagnostics {
		if strings.Contains(diagnostic.Message, `skill "priority"`) {
			priorityDiagnostics++
		}
	}
	if priorityDiagnostics != 4 { // one selected winner and three shadowed copies
		t.Errorf("got %d diagnostics for priority winner/losers, want 4", priorityDiagnostics)
	}
	if len(snapshot.Diagnostics) == 0 {
		t.Fatal("malformed skill did not produce a diagnostic")
	}

	allState := strings.Join([]string{
		fmtSnapshot(snapshot), snapshot.Instruction(true),
	}, "\n")
	if strings.Contains(allState, "BODY_MARKER_9d3e7a") {
		t.Fatal("skill body was retained in the snapshot or instruction catalog")
	}
}

func TestInstructionListsOnlyInvocableSkillsWhenReaderExists(t *testing.T) {
	snapshot := Snapshot{Skills: []Skill{
		{
			Name: "build", Description: "build the application", Path: "/work/project/.hjh/skills/build/SKILL.md",
			Directory: "/work/project/.hjh/skills/build", Source: "project-hjh",
		},
		{
			Name: "manual", Description: "only when explicitly expanded", Path: "/work/project/.agents/skills/manual/SKILL.md",
			Directory: "/work/project/.agents/skills/manual", Source: "project-agents", DisableModelInvocation: true,
		},
	}}
	prompt := snapshot.Instruction(true)
	for _, want := range []string{
		"build: build the application",
		"/work/project/.hjh/skills/build/SKILL.md",
		"/work/project/.hjh/skills/build",
		"return await tools.read_file({path: absoluteSkillPath});",
		"body from the outer script",
		"The inner tool observation alone does not put the body in the conversation.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("skill instruction does not contain %q", want)
		}
	}
	if strings.Contains(prompt, "manual:") {
		t.Error("disable-model-invocation skill was listed for automatic selection")
	}
	if noReaderPrompt := snapshot.Instruction(false); strings.Contains(noReaderPrompt, "Available skills") || strings.Contains(noReaderPrompt, "read_file") {
		t.Errorf("reader guidance or skills were presented without read_file: %q", noReaderPrompt)
	}

	orientation := snapshot.Orientation(contracts.BaseInstruction{BuiltinVersion: "test"}, "/work/project", "default", "preview", true)
	if len(orientation.Skills) != 1 || orientation.Skills[0].Name != "build" {
		t.Errorf("model-facing orientation skills = %#v", orientation.Skills)
	}
	if len(orientation.DiscoveredSkills) != 2 {
		t.Errorf("all discovered orientation skills = %#v", orientation.DiscoveredSkills)
	}
	withoutReader := snapshot.Orientation(contracts.BaseInstruction{}, "/work/project", "no-reader", "preview", false)
	if len(withoutReader.Skills) != 0 || len(withoutReader.DiscoveredSkills) != 2 {
		t.Errorf("orientation did not distinguish presented and discovered skills: %#v", withoutReader)
	}
}

func setUserDirectories(t *testing.T) (home, config string) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	config = filepath.Join(base, "config")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)
	return home, config
}

func writeSkill(t *testing.T, directory, content string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("---\n"+content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fmtSnapshot(snapshot Snapshot) string {
	var parts []string
	for _, skill := range snapshot.Skills {
		parts = append(parts, skill.Name, skill.Description, skill.Path, skill.Directory, skill.Source)
	}
	for _, instruction := range snapshot.Instructions {
		parts = append(parts, instruction.Path, string(instruction.Bytes), instruction.Digest)
	}
	for _, diagnostic := range snapshot.Diagnostics {
		parts = append(parts, diagnostic.Path, diagnostic.Message)
	}
	return strings.Join(parts, "\n")
}
