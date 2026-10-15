package extensions

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestHelpInputSchemaPreservesNormalRequiredAndConstraints(t *testing.T) {
	original := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	before := string(original)
	t.Logf("effective schema: %s", HelpInputSchema(original))
	var schema struct {
		Type                 string                     `json:"type"`
		Properties           map[string]json.RawMessage `json:"properties"`
		Required             []string                   `json:"required"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
		AnyOf                []struct {
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
		} `json:"anyOf"`
	}
	if err := json.Unmarshal(HelpInputSchema(original), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" || len(schema.Required) != 0 || len(schema.AnyOf) != 2 {
		t.Fatalf("help schema is not a root object with separate branches: %+v", schema)
	}
	if string(schema.Properties["path"]) != `{"type":"string"}` || !strings.Contains(string(schema.Properties["help"]), `"type":"boolean"`) {
		t.Fatalf("root does not describe normal args and help: %+v", schema.Properties)
	}
	if !reflect.DeepEqual(schema.AnyOf[0].Required, []string{"help"}) || !strings.Contains(string(schema.AnyOf[0].Properties["help"]), `"enum":[true]`) {
		t.Fatalf("help-only branch does not require help:true: %+v", schema.AnyOf[0])
	}
	normal := schema.AnyOf[1]
	if !reflect.DeepEqual(normal.Required, []string{"path"}) || schema.AdditionalProperties == nil || *schema.AdditionalProperties ||
		len(normal.Properties) != 1 || !strings.Contains(string(normal.Properties["help"]), `"enum":[false]`) {
		t.Fatalf("normal branch lost the original contract or accepts help:true: %+v", normal)
	}
	if string(original) != before {
		t.Fatal("help schema generation mutated the declared input schema")
	}
}

func TestHelpSchemaSharedByDefinitionsAndLegacyContract(t *testing.T) {
	tool := Tool{Name: "custom", InputSchema: json.RawMessage(`{"type":"object","required":["path"]}`)}
	snapshot := Snapshot{Tools: []Tool{tool}}
	definition := tool.definition()
	if string(definition.InputSchema) != string(HelpInputSchema(tool.InputSchema)) || !reflect.DeepEqual(snapshot.Definitions(), []contracts.ToolDefinition{definition}) {
		t.Fatal("tool and snapshot model definitions use different schema generation")
	}
	data, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var saved configurationSnapshot
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if string(saved.Tools[0].Contract.InputSchema) != string(definition.InputSchema) {
		t.Fatal("legacy snapshot contract differs from model definition")
	}
	if string(snapshot.Tools[0].InputSchema) != string(tool.InputSchema) {
		t.Fatal("snapshot no longer retains the declared schema")
	}
}

func TestToolHelpUsesCapturedResourceAndDistinguishesMissingReference(t *testing.T) {
	snapshot := Snapshot{Resources: []Resource{{Path: "custom/help.md", Bytes: []byte("# Captured help\n")}}}
	tool := Tool{Name: "custom", HelpFile: "./custom/help.md"}
	help, err := snapshot.ToolHelp(tool)
	if err != nil || !help.Available || help.Name != "custom" || help.Text != "# Captured help\n" {
		t.Fatalf("captured help = %+v, %v", help, err)
	}
	tool.HelpFile = ""
	help, err = snapshot.ToolHelp(tool)
	if err != nil || help.Available || help.Name != "custom" || help.Text == "" {
		t.Fatalf("unregistered help = %+v, %v", help, err)
	}
	tool.HelpFile = "missing.md"
	if _, err = snapshot.ToolHelp(tool); err == nil {
		t.Fatal("configured but missing resource was treated as unregistered help")
	}
}
