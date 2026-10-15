package extensions

import (
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

// HelpInputSchema adds the common help option to an object input schema without
// changing the declared schema or weakening its normal required arguments.
// Tool authors reserve help for this common option. The root remains an object;
// properties and additionalProperties are shared at the root; operation-only
// constraints such as required remain in the normal branch.
func HelpInputSchema(input json.RawMessage) json.RawMessage {
	var normal map[string]json.RawMessage
	_ = json.Unmarshal(input, &normal) // Tool input schemas are object schemas.
	if normal == nil {
		normal = map[string]json.RawMessage{}
	}
	var properties map[string]json.RawMessage
	_ = json.Unmarshal(normal["properties"], &properties)
	if properties == nil {
		properties = map[string]json.RawMessage{}
	}
	properties["help"] = contracts.JSON(map[string]any{
		"type": "boolean", "description": "Return tool help without executing the normal operation; no normal arguments are required when true.",
	})
	root := map[string]any{"type": "object", "properties": properties}
	// additionalProperties must see the shared property declarations. Local
	// definitions remain at the root so existing references keep their target.
	for _, key := range []string{"additionalProperties", "$defs", "definitions"} {
		if value, ok := normal[key]; ok {
			root[key] = value
			delete(normal, key)
		}
	}
	delete(normal, "type")
	normal["properties"] = contracts.JSON(map[string]any{"help": map[string]any{"type": "boolean", "enum": []bool{false}}})
	root["anyOf"] = []any{
		map[string]any{
			"properties": map[string]any{"help": map[string]any{"type": "boolean", "enum": []bool{true}}},
			"required":   []string{"help"},
		},
		normal,
	}
	return contracts.JSON(root)
}

// ToolHelp is the result of a common help call, not a tool implementation result.
type ToolHelp struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Text      string `json:"text"`
}

// ToolHelp resolves a selected declaration's help only from this capture. A
// missing declaration is different from a configured file missing in capture.
func (s Snapshot) ToolHelp(tool Tool) (ToolHelp, error) {
	result := ToolHelp{Name: tool.Name, Text: "No detailed help is registered for this tool."}
	if tool.HelpFile == "" {
		return result, nil
	}
	path := cleanRelativePath(tool.HelpFile)
	for _, resource := range s.Resources {
		if cleanRelativePath(resource.Path) == path {
			result.Available = true
			result.Text = string(resource.Bytes)
			return result, nil
		}
	}
	return ToolHelp{}, fmt.Errorf("captured help file for tool %q not found: %s", tool.Name, tool.HelpFile)
}
