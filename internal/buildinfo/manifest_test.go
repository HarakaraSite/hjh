package buildinfo

import (
	"fmt"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionLineIncludesManifestBuildDetails(t *testing.T) {
	manifest, err := Current()
	if err != nil {
		t.Fatal(err)
	}

	line, err := VersionLine()
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"hjh " + manifest.ProductVersion,
		"source " + manifest.SourceRevision,
		"Go " + manifest.GoVersion,
		"target " + manifest.Target,
		"binary sha256:" + manifest.BuildID,
		"runtime sha256:" + manifest.EmbeddedRuntimeSHA256,
		fmt.Sprintf("config-schema=%d", manifest.AgentConfigurationSchemaVersion),
		"tool=" + strings.Join(manifest.SupportedToolAPIContracts, ","),
		"hook=" + strings.Join(manifest.SupportedHookAPIContracts, ","),
	} {
		if !strings.Contains(line, part) {
			t.Errorf("VersionLine() = %q, missing %q", line, part)
		}
	}
	if manifest.SourceDirty && !strings.Contains(line, " (dirty)") {
		t.Errorf("VersionLine() = %q, missing dirty marker", line)
	}
}

func TestSourceMetadataUsesLinkerValues(t *testing.T) {
	oldRevision, oldDirty := SourceRevision, SourceDirty
	SourceRevision, SourceDirty = "package-revision", "false"
	t.Cleanup(func() { SourceRevision, SourceDirty = oldRevision, oldDirty })

	revision, dirty := sourceMetadata()
	if revision != "package-revision" || dirty {
		t.Fatalf("sourceMetadata() = %q, %t; want linker revision and clean state", revision, dirty)
	}
}

func TestSourceFromBuildInfoUsesVCSSettings(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "go-build-revision"},
		{Key: "vcs.modified", Value: "true"},
	}}
	revision, dirty, ok := sourceFromBuildInfo(info)
	if !ok || revision != "go-build-revision" || !dirty {
		t.Fatalf("sourceFromBuildInfo() = %q, %t, %t", revision, dirty, ok)
	}
}
