package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/validation"
	"github.com/tekroo-ai/teams/organization"
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

func TestPlanGoModificationExamplesAreNotNewFileManifests(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.go"), []byte("package existing\ntype Existing int\n"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := architectureStageResult{Tasks: []architectureTaskResult{{WriteScope: []string{"existing.go"}, Description: "```go\n// modify: existing.go\nconst NewName = 1\n```"}}}
	if err := checkPlanGoDeclarations(t.Context(), root, plan); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		description string
		scope       []string
		want        string
	}{
		{"```go\n// modify: missing.go\nconst NewName = 1\n```", []string{"missing.go"}, "without explicit declarations"},
		{"```go\n// modify: existing.go (existing file)\nconst NewName = 1\n```", []string{"existing.go"}, "without annotations"},
		{"```go\n// path: existing.go\npackage existing\ntype Existing string\n```", []string{"existing.go"}, "conflicts with existing.go"},
		{"```go\n// modify: existing.go\nconst NewName = 1\n```", []string{"other.go"}, "outside its write scope"},
		{"```go\n// path: ../outside.go\npackage existing\ntype Other int\n```", []string{"existing.go"}, "invalid declaration path"},
	} {
		plan.Tasks[0].Description, plan.Tasks[0].WriteScope = change.description, change.scope
		err := checkPlanGoDeclarations(t.Context(), root, plan)
		if err == nil || !strings.Contains(err.Error(), change.want) {
			t.Fatalf("want %q: %v", change.want, err)
		}
	}
}

type r63DeclarationDrafts struct {
	SourceSHA    string `json:"source_snapshot_sha256"`
	InvocationID string `json:"invocation_id"`
	Drafts       []struct {
		Receipt   string          `json:"receipt"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"drafts"`
}

func loadR63DeclarationDrafts(t *testing.T) r63DeclarationDrafts {
	t.Helper()
	raw, err := os.ReadFile("testdata/r63-declaration-drafts.json")
	if err != nil {
		t.Fatal(err)
	}
	var drafts r63DeclarationDrafts
	if err := json.Unmarshal(raw, &drafts); err != nil {
		t.Fatal(err)
	}
	if drafts.SourceSHA != "09c9a04179d53524a0296cb9b1f4b19a15517aea457d0a2ad7432a18a41e1a43" || len(drafts.Drafts) != 3 {
		t.Fatal("unbound raw fixture")
	}
	return drafts
}

func r63DraftOutput(t *testing.T, arguments json.RawMessage) []byte {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &envelope); err != nil {
		t.Fatal(err)
	}
	// The native finalizer supplies this fixed transport constant; no authored
	// work_product field or draft content is rewritten for the raw replays.
	envelope["schema_version"] = json.RawMessage(`"1.0.0"`)
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(application.OrganizationalResultMarker+"\n"), raw...)
}

func r63DeclarationFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// These paths existed at r63's baseline. Their presence must not be
	// confused with proposed new files when replaying the unchanged drafts.
	for path, pkg := range map[string]string{
		"adapters/mcp/handler.go": "mcp", "adapters/operatorhttp/handler.go": "operatorhttp",
		"adapters/operationalruntime/production.go": "operationalruntime", "cmd/tekroo/main.go": "main",
		"adapters/operatortools/service.go": "operatortools",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte("package "+pkg+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestR63RawDraftFindingsExposeGraphAndAllManifestErrorsTogether(t *testing.T) {
	drafts := loadR63DeclarationDrafts(t)
	root := r63DeclarationFixtureRoot(t)
	for index, draft := range drafts.Drafts {
		output := r63DraftOutput(t, draft.Arguments)
		_, structuralErr := parseArchitectureStageResult(output, nil)
		err := planningDeclarationFindings(t.Context(), root, stageArchitecture, output, structuralErr)
		if err == nil {
			t.Fatalf("raw rejected draft %d admitted", index)
		}
		if index < 2 {
			for _, path := range []string{"organization/role_alias.go", "adapters/memory/role_alias_store.go", "adapters/mongo/role_alias_store.go", "adapters/operationalruntime/role_alias_service.go"} {
				if !strings.Contains(err.Error(), path) {
					t.Fatalf("draft %d hides %s: %v", index, path, err)
				}
			}
		}
		if index == 0 && !strings.Contains(err.Error(), "validates references non-prior task index 1") {
			t.Fatal(err)
		}
		if index == 2 && !strings.Contains(err.Error(), "without annotations") {
			t.Fatal(err)
		}
	}
}

func TestR63AggregatedCorrectionRetainsEveryRawDraftAndPriorEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	drafts := loadR63DeclarationDrafts(t)
	root := r63DeclarationFixtureRoot(t)
	// This explicit corrected fixture, not a production auto-rewrite, uses the
	// final model-authored declarations and marks the existing-file reference.
	good := []byte(strings.Replace(string(drafts.Drafts[2].Arguments), "// path: adapters/mcp/handler.go (new constants, existing file)", "// modify: adapters/mcp/handler.go", 1))
	model := &declarationGuardModel{turns: []agentruntime.Completion{
		{ToolCalls: []agentruntime.ToolCall{{ID: "source", Name: "read_file", Arguments: json.RawMessage(`{"path":"organization/federation.go"}`)}}},
	}}
	for index, draft := range drafts.Drafts {
		model.turns = append(model.turns, agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: fmt.Sprint("draft-", index), Name: "submit_result", Arguments: draft.Arguments}}})
	}
	// Original r63 fails after its third rejection, so the corrected draft is
	// assessed separately below; no fourth production submission is enabled.
	journal := &declarationGuardJournal{}
	validate := func(ctx context.Context, output []byte) error {
		_, err := parseArchitectureStageResult(output, nil)
		return planningDeclarationFindings(ctx, root, stageArchitecture, output, err)
	}
	runner := agentruntime.Runner{Journal: journal, Model: model, Tools: declarationGuardRead{}, FinalTool: "submit_result", SystemPrompt: "original pinned instructions", MaxTurns: 4, MaxFinalSubmissions: 3,
		Finalize: func(raw json.RawMessage) (string, error) { return string(r63DraftOutput(t, raw)), nil }, ValidateFinalResult: validate}
	_, err := runner.Run(ctx, "r63-replay", strings.Repeat("a", 64), "original decision context")
	if err == nil || !strings.Contains(err.Error(), "after 3 submissions") {
		t.Fatal(err)
	}
	if len(model.seen) != 4 {
		t.Fatalf("unexpected model calls: %d", len(model.seen))
	}
	history, _ := json.Marshal(model.seen[3])
	for _, want := range []string{"original pinned instructions", "original decision context", "RouteID", "validates references non-prior task index", "adapters/memory/role_alias_store.go", "bodyless function signatures"} {
		if !strings.Contains(string(history), want) {
			t.Fatalf("lost %q", want)
		}
	}
	if err := validate(t.Context(), r63DraftOutput(t, good)); err != nil {
		t.Fatalf("explicitly corrected draft rejected: %v", err)
	}
	// A first-draft rejection followed by one complete correction fits the
	// unchanged three-submission budget and never repeats discovery.
	model = &declarationGuardModel{turns: []agentruntime.Completion{
		{ToolCalls: []agentruntime.ToolCall{{ID: "bad", Name: "submit_result", Arguments: drafts.Drafts[0].Arguments}}},
		{ToolCalls: []agentruntime.ToolCall{{ID: "corrected", Name: "submit_result", Arguments: good}}},
	}}
	runner.Model, runner.Journal = model, &declarationGuardJournal{}
	if _, err := runner.Run(ctx, "corrected-replay", strings.Repeat("b", 64), "original decision context"); err != nil {
		t.Fatal(err)
	}
	if len(model.seen) != 2 {
		t.Fatal("correction repeated work")
	}
}

func TestPlanDeclarationFindingsBoundOutputAndRejectUndecodableInput(t *testing.T) {
	root := t.TempDir()
	plan := architectureStageResult{}
	for i := 0; i < 80; i++ {
		plan.Tasks = append(plan.Tasks, architectureTaskResult{WriteScope: []string{fmt.Sprintf("new%d.go", i)}})
	}
	err := checkPlanGoDeclarations(t.Context(), root, plan)
	if err == nil || strings.Count(err.Error(), "without explicit declarations") != validation.MaximumFindings || !strings.Contains(err.Error(), "additional checks may be omitted") {
		t.Fatal(err)
	}
	if err := planningDeclarationFindings(t.Context(), root, stageArchitecture, []byte("invalid JSON"), nil); err == nil {
		t.Fatal("undecodable output admitted")
	}
}

func TestPlanFinalizationDeclarationFindingsPreserveStructuralRejection(t *testing.T) {
	drafts := loadR63DeclarationDrafts(t)
	output := r63DraftOutput(t, drafts.Drafts[0].Arguments)
	// Finalization uses the same declaration guard, but keeps its own required
	// envelope identity and handoff checks before inspecting independent inputs.
	output = []byte(strings.Replace(string(output), `"result_type":"FEATURE_PLAN"`, `"result_type":"FEATURE_EXECUTION_PLAN","source_design_digest":"`+strings.Repeat("a", 64)+`","handoffs":[]`, 1))
	_, structuralErr := parsePlanFinalizationStageResult(output, nil)
	err := planningDeclarationFindings(t.Context(), r63DeclarationFixtureRoot(t), stagePlanFinalization, output, structuralErr)
	if !errors.Is(err, organization.ErrInvalidFeature) || !strings.Contains(err.Error(), "validates references non-prior task index") || !strings.Contains(err.Error(), "bodyless function signatures") {
		t.Fatalf("independent findings hid finalization rejection: %v", err)
	}
	broken := []byte(strings.Replace(string(output), `"handoffs":[]`, `"handoffs":null`, 1))
	if err := planningDeclarationFindings(t.Context(), t.TempDir(), stagePlanFinalization, broken, nil); !errors.Is(err, organization.ErrInvalidFeature) {
		t.Fatalf("invalid finalization envelope admitted: %v", err)
	}
}
