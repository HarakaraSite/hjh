package libraryexperiment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0644)
	})
}
func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (s *suite) distribution() (any, error) {
	root := filepath.Join(s.Packages, s.prefix)
	modules := filepath.Join(root, "extensions")
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	if err := copyTree(s.Tools, modules); err != nil {
		return nil, err
	}
	exe := filepath.Join(root, "s03")
	b, err := os.ReadFile(s.Exe)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(exe, b, 0755); err != nil {
		return nil, err
	}
	beforeHash, err := hashFile(exe)
	if err != nil {
		return nil, err
	}
	if err = checkManifest(modules); err != nil {
		return nil, err
	}
	emptyPath := filepath.Join(root, "empty-path")
	if err = os.MkdirAll(emptyPath, 0755); err != nil {
		return nil, err
	}
	config := filepath.Join(root, "hook-config.yaml")
	if err = os.WriteFile(config, []byte("note: prepared\n"), 0644); err != nil {
		return nil, err
	}
	input := map[string]any{"url": s.server.URL, "path": filepath.Join(root, "report.json"), "hookConfigPath": config}
	inputFile := filepath.Join(root, "input.json")
	if err = writeJSON(inputFile, input); err != nil {
		return nil, err
	}
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "PATH=") {
			env = append(env, e)
		}
	}
	env = append(env, "PATH="+emptyPath)
	observations := []any{}
	db := filepath.Join(root, "history.sqlite")
	for _, step := range []struct{ name, tool, hook string }{
		{"distributed_before", "tool-one", "hook-one"},
		{"distributed_edited", "tool-distributed", "hook-distributed"},
	} {
		if step.name == "distributed_edited" {
			if err = replace(filepath.Join(modules, "modules/catalog.js"), "tool-one", "tool-distributed"); err != nil {
				return nil, err
			}
			if err = replace(filepath.Join(modules, "hooks.js"), "hook-one", "hook-distributed"); err != nil {
				return nil, err
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, exe, "-worker", "-tools", modules, "-db", db, "-run", s.prefix+"-"+step.name, "-input", inputFile)
		cmd.Dir = root
		cmd.Env = env
		output, runErr := cmd.Output()
		cancel()
		if runErr != nil {
			return observations, fmt.Errorf("distributed worker: %w; output %s", runErr, output)
		}
		var o Observation
		if err = json.Unmarshal(output, &o); err != nil {
			return observations, err
		}
		v, err := verify(o, input, step.tool, step.hook)
		if err != nil {
			return observations, err
		}
		if err = writeJSON(filepath.Join(s.Out, step.name+"-view.json"), o); err != nil {
			return observations, err
		}
		if err = copyTreeFile(input["path"].(string), filepath.Join(s.Out, step.name+"-report.json")); err != nil {
			return observations, err
		}
		observations = append(observations, map[string]any{"run": o.History.Run, "value": v, "readbackFile": step.name + "-view.json", "reportFile": step.name + "-report.json"})
	}
	if err = copyTreeFile(db, filepath.Join(s.Out, "distributed-history.sqlite")); err != nil {
		return observations, err
	}
	afterHash, err := hashFile(exe)
	if err != nil {
		return observations, err
	}
	return map[string]any{"packageDirectory": root, "binarySHA256": afterHash, "binaryUnchanged": beforeHash == afterHash, "workerPATH": emptyPath, "preparedVendorVerified": true, "workers": observations, "database": "distributed-history.sqlite", "runtimeDependencies": "Go executable + editable JS + prepared vendor; no Node/npm/curl/OS command launched"}, check(beforeHash == afterHash, "package binary changed during edits")
}

func copyTreeFile(source, target string) error {
	b, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, b, 0644)
}

// Verify the actual copied dependency bytes against the preparation manifest.
func checkManifest(modules string) error {
	b, err := os.ReadFile(filepath.Join(modules, "vendor/manifest.json"))
	if err != nil {
		return err
	}
	var manifest struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"files"`
	}
	if err = json.Unmarshal(b, &manifest); err != nil {
		return err
	}
	for file, entry := range manifest.Files {
		path := filepath.Join(modules, "vendor", filepath.FromSlash(file))
		actual, err := hashFile(path)
		if err != nil {
			return err
		}
		stat, err := os.Stat(path)
		if err != nil {
			return err
		}
		if actual != entry.SHA256 || stat.Size() != int64(entry.Bytes) {
			return fmt.Errorf("copied dependency differs: %s", file)
		}
	}
	return nil
}
