package toolactivity

import (
	"encoding/json"
	"testing"
)

func TestRunJSPreviewShowsLeadingPurposeComment(t *testing.T) {
	code := "\n  // Read the README and compare configuration files\r\nreturn await tools.read_file({path:'README.md'});"
	input, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	if preview := Preview("run_js", input); preview != "Read the README and compare configuration files" {
		t.Fatalf("run_js activity did not show its purpose: %q", preview)
	}
	// Ordinary code stays out of the compact activity row.
	input = json.RawMessage(`{"code":"const value = 42;\n// This is not the script's leading purpose\nreturn value;"}`)
	if preview := Preview("run_js", input); preview != "" {
		t.Fatalf("activity showed code or a later comment: %q", preview)
	}
}

func TestAgentControlPreviewsShowTaskOrExecutionID(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"spawn_agent", `{"role":"reviewer","task":"Inspect the source\nand report"}`, "reviewer · Inspect the source"},
		{"agent_status", `{"executionId":"child-1"}`, "child-1"},
		{"steer_agent", `{"executionId":"child-1","text":"Include the deadline\nand the owner"}`, "child-1 · Include the deadline"},
		{"collect_agent", `{"executionId":"child-2"}`, "child-2"},
		{"cancel_agent", `{"executionId":"child-3"}`, "child-3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Preview(tc.name, json.RawMessage(tc.input)); got != tc.want {
				t.Fatalf("Preview(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
