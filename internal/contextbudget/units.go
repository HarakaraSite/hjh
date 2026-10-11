package contextbudget

import (
	"encoding/json"

	"hjh/internal/contracts"
)

type projectionUnit struct {
	kind     string
	indices  []int
	complete bool
}

func (u projectionUnit) touchesAny(indices map[int]struct{}) bool {
	for _, index := range u.indices {
		if _, found := indices[index]; found {
			return true
		}
	}
	return false
}

// projectionUnits groups an assistant call and its matching following tool
// results as one unit. A result outside a complete exchange stays its own
// protected orphan unit.
func projectionUnits(entries []Entry) []projectionUnit {
	units := make([]projectionUnit, 0, len(entries))
	for index := 0; index < len(entries); {
		message := entries[index].Message
		if message.Role != "assistant" {
			if message.Role == "tool" {
				units = append(units, projectionUnit{kind: "orphan_tool", indices: []int{index}})
			}
			index++
			continue
		}

		callIDs := assistantCallIDs(message)
		if len(callIDs) == 0 {
			units = append(units, projectionUnit{
				kind: "assistant_response", indices: []int{index}, complete: true,
			})
			index++
			continue
		}

		expected := make(map[string]struct{}, len(callIDs))
		for _, callID := range callIDs {
			expected[callID] = struct{}{}
		}
		results := make(map[string]struct{}, len(expected))
		indices := []int{index}
		next := index + 1
		for next < len(entries) && len(results) < len(expected) {
			candidate := entries[next].Message
			if candidate.Role != "tool" {
				break
			}
			matching := false
			for _, callID := range toolResultCallIDs(candidate) {
				if _, expectedCall := expected[callID]; expectedCall {
					matching = true
					results[callID] = struct{}{}
				}
			}
			if !matching {
				break
			}
			indices = append(indices, next)
			next++
		}
		units = append(units, projectionUnit{
			kind: "tool_exchange", indices: indices, complete: len(results) == len(expected),
		})
		index = next
	}
	return units
}

func protectedCurrentIndices(entries []Entry, units []projectionUnit) map[int]struct{} {
	protected := make(map[int]struct{})
	protectTaskAndIncomplete(entries, units, protected)
	for index := len(units) - 1; index >= 0; index-- {
		unit := units[index]
		if unit.kind == "assistant_response" && unit.complete {
			protectUnit(protected, unit)
			break
		}
	}
	for index := len(units) - 1; index >= 0; index-- {
		unit := units[index]
		if unit.kind == "tool_exchange" && unit.complete {
			protectUnit(protected, unit)
			break
		}
	}
	return protected
}

func protectedHistoryIndices(entries []Entry, units []projectionUnit) map[int]struct{} {
	return protectedCurrentIndices(entries, units)
}

func protectTaskAndIncomplete(entries []Entry, units []projectionUnit, protected map[int]struct{}) {
	for index, entry := range entries {
		if entry.Message.Role == "user" || (entry.Message.Steering != nil && *entry.Message.Steering) {
			protected[index] = struct{}{}
		}
	}
	for _, unit := range units {
		if !unit.complete {
			protectUnit(protected, unit)
		}
	}
}

func protectUnit(protected map[int]struct{}, unit projectionUnit) {
	for _, index := range unit.indices {
		protected[index] = struct{}{}
	}
}

func oldestRemovableUnits(units []projectionUnit, protected map[int]struct{}) []projectionUnit {
	removable := make([]projectionUnit, 0, len(units))
	for _, unit := range units {
		if !unit.complete || unit.kind == "orphan_tool" {
			continue
		}
		if unit.touchesAny(protected) {
			continue
		}
		removable = append(removable, unit)
	}
	return removable
}

func assistantCallIDs(message contracts.Message) []string {
	var calls []contracts.ToolCall
	if json.Unmarshal(message.Content, &calls) != nil || len(calls) == 0 {
		return nil
	}
	callIDs := make([]string, 0, len(calls))
	for _, call := range calls {
		callIDs = append(callIDs, call.CallID)
	}
	return callIDs
}

func toolResultCallIDs(message contracts.Message) []string {
	var results []contracts.ToolResult
	if json.Unmarshal(message.Content, &results) != nil {
		return nil
	}
	callIDs := make([]string, 0, len(results))
	for _, result := range results {
		callIDs = append(callIDs, result.CallID)
	}
	return callIDs
}
