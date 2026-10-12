package contracts

// BaseInstruction identifies the common instruction sources without their text.
type BaseInstruction struct {
	BuiltinVersion       string `json:"builtinVersion"`
	ConfigPath           string `json:"configPath"`
	HasSystemInstruction bool   `json:"hasSystemInstruction"`
}

// InstructionSource describes an AGENTS.md read used to assemble instructions.
type InstructionSource struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// SkillSummary contains discovery metadata, never the skill's instruction body.
type SkillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Directory   string `json:"directory"`
	Source      string `json:"source"`
}

type ContextDiagnostic struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Orientation is owned by the execution layer. Preview and actual reads are
// distinguished so clients never present a preview as a frozen run snapshot.
type Orientation struct {
	Mode             string              `json:"mode"`
	Workspace        string              `json:"workspace"`
	Role             string              `json:"role"`
	Base             BaseInstruction     `json:"base"`
	Instructions     []InstructionSource `json:"instructions"`
	Skills           []SkillSummary      `json:"skills"`
	DiscoveredSkills []SkillSummary      `json:"discoveredSkills"`
	Diagnostics      []ContextDiagnostic `json:"diagnostics"`
}

// Clone detaches the slices shared by run-state readers from their owner.
func (o Orientation) Clone() Orientation {
	o.Instructions = append([]InstructionSource{}, o.Instructions...)
	o.Skills = append([]SkillSummary{}, o.Skills...)
	o.DiscoveredSkills = append([]SkillSummary{}, o.DiscoveredSkills...)
	o.Diagnostics = append([]ContextDiagnostic{}, o.Diagnostics...)
	return o
}
