package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/provider/protocol"
)

var helpAdapters = []struct {
	name, api string
	namespace bool
}{
	{"chat-completions", "chat-completions", false},
	{"responses", "responses", false},
	{"responses-namespace", "responses", true},
}

func captureHelpDefaults(t *testing.T, role string) extensions.Snapshot {
	t.Helper()
	workspace := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.Capture(filepath.Join(dir, "config.json"), role)
	if err != nil {
		t.Fatal(err)
	}
	if err = extensions.ApplyToolUsagePolicy(&snapshot, workspace); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func helpRequest(snapshot extensions.Snapshot) contracts.ModelRequest {
	return contracts.ModelRequest{
		Model:       contracts.ModelSelection{ModelID: "local-help-test", Effort: "auto"},
		Instruction: snapshot.SystemInstruction,
		Messages:    []contracts.Message{contracts.User("Read the read_file help and report completion.")},
		Tools:       snapshot.Definitions(),
	}
}

func helpWireFunctions(t *testing.T, body []byte, api string, namespace bool) []map[string]json.RawMessage {
	t.Helper()
	var wire struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	functions := wire.Tools
	if namespace {
		if len(functions) != 1 || string(functions[0]["type"]) != `"namespace"` || string(functions[0]["name"]) != `"hjh"` {
			t.Fatalf("missing hjh namespace: %s", body)
		}
		if err := json.Unmarshal(functions[0]["tools"], &functions); err != nil {
			t.Fatal(err)
		}
	}
	if api == "chat-completions" {
		for i, tool := range functions {
			if string(tool["type"]) != `"function"` {
				t.Fatalf("not a function tool: %s", body)
			}
			if err := json.Unmarshal(tool["function"], &functions[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	return functions
}

// This checks the schema actually sent by each adapter, not a hand-written
// schema fixture. The help branch accepts {help:true} alone; the normal branch
// retains every declared constraint and only permits help to be absent/false.
func assertHelpWireSchema(t *testing.T, parameters, declared json.RawMessage) {
	t.Helper()
	var schema struct {
		Type       string                       `json:"type"`
		Properties map[string]json.RawMessage   `json:"properties"`
		Required   []string                     `json:"required"`
		AnyOf      []map[string]json.RawMessage `json:"anyOf"`
	}
	if err := json.Unmarshal(parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" || len(schema.Required) != 0 || len(schema.AnyOf) != 2 {
		t.Fatalf("help-only call cannot use a root object with conditional required: %s", parameters)
	}
	var required []string
	var helpProperties map[string]json.RawMessage
	if err := json.Unmarshal(schema.AnyOf[0]["required"], &required); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(schema.AnyOf[0]["properties"], &helpProperties); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(required, []string{"help"}) || len(helpProperties) != 1 {
		t.Fatalf("help branch requires normal arguments: %s", parameters)
	}
	assertHelpBoolean := func(raw json.RawMessage, want []bool) {
		var property struct {
			Type string `json:"type"`
			Enum []bool `json:"enum"`
		}
		if err := json.Unmarshal(raw, &property); err != nil {
			t.Fatal(err)
		}
		if property.Type != "boolean" || !reflect.DeepEqual(property.Enum, want) {
			t.Fatalf("help boolean = %s, want enum %v", raw, want)
		}
	}
	assertHelpBoolean(schema.Properties["help"], nil)
	assertHelpBoolean(helpProperties["help"], []bool{true})

	var original, normal, effective map[string]any
	if err := json.Unmarshal(declared, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contracts.JSON(schema.AnyOf[1]), &normal); err != nil {
		t.Fatal(err)
	}
	assertHelpBoolean(contracts.JSON(normal["properties"].(map[string]any)["help"]), []bool{false})
	// Constraints shared at the root also apply to the normal branch. Compare
	// their effective conjunction, rather than requiring duplicated properties.
	if err := json.Unmarshal(parameters, &effective); err != nil {
		t.Fatal(err)
	}
	delete(effective, "anyOf")
	for key, value := range normal {
		if key == "properties" {
			for name, property := range value.(map[string]any) {
				effective["properties"].(map[string]any)[name] = property
			}
		} else {
			effective[key] = value
		}
	}
	delete(effective["properties"].(map[string]any), "help")
	if !reflect.DeepEqual(effective, original) {
		t.Fatalf("normal schema lost original required or other constraints:\nnormal: %s\noriginal: %s", contracts.JSON(effective), declared)
	}
	for name, property := range original["properties"].(map[string]any) {
		var rootProperty any
		if err := json.Unmarshal(schema.Properties[name], &rootProperty); err != nil || !reflect.DeepEqual(rootProperty, property) {
			t.Fatalf("root no longer describes normal argument %s: %s", name, parameters)
		}
	}
}

func TestToolHelpDefaultSchemasThroughPrepare(t *testing.T) {
	snapshot := captureHelpDefaults(t, "default")
	configBytes, err := extensions.EmbeddedConfig()
	if err != nil {
		t.Fatal(err)
	}
	var config extensions.Config
	if err = json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	// The default role is the standard tool composition, including run_js.
	if len(snapshot.Tools) != len(config.Tools)+1 {
		t.Fatal("default role no longer covers all standard tools; update the capture selection")
	}
	for _, adapter := range helpAdapters {
		t.Run(adapter.name, func(t *testing.T) {
			provider := protocol.New(protocol.Config{API: adapter.api, Options: protocol.Options{NamespaceTools: adapter.namespace}})
			request := helpRequest(snapshot)
			body, err := provider.Prepare(request)
			if err != nil {
				t.Fatal(err)
			}
			functions := helpWireFunctions(t, body, adapter.api, adapter.namespace)
			if len(functions) != len(snapshot.Tools) {
				t.Fatalf("wire tool count = %d, selected = %d", len(functions), len(snapshot.Tools))
			}
			for i, tool := range snapshot.Tools {
				t.Run(tool.Name, func(t *testing.T) {
					function := functions[i]
					if string(function["name"]) != string(contracts.JSON(tool.Name)) || !bytes.Equal(function["parameters"], request.Tools[i].InputSchema) {
						t.Fatalf("adapter changed captured definition for %s", tool.Name)
					}
					if adapter.api == "responses" && string(function["strict"]) != "false" {
						t.Fatal("Responses may normalize conditional/optional arguments without strict:false")
					}
					assertHelpWireSchema(t, function["parameters"], tool.InputSchema)
				})
			}
			if !bytes.Contains(body, contracts.JSON(snapshot.SystemInstruction)) || !strings.Contains(snapshot.SystemInstruction, `{"help":true}`) {
				t.Fatal("initial wire lacks the captured instruction and common help guidance")
			}
		})
	}
}

func TestToolHelpLocalProtocolExchange(t *testing.T) {
	snapshot := captureHelpDefaults(t, "default")
	var readFile extensions.Tool
	for _, tool := range snapshot.Tools {
		if tool.Name == "read_file" {
			readFile = tool
		}
	}
	help, err := snapshot.ToolHelp(readFile)
	if err != nil || !help.Available || help.Text == "" {
		t.Fatalf("default read_file help = %+v, %v", help, err)
	}
	helpText := string(contracts.JSON(help))
	for _, adapter := range helpAdapters {
		t.Run(adapter.name, func(t *testing.T) {
			captured := make(chan []byte, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				captured <- body
				// Only complete after receiving the actual captured help result.
				final := bytes.Contains(body, contracts.JSON(helpText))
				w.Header().Set("Content-Type", "text/event-stream")
				if adapter.api == "chat-completions" {
					if final {
						fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Help received.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"help_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"help\\\":true}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
					}
					return
				}
				var output []any
				if final {
					output = []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "Help received."}}}}
				} else {
					call := map[string]any{"type": "function_call", "id": "fc_help", "call_id": "help_1", "name": "read_file", "arguments": `{"help":true}`}
					if adapter.namespace {
						call["namespace"] = "hjh"
					}
					output = []any{call}
				}
				event := contracts.JSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_help", "model": "local-help-test", "status": "completed", "output": output}})
				fmt.Fprintf(w, "event: response.completed\ndata: %s\n\n", event)
			}))
			defer server.Close()
			provider := protocol.New(protocol.Config{API: adapter.api, BaseURL: server.URL, Options: protocol.Options{NamespaceTools: adapter.namespace}})
			request := helpRequest(snapshot)
			generate := func() contracts.ModelResult {
				t.Helper()
				prepared, err := provider.Prepare(request)
				if err != nil {
					t.Fatal(err)
				}
				request.WireBody = prepared
				result, err := provider.Generate(context.Background(), request, nil)
				if err != nil {
					t.Fatal(err)
				}
				if got := <-captured; !bytes.Equal(got, prepared) {
					t.Fatal("HTTP request differs from exact Prepare bytes")
				}
				return result
			}
			result := generate()
			if len(result.Calls) != 1 || result.Calls[0].Name != "read_file" || result.Calls[0].CallID != "help_1" || string(result.Calls[0].Arguments) != `{"help":true}` {
				t.Fatalf("decoded help-only call = %+v", result.Calls)
			}
			firstBytes := len(request.WireBody)
			request.Messages = append(request.Messages,
				contracts.AssistantCalls(result.Calls, result.Text, result.ProviderState),
				contracts.ToolResults([]contracts.ToolResult{{CallID: result.Calls[0].CallID, Name: "read_file", Text: helpText, Outcome: "success"}}),
			)
			result = generate()
			if result.Text != "Help received." || len(result.Calls) != 0 {
				t.Fatalf("final result = %+v", result)
			}
			t.Logf("help exchange: first_wire_bytes=%d help_result_bytes=%d followup_wire_bytes=%d retained_exchange_bytes=%d additional_model_round_trips=1", firstBytes, len(helpText), len(request.WireBody), len(request.WireBody)-firstBytes)
		})
	}
}

// Keep measurements as observations, not byte-count gates: schema growth can
// outweigh shorter prose for smaller roles. All adapters use the same task.
func TestToolHelpWireMeasurements(t *testing.T) {
	for _, role := range []string{"default", "reviewer", "explorer", "implementer"} {
		t.Run(role, func(t *testing.T) {
			snapshot := captureHelpDefaults(t, role)
			var policyBytes, guidelineBytes, descriptionBytes, schemaBytes int
			for _, raw := range snapshot.InstructionComponents {
				var component struct{ Identity, Text string }
				if err := json.Unmarshal(raw, &component); err != nil {
					t.Fatal(err)
				}
				switch component.Identity {
				case "instruction:tool-usage-policy":
					policyBytes += len(component.Text)
				case "instruction:active-tool-guidelines":
					guidelineBytes += len(component.Text)
				}
			}
			request := helpRequest(snapshot)
			for _, tool := range request.Tools {
				descriptionBytes += len(tool.Description)
				schemaBytes += len(tool.InputSchema)
			}
			t.Logf("role=%s tools=%d policy_bytes=%d guidelines_bytes=%d description_bytes=%d effective_schema_bytes=%d instruction_bytes=%d", role, len(request.Tools), policyBytes, guidelineBytes, descriptionBytes, schemaBytes, len(request.Instruction))
			for _, adapter := range helpAdapters {
				provider := protocol.New(protocol.Config{API: adapter.api, Options: protocol.Options{NamespaceTools: adapter.namespace}})
				body, err := provider.Prepare(request)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("role=%s adapter=%s first_wire_bytes=%d", role, adapter.name, len(body))
			}
		})
	}
}
