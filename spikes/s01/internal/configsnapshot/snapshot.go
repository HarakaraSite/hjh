// Package configsnapshot captures executable configuration and external source bytes.
package configsnapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
)

type Settings struct {
	Model       string `json:"model"`
	Instruction string `json:"instruction"`
	Extensions  string `json:"extensions"`
	Hooks       string `json:"hooks"`
}
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}
type Descriptor struct {
	Settings     Settings          `json:"settings"`
	Go           string            `json:"go"`
	BinarySHA256 string            `json:"binarySHA256"`
	GoModules    map[string]string `json:"goModules"`
	Files        []File            `json:"files"`
}
type Snapshot struct {
	ID         string            `json:"id"`
	Descriptor Descriptor        `json:"descriptor"`
	Assets     map[string][]byte `json:"assets"`
}

func Digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

func Capture(settingsPath, exe string) (Snapshot, error) {
	s := Snapshot{Assets: map[string][]byte{}}
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s.Descriptor.Settings); err != nil {
		return s, err
	}
	settings := &s.Descriptor.Settings
	if !filepath.IsAbs(settings.Extensions) {
		settings.Extensions = filepath.Join(filepath.Dir(settingsPath), settings.Extensions)
	}
	settings.Extensions, err = filepath.Abs(settings.Extensions)
	if err != nil {
		return s, err
	}
	b, err = os.ReadFile(exe)
	if err != nil {
		return s, err
	}
	s.Descriptor.BinarySHA256 = Digest(b)
	s.Descriptor.Go = runtime.Version()
	s.Descriptor.GoModules = map[string]string{}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			s.Descriptor.GoModules[dep.Path] = dep.Version
		}
	}
	err = filepath.WalkDir(settings.Extensions, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(settings.Extensions, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		s.Assets[rel] = data
		s.Descriptor.Files = append(s.Descriptor.Files, File{Path: rel, SHA256: Digest(data), Bytes: len(data)})
		return nil
	})
	if err != nil {
		return s, err
	}
	b, err = json.Marshal(s.Descriptor)
	if err != nil {
		return s, err
	}
	s.ID = Digest(b)
	return s, nil
}

// Materialize uses captured bytes; later edits to the original files cannot affect this Worker.
func (s Snapshot) Materialize(workdir string) (string, error) {
	if err := os.MkdirAll(workdir, 0755); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(workdir, "config-"+s.ID[:12]+"-")
	if err != nil {
		return "", err
	}
	for _, file := range s.Descriptor.Files {
		path := filepath.Join(dir, filepath.FromSlash(file.Path))
		data := s.Assets[file.Path]
		if Digest(data) != file.SHA256 {
			return "", fmt.Errorf("stored snapshot bytes differ: %s", file.Path)
		}
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
		if err = os.WriteFile(path, data, 0644); err != nil {
			return "", err
		}
	}
	return dir, nil
}
