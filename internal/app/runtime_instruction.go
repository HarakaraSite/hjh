package app

import (
	"fmt"
	"strings"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
)

const executionContextInstructionIdentity = "instruction:execution-context"

// addExecutionContextInstruction gives the model the effective selection and
// workspace already resolved for this execution, and archives the same text
// that is sent in the model request.
func addExecutionContextInstruction(snapshot *extensions.Snapshot, model contracts.ModelSelection, workspace string) {
	text := fmt.Sprintf("Current execution (resolved values):\n- Provider: %q\n- API: %q\n- Model: %q\n- Effort: %q\n- Role: %q\n- Workspace root: %q",
		model.Provider, model.API, model.ModelID, model.Effort, snapshot.Agent.Name, workspace)
	snapshot.SystemInstruction = strings.TrimRight(snapshot.SystemInstruction, "\n") + "\n\n" + text
	snapshot.InstructionComponents = append(snapshot.InstructionComponents,
		contracts.JSON(map[string]string{"identity": executionContextInstructionIdentity, "text": text}))
}
