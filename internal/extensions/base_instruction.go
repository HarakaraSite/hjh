package extensions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"hjh/internal/buildinfo"
	"hjh/internal/contracts"
)

const baseInstructionIdentity = "instruction:base"

type selectedBaseInstruction struct {
	text   string
	base   contracts.BaseInstruction
	source *Resource
}

// selectBaseInstruction owns the user-scoped replacement for the built-in
// common instruction. Capture and orientation previews use the same selector.
func selectBaseInstruction(cfg Config, configPath, roleName string) (selectedBaseInstruction, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return selectedBaseInstruction{}, fmt.Errorf("resolve base instruction directory: %w", err)
	}
	path := filepath.Join(root, "hjh", "instructions", "base.md")
	data, err := os.ReadFile(path)
	base := contracts.BaseInstruction{ConfigPath: configPath, HasSystemInstruction: cfg.SystemInstruction != ""}
	if errors.Is(err, os.ErrNotExist) {
		text := builtinBaseInstruction(roleName)
		base.BuiltinVersion, base.Digest = buildinfo.Version, Digest([]byte(text))
		return selectedBaseInstruction{text: text, base: base}, nil
	}
	if err != nil {
		return selectedBaseInstruction{}, fmt.Errorf("read base instruction %s: %w", path, err)
	}
	base.Path, base.Digest = path, Digest(data)
	return selectedBaseInstruction{text: string(data), base: base, source: &Resource{
		LogicalIdentity: baseInstructionIdentity, SourceLocator: path,
		ContentDigest: base.Digest, Bytes: data,
	}}, nil
}

func builtinBaseInstruction(roleName string) string {
	return "You are a generic agent executing role \"" + roleName + "\". Use the registered tool definitions to perform the requested work and report the result.\n\n" +
		"During multi-step work, share useful findings and progress as concise ordinary assistant text before continuing with tools."
}
