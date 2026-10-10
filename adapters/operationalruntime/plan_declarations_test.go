package operationalruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
)

func TestPlanDeclarationPublicationGuardRejectsOmissionAndCollision(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "organization"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "organization", "federation.go"), []byte("package organization\ntype AliasBinding struct { RouteID string }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := architectureStageResult{Tasks: []architectureTaskResult{{Description: "Define AliasBinding{Alias, ActorFQN, Revision} in organization/alias.go.", WriteScope: []string{"organization/alias.go", "organization/alias_test.go"}}}}
	if err := checkPlanGoDeclarations(t.Context(), root, plan); err == nil || !strings.Contains(err.Error(), "without explicit declarations") {
		t.Fatalf("r62 prose-only declaration admitted: %v", err)
	}
	plan.Tasks[0].Description = "New declaration:\n```go\n// path: organization/alias.go\npackage organization\ntype AliasBinding struct { Alias string; ActorFQN string; Revision uint64 }\n```"
	if err := checkPlanGoDeclarations(t.Context(), root, plan); err == nil || !strings.Contains(err.Error(), "conflicts with organization/federation.go:2") {
		t.Fatalf("collision published: %v", err)
	}
	plan.Tasks[0].Description = strings.Replace(plan.Tasks[0].Description, "type AliasBinding", "type ActorAliasBinding", 1)
	if err := checkPlanGoDeclarations(t.Context(), root, plan); err != nil {
		t.Fatalf("corrected same-scope proposal rejected: %v", err)
	}
	plan.Tasks[0].Description = strings.Replace(plan.Tasks[0].Description, "organization/alias.go", "organization/outside.go", 1)
	if err := checkPlanGoDeclarations(t.Context(), root, plan); err == nil {
		t.Fatal("outside-scope declaration admitted")
	}
}

type declarationGuardJournal struct{ entries []agentruntime.Entry }

func (j *declarationGuardJournal) Load(context.Context, string) ([]agentruntime.Entry, error) {
	return append([]agentruntime.Entry(nil), j.entries...), nil
}
func (j *declarationGuardJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if expected != uint64(len(j.entries)) {
		return agentruntime.Entry{}, agentruntime.ErrConflict
	}
	entry := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	j.entries = append(j.entries, entry)
	return entry, nil
}

type declarationGuardModel struct {
	turns []agentruntime.Completion
	seen  [][]agentruntime.Message
}

func (m *declarationGuardModel) Complete(_ context.Context, messages []agentruntime.Message) (agentruntime.Completion, error) {
	m.seen = append(m.seen, append([]agentruntime.Message(nil), messages...))
	turn := m.turns[0]
	m.turns = m.turns[1:]
	return turn, nil
}

type declarationGuardRead struct{}

func (declarationGuardRead) ExecuteReadOnly(context.Context, agentruntime.ToolCall) (json.RawMessage, error) {
	return json.RawMessage(`{"output":"package organization; type AliasBinding struct { RouteID string }"}`), nil
}

func TestPlanGuardCorrectionRetainsPriorReadAndRejectedDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "organization"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "organization/federation.go"), []byte("package organization\ntype AliasBinding struct { RouteID string }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := architectureStageResult{Tasks: []architectureTaskResult{{Description: "```go\n// path: organization/alias.go\npackage organization\ntype AliasBinding struct { Alias string }\n```", WriteScope: []string{"organization/alias.go"}}}}
	bad, _ := json.Marshal(plan)
	plan.Tasks[0].Description = strings.Replace(plan.Tasks[0].Description, "type AliasBinding", "type ActorAliasBinding", 1)
	good, _ := json.Marshal(plan)
	model := &declarationGuardModel{turns: []agentruntime.Completion{
		{ToolCalls: []agentruntime.ToolCall{{ID: "source", Name: "read_file", Arguments: json.RawMessage(`{"path":"organization/federation.go"}`)}}},
		{ToolCalls: []agentruntime.ToolCall{{ID: "bad", Name: "submit_result", Arguments: bad}}},
		{ToolCalls: []agentruntime.ToolCall{{ID: "good", Name: "submit_result", Arguments: good}}},
	}}
	journal := &declarationGuardJournal{}
	runner := agentruntime.Runner{Journal: journal, Model: model, Tools: declarationGuardRead{}, FinalTool: "submit_result", SystemPrompt: "original pinned instructions", MaxTurns: 3, MaxFinalSubmissions: 3,
		Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil },
		ValidateFinalResult: func(ctx context.Context, raw []byte) error {
			var plan architectureStageResult
			if err := json.Unmarshal(raw, &plan); err != nil {
				return err
			}
			return checkPlanGoDeclarations(ctx, root, plan)
		},
	}
	requestDigest := strings.Repeat("a", 64)
	result, err := runner.Run(ctx, "same-assessment", requestDigest, "original decision context")
	if err != nil || result != string(good) || len(model.seen) != 3 {
		t.Fatalf("correction: %q %v", result, err)
	}
	contextBytes, _ := json.Marshal(model.seen[2])
	for _, required := range []string{"original pinned instructions", "original decision context", "RouteID", "conflicts with organization/federation.go:2", "AliasBinding"} {
		if !strings.Contains(string(contextBytes), required) {
			t.Fatalf("correction lost %q: %s", required, contextBytes)
		}
	}
	if _, err := runner.Run(ctx, "same-assessment", requestDigest, "original decision context"); err != nil || len(model.seen) != 3 {
		t.Fatalf("completed replay repeated assessment: %v", err)
	}
}

func TestPlanExistingInterfaceReuseDoesNotRequireNewDeclaration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.go"), []byte("package existing\ntype Interface interface { Read() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := architectureStageResult{Tasks: []architectureTaskResult{{Description: "Use the existing Interface; modify its implementation without introducing new declarations.", WriteScope: []string{"existing.go"}}}}
	if err := checkPlanGoDeclarations(t.Context(), root, plan); err != nil {
		t.Fatal(err)
	}
}
