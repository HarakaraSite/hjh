// Package buildinfo describes the actual Go executable used for an execution.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"runtime"
)

var Version = "0.1.0-dev"
var SourceRevision = "uncommitted"
var SourceDirty = "true"

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
	return Manifest{SchemaVersion: 1, ProductVersion: Version, BuildID: digest, SourceRevision: SourceRevision,
		SourceDirty: SourceDirty == "true", GoVersion: runtime.Version(), Target: runtime.GOOS + "/" + runtime.GOARCH,
		EmbeddedRuntimeSHA256: digest, AgentConfigurationSchemaVersion: 1,
		SupportedToolAPIContracts: []string{"hjh-tool/v1"}, SupportedHookAPIContracts: []string{"hjh-hook/v1"}}, nil
}
