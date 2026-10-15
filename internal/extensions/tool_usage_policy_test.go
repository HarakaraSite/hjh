package extensions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolUsagePolicySelectionAndComposition(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	xdg, workspace, extensionDir := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := InstallDefaults(extensionDir); err != nil {
		t.Fatal(err)
	}
	capture := func() Snapshot {
		t.Helper()
		s, err := CaptureDir(extensionDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := ApplyToolUsagePolicy(&s, workspace); err != nil {
			t.Fatal(err)
		}
		return s
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	initial := capture()
	if !strings.Contains(initial.SystemInstruction, "## Tool usage policy") || initial.ToolUsagePolicySource != nil {
		t.Fatal("builtin policy absent or has external provenance")
	}
	userPath := filepath.Join(xdg, "hjh", "instructions", "tool-usage.md")
	workspacePath := filepath.Join(workspace, ".hjh", "instructions", "tool-usage.md")
	write(userPath, "USER_POLICY")
	user := capture()
	if !strings.Contains(user.SystemInstruction, "USER_POLICY") || user.ToolUsagePolicySource.SourceLocator != userPath {
		t.Fatal("user policy not selected")
	}
	write(workspacePath, "WORKSPACE_POLICY")
	write(filepath.Join(xdg, "hjh", "instructions", "base.md"), "CUSTOM_BASE")
	local := capture()
	if !strings.HasPrefix(local.SystemInstruction, "CUSTOM_BASE\n\nWORKSPACE_POLICY\n\n## Active tool guidelines") || strings.Contains(local.SystemInstruction, "USER_POLICY") {
		t.Fatal("workspace did not replace user or independent instructions lost their order")
	}
	if string(local.ToolUsagePolicySource.Bytes) != "WORKSPACE_POLICY" || local.ToolUsagePolicySource.SourceLocator != workspacePath {
		t.Fatal("policy provenance mismatch")
	}
	write(workspacePath, "NEXT_POLICY")
	if strings.Contains(local.SystemInstruction, "NEXT_POLICY") || !strings.Contains(capture().SystemInstruction, "NEXT_POLICY") {
		t.Fatal("policy not frozen/refreshed on preparation")
	}
	write(workspacePath, "")
	empty := capture()
	if strings.Contains(empty.SystemInstruction, "POLICY") || empty.ToolUsagePolicySource == nil || len(empty.ToolUsagePolicySource.Bytes) != 0 {
		t.Fatal("empty override did not suppress policy")
	}
}
