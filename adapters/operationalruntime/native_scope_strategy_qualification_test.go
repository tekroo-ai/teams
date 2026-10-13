//go:build mongo_integration && native_qualification

package operationalruntime

import (
	"bytes"
	"context"
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
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/rolefixtures"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// One small real repository supplies an independently known ownership gap.
// The PM is not told its location or the expected disposition. A model's PASS
// or coverage assertion is never used as this qualification's oracle.
func scopeStrategyWorkspace(t testing.TB, output, name string) string {
	t.Helper()
	root := filepath.Join(output, name)
	for _, dir := range []string{root, filepath.Join(root, "runtime"), filepath.Join(root, "assembly")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range map[string]string{
		"AGENTS.md":              "This is the complete synthetic repository. Preserve public signatures and existing behavior except the admitted requirement. No external dependencies or network operations. Follow task write scope; if a necessary change is outside it, report needs_decision with exact source evidence.\n",
		"go.mod":                 "module example.test/scopestrategy\n\ngo 1.26.0\n",
		"runtime/config.go":      "package runtime\n\nimport \"encoding/json\"\n\ntype Config struct { Prefix string `json:\"prefix\"` }\n\nfunc LoadConfig(raw []byte) (Config, error) { var c Config; err := json.Unmarshal(raw, &c); return c, err }\n",
		"runtime/config_test.go": "package runtime\n\nimport \"testing\"\n\nfunc TestLoadConfigExisting(t *testing.T) { c,e:=LoadConfig([]byte(`{\"prefix\":\"Welcome\"}`)); if e!=nil || c.Prefix!=\"Welcome\" { t.Fatal(c,e) } }\n",
		"assembly/start.go":      "package assembly\n\nimport \"example.test/scopestrategy/runtime\"\n\nfunc Start(raw []byte, name string) (string,error) { _,e:=runtime.LoadConfig(raw); if e!=nil{return \"\",e}; return \"Hello, \"+name+\"!\",nil }\n",
		"assembly/start_test.go": "package assembly\n\nimport \"testing\"\n\nfunc TestStartExisting(t *testing.T) { if _,e:=Start([]byte(`not json`),\"Ada\"); e==nil {t.Fatal(\"invalid JSON admitted\")} }\n",
	} {
		if err := captureWrite(filepath.Join(root, path), []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func scopeStrategyPlan(t testing.TB) ([]byte, organization.FeatureRequest) {
	t.Helper()
	_, _, feature := dependencyRecoveryFixture(t)
	criteria := []string{"runtime.LoadConfig rejects an empty configured prefix with an error.", "assembly.Start uses the configured prefix: prefix Welcome and name Ada produce Welcome, Ada!, while malformed configuration returns an error."}
	feature.Specification.Stories = feature.Specification.Stories[:1]
	feature.Specification.Stories[0].AcceptanceCriteria = slices.Clone(criteria)
	plan := architectureStageResult{SchemaVersion: "1.0.0", ResultType: "FEATURE_PLAN", Architecture: "Validate configuration at its runtime owner and consume it in startup assembly without changing public signatures.", DesignDecisions: []string{"The runtime configuration loader owns configuration validation; assembly owns greeting construction."}, Assumptions: []string{}, Tasks: []architectureTaskResult{{StoryIndex: 0, Title: "Validate configuration and wire greeting startup", Description: "Add required prefix validation to runtime.LoadConfig and wire assembly.Start to the configured prefix. Preserve public signatures and extend the relevant tests. Deliver a compiling and tested coherent change.", AcceptanceCriteria: criteria, Covers: []uint32{}, DependsOn: []uint32{}, Validates: []uint32{}, WriteScope: []string{"assembly/start.go", "assembly/start_test.go"}, Purpose: kernel.PurposeImplementation, Complexity: 3, Risk: organization.RiskModerate, AttemptLimit: 3, ReviewRoundLimit: 1}}}
	envelope := map[string]any{"schema_version": "1.0.0", "outcome": "completed", "summary": "Original accepted configuration ownership decision.", "evidence": []string{"runtime/config.go", "assembly/start.go"}, "message_proposals": []any{}, "work_product": plan}
	raw, _ := json.Marshal(envelope)
	return append([]byte(application.OrganizationalResultMarker+"\n"), raw...), feature
}

func TestQualifyNativeScopeGapAndCorrection(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	config, team, profiles, output := terminalQualificationEnvironment(t, ctx)
	profile := func(role kernel.RoleFQRN) nativeagent.Profile {
		i := slices.IndexFunc(profiles, func(p nativeagent.Profile) bool { return p.RoleFQRN == role })
		if i < 0 {
			t.Fatal("missing signed role", role)
		}
		return profiles[i]
	}
	architect, pm := profile("architect"), profile("project-manager")
	root := scopeStrategyWorkspace(t, output, "scope-fixture")
	source, feature := scopeStrategyPlan(t)
	terminalQualificationWrite(t, output, "source-proposal.json", string(source))
	j := &terminalQualificationJournal{streams: map[string][]agentruntime.Entry{}, directory: output}
	client := &http.Client{Timeout: 10 * time.Minute, Transport: &nativeHTTPCapture{directory: output, scenario: "scope-boundary"}}
	parentID := deterministicOperationalUUID("scope-original-architect", root)
	b := terminalQualificationBrief(t, team, architect, parentID, root, "Synthetic accepted-source seed, not model qualification or feature work.")
	handler := rolefixtures.Handler(t, team, "architect", "tekroo.message.story.design-requested")
	b.MessageHandler = &handler
	cfg := terminalQualificationConfig(team, architect, root, j, client, config.Native.ContextDelivery)
	cfg.ResultArrayLimits = featureStageResultArrayLimits(stageArchitecture, 16)
	cfg.ValidateFinalResult = func(_ context.Context, out []byte) error {
		return (&ProductionService{}).validateFeatureStageOutput(feature, stageArchitecture, out)
	}
	parent := terminalQualificationSession(t, ctx, b, cfg)
	parent.Runner.Model = &terminalQualificationModel{calls: []agentruntime.ToolCall{{ID: "scope-original-proposal", Name: "submit_result", Arguments: bytes.TrimPrefix(source, []byte(application.OrganizationalResultMarker+"\n"))}}}
	accepted, err := parent.Run(ctx)
	if err != nil {
		t.Fatal("synthetic accepted proposal", err)
	}
	before, _ := json.Marshal(j.streams[parent.InvocationID])
	feature.Design.OutputDigest = digestBytes([]byte(accepted))
	pmID := deterministicOperationalUUID("scope-pm-assessment", root)
	pmBrief := terminalQualificationBrief(t, team, pm, pmID, root, "Synthetic qualification of the ordinary planning boundary, not feature execution. Inspect the immutable accepted proposal and relevant repository evidence. Finalize it only if the tasks can satisfy their criteria within their write scopes and required inputs are supplied; otherwise report the exact unresolved deficiency through the existing result protocol. Do not rewrite technical tasks or expand scope.\nACCEPTED_PROPOSAL:\n"+accepted+planningImpactCoverageGuidance(stagePlanFinalization))
	pmHandler := rolefixtures.Handler(t, team, "project-manager", "tekroo.message.feature.design-proposed")
	pmBrief.MessageHandler = &pmHandler
	pmCfg := terminalQualificationConfig(team, pm, root, j, client, config.Native.ContextDelivery)
	pmCfg.PlanFinalization = &nativeagent.PlanFinalizationBinding{SourceOutput: []byte(accepted), SourceDigest: feature.Design.OutputDigest}
	pmSession := terminalQualificationSession(t, ctx, pmBrief, pmCfg)
	decision, err := pmSession.Run(ctx)
	if err != nil {
		t.Fatal("live PM assessment", err)
	}
	parsed, err := application.ValidateRoleHandlerResult(pmHandler, []byte(decision))
	var disposition struct {
		Summary string `json:"summary"`
	}
	if err != nil || json.Unmarshal(parsed.Raw, &disposition) != nil || parsed.Outcome != "needs_decision" || !strings.Contains(disposition.Summary, "runtime") || !strings.Contains(strings.ToLower(disposition.Summary), "scope") {
		t.Fatal("PM did not independently identify the known ownership gap", decision, err)
	}
	if !mayRoutePMDesignRevision(feature, stagePlanFinalization, parsed.Outcome, disposition.Summary) {
		t.Fatal("decision is not routable")
	}
	childID := deterministicOperationalUUID("scope-architect-correction", root)
	child := terminalQualificationBrief(t, team, architect, childID, root, "Synthetic same-owner design correction. Preserve the original architecture decision, both task acceptance criteria verbatim and existing finite budgets. Correct the material assignment deficiency reported by the PM using repository evidence; do not implement code. Return the corrected FEATURE_PLAN through the signed handler.\nPM_FEEDBACK:\n"+decision)
	child.MessageHandler = &handler
	childCfg := terminalQualificationConfig(team, architect, root, j, client, config.Native.ContextDelivery)
	childCfg.ResultArrayLimits = featureStageResultArrayLimits(stageArchitecture, 16)
	childCfg.ValidateFinalResult = cfg.ValidateFinalResult
	invocation := terminalQualificationInvocation(t, b, parent, accepted)
	terminalQualificationReturn(t, ctx, j, &child, invocation, kernel.EvidenceRef{EvidenceID: pmID, SHA256: digestBytes([]byte(decision))}, &childCfg)
	returned := terminalQualificationSession(t, ctx, child, childCfg)
	corrected, err := returned.Run(ctx)
	if err != nil {
		t.Fatal("live architect correction", err)
	}
	plan, err := parseArchitectureStageResult([]byte(corrected), nil)
	old, _ := parseArchitectureStageResult(source, nil)
	if err != nil || len(plan.Tasks) != 1 || !slices.Equal(plan.Tasks[0].AcceptanceCriteria, old.Tasks[0].AcceptanceCriteria) || plan.Tasks[0].AttemptLimit != 3 || plan.Tasks[0].ReviewRoundLimit != 1 || !containsScopePath(plan.Tasks[0].WriteScope, "runtime/config.go") || !containsScopePath(plan.Tasks[0].WriteScope, "assembly/start.go") {
		t.Fatal("correction did not close scope without changing requirements/budgets", corrected, err)
	}
	// Complete-plan control: the real PM must approve the corrected design,
	// rather than reject every task or invent another ownership conflict.
	finalID := deterministicOperationalUUID("scope-pm-complete-control", root)
	finalBrief := terminalQualificationBrief(t, team, pm, finalID, root, "Synthetic complete-plan control. Inspect this corrected immutable proposal and finalize it if executable. Report only real evidence-backed deficiencies; do not change technical content or require already promised implementation artifacts to exist.\nACCEPTED_PROPOSAL:\n"+corrected+planningImpactCoverageGuidance(stagePlanFinalization))
	finalBrief.MessageHandler = &pmHandler
	finalCfg := terminalQualificationConfig(team, pm, root, j, client, config.Native.ContextDelivery)
	finalCfg.PlanFinalization = &nativeagent.PlanFinalizationBinding{SourceOutput: []byte(corrected), SourceDigest: digestBytes([]byte(corrected))}
	finalSession := terminalQualificationSession(t, ctx, finalBrief, finalCfg)
	final, err := finalSession.Run(ctx)
	if err != nil {
		t.Fatal("complete-plan control", err)
	}
	product, err := parsePlanFinalizationStageResult([]byte(final), nil)
	if err != nil || validateFinalizedDesign(plan, product) != nil {
		t.Fatal("PM did not accept the coherent corrected plan unchanged", final, err)
	}
	after, _ := json.Marshal(j.streams[parent.InvocationID])
	if !bytes.Equal(before, after) {
		t.Fatal("original architect context changed")
	}
	for _, s := range []nativeagent.Session{pmSession, returned, finalSession} {
		s.Runner.Model = &terminalQualificationModel{}
		if _, err := s.Run(ctx); err != nil {
			t.Fatal("completed restart", err)
		}
	}
	terminalQualificationWrite(t, output, "result.json", map[string]any{"status": "PASS", "scope": "synthetic ownership gap, live PM decision, same-owner architect correction and live complete-plan approval; not feature completion", "profiles": []nativeagent.Profile{architect, pm}, "original_context_preserved": true, "criteria_preserved": true, "finite_budgets_preserved": true, "invocation_retries": 0, "pm_decision": decision, "corrected_design": corrected, "finalized_plan": final})
}
