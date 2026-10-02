package openhands

import (
	"encoding/json"
	"testing"
)

func TestTeamsMCPActionsRetainRepositoryGrounding(t *testing.T) {
	for _, test := range []struct {
		name        string
		data        string
		wantPath    string
		contentRead bool
		orientation bool
	}{
		{"read_file", `{"path":"AGENTS.md"}`, "AGENTS.md", true, false},
		{"list_files", `{"path":"src"}`, "src", false, true},
		{"git_status", `{}`, "", false, true},
		{"git_diff", `{"path":"src/feature.go"}`, "src/feature.go", true, false},
		{"teams_read_file", `{"path":"AGENTS.md"}`, "AGENTS.md", true, false},
		{"teams_list_files", `{"path":"src"}`, "src", false, true},
		{"teams_git_status", `{}`, "", false, true},
		{"teams_git_diff", `{"path":"src/feature.go"}`, "src/feature.go", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{
				"id": "event-1", "kind": "ActionEvent", "source": "agent", "tool_name": test.name,
				"action": json.RawMessage(`{"data":` + test.data + `}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			event, err := decodeEvent(raw)
			if err != nil || event.ActionPath != test.wantPath || !repositoryAction(event) || repositoryContentReadAction(event) != test.contentRead || workspaceOrientationAction(event) != test.orientation {
				t.Fatalf("MCP action grounding = %+v, %v", event, err)
			}
			if (test.name == "read_file" || test.name == "teams_read_file") && !agentsInstructionReadAction(event) {
				t.Fatal("AGENTS.md read was not recognized")
			}
		})
	}
}
