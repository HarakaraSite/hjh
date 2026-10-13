package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestBaseInstructionReplacementAndConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := t.TempDir()
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.SystemInstruction = "ADDITIONAL_CONFIG_INSTRUCTION"
	data = contracts.JSON(cfg)
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	initial, err := Capture(configPath)
	if err != nil || initial.BaseInstruction.Path != "" || initial.BaseInstruction.BuiltinVersion == "" || !strings.Contains(initial.SystemInstruction, builtinBaseInstruction("default")) {
		t.Fatalf("missing file did not use the builtin: %+v, %v", initial.BaseInstruction, err)
	}
	path := filepath.Join(home, ".config", "hjh", "instructions", "base.md")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	const text = "CUSTOM_BASE\n日本語の基本指示。\n"
	write(path, text)
	custom, err := Capture(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if custom.BaseInstruction.Path != path || custom.BaseInstruction.Digest != Digest([]byte(text)) || custom.BaseInstruction.BuiltinVersion != "" || custom.BaseInstructionSource == nil || string(custom.BaseInstructionSource.Bytes) != text {
		t.Fatalf("custom base/source = %+v, %+v", custom.BaseInstruction, custom.BaseInstructionSource)
	}
	if strings.Contains(custom.SystemInstruction, "You are a generic agent") || strings.Contains(custom.SystemInstruction, "During multi-step work") {
		t.Fatal("custom base was added instead of replacing the builtin")
	}
	for _, part := range []string{text, cfg.Roles["default"].Instruction, "ADDITIONAL_CONFIG_INSTRUCTION", "## Active tool guidelines", "Available roles for run_agent"} {
		if !strings.Contains(custom.SystemInstruction, part) {
			t.Fatalf("replacement omitted an independent instruction: %q", part)
		}
	}
	profile, err := ResolveInstructionProfile(data, configPath, "")
	if err != nil || !reflect.DeepEqual(profile.Base, custom.BaseInstruction) {
		t.Fatalf("preview/capture base mismatch: %+v, %v", profile.Base, err)
	}
	encoded, err := custom.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Base contracts.BaseInstruction `json:"baseInstruction"`
	}
	if err := json.Unmarshal(encoded, &saved); err != nil || !reflect.DeepEqual(saved.Base, custom.BaseInstruction) {
		t.Fatalf("snapshot omitted the actual base source: %+v %v", saved.Base, err)
	}
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	xdgPath := filepath.Join(xdg, "hjh", "instructions", "base.md")
	write(xdgPath, "XDG_BASE")
	xdgCapture, err := Capture(configPath)
	if err != nil || xdgCapture.BaseInstruction.Path != xdgPath || !strings.Contains(xdgCapture.SystemInstruction, "XDG_BASE") || strings.Contains(xdgCapture.SystemInstruction, "CUSTOM_BASE") {
		t.Fatalf("XDG base not selected: %+v %v", xdgCapture.BaseInstruction, err)
	}
}
