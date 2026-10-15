package extensions

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hjh/internal/buildinfo"
	"hjh/internal/contracts"
)

//go:embed tool_usage_policy.md
var builtinToolUsagePolicy string

const toolUsagePolicyIdentity = "instruction:tool-usage-policy"

// ToolUsagePolicy is selected once during preparation of a root or child
// execution. Source is instruction provenance, not a VM/module resource.
type ToolUsagePolicy struct {
	Text           string
	Path           string
	Digest         string
	BuiltinVersion string
	Source         *Resource
}

// SelectToolUsagePolicy uses the real workspace, independently of --config's
// extension directory. An existing empty file is an explicit empty policy.
func SelectToolUsagePolicy(workspace string) (ToolUsagePolicy, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return ToolUsagePolicy{}, fmt.Errorf("resolve tool usage instruction directory: %w", err)
	}
	paths := []string{filepath.Join(root, "hjh", "instructions", "tool-usage.md")}
	if workspace != "" {
		paths = append([]string{filepath.Join(workspace, ".hjh", "instructions", "tool-usage.md")}, paths...)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return ToolUsagePolicy{}, fmt.Errorf("read tool usage instruction %s: %w", path, err)
		}
		digest := Digest(data)
		return ToolUsagePolicy{Text: string(data), Path: path, Digest: digest, Source: &Resource{
			LogicalIdentity: toolUsagePolicyIdentity, SourceLocator: path, ContentDigest: digest, Bytes: data,
		}}, nil
	}
	return ToolUsagePolicy{Text: builtinToolUsagePolicy, Digest: Digest([]byte(builtinToolUsagePolicy)), BuiltinVersion: buildinfo.Version}, nil
}

// ApplyToolUsagePolicy completes a captured composition with the workspace's
// short tool-selection policy. Parent and child preparation share this path.
// The captured base stays first; role/tool/workspace instructions stay separate.
func ApplyToolUsagePolicy(snapshot *Snapshot, workspace string) error {
	policy, err := SelectToolUsagePolicy(workspace)
	if err != nil {
		return err
	}
	var base string
	for _, component := range snapshot.InstructionComponents {
		var saved struct {
			Identity string `json:"identity"`
			Text     string `json:"text"`
		}
		if json.Unmarshal(component, &saved) == nil && saved.Identity == baseInstructionIdentity {
			base = saved.Text
		}
	}
	if policy.Text != "" {
		if base != "" && strings.HasPrefix(snapshot.SystemInstruction, base) {
			rest := strings.TrimPrefix(snapshot.SystemInstruction[len(base):], "\n\n")
			snapshot.SystemInstruction = base + "\n\n" + policy.Text
			if rest != "" {
				snapshot.SystemInstruction += "\n\n" + rest
			}
		} else if snapshot.SystemInstruction != "" {
			snapshot.SystemInstruction = policy.Text + "\n\n" + snapshot.SystemInstruction
		} else {
			snapshot.SystemInstruction = policy.Text
		}
	}
	snapshot.ToolUsagePolicySource = policy.Source
	snapshot.InstructionComponents = append(snapshot.InstructionComponents, contracts.JSON(map[string]any{
		"identity": toolUsagePolicyIdentity, "text": policy.Text,
		"source": map[string]string{"path": policy.Path, "digest": policy.Digest, "builtinVersion": policy.BuiltinVersion},
	}))
	return nil
}
