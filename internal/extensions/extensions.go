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
	Name             string          `json:"name"`
	Revision         string          `json:"revision"`
	Description      string          `json:"description"`
	InputSchema      json.RawMessage `json:"inputSchema"`
	PromptGuidelines []string        `json:"promptGuidelines,omitempty"`
	Module           string          `json:"module"`
	Export           string          `json:"export"`
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
	Name             string         `json:"name"`
	Revision         string         `json:"revision"`
	Source           string         `json:"source"`
	Entry            string         `json:"entry,omitempty"`
	Contract         LegacyContract `json:"contract"`
	PromptGuidelines []string       `json:"promptGuidelines,omitempty"`
	FileAccess       string         `json:"fileAccess"`
	Paths            LegacyPaths    `json:"paths"`
}

type LegacyHook struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Contract string   `json:"contract"`
	Handlers []string `json:"handlers"`
}

type configurationSnapshot struct {
	SchemaVersion         int                       `json:"schemaVersion"`
	ConfigurationID       string                    `json:"configurationId"`
	Agent                 LegacyAgent               `json:"agent"`
	Source                LegacySource              `json:"source"`
	BaseInstruction       contracts.BaseInstruction `json:"baseInstruction"`
	SystemInstruction     string                    `json:"systemInstruction"`
	InstructionComponents []json.RawMessage         `json:"instructionComponents"`
	ToolPaths             LegacyToolPaths           `json:"toolPaths"`
	Tools                 []LegacyTool              `json:"tools"`
	Hooks                 []LegacyHook              `json:"hooks"`
	Rejections            []json.RawMessage         `json:"rejections"`
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
	BaseInstruction       contracts.BaseInstruction
	BaseInstructionSource *Resource
	InstructionComponents []json.RawMessage
	ToolPaths             []string
	Tools                 []Tool
	Hooks                 []LegacyHook
	HookPath              string
	Rejections            []json.RawMessage
	Resources             []Resource
	Root                  string
}

// CaptureOptions applies runtime-only choices while capturing an extension
// composition. A nil Tools pointer keeps the selected role's configured list;
// a non-nil pointer replaces it, including when the list is empty.
type CaptureOptions struct {
	Role  string
	Tools *[]string
}

var runJSInputSchema = json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}},"required":["code"]}`)

const runJSPurposeInstruction = "Begin the code with a short one-line // comment in English stating its purpose. The tool activity row displays that comment."
const runJSRuntimeInstruction = "The async JavaScript body supports Promise and the selected tools, but QuickJS provides no setTimeout, setInterval, or browser timer APIs."

// RunJSDefinition returns the model-facing built-in for generated JavaScript.
// Delegation is described only when run_agent is part of the selected role.
func RunJSDefinition(delegation bool) contracts.ToolDefinition {
	var selected []string
	if delegation {
		selected = append(selected, "run_agent")
	}
	return RunJSDefinitionForTools(selected)
}

// RunJSDefinitionForTools describes only the child operations available to
// generated JavaScript through the selected external tool composition.
func RunJSDefinitionForTools(selected []string) contracts.ToolDefinition {
	return runJSToolForTools(selected).definition()
}

func runJSToolForTools(selected []string) Tool {
	description := "Run generated JavaScript that can call selected registered tools through tools.NAME(arguments). Return a JSON result from the async code body."
	guidelines := []string{
		"Use run_js to compose selected tools with JavaScript. Its code input is an async JavaScript function body; selected tools are available as tools.NAME(arguments). Await calls and return useful results.",
		runJSPurposeInstruction,
		runJSRuntimeInstruction,
	}
	if containsTool(selected, "run_agent") {
		guidelines = append(guidelines, "From generated JavaScript, call api.agents.run({task, role?, model?, tools?}) to use the selected run_agent tool. Await results, or use Promise.all for independent delegations.")
	}
	if containsTool(selected, "spawn_agent") {
		guidelines = append(guidelines, "From generated JavaScript, call api.agents.spawn({task, role?, model?, tools?}) to use the selected spawn_agent tool.")
	}
	if containsTool(selected, "agent_status") {
		guidelines = append(guidelines, "From generated JavaScript, call api.agents.status({executionId, after?}) to use the selected agent_status tool. Return the reports you need so they enter your next model input.")
	}
	if containsTool(selected, "steer_agent") {
		guidelines = append(guidelines, "From generated JavaScript, call api.agents.steer({executionId, text}) to use the selected steer_agent tool.")
	}
	if containsTool(selected, "collect_agent") {
		guidelines = append(guidelines, "From generated JavaScript, await api.agents.collect({executionId}) to use the selected collect_agent tool.")
	}
	if containsTool(selected, "cancel_agent") {
		guidelines = append(guidelines, "From generated JavaScript, call api.agents.cancel({executionId}) to use the selected cancel_agent tool.")
	}
	if containsTool(selected, "command") {
		guidelines = append(guidelines, "For an explicit delay from generated JavaScript, await tools.command({program:\"sleep\",args:[\"SECONDS\"]}).")
	}
	return Tool{
		Name:             "run_js",
		Revision:         buildinfo.Version,
		Description:      description,
		InputSchema:      append(json.RawMessage(nil), runJSInputSchema...),
		PromptGuidelines: guidelines,
	}
}

func (tool Tool) definition() contracts.ToolDefinition {
	return contracts.ToolDefinition{Name: tool.Name, Description: tool.Description,
		InputSchema: append(json.RawMessage(nil), tool.InputSchema...)}
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
	return capture(filepath.Dir(abs), abs, selected, nil)
}

// CaptureWithOptions captures one role and optionally replaces its selected
// tools for this execution without changing the source configuration bytes.
func CaptureWithOptions(configPath string, options CaptureOptions) (Snapshot, error) {
	if configPath == "" {
		return Snapshot{}, fmt.Errorf("extension config path is empty")
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return Snapshot{}, err
	}
	return capture(filepath.Dir(abs), abs, options.Role, options.Tools)
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
	return capture(abs, filepath.Join(abs, "config.json"), selected, nil)
}

func capture(root, configPath, selectedRole string, toolOverride *[]string) (Snapshot, error) {
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

	roleName, err := selectedRoleName(cfg, configPath, selectedRole)
	if err != nil {
		return Snapshot{}, err
	}
	role, ok := cfg.Roles[roleName]
	if !ok { // selectedRoleName validates this before resource resolution.
		return Snapshot{}, fmt.Errorf("extension role %q is not defined", roleName)
	}
	if toolOverride != nil {
		role.Tools = append([]string{}, (*toolOverride)...)
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
	if containsTool(toolNames, "run_agent") || containsTool(toolNames, "spawn_agent") {
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
		InstructionComponents: append([]json.RawMessage{}, cfg.InstructionComponents...),
		ToolPaths:             cfg.ToolPaths,
		Tools:                 selectedTools,
		Hooks:                 legacyHooks,
		HookPath:              hookPath,
		Rejections:            cfg.Rejections,
		Resources:             resources,
		Root:                  root,
	}
	activeGuidelines := toolGuidelinesSection(selectedTools)
	base, err := selectBaseInstruction(cfg, configPath, roleName)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.SystemInstruction = assembleInstruction(base.text, cfg.SystemInstruction, role, selectedTools, activeGuidelines, catalog.Roles)
	snapshot.BaseInstruction, snapshot.BaseInstructionSource = base.base, base.source
	snapshot.InstructionComponents = append(snapshot.InstructionComponents,
		contracts.JSON(map[string]any{"identity": baseInstructionIdentity, "text": base.text, "source": base.base}))
	if activeGuidelines != "" {
		snapshot.InstructionComponents = append(snapshot.InstructionComponents,
			contracts.JSON(map[string]string{"identity": "instruction:active-tool-guidelines", "text": activeGuidelines}))
	}
	return snapshot, nil
}

// InstructionProfile resolves the metadata needed for context discovery without
// evaluating modules, installing extensions or resolving model credentials.
type InstructionProfile struct {
	Role     string
	ReadFile bool
	Base     contracts.BaseInstruction
}

func ResolveInstructionProfile(data []byte, configPath, selectedRole string) (InstructionProfile, error) {
	cfg, err := decodeConfig(data, configPath)
	if err != nil {
		return InstructionProfile{}, err
	}
	name, err := selectedRoleName(cfg, configPath, selectedRole)
	if err != nil {
		return InstructionProfile{}, err
	}
	tools, err := resolveTools(cfg, name, cfg.Roles[name])
	if err != nil {
		return InstructionProfile{}, err
	}
	base, err := selectBaseInstruction(cfg, configPath, name)
	if err != nil {
		return InstructionProfile{}, err
	}
	return InstructionProfile{Role: name, ReadFile: containsToolFromTools(tools, "read_file"),
		Base: base.base}, nil
}

// EmbeddedConfig returns the built-in configuration bytes without installing
// editable files into a workspace. Metadata-only previews use this when a new
// workspace has not been initialized by a run yet.
func EmbeddedConfig() ([]byte, error) {
	return defaultAssets.ReadFile("assets/config.json")
}

// ResolveRole selects and validates a role from configuration metadata only.
// It does not read or capture tool and hook resources.
func ResolveRole(data []byte, configPath, selectedRole string) (string, error) {
	cfg, err := decodeConfig(data, configPath)
	if err != nil {
		return "", err
	}
	return selectedRoleName(cfg, configPath, selectedRole)
}

func selectedRoleName(cfg Config, configPath, selectedRole string) (string, error) {
	roleName := selectedRole
	if roleName == "" {
		roleName = cfg.DefaultRole
	}
	if _, ok := cfg.Roles[roleName]; !ok {
		return "", fmt.Errorf("extension config %s role %q is not defined", configPath, roleName)
	}
	return roleName, nil
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
			selected = append(selected, runJSToolForTools(role.Tools))
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

func toolGuidelinesSection(tools []Tool) string {
	var guidelines []string
	for _, tool := range tools {
		for _, guideline := range tool.PromptGuidelines {
			guidelines = append(guidelines, "- "+tool.Name+": "+guideline)
		}
	}
	if len(guidelines) == 0 {
		return ""
	}
	return "## Active tool guidelines\n" + strings.Join(guidelines, "\n")
}

func assembleInstruction(baseInstruction, configInstruction string, role Role, tools []Tool, activeGuidelines string, catalog []RoleInfo) string {
	var sections []string
	if baseInstruction != "" {
		sections = append(sections, baseInstruction)
	}
	if activeGuidelines != "" {
		sections = append(sections, activeGuidelines)
	}
	if role.Description != "" {
		sections = append(sections, "Role description: "+role.Description)
	}
	if role.Instruction != "" {
		sections = append(sections, "Role instruction:\n"+role.Instruction)
	}
	if configInstruction != "" {
		sections = append(sections, "Additional system instruction:\n"+configInstruction)
	}
	if containsToolFromTools(tools, "run_agent") || containsToolFromTools(tools, "spawn_agent") {
		var lines []string
		for _, info := range catalog {
			lines = append(lines, "- "+info.Name+": "+info.Description+" (tools: ["+strings.Join(info.Tools, ", ")+"])")
		}
		catalogName := "run_agent"
		if !containsToolFromTools(tools, "run_agent") {
			catalogName = "spawn_agent"
		}
		sections = append(sections, "Available roles for "+catalogName+":\n"+strings.Join(lines, "\n"))
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
			Contract:         LegacyContract{Name: tool.Name, Description: tool.Description, InputSchema: append(json.RawMessage(nil), tool.InputSchema...)},
			PromptGuidelines: append([]string(nil), tool.PromptGuidelines...),
			FileAccess:       "unmanaged", Paths: LegacyPaths{Allow: []string{}, Deny: []string{}},
		})
	}
	toolPathSource := ""
	if len(s.ToolPaths) > 0 {
		toolPathSource = s.ToolPaths[0]
	}
	return json.Marshal(configurationSnapshot{
		SchemaVersion: 1, ConfigurationID: s.ConfigurationID, Agent: s.Agent, Source: s.Source,
		BaseInstruction:   s.BaseInstruction,
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
		return []LegacyHook{{Name: "extension-hooks", Path: cleanRelativePath(pathValue), Contract: "hjh-hook/v1", Handlers: defaultHookHandlers()}}, cleanRelativePath(pathValue), nil
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
		cfg.Handlers = defaultHookHandlers()
	}
	module = cleanRelativePath(module)
	return []LegacyHook{{Name: cfg.Name, Path: module, Contract: cfg.Contract, Handlers: cfg.Handlers}}, module, nil
}

func defaultHookHandlers() []string {
	return []string{"runtimeStart", "beforeTurn", "beforeTool", "afterTool", "afterTurn", "runtimeStop"}
}

func cleanRelativePath(value string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
}
