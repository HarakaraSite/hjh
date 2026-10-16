// Package buildinfo describes the actual Go executable used for an execution.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
)

var Version = "1.0.0"

// SourceRevision and SourceDirty are set by distribution builds. Ordinary Go
// builds use the VCS settings recorded by the Go tool instead.
var SourceRevision = ""
var SourceDirty = ""

type Manifest struct {
	SchemaVersion                   int      `json:"schemaVersion"`
	ProductVersion                  string   `json:"productVersion"`
	BuildID                         string   `json:"buildId"`
	SourceRevision                  string   `json:"sourceRevision"`
	SourceDirty                     bool     `json:"sourceDirty"`
	GoVersion                       string   `json:"goVersion"`
	Target                          string   `json:"target"`
	EmbeddedRuntimeSHA256           string   `json:"embeddedRuntimeSha256"`
	AgentConfigurationSchemaVersion int      `json:"agentConfigurationSchemaVersion"`
	SupportedToolAPIContracts       []string `json:"supportedToolApiContracts"`
	SupportedHookAPIContracts       []string `json:"supportedHookApiContracts"`
}

func Current() (Manifest, error) {
	sourceRevision, sourceDirty := sourceMetadata()
	path, err := os.Executable()
	if err != nil {
		// A minimal Linux root can run this static binary without /proc. Use
		// its actual invocation path there, as in the distribution spike.
		path, err = exec.LookPath(os.Args[0])
		if err != nil {
			return Manifest{}, err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return Manifest{}, err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	return Manifest{SchemaVersion: 1, ProductVersion: Version, BuildID: digest, SourceRevision: sourceRevision,
		SourceDirty: sourceDirty, GoVersion: runtime.Version(), Target: runtime.GOOS + "/" + runtime.GOARCH,
		EmbeddedRuntimeSHA256: digest, AgentConfigurationSchemaVersion: 1,
		SupportedToolAPIContracts: []string{"hjh-tool/v1"}, SupportedHookAPIContracts: []string{"hjh-hook/v1"}}, nil
}

// VersionLine returns the version and build details for the executable being
// run. The digest fields identify the exact executable and its embedded
// runtime as recorded in the manifest.
func VersionLine() (string, error) {
	manifest, err := Current()
	if err != nil {
		return "", err
	}
	dirty := ""
	if manifest.SourceDirty {
		dirty = " (dirty)"
	}
	return fmt.Sprintf("hjh %s | source %s%s | Go %s | target %s | binary sha256:%s | runtime sha256:%s | contracts config-schema=%d tool=%s hook=%s",
		manifest.ProductVersion, manifest.SourceRevision, dirty, manifest.GoVersion, manifest.Target,
		manifest.BuildID, manifest.EmbeddedRuntimeSHA256, manifest.AgentConfigurationSchemaVersion,
		strings.Join(manifest.SupportedToolAPIContracts, ","), strings.Join(manifest.SupportedHookAPIContracts, ",")), nil
}

func sourceMetadata() (string, bool) {
	if SourceRevision != "" {
		return SourceRevision, SourceDirty == "true"
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if revision, dirty, ok := sourceFromBuildInfo(info); ok {
			return revision, dirty
		}
	}
	// This fallback is used only when the executable has no linker metadata and
	// was built without Go VCS settings (for example, from a source archive).
	return "uncommitted", true
}

func sourceFromBuildInfo(info *debug.BuildInfo) (string, bool, bool) {
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	revision := settings["vcs.revision"]
	if revision == "" {
		return "", false, false
	}
	return revision, settings["vcs.modified"] == "true", true
}
