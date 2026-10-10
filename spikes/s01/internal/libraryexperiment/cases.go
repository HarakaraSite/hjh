package libraryexperiment

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

func (s *suite) normal() (any, error) {
	input := s.input("normal")
	o, err := s.execute("normal", s.Tools, filepath.Join(s.Tools, "hooks.js"), generatedJS, input)
	if err != nil {
		return o, err
	}
	v, err := verify(o, input, "tool-one", "hook-one")
	return map[string]any{"run": o.History.Run, "readbackFile": "normal-view.json", "reportFile": input["path"], "value": v, "eventCount": len(o.History.Events)}, err
}

func verify(o Observation, input map[string]any, toolRevision, hookRevision string) (Value, error) {
	var v Value
	if err := json.Unmarshal(o.Worker.Value, &v); err != nil {
		return v, err
	}
	b, err := os.ReadFile(input["path"].(string))
	if err != nil {
		return v, err
	}
	var report Report
	if err = json.Unmarshal(b, &report); err != nil {
		return v, err
	}
	if err = check(o.History.Status == "completed" && o.Worker.HostOperationsAfterReturn == 0 && v.Report.Project == "hjh-library-spike" && v.Report.ToolRevision == toolRevision && v.Report.HookRevision == hookRevision && v.Report.Note == "prepared:"+hookRevision && reflect.DeepEqual(v.Names, []string{"accepted", "prerelease"}) && len(v.Report.Compatible) == 2 && v.Report.Compatible[0].Version == "1.4.2" && v.Report.Compatible[1].Version == "2.1.0-beta.2" && reflect.DeepEqual(report, v.Report) && len(b) == v.SavedBytes, "library result, editable behavior or real file readback mismatch"); err != nil {
		return v, err
	}
	workflow, okWorkflow := find(o, "tool", "workflow")
	catalog, okCatalog := find(o, "tool", "catalog")
	http, okHTTP := find(o, "tool", "http")
	goHTTP, okGoHTTP := find(o, "host", "http")
	write, okWrite := find(o, "tool", "writeFile")
	read, okRead := find(o, "tool", "readFile")
	if err = check(okWorkflow && okCatalog && okHTTP && okGoHTTP && okWrite && okRead && workflow.Parent == o.History.Root && catalog.Parent == workflow.ID && http.Parent == catalog.ID && goHTTP.Parent == http.ID && write.Parent == workflow.ID && read.Parent == workflow.ID, "nested tools or host parent mismatch"); err != nil {
		return v, err
	}
	// Every executed tool on this route must go through both editable hooks.
	for _, tool := range o.History.Calls {
		if tool.Kind != "tool" {
			continue
		}
		before, after := false, false
		for _, hook := range o.History.Calls {
			if hook.Kind != "hook" || hook.Parent != tool.ID {
				continue
			}
			if hook.Status != "completed" {
				return v, fmt.Errorf("hook failed for %s", tool.Name)
			}
			before = before || hook.Name == "beforeTool"
			after = after || hook.Name == "afterTool"
		}
		if !before || !after {
			return v, fmt.Errorf("tool %s omitted hooks", tool.Name)
		}
	}
	for _, hook := range o.History.Calls {
		if hook.Kind != "hook" || hook.Name != "beforeTool" || hook.Parent != catalog.ID {
			continue
		}
		for _, call := range o.History.Calls {
			if call.Kind == "host" && call.Name == "readFile" && call.Parent == hook.ID && call.Status == "completed" {
				return v, nil
			}
		}
	}
	return v, errors.New("hook's Go file API call was not recorded under hook")
}

func (s *suite) edits() (any, error) {
	modules, err := os.MkdirTemp("", "hjh-s03-edits-")
	if err != nil {
		return nil, err
	}
	if err = copyTree(s.Tools, modules); err != nil {
		return nil, err
	}
	beforeHash, err := hashFile(s.Exe)
	if err != nil {
		return nil, err
	}
	observations := []any{}
	for _, step := range []struct{ name, tool, hook string }{
		{"edit_before", "tool-one", "hook-one"},
		{"edit_tool", "tool-two", "hook-one"},
		{"edit_hook", "tool-two", "hook-two"},
	} {
		if step.name == "edit_tool" {
			err = replace(filepath.Join(modules, "modules/catalog.js"), "tool-one", "tool-two")
		}
		if step.name == "edit_hook" {
			err = replace(filepath.Join(modules, "hooks.js"), "hook-one", "hook-two")
		}
		if err != nil {
			return observations, err
		}
		input := s.input(step.name)
		o, err := s.execute(step.name, modules, filepath.Join(modules, "hooks.js"), generatedJS, input)
		if err != nil {
			return observations, err
		}
		v, err := verify(o, input, step.tool, step.hook)
		if err != nil {
			return observations, err
		}
		observations = append(observations, map[string]any{"run": o.History.Run, "readbackFile": step.name + "-view.json", "value": v})
	}
	afterHash, err := hashFile(s.Exe)
	if err != nil {
		return observations, err
	}
	return map[string]any{"workers": observations, "sourceCopy": modules, "sameProcess": true, "binarySHA256": afterHash}, check(beforeHash == afterHash, "binary changed during source-only edits")
}

func (s *suite) yamlError() (any, error) {
	input := s.input("yaml_error")
	input["url"] = s.server.URL + "/invalid"
	o, runErr := s.execute("yaml_error", s.Tools, filepath.Join(s.Tools, "hooks.js"), generatedJS, input)
	if runErr == nil {
		return o, errors.New("malformed real YAML unexpectedly succeeded")
	}
	catalog, ok := find(o, "tool", "catalog")
	_, fileErr := os.Stat(input["path"].(string))
	return map[string]any{"run": o.History.Run, "error": runErr.Error(), "readbackFile": "yaml_error-view.json"}, check(o.History.Status == "failed" && ok && catalog.Status == "failed" && strings.Contains(runErr.Error(), "YAMLParseError") && errors.Is(fileErr, os.ErrNotExist), "YAML parse error did not reach parent/history, or report was written after failure")
}

func (s *suite) hookError() (any, error) {
	modules, err := os.MkdirTemp("", "hjh-s03-hook-error-")
	if err != nil {
		return nil, err
	}
	if err = copyTree(s.Tools, modules); err != nil {
		return nil, err
	}
	if err = replace(filepath.Join(modules, "hooks.js"), "return { ...args, note: config.note + ':hook-one' };", "throw new Error('hook edit failure');"); err != nil {
		return nil, err
	}
	o, runErr := s.execute("hook_error", modules, filepath.Join(modules, "hooks.js"), generatedJS, s.input("hook_error"))
	if runErr == nil {
		return o, errors.New("edited hook failure did not propagate")
	}
	catalog, ok := find(o, "tool", "catalog")
	found := false
	for _, call := range o.History.Calls {
		if call.Kind == "hook" && call.Parent == catalog.ID && call.Status == "failed" && strings.Contains(call.Error, "hook edit failure") {
			found = true
		}
	}
	_, ranHTTP := find(o, "host", "http")
	return map[string]any{"run": o.History.Run, "error": runErr.Error(), "readbackFile": "hook_error-view.json"}, check(ok && catalog.Status == "failed" && o.History.Status == "failed" && found && !ranHTTP && strings.Contains(runErr.Error(), "hook edit failure"), "beforeTool error or its effect on execution was lost")
}

func (s *suite) capabilities() (any, error) {
	o, err := s.execute("globals", s.Tools, "", `return Object.fromEntries(["fetch","URL","TextEncoder","Intl","process","require","Buffer","setTimeout"].map(name => [name,typeof globalThis[name]]));`, nil)
	if err != nil {
		return o, err
	}
	probes := []any{}
	rawCommonJS, err := os.ReadFile(filepath.Join(s.Tools, "vendor/semver/raw-index.cjs"))
	if err != nil {
		return nil, err
	}
	for _, probe := range []struct{ name, source string }{
		{"bare_yaml", "import {parse} from 'yaml'; export function probe(){ return parse('a: 1'); }"},
		{"raw_commonjs", fmt.Sprintf("import * as semver from %q; export function probe(){ return semver; }", filepath.Join(s.Tools, "vendor/semver/raw-index.cjs"))},
		// Isolate the published CommonJS body so its original exception is visible
		// through the already-observed tool Promise path, independently of module initialization.
		{"commonjs_body", "export function probe(){\n" + string(rawCommonJS) + "\n}"},
		{"node_fs", "import * as fs from 'node:fs'; export function probe(){ return fs; }"},
	} {
		modules, err := os.MkdirTemp("", "hjh-s03-probe-")
		if err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(modules, "index.js"), []byte(probe.source), 0644); err != nil {
			return nil, err
		}
		observed, runErr := s.execute(probe.name, modules, "", `return tools.probe();`, nil)
		message := ""
		if runErr != nil {
			message = runErr.Error()
		}
		probes = append(probes, map[string]any{"name": probe.name, "supported": runErr == nil, "observedError": message, "run": observed.History.Run, "readbackFile": probe.name + "-view.json"})
		// Outcomes describe this runtime; unsupported behavior is not a future rejection contract.
		if observed.History.Status == "" {
			return probes, errors.New("module probe did not preserve execution evidence")
		}
	}
	return map[string]any{"globals": json.RawMessage(o.Worker.Value), "globalsReadbackFile": "globals-view.json", "moduleProbes": probes}, nil
}

func replace(path, old, new string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), old) {
		return fmt.Errorf("edit target absent in %s", path)
	}
	return os.WriteFile(path, []byte(strings.ReplaceAll(string(b), old, new)), 0644)
}
