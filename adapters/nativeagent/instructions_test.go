package nativeagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
)

func TestSchemaActionTransportInstructionIsInPinnedModelLayer(t *testing.T) {
	prompt, err := assembleInstructions(application.ExecutionBrief{}, Profile{ResponseMode: ResponseModeJSONSchemaActions}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	modelStart, universalStart := strings.Index(prompt, "[MODEL |"), strings.Index(prompt, "[TEKROO |")
	instruction := strings.Index(prompt, agentruntime.StructuredTurnInstruction)
	if instruction < modelStart || instruction >= universalStart {
		t.Fatalf("transport instruction not pinned in model layer: %s", prompt)
	}
	legacy, err := assembleInstructions(application.ExecutionBrief{}, Profile{}, t.TempDir())
	if err != nil || strings.Contains(legacy, agentruntime.StructuredTurnInstruction) {
		t.Fatalf("opt-in instruction leaked into legacy mode: %v", err)
	}
}

func TestFiveInstructionLayersAreOrderedAndStructuredJSONRuleIsPinned(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Project rule: run focused tests.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	brief := application.ExecutionBrief{RoleGrounding: application.RoleExecutionGrounding{
		Instructions: "Role rule: implement only the task.", TeamInstructions: "Team rule: keep the DAG acyclic.",
	}}
	prompt, err := assembleInstructions(brief, Profile{ModelInstructions: "Model rule: use native tool calls."}, root)
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, layer := range []string{"[ROLE |", "[PROJECT |", "[TEAM |", "[MODEL |", "[TEKROO |"} {
		index := strings.Index(prompt, layer)
		if index <= previous {
			t.Fatalf("layer %q out of order in %q", layer, prompt)
		}
		previous = index
	}
	for _, instruction := range []string{"Project rule: run focused tests.", "Team rule: keep the DAG acyclic.", "Model rule: use native tool calls.", "Never put JSON-encoded text inside a string"} {
		if !strings.Contains(prompt, instruction) {
			t.Fatalf("missing instruction %q", instruction)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Revised project rule.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	revised, err := assembleInstructions(brief, Profile{ModelInstructions: "Model rule: use native tool calls."}, root)
	if err != nil || revised == prompt {
		t.Fatalf("project instruction change did not change pinned prompt: %v", err)
	}
}

func TestProjectInstructionReadRejectsOversizedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(strings.Repeat("x", maxProjectInstructionsBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := projectInstructions(root); err == nil {
		t.Fatal("oversized project instructions admitted")
	}
}
