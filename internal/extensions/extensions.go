// Package extensions captures editable external ESM tools and hooks for a Worker.
package extensions

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"hjh/internal/buildinfo"
	"hjh/internal/contracts"
)

//go:embed assets/*
var defaultAssets embed.FS

// Config and Tool are the concise editable JSON form. Snapshot.JSON converts
// them to the legacy WorkerConfigurationSnapshot contract stored in history.
type Config struct {
	DefaultRole           string            `json:"defaultRole"`
	Roles                 map[string]Role   `json:"roles"`
	SystemInstruction     string            `json:"systemInstruction"`
	InstructionComponents []json.RawMessage `json:"instructionComponents"`
	ToolPaths             []string          `json:"toolPaths"`
	Tools                 []Tool            `json:"tools"`
	Hooks                 json.RawMessage   `json:"hooks"`
	Rejections            []json.RawMessage `json:"rejections"`
}

type Role struct {
	Revision    string   `json:"revision"`
	Description string   `json:"description"`
	Instruction string   `json:"instruction"`
	Tools       []string `json:"tools"`
}

type RoleInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}

type RoleCatalog struct {
	DefaultRole string     `json:"defaultRole"`
	Roles       []RoleInfo `json:"roles"`
}

type Tool struct {
	Name        string          `json:"name"`
	Revision    string          `json:"revision"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Module      string          `json:"module"`
	Export      string          `json:"export"`
}

type LegacyAgent struct {
	Name        string   `json:"name"`
	Revision    string   `json:"revision"`
	Instruction string   `json:"instruction"`
	Tools       []string `json:"tools"`
	Agents      []string `json:"agents"`
	Hooks       []string `json:"hooks,omitempty"`
}

type LegacySource struct {
	Kind string `json:"kind"`
	File string `json:"file,omitempty"`
}

type LegacyToolPaths struct {
	Source string   `json:"source,omitempty"`
	Deny   []string `json:"deny"`
}

type LegacyContract struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type LegacyPaths struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

type LegacyTool struct {
	Name       string         `json:"name"`
	Revision   string         `json:"revision"`
	Source     string         `json:"source"`
	Entry      string         `json:"entry,omitempty"`
	Contract   LegacyContract `json:"contract"`
	FileAccess string         `json:"fileAccess"`
	Paths      LegacyPaths    `json:"paths"`
}

type LegacyHook struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Contract string   `json:"contract"`
	Handlers []string `json:"handlers"`
}

type configurationSnapshot struct {
	SchemaVersion         int               `json:"schemaVersion"`
	ConfigurationID       string            `json:"configurationId"`
	Agent                 LegacyAgent       `json:"agent"`
	Source                LegacySource      `json:"source"`
	SystemInstruction     string            `json:"systemInstruction"`
	InstructionComponents []json.RawMessage `json:"instructionComponents"`
	ToolPaths             LegacyToolPaths   `json:"toolPaths"`
	Tools                 []LegacyTool      `json:"tools"`
	Hooks                 []LegacyHook      `json:"hooks"`
	Rejections            []json.RawMessage `json:"rejections"`
}

type Resource struct {
	Path            string `json:"path"`
	LogicalIdentity string `json:"logicalIdentity"`
	SourceLocator   string `json:"sourceLocator"`
	ContentDigest   string `json:"contentDigest"`
	Bytes           []byte `json:"-"`
}

type Snapshot struct {
	ConfigurationID       string
	Agent                 LegacyAgent
	Catalog               []RoleInfo
	Source                LegacySource
	SystemInstruction     string
	InstructionComponents []json.RawMessage
	ToolPaths             []string
	Tools                 []Tool
	Hooks                 []LegacyHook
	HookPath              string
	Rejections            []json.RawMessage
	Resources             []Resource
	Root                  string
}

var runJSInputSchema = json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}},"required":["code"]}`)

// RunJSDefinition returns the model-facing built-in for generated JavaScript.
// Delegation is described only when run_agent is part of the selected role.
func RunJSDefinition(delegation bool) contracts.ToolDefinition {
	description := "Run generated JavaScript that can call selected registered tools through tools.NAME(arguments). Return a JSON result from the async code body."
	if delegation {
		description += " It can delegate with api.agents.run({task, role?}), returning a child result with executionId, outcome, and optional finalText or error; await results or use Promise.all for independent delegations."
	}
	return contracts.ToolDefinition{
		Name:        "run_js",
		Description: description,
		InputSchema: append(json.RawMessage(nil), runJSInputSchema...),
	}
}

func Capture(configPath string, role ...string) (Snapshot, error) {
	if configPath == "" {
		return Snapshot{}, fmt.Errorf("extension config path is empty")
	}
	if len(role) > 1 {
		return Snapshot{}, fmt.Errorf("at most one extension role may be selected")
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return Snapshot{}, err
	}
	selected := ""
	if len(role) == 1 {
		selected = role[0]
	}
	return capture(filepath.Dir(abs), abs, selected)
}

func CaptureDir(dir string, role ...string) (Snapshot, error) {
	if dir == "" {
		return Snapshot{}, fmt.Errorf("extension directory is empty")
	}
	if len(role) > 1 {
		return Snapshot{}, fmt.Errorf("at most one extension role may be selected")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Snapshot{}, err
	}
	selected := ""
	if len(role) == 1 {
		selected = role[0]
	}
	return capture(abs, filepath.Join(abs, "config.json"), selected)
}

func capture(root, configPath, selectedRole string) (Snapshot, error) {
	configPath = filepath.Clean(configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Snapshot{}, err
	}
	cfg, err := decodeConfig(data, configPath)
	if err != nil {
		return Snapshot{}, err
	}

	resources := make([]Resource, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents := data
		if filepath.Clean(path) != configPath {
			loaded, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			contents = loaded
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		resources = append(resources, Resource{
			Path:            rel,
			LogicalIdentity: "extensions/" + rel,
			SourceLocator:   path,
			ContentDigest:   Digest(contents),
			Bytes:           append([]byte(nil), contents...),
		})
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}

	roleName := selectedRole
	if roleName == "" {
		roleName = cfg.DefaultRole
	}
	role, ok := cfg.Roles[roleName]
	if !ok {
		return Snapshot{}, fmt.Errorf("extension role %q is not defined", roleName)
	}
	selectedTools, err := resolveTools(cfg, roleName, role)
	if err != nil {
		return Snapshot{}, err
	}
	catalog := catalogFromConfig(cfg)
	legacyHooks, hookPath, err := legacyHooksFromConfig(cfg.Hooks)
	if err != nil {
		return Snapshot{}, err
	}
	roleRevision := role.Revision
	if roleRevision == "" {
		roleRevision = "1"
	}
	toolNames := make([]string, len(selectedTools))
	for i, tool := range selectedTools {
		toolNames[i] = tool.Name
	}
	agents := []string{}
	if containsTool(toolNames, "run_agent") {
		agents = make([]string, len(catalog.Roles))
		for i, info := range catalog.Roles {
			agents[i] = info.Name
		}
	}
	snapshot := Snapshot{
		ConfigurationID:       uuid.NewString(),
		Agent:                 LegacyAgent{Name: roleName, Revision: roleRevision, Instruction: role.Instruction, Tools: toolNames, Agents: agents},
		Catalog:               catalog.Roles,
		Source:                LegacySource{Kind: "external", File: configPath},
		InstructionComponents: cfg.InstructionComponents,
		ToolPaths:             cfg.ToolPaths,
		Tools:                 selectedTools,
		Hooks:                 legacyHooks,
		HookPath:              hookPath,
		Rejections:            cfg.Rejections,
		Resources:             resources,
		Root:                  root,
	}
	snapshot.SystemInstruction = assembleInstruction(cfg.SystemInstruction, role, roleName, selectedTools, catalog.Roles)
	return snapshot, nil
}

// LoadCatalog reads role names and descriptions without resolving a selected
// role or capturing extension resources.
func LoadCatalog(configPath string) (RoleCatalog, error) {
	if configPath == "" {
		return RoleCatalog{}, fmt.Errorf("extension config path is empty")
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return RoleCatalog{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return RoleCatalog{}, err
	}
	cfg, err := decodeConfig(data, abs)
	if err != nil {
		return RoleCatalog{}, err
	}
	return catalogFromConfig(cfg), nil
}

func decodeConfig(data []byte, configPath string) (Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("read extension config %s: %w", configPath, err)
	}
	if cfg.DefaultRole == "" {
		return Config{}, fmt.Errorf("extension config %s has no defaultRole", configPath)
	}
	if _, ok := cfg.Roles[cfg.DefaultRole]; !ok {
		return Config{}, fmt.Errorf("extension config %s defaultRole %q is not defined", configPath, cfg.DefaultRole)
	}
	for name, role := range cfg.Roles {
		if role.Revision == "" {
			role.Revision = "1"
		}
		if role.Tools == nil {
			role.Tools = []string{}
		}
		cfg.Roles[name] = role
	}
	if cfg.ToolPaths == nil {
		cfg.ToolPaths = []string{}
	}
	if cfg.InstructionComponents == nil {
		cfg.InstructionComponents = []json.RawMessage{}
	}
	if cfg.Rejections == nil {
		cfg.Rejections = []json.RawMessage{}
	}
	for i := range cfg.Tools {
		tool := &cfg.Tools[i]
		if tool.Revision == "" {
			tool.Revision = "1"
		}
		if len(tool.InputSchema) == 0 || string(tool.InputSchema) == "null" {
			tool.InputSchema = json.RawMessage(`{"type":"object"}`)
		}
		if tool.Module == "" && len(cfg.ToolPaths) == 1 {
			tool.Module = cfg.ToolPaths[0]
		}
		tool.Module = cleanRelativePath(tool.Module)
		if tool.Export == "" {
			tool.Export = tool.Name
		}
	}
	return cfg, nil
}

func catalogFromConfig(cfg Config) RoleCatalog {
	names := make([]string, 0, len(cfg.Roles))
	for name := range cfg.Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	roles := make([]RoleInfo, 0, len(names))
	for _, name := range names {
		role := cfg.Roles[name]
		tools := append([]string(nil), role.Tools...)
		if tools == nil {
			tools = []string{}
		}
		roles = append(roles, RoleInfo{Name: name, Description: role.Description, Tools: tools})
	}
	return RoleCatalog{DefaultRole: cfg.DefaultRole, Roles: roles}
}

func resolveTools(cfg Config, roleName string, role Role) ([]Tool, error) {
	external := make(map[string][]Tool, len(cfg.Tools))
	for _, tool := range cfg.Tools {
		external[tool.Name] = append(external[tool.Name], tool)
	}
	selected := make([]Tool, 0, len(role.Tools))
	seen := make(map[string]struct{}, len(role.Tools))
	for _, name := range role.Tools {
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("extension role %q selects tool %q more than once", roleName, name)
		}
		seen[name] = struct{}{}
		if name == "run_js" {
			if len(external[name]) != 0 {
				return nil, fmt.Errorf("extension role %q selects built-in tool %q, which is also defined externally", roleName, name)
			}
			definition := RunJSDefinition(containsTool(role.Tools, "run_agent"))
			selected = append(selected, Tool{Name: definition.Name, Revision: buildinfo.Version, Description: definition.Description,
				InputSchema: append(json.RawMessage(nil), definition.InputSchema...)})
			continue
		}
		matches := external[name]
		if len(matches) == 0 {
			return nil, fmt.Errorf("extension role %q selects unknown tool %q", roleName, name)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("extension role %q selects ambiguous tool %q", roleName, name)
		}
		selected = append(selected, matches[0])
	}
	return selected, nil
}

func assembleInstruction(configInstruction string, role Role, roleName string, tools []Tool, catalog []RoleInfo) string {
	var sections []string
	sections = append(sections, "You are a generic agent executing role \""+roleName+"\". Use the registered tool definitions to perform the requested work and report the result.")
	if role.Description != "" {
		sections = append(sections, "Role description: "+role.Description)
	}
	if containsToolFromTools(tools, "run_js") {
		sections = append(sections, "Use run_js to compose the selected tools with JavaScript. Its code input is an async JavaScript function body; in that body, selected tools are available as tools.NAME(arguments). Await calls and return useful results.")
	}
	if containsToolFromTools(tools, "run_agent") {
		sections = append(sections, "Use run_agent to delegate an independent task; it accepts a task and optional role and returns the recorded child result. Pass necessary context in the task; child conversations are independent.")
		if containsToolFromTools(tools, "run_js") {
			sections = append(sections, "From generated JavaScript, api.agents.run({task, role?}) invokes the selected run_agent tool through its normal tool and hook path.")
		}
	}
	if role.Instruction != "" {
		sections = append(sections, "Role instruction:\n"+role.Instruction)
	}
	if configInstruction != "" {
		sections = append(sections, "Additional system instruction:\n"+configInstruction)
	}
	if containsToolFromTools(tools, "run_agent") {
		var lines []string
		for _, info := range catalog {
			lines = append(lines, "- "+info.Name+": "+info.Description)
		}
		sections = append(sections, "Available roles for run_agent:\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

func containsTool(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func containsToolFromTools(tools []Tool, target string) bool {
	for _, tool := range tools {
		if tool.Name == target {
			return true
		}
	}
	return false
}

func (s Snapshot) JSON() ([]byte, error) {
	tools := make([]LegacyTool, 0, len(s.Tools))
	for _, tool := range s.Tools {
		source, entry := "external", tool.Module
		if tool.Name == "run_js" && tool.Module == "" {
			source, entry = "bundled", ""
		}
		tools = append(tools, LegacyTool{
			Name: tool.Name, Revision: tool.Revision, Source: source, Entry: entry,
			Contract:   LegacyContract{Name: tool.Name, Description: tool.Description, InputSchema: append(json.RawMessage(nil), tool.InputSchema...)},
			FileAccess: "unmanaged", Paths: LegacyPaths{Allow: []string{}, Deny: []string{}},
		})
	}
	toolPathSource := ""
	if len(s.ToolPaths) > 0 {
		toolPathSource = s.ToolPaths[0]
	}
	return json.Marshal(configurationSnapshot{
		SchemaVersion: 1, ConfigurationID: s.ConfigurationID, Agent: s.Agent, Source: s.Source,
		SystemInstruction: s.SystemInstruction, InstructionComponents: s.InstructionComponents,
		ToolPaths: LegacyToolPaths{Source: toolPathSource, Deny: []string{}}, Tools: tools,
		Hooks: s.Hooks, Rejections: s.Rejections,
	})
}

func (s Snapshot) MarshalJSON() ([]byte, error) { return s.JSON() }

func (s Snapshot) Definitions() []contracts.ToolDefinition {
	definitions := make([]contracts.ToolDefinition, 0, len(s.Tools))
	for _, tool := range s.Tools {
		definitions = append(definitions, contracts.ToolDefinition{
			Name: tool.Name, Description: tool.Description,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
		})
	}
	return definitions
}

func (s Snapshot) HasTool(name string) bool {
	for _, tool := range s.Tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func (s Snapshot) HookModule() string { return s.HookPath }

func (s Snapshot) FrozenResources() map[string][]byte {
	files := make(map[string][]byte, len(s.Resources))
	for _, resource := range s.Resources {
		files[cleanRelativePath(resource.Path)] = append([]byte(nil), resource.Bytes...)
	}
	return files
}

// InstallDefaults installs editable external files and leaves existing files as-is.
func InstallDefaults(dir string) error {
	if dir == "" {
		return fmt.Errorf("extension install directory is empty")
	}
	return fs.WalkDir(defaultAssets, "assets", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := defaultAssets.ReadFile(path)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "assets/")
		destination := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err = os.Stat(destination); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if os.IsExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		_, writeErr := file.Write(contents)
		closeErr := file.Close()
		return errors.Join(writeErr, closeErr)
	})
}

func Digest(contents []byte) string {
	h := sha256.Sum256(contents)
	return "sha256:" + hex.EncodeToString(h[:])
}

func legacyHooksFromConfig(value json.RawMessage) ([]LegacyHook, string, error) {
	if len(value) == 0 || string(value) == "null" || string(value) == `""` {
		return []LegacyHook{}, "", nil
	}
	var pathValue string
	if err := json.Unmarshal(value, &pathValue); err == nil {
		return []LegacyHook{{Name: "extension-hooks", Path: cleanRelativePath(pathValue), Contract: "hjh-hook/v1", Handlers: []string{"beforeTool", "afterTool"}}}, cleanRelativePath(pathValue), nil
	}
	var cfg struct {
		Name     string   `json:"name"`
		Module   string   `json:"module"`
		Path     string   `json:"path"`
		Contract string   `json:"contract"`
		Handlers []string `json:"handlers"`
	}
	if err := json.Unmarshal(value, &cfg); err != nil {
		return nil, "", fmt.Errorf("decode extension hooks: %w", err)
	}
	module := cfg.Module
	if module == "" {
		module = cfg.Path
	}
	if module == "" {
		return []LegacyHook{}, "", nil
	}
	if cfg.Name == "" {
		cfg.Name = "extension-hooks"
	}
	if cfg.Contract == "" {
		cfg.Contract = "hjh-hook/v1"
	}
	if cfg.Handlers == nil {
		cfg.Handlers = []string{"beforeTool", "afterTool"}
	}
	module = cleanRelativePath(module)
	return []LegacyHook{{Name: cfg.Name, Path: module, Contract: cfg.Contract, Handlers: cfg.Handlers}}, module, nil
}

func cleanRelativePath(value string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
}
