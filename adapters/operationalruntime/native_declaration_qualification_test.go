//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/organization"
)

// This scoped live capability check never loads a production service or writes
// MongoDB/config. It uses the installed model profile, signed handler and native
// boundary, a small fixture, fresh journal and exact HTTP capture. It is NOT a
// retry of the historical invocation or a full feature qualification.
func TestNativeArchitectDeclarationGuardQualification(t *testing.T) {
	configPath, rawPath, output := os.Getenv("TEKROO_DECLARATION_QUAL_CONFIG"), os.Getenv("TEKROO_DECLARATION_QUAL_RAW"), os.Getenv("TEKROO_DECLARATION_QUAL_OUTPUT")
	if configPath == "" {
		t.Skip("explicit live architect qualification authorization required")
	}
	if !filepath.IsAbs(rawPath) || !filepath.IsAbs(output) {
		t.Fatal("absolute source/output required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	config, err := LoadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// The normal team remains paused throughout this separate capability check.
	status, err := exec.CommandContext(ctx, "/Users/paul/.local/opt/tekroo-teams-v4/bin/tekroo", "-config", configPath, "status").Output()
	var state struct {
		State  string
		Worker struct{ Active int }
	}
	if err != nil || json.Unmarshal(status, &state) != nil || state.State != "PAUSED" || state.Worker.Active != 0 {
		t.Fatal("Teams must be paused and idle")
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Entries []struct {
			Invocation string          `json:"invocation_id"`
			Kind       string          `json:"kind"`
			Payload    json.RawMessage `json:"payload"`
		} `json:"agent_turn_entries"`
		Features []struct {
			Data organization.FeatureRequest `json:"data"`
		} `json:"feature_requests"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	var brief application.ExecutionBrief
	sourceInvocation := ""
	for _, entry := range snapshot.Entries {
		if entry.Kind != "started" {
			continue
		}
		var start struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(entry.Payload, &start); err != nil {
			t.Fatal(err)
		}
		var prompt struct {
			Brief application.ExecutionBrief `json:"execution_brief"`
		}
		if err := json.Unmarshal([]byte(start.Prompt), &prompt); err != nil {
			t.Fatal(err)
		}
		if prompt.Brief.RoleGrounding.RoleFQRN == "architect" {
			brief, sourceInvocation = prompt.Brief, entry.Invocation
			break
		}
	}
	if sourceInvocation == "" || len(snapshot.Features) != 1 {
		t.Fatal("missing unique architect source/feature")
	}
	var profile ProductionProfile
	for _, p := range config.Profiles {
		if p.RoleFQRN == "architect" {
			profile = p
			break
		}
	}
	if profile.NativeSettings == nil || profile.ModelProfileDigest != brief.ModelProfileDigest {
		t.Fatal("source model profile changed")
	}
	settings := *profile.NativeSettings
	client := &http.Client{Timeout: 3 * time.Minute}
	response, err := client.Get(strings.TrimSuffix(settings.BaseURL, "/v1") + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !strings.Contains(string(metrics), "vllm:num_requests_running 0\n") || !strings.Contains(string(metrics), "vllm:num_requests_waiting 0\n") {
		t.Fatal("model server must be idle")
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal("single-use output directory: ", err)
	}
	root := filepath.Join(output, "fixture")
	if err := os.MkdirAll(filepath.Join(root, "organization"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, source := range map[string]string{
		"AGENTS.md":                "This is a read-only architect declaration-format qualification, not a feature implementation. Use only the exposed tools.\n",
		"go.mod":                   "module example.test/declarationqual\n\ngo 1.26.0\n",
		"organization/existing.go": "package organization\n\ntype AliasBinding struct { RouteID string }\nvar aliasPattern = \"federation-owned\"\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Declaration Qualification", "-c", "user.email=qualification@example.test", "commit", "-qm", "fixture"}} {
		nativeQualificationGit(t, root, args...)
	}
	head := strings.TrimSpace(nativeQualificationGit(t, root, "rev-parse", "HEAD"))
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := ids.Next()
	if err != nil {
		t.Fatal(err)
	}
	brief.InvocationID = id
	brief.Scope.WorkspaceID, brief.Scope.WorktreeID, brief.Scope.Branch, brief.Scope.BaselineSHA = "declaration-qualification", "declaration-qualification", "qualification", head
	brief.SemanticContext = application.SemanticContextRequest{}
	brief.Task.Description = "SCOPED DECLARATION-FORMAT QUALIFICATION ONLY, NOT COMPLETION OF THE SOURCE FEATURE. Inspect organization/existing.go. Return FEATURE_PLAN with exactly one IMPLEMENTATION task, story_index=0, complexity<=3, depends_on=[], validates=[], attempt_limit=2, review_round_limit=1. It proposes a new organization/local_alias.go containing a small local alias value type plus one pure resolver function with a working implementation and unit tests. Avoid names already in the owning package. Its write_scope must contain organization/local_alias.go, organization/local_alias_test.go and organization/existing.go. Include a Go declaration manifest for the new file and, separately, a marked Go modification/example snippet showing a comment-only change in existing.go. Only the plan manifest omits function bodies; the future coder must implement them, not defer them to a follow-up. Include an acceptance criterion that go test ./organization passes on the completed implementation. Do not implement anything, run tests or edit files yourself: your role is to PLAN the implementation. Use the declaration-format guidance; complete this bounded capability check without mapping unrelated code."
	updated := false
	for i, instruction := range brief.ExecutionGuidance {
		if strings.HasPrefix(instruction, "Before naming a new Go package-level declaration") {
			brief.ExecutionGuidance[i], updated = application.GoDeclarationManifestGuidance, true
		}
	}
	if !updated {
		t.Fatal("source lacks declaration guidance")
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	digest := digestBytes(encoded)
	binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: brief.RoleGrounding.Permissions, Purpose: brief.Purpose, EffectPolicyDigest: brief.EffectPolicyDigest}}
	nativeProfile := nativeagent.Profile{RoleFQRN: profile.RoleFQRN, RoleBundleDigest: profile.RoleBundleDigest, ModelProfileDigest: profile.ModelProfileDigest, RuntimeIdentityDigest: profile.RuntimeIdentityDigest, ToolPolicyDigest: profile.ToolPolicyDigest, EffectPolicyDigest: profile.EffectPolicyDigest, BaseURL: settings.BaseURL, Model: settings.Model, ModelInstructions: settings.ModelInstructions, ResponseMode: settings.ResponseMode, MaxOutputTokens: settings.MaxOutputTokens, MaxTurns: settings.MaxTurns, Reasoning: settings.Reasoning}
	for _, def := range agenttools.ReadOnlyDefinitions(brief.RoleGrounding.Permissions) {
		nativeProfile.AllowedReadTools = append(nativeProfile.AllowedReadTools, def.Name)
	}
	slices.Sort(nativeProfile.AllowedReadTools)
	journal := &nativeReleaseJournal{path: filepath.Join(output, "journal.json")}
	client.Transport = &nativeHTTPCapture{directory: output, scenario: "architect-declaration-guard"}
	service := &ProductionService{planning: config.Planning}
	feature := snapshot.Features[0].Data
	validate := func(ctx context.Context, result []byte) error {
		workflowErr := service.validateFeatureStageOutputWithEvidence(ctx, feature, stageArchitecture, result)
		return planningDeclarationFindings(ctx, root, stageArchitecture, result, workflowErr)
	}
	session, err := nativeagent.PrepareReadOnly(ctx, brief, digest, nativeagent.Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: 20 * time.Second}}, Journal: journal, HTTP: client, Profile: nativeProfile, ValidateFinalResult: validate})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	result, runErr := session.Run(ctx)
	counts := map[string]int{}
	seenCalls := map[string]bool{}
	toolErrors := []agentruntime.ToolResult{}
	for _, entry := range journal.entries {
		if entry.Kind == agentruntime.ModelTurn {
			var turn agentruntime.Completion
			if err := json.Unmarshal(entry.Payload, &turn); err != nil {
				t.Fatal(err)
			}
			for _, call := range turn.ToolCalls {
				if !seenCalls[call.ID] {
					seenCalls[call.ID] = true
					counts[call.Name]++
				}
			}
		}
		if entry.Kind == agentruntime.ToolDone {
			var done agentruntime.ToolResult
			if err := json.Unmarshal(entry.Payload, &done); err != nil {
				t.Fatal(err)
			}
			if done.Error != "" {
				toolErrors = append(toolErrors, done)
			}
		}
	}
	if runErr == nil {
		plan, err := parseArchitectureStageResult([]byte(result), nil)
		if err != nil || len(plan.Tasks) != 1 {
			runErr = fmt.Errorf("qualification expected one valid task: %v", err)
		} else {
			if !strings.Contains(strings.Join(plan.Tasks[0].AcceptanceCriteria, "\n"), "go test ./organization") {
				runErr = fmt.Errorf("qualification omitted runnable implementation acceptance criterion")
			}
			if strings.Contains(strings.ToLower(plan.Tasks[0].Description), "will not compile") {
				runErr = fmt.Errorf("qualification incorrectly planned an unbuildable implementation")
			}
			blocks, err := explicitPlanGoBlocks(plan.Tasks[0].Description)
			if err != nil {
				runErr = err
			} else {
				newFile, modification := false, false
				for _, b := range blocks {
					newFile = newFile || b.Path == "organization/local_alias.go" && !b.Modification
					modification = modification || b.Path == "organization/existing.go" && b.Modification
				}
				if !newFile || !modification {
					runErr = fmt.Errorf("qualification did not exercise both distinct markers")
				}
			}
		}
	}
	if status := nativeQualificationGit(t, root, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Fatal("read-only qualification mutated fixture")
	}
	receipt := map[string]any{"source_invocation": sourceInvocation, "source_snapshot_sha256": digestBytes(raw), "qualification_invocation": id, "model_profile_digest": profile.ModelProfileDigest, "request_digest": digest, "fixture_baseline": head, "started_at": started, "finished_at": time.Now().UTC(), "tool_counts": counts, "tool_errors": toolErrors, "result": result, "workflow_advancement": "NOT_RUN", "production_deployment": "NOT_RUN", "full_feature_qualification": "NOT_RUN", "passed": runErr == nil}
	if runErr != nil {
		receipt["error"] = runErr.Error()
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "receipt.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	t.Logf("live declaration guard qualified: %s", filepath.Join(output, "receipt.json"))
}
