package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

func requireExecutionContextInstruction(t *testing.T, instruction string, model contracts.ModelSelection, role, workspace string) {
	t.Helper()
	for _, expected := range []string{
		"- Provider: " + quoted(model.Provider),
		"- API: " + quoted(model.API),
		"- Model: " + quoted(model.ModelID),
		"- Effort: " + quoted(model.Effort),
		"- Role: " + quoted(role),
		"- Workspace root: " + quoted(workspace),
	} {
		if !strings.Contains(instruction, expected) {
			t.Errorf("execution instruction omits %q: %s", expected, instruction)
		}
	}
	if strings.Contains(instruction, "authProfile") || strings.Contains(instruction, "Authorization") {
		t.Errorf("execution instruction includes authentication metadata: %s", instruction)
	}
}

func requireSavedExecutionContext(t *testing.T, store *history.Store, configurationID, instruction string, model contracts.ModelSelection, role, workspace string) {
	t.Helper()
	configuration, err := store.Configuration(t.Context(), configurationID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		SystemInstruction     string `json:"systemInstruction"`
		InstructionComponents []struct {
			Identity string `json:"identity"`
			Text     string `json:"text"`
		} `json:"instructionComponents"`
	}
	if err := json.Unmarshal(configuration.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	// The captured composition is the base; lifecycle context contributions
	// are archived separately and appended before provider request preparation.
	if !strings.HasPrefix(instruction, snapshot.SystemInstruction) {
		t.Fatalf("saved base instruction differs from model request: saved=%q request=%q", snapshot.SystemInstruction, instruction)
	}
	var found bool
	for _, component := range snapshot.InstructionComponents {
		if component.Identity == executionContextInstructionIdentity {
			found = true
			if !strings.HasSuffix(strings.TrimSpace(snapshot.SystemInstruction), component.Text) {
				t.Errorf("saved execution-context component is absent from the model instruction: %+v", component)
			}
			requireExecutionContextInstruction(t, component.Text, model, role, workspace)
		}
	}
	if !found {
		t.Fatal("saved configuration omits the execution-context instruction component")
	}
}

func quoted(value string) string { return strconv.Quote(value) }
