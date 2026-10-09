//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Explicit opt-in: real-model qualification of the new PM handler contract.
// Other profiles, model settings and prior role evidence remain unchanged.
// Scripted protocol/restart tests qualify the harness separately.
func TestPreparePMDecisionContractRelease(t *testing.T) {
	source, output := os.Getenv("TEKROO_PM_DECISION_CONFIG"), os.Getenv("TEKROO_PM_DECISION_OUTPUT")
	if source == "" || output == "" {
		t.Skip("explicit qualification paths required")
	}
	config, err := LoadProductionConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(output, name), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("baseline-config.json", config)
	var manifest organization.TeamManifest
	if err := readStrictJSONFile(config.Organization.ManifestFile, &manifest); err != nil {
		t.Fatal(err)
	}
	keys := map[string]ed25519.PublicKey{}
	for _, publisher := range config.Organization.Publishers {
		raw, err := os.ReadFile(publisher.PublicKeyFile)
		if err != nil {
			t.Fatal(err)
		}
		key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(key) != ed25519.PublicKeySize {
			t.Fatal("invalid publisher")
		}
		keys[publisher.KeyID] = key
	}
	rolePath, err := filepath.Abs("../../config/starter-team/roles-v4/project-manager-2.2.2/role.json")
	if err != nil {
		t.Fatal(err)
	}
	var candidate organization.RoleBundle
	if err := readStrictJSONFile(rolePath, &candidate); err != nil {
		t.Fatal(err)
	}
	roleDigest, err := candidate.ContentDigest()
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := organization.LoadRoleBundle(rolePath, roleDigest, candidate.PublisherKeyID, keys)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := organization.LoadRolePackage(rolePath, bundle)
	if err != nil {
		t.Fatal(err)
	}
	selected := slices.IndexFunc(config.Profiles, func(p ProductionProfile) bool { return p.RoleFQRN == "project-manager" })
	if selected < 0 {
		t.Fatal("missing PM profile")
	}
	p := &config.Profiles[selected]
	old := *p
	p.RoleBundleDigest = roleDigest
	p.ModelProfileDigest, err = nativeagent.ModelProfileDigest(p.RoleFQRN, roleDigest, *p.NativeSettings)
	if err != nil {
		t.Fatal(err)
	}
	if surface, err := agenttools.NativeToolSurfaceDigest(bundle.Permissions); err != nil || surface != p.ToolPolicyDigest {
		t.Fatal("tool policy changed unexpectedly")
	}
	ids, err := NewUUIDv7Source(SystemClock{})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeText(t, filepath.Join(root, "AGENTS.md"), "This qualification repository supplies only the admitted product request. No implementation discovery is required; preserve its criteria.\n", 0600)
	// Repository-read tools remain available under the unchanged PM profile.
	// Give them a real Git root; a plain temporary directory is not an
	// equivalent fixture and rejects otherwise valid git_log/status calls.
	candidateGit(t, root, "init", "--initial-branch=qualification")
	candidateGit(t, root, "config", "user.name", "PM Contract Qualification")
	candidateGit(t, root, "config", "user.email", "qualification@example.test")
	candidateGit(t, root, "add", "AGENTS.md")
	candidateGit(t, root, "-c", "core.hooksPath=/dev/null", "commit", "-m", "Admitted contract qualification input")
	readTools := []string{}
	for _, tool := range agenttools.ReadOnlyDefinitions(bundle.Permissions) {
		readTools = append(readTools, tool.Name)
	}
	slices.Sort(readTools)
	evidence := []kernel.UUIDv7{}
	for _, scenario := range []struct{ name, message, outcome, description string }{
		{"specification-completed", "tekroo.message.feature.refined", "completed", "Specify one story for this fully refined product request: a registered actor's optional alias addresses exactly that actor; its full FQN remains usable. Preserve both criteria. No product questions are outstanding."},
		{"specification-needs-decision", "tekroo.message.feature.refined", "needs_decision", "The admitted refinement explicitly leaves an unresolved product decision: whether a shared alias addresses one actor or broadcasts to all actors. It forbids choosing either interpretation without the principal. Report the exact blocking question; no responsible story can be specified yet. Do not invent a story."},
		{"finalization-needs-decision", "tekroo.message.feature.design-proposed", "needs_decision", "Review the immutable proposal below. Its task description and acceptance criterion are placeholders and cannot describe executable work. Report the technical deficiency for its architect; do not rewrite the task."},
		{"finalization-completed", "tekroo.message.feature.design-proposed", "completed", "Finalize the immutable single-task proposal below. It includes all alias resolver edits and tests within one task, so no cross-task dependency or capability handoff is needed. Preserve the task exactly."},
	} {
		id, err := ids.Next()
		if err != nil {
			t.Fatal(err)
		}
		handler := pkg.Handlers[scenario.message]
		actor := kernel.ActorFQN("teams::project-manager-1")
		brief := application.ExecutionBrief{ContractManifest: kernel.ContractIdentity, InvocationID: id, ActorFQN: actor, Purpose: kernel.PurposeHandoff, ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest,
			RoleGrounding:  application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: p.RoleFQRN, BundleVersion: bundle.Version, BundleDigest: roleDigest, Capabilities: bundle.Capabilities, Permissions: bundle.Permissions, Instructions: pkg.Charter},
			MessageHandler: &application.MessageHandlerGrounding{MessageType: scenario.message, Instructions: handler.Instructions, ResultSchema: handler.ResultSchema, ResultSchemaDigest: handler.Binding.ResultSchema.SHA256, AllowedResults: handler.Binding.AllowedResults},
			Task:           application.TaskExecutionSpecification{Title: scenario.name, Description: "This is a pre-implementation handler-contract qualification. The supplied request/proposal is the complete admitted input; repository source files need not exist yet. Task descriptions and acceptance criteria are prose, not file paths to inspect or format.\n" + scenario.description, AcceptanceCriteria: []string{"Preserve the admitted contract and report only justified work."}},
			ResultProtocol: &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Instruction: "Submit the structured handler result."},
		}
		settings := p.NativeSettings
		journal := &nativeReleaseJournal{path: filepath.Join(output, scenario.name+"-journal.json")}
		binding := nativeReleaseBinding{authority: agenttools.Authority{WorkspaceRoot: root, Permissions: bundle.Permissions, Purpose: brief.Purpose, EffectPolicyDigest: p.EffectPolicyDigest}}
		cfg := nativeagent.Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Minute}}, Journal: journal, HTTP: &http.Client{Timeout: 10 * time.Minute}, Profile: nativeagent.Profile{RoleFQRN: p.RoleFQRN, RoleBundleDigest: roleDigest, ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest, BaseURL: settings.BaseURL, Model: settings.Model, ModelInstructions: settings.ModelInstructions, ResponseMode: settings.ResponseMode, MaxOutputTokens: settings.MaxOutputTokens, MaxTurns: settings.MaxTurns, Reasoning: settings.Reasoning, AllowedReadTools: readTools}}
		if scenario.message == "tekroo.message.feature.design-proposed" {
			description, criterion := "Implement the alias resolver and preserve full FQN lookup in resolver.go; add focused alias and FQN tests in resolver_test.go", "Alias lookup selects the registered actor and full FQN lookup still selects that actor"
			if scenario.outcome == "needs_decision" {
				description, criterion = "Placeholder", "placeholder"
			}
			architecture := architectureStageResult{SchemaVersion: "1.0.0", ResultType: "FEATURE_PLAN", Architecture: "Resolve a registered alias without changing actor authority", DesignDecisions: stageDecisionList{"Alias and full FQN select the same registered actor"}, Assumptions: []string{}, Tasks: []architectureTaskResult{{Title: "Alias resolver", Description: description, AcceptanceCriteria: []string{criterion}, Covers: []uint32{}, Purpose: kernel.PurposeImplementation, Complexity: 4, Risk: organization.RiskLow, AttemptLimit: 2, ReviewRoundLimit: 1, DependsOn: []uint32{}, Validates: []uint32{}, WriteScope: []string{"resolver.go", "resolver_test.go"}}}}
			raw := []byte(application.OrganizationalResultMarker + string(mustJSON(map[string]any{"outcome": "completed", "work_product": architecture})))
			cfg.PlanFinalization = &nativeagent.PlanFinalizationBinding{SourceOutput: raw, SourceDigest: digestBytes(raw)}
			brief.Task.Description += "\nARCHITECT_PROPOSAL_JSON:\n" + string(raw)
		}
		raw := mustJSON(brief)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
		session, err := nativeagent.PrepareReadOnly(ctx, brief, digestBytes(raw), cfg)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Logf("qualifying %s invocation=%s", scenario.name, id)
		started := time.Now().UTC()
		result, err := session.Run(ctx)
		finished := time.Now().UTC()
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := application.ValidateRoleHandlerResult(*brief.MessageHandler, []byte(result))
		if err != nil || parsed.Outcome != scenario.outcome {
			t.Fatalf("wrong disposition %s: %v %s", scenario.name, err, result)
		}
		for _, entry := range journal.entries {
			if entry.Kind == agentruntime.ModelRejected {
				t.Fatal("model schema rejection", string(entry.Payload))
			}
			if entry.Kind == agentruntime.ToolDone {
				var done agentruntime.ToolResult
				if json.Unmarshal(entry.Payload, &done) != nil || done.Error != "" {
					t.Fatal("tool error", string(entry.Payload))
				}
			}
		}
		if scenario.message == "tekroo.message.feature.design-proposed" && scenario.outcome == "completed" {
			var final architectureStageResult
			if err := decodeOrganizationalStageResult([]byte(result), &final); err != nil || final.SourceDesignDigest != cfg.PlanFinalization.SourceDigest || len(final.Tasks) != 1 {
				t.Fatal("assembled plan lost source", err)
			}
		}
		write(scenario.name+"-measurement.json", map[string]any{"invocation_id": id, "model_profile_digest": p.ModelProfileDigest, "bundle_digest": roleDigest, "started_at": started, "finished_at": finished, "expected_outcome": scenario.outcome, "result": result, "journal_file": journal.path})
		evidence = append(evidence, id)
	}
	corpusID, err := ids.Next()
	if err != nil {
		t.Fatal(err)
	}
	qualID, err := ids.Next()
	if err != nil {
		t.Fatal(err)
	}
	corpus := application.QualificationCorpusDefinition{CorpusID: corpusID, Revision: 1, DecisionRoute: p.DecisionRoute, QualifiedRole: "project-manager", WorkKinds: []kernel.WorkKind{kernel.WorkDesign}, ScenarioIDs: []string{"specification-completed", "specification-needs-decision", "finalization-needs-decision", "finalization-completed"}, ThresholdDigest: digestBytes([]byte("PM 2.2.2 real typed handlers: exact disposition; strict signed schema; original design preserved; no tool errors or model-schema corrections. Harness context/restart behavior is separately tested.")), ToolSurfaceDigest: p.ToolPolicyDigest}
	corpusDigest, err := corpus.Digest()
	if err != nil {
		t.Fatal(err)
	}
	qual := kernel.ModelProfileQualification{QualificationID: qualID, QualificationCorpusDigest: corpusDigest, ModelProfileDigest: p.ModelProfileDigest, DecisionRoute: p.DecisionRoute, QualifiedRole: "project-manager", QualifiedWorkKinds: []kernel.WorkKind{kernel.WorkDesign}, Status: kernel.QualificationPass, ObservedAt: time.Now().UTC(), EvidenceIDs: evidence}
	qual.QualificationDigest, err = application.QualificationDigest(qual)
	if err != nil {
		t.Fatal(err)
	}
	p.Qualification, p.QualificationCorpus = &qual, &corpus
	write("prior-pm-profile.json", old)
	if err := os.Mkdir(filepath.Join(output, "roles"), 0700); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Roles {
		role := &manifest.Roles[i]
		path := filepath.Join(filepath.Dir(config.Organization.ManifestFile), role.BundlePath)
		if role.Role == "project-manager" {
			path = rolePath
			role.BundleDigest, role.ModelProfileDigest = roleDigest, p.ModelProfileDigest
		}
		if err := os.Symlink(filepath.Dir(path), filepath.Join(output, "roles", role.Role)); err != nil {
			t.Fatal(err)
		}
		role.BundlePath = filepath.Join("roles", role.Role, "role.json")
	}
	write("team.json", manifest)
	config.Organization.ManifestFile = filepath.Join(output, "team.json")
	raw, err := os.ReadFile(config.Organization.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	config.Organization.ManifestDigest = digestBytes(raw)
	write("tekrood.native-candidate.json", config)
	if _, err := LoadProductionConfig(filepath.Join(output, "tekrood.native-candidate.json")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationalProfileQualifications(config.Profiles, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
