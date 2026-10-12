package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/rolefixtures"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func dependencyRecoveryFixture(t testing.TB) ([]byte, []json.RawMessage, organization.FeatureRequest) {
	t.Helper()
	raw, err := os.ReadFile("testdata/plan-dependency-recovery.json")
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(application.OrganizationalResultMarker + "\n" + strings.TrimSpace(string(raw)))
	handoffs := []map[string]any{}
	for _, consumer := range []int{1, 2} {
		handoffs = append(handoffs, map[string]any{"provider_task_index": 0, "consumer_task_index": consumer, "capability": "provider.payload.value", "contract": map[string]any{"provided_interface": "Payload.Value string", "required_inputs": []map[string]string{{"input": "Payload containing Value string", "source": "provider.go, implemented by task 0"}}, "consumer_usage": "Read the public Value field without modifying provider source", "verification": "Consumer tests construct Payload with Value and invoke the actual consumer"}})
	}
	deltas := []json.RawMessage{}
	for _, dependencies := range [][]map[string]any{{{"task_index": 1, "depends_on": []int{1}}}, {}, {{"task_index": 0, "depends_on": []int{}}, {"task_index": 1, "depends_on": []int{0}}}} {
		value, err := json.Marshal(map[string]any{"outcome": "completed", "summary": "Finalize the unchanged provider/consumer design", "evidence": []string{}, "message_proposals": []any{}, "work_product": map[string]any{"task_dependencies": dependencies, "handoffs": handoffs}})
		if err != nil {
			t.Fatal(err)
		}
		deltas = append(deltas, value)
	}
	feature := organization.FeatureRequest{Input: organization.FeatureRequestInput{MaximumTasks: 16, MaximumStories: 1, MaximumHops: 16, AcceptanceCriteria: []string{"Consumers use the provider payload value; unrelated behavior stays unchanged."}}, Specification: &organization.FeatureSpecification{Stories: []organization.PlannedStory{{Title: "Provider consumers", AcceptanceCriteria: []string{"Consumers use the provider payload value; unrelated behavior stays unchanged."}}}}, Design: &organization.FeatureDesignCandidate{OutputDigest: digestBytes(source)}}
	return source, deltas, feature
}

func dependencyRecoveryValidator(feature organization.FeatureRequest, source []byte) func(context.Context, []byte) error {
	return func(_ context.Context, output []byte) error {
		service := &ProductionService{planning: ProductionPlanning{InitialPlanTaskLimit: 16}}
		if err := service.validateFeatureStageOutput(feature, stagePlanFinalization, output); err != nil {
			return err
		}
		proposed, err := parseArchitectureStageResult(source, nil)
		if err != nil {
			return err
		}
		finalized, err := parsePlanFinalizationStageResult(output, nil)
		if err != nil {
			return err
		}
		return validateFinalizedDesign(proposed, finalized)
	}
}

func dependencyRecoverySession(t testing.TB, ctx context.Context, team organization.LoadedTeam, profile nativeagent.Profile, root string, journal agentruntime.Journal, client *http.Client, policy *agentruntime.ContextDeliveryPolicy, source []byte, feature organization.FeatureRequest) nativeagent.Session {
	t.Helper()
	roleIndex := slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(profile.RoleFQRN) })
	if roleIndex < 0 {
		t.Fatal("signed role missing")
	}
	role := team.Roles[roleIndex]
	message := "tekroo.message.story.design-requested"
	description := "SYNTHETIC BOUNDARY QUALIFICATION, not feature work. Submit the supplied complete FEATURE_PLAN unchanged as the architect's proposal; omit depends_on for task 0 and task 1, retain task 2 depends_on=[0]. No implementation, edits or test execution is authorized.\nFIXTURE_PLAN_JSON:\n" + string(source)
	if profile.RoleFQRN == "project-manager" {
		message = "tekroo.message.feature.design-proposed"
		description = "SYNTHETIC ARCHITECT-TO-PM QUALIFICATION, not a historical run recovery. Finalize the accepted immutable proposal. Both consumers require the provider's Value field. Add only the missing task 1 prerequisite on task 0; preserve task 2's existing prerequisite. In task_dependencies also explicitly confirm task 0 has no prerequisites with task_index=0 and depends_on=[]. Supply precise provider/consumer handoffs for both consumers. Keep the technical content unchanged. Continue the retained draft and error history in this same invocation; do not restart or change the source. The exact fixture is the complete input; no repository investigation or test execution is needed.\nARCHITECT_PROPOSAL_JSON:\n" + string(source)
	}
	id := deterministicOperationalUUID("dependency-recovery-qualification", root, string(profile.RoleFQRN))
	actor := kernel.ActorFQN("teams::" + string(profile.RoleFQRN) + "-1")
	handler := rolefixtures.Handler(t, team, string(profile.RoleFQRN), message)
	brief := application.ExecutionBrief{ContractManifest: kernel.ContractIdentity, InvocationID: id, ActorFQN: actor, Purpose: kernel.PurposeHandoff, ModelProfileDigest: profile.ModelProfileDigest, RuntimeIdentityDigest: profile.RuntimeIdentityDigest, ToolPolicyDigest: profile.ToolPolicyDigest, EffectPolicyDigest: profile.EffectPolicyDigest,
		RoleGrounding: application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: profile.RoleFQRN, BundleVersion: role.Bundle.Version, BundleDigest: profile.RoleBundleDigest, Capabilities: role.Bundle.Capabilities, Permissions: role.Bundle.Permissions, Instructions: role.Package.Charter}, MessageHandler: &handler,
		Task: application.TaskExecutionSpecification{TaskID: id, SourceDigest: digestBytes(source), Title: "Architect-to-PM dependency boundary", Description: description, AcceptanceCriteria: []string{"Preserve technical content and existing edges; correct invalid edges and handoffs within the finite invocation."}}, ResultProtocol: &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Instruction: "Submit the structured handler result through submit_result."}}
	binding := dependencyRecoveryBinding{agenttools.Authority{WorkspaceRoot: root, Permissions: role.Bundle.Permissions, Purpose: brief.Purpose, EffectPolicyDigest: profile.EffectPolicyDigest}}
	config := nativeagent.Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: journal, HTTP: client, ContextDelivery: policy, Profile: profile}
	if profile.RoleFQRN == "project-manager" {
		config.ResultArrayLimits = featureStageResultArrayLimits(stagePlanFinalization, 16)
		config.PlanFinalization = &nativeagent.PlanFinalizationBinding{SourceOutput: slices.Clone(source), SourceDigest: digestBytes(source)}
		config.ValidateFinalResult = dependencyRecoveryValidator(feature, source)
	} else {
		config.ResultArrayLimits = append(featureStageResultArrayLimits(stageArchitecture, 16), authoredValidationLinkLimits(structuralTaskRoutingPolicy())...)
		config.ResultEnumLimits = []nativeagent.ResultEnumLimit{{PropertyPath: []string{"work_product", "tasks", "*", "purpose"}, Values: []string{"IMPLEMENTATION"}}}
		config.ValidateFinalResult = func(_ context.Context, out []byte) error {
			return (&ProductionService{}).validateFeatureStageOutput(feature, stageArchitecture, out)
		}
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	session, err := nativeagent.PrepareReadOnly(ctx, brief, digestBytes(encoded), config)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

type dependencyRecoveryBinding struct{ authority agenttools.Authority }

func (b dependencyRecoveryBinding) BindToolInvocation(context.Context, kernel.UUIDv7, kernel.Digest) (agenttools.Authority, error) {
	return b.authority, nil
}

type dependencyRecoveryJournal struct{ entries []agentruntime.Entry }

func (j *dependencyRecoveryJournal) Load(context.Context, string) ([]agentruntime.Entry, error) {
	return slices.Clone(j.entries), nil
}
func (j *dependencyRecoveryJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if expected != uint64(len(j.entries)) {
		return agentruntime.Entry{}, agentruntime.ErrConflict
	}
	e := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: slices.Clone(payload)}
	j.entries = append(j.entries, e)
	return e, nil
}

type dependencyRecoveryModel struct {
	submissions []json.RawMessage
	next        int
	verify      bool
}

func (m *dependencyRecoveryModel) Complete(_ context.Context, history []agentruntime.Message) (agentruntime.Completion, error) {
	if m.next >= len(m.submissions) {
		return agentruntime.Completion{}, errors.New("unexpected additional model turn")
	}
	if m.verify && m.next == 2 {
		found := map[string]bool{}
		drafts := 0
		for _, message := range history {
			if message.Role == "tool" {
				found[message.Content] = true
			}
			for _, call := range message.ToolCalls {
				if call.Name == "submit_result" {
					drafts++
				}
			}
		}
		self, upstream := false, false
		for content := range found {
			self = self || strings.Contains(content, "not self-referencing")
			upstream = upstream || strings.Contains(content, "not upstream of consumer task 1")
		}
		if !self || !upstream || drafts != 2 {
			return agentruntime.Completion{}, fmt.Errorf("retained correction history lost: self=%t upstream=%t drafts=%d", self, upstream, drafts)
		}
	}
	i := m.next
	m.next++
	return agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{{ID: fmt.Sprintf("dependency-fixture-draft-%d", i+1), Name: "submit_result", Arguments: m.submissions[i]}}}, nil
}

func assertDependencyRecoveryJournal(t testing.TB, entries []agentruntime.Entry) {
	t.Helper()
	errorsSeen := []string{}
	submissions := 0
	finished := 0
	for _, e := range entries {
		switch e.Kind {
		case agentruntime.ModelTurn:
			var turn agentruntime.Completion
			if json.Unmarshal(e.Payload, &turn) != nil {
				t.Fatal("invalid turn")
			}
			for _, c := range turn.ToolCalls {
				if c.Name != "submit_result" {
					t.Fatal("unexpected recovery tool", c.Name)
				}
				submissions++
			}
		case agentruntime.ToolDone:
			var done agentruntime.ToolResult
			if json.Unmarshal(e.Payload, &done) != nil || done.Name != "submit_result" || done.Error == "" {
				t.Fatal("unexpected recovery receipt")
			}
			errorsSeen = append(errorsSeen, done.Error)
		case agentruntime.Finished:
			finished++
		case agentruntime.Failed, agentruntime.ModelRejected, agentruntime.ModelPartial:
			t.Fatal("unexpected terminal/rejected/partial response", string(e.Payload))
		}
	}
	if submissions != 3 || finished != 1 || len(errorsSeen) != 2 || !strings.Contains(errorsSeen[0], "not self-referencing") || !strings.Contains(errorsSeen[1], "not upstream of consumer task 1") {
		t.Fatalf("wrong recovery sequence: submissions=%d finished=%d errors=%v", submissions, finished, errorsSeen)
	}
}

func TestArchitectToPMDependencyFailureAndCorrection(t *testing.T) {
	team := rolefixtures.Current(t)
	root := t.TempDir()
	source, deltas, feature := dependencyRecoveryFixture(t)
	profile := func(name kernel.RoleFQRN) nativeagent.Profile {
		r := team.Roles[slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(name) })]
		return nativeagent.Profile{RoleFQRN: name, RoleBundleDigest: r.Binding.BundleDigest, ModelProfileDigest: r.Binding.ModelProfileDigest, RuntimeIdentityDigest: repeatedDigest('a'), ToolPolicyDigest: repeatedDigest('b'), EffectPolicyDigest: repeatedDigest('c'), BaseURL: "http://127.0.0.1:1/v1", Model: "scripted-not-live", MaxOutputTokens: 32768, MaxTurns: 8}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	architectJournal := &dependencyRecoveryJournal{}
	architect := dependencyRecoverySession(t, ctx, team, profile("architect"), root, architectJournal, http.DefaultClient, nil, source, feature)
	arguments := json.RawMessage(bytes.TrimPrefix(source, []byte(application.OrganizationalResultMarker+"\n")))
	architect.Runner.Model = &dependencyRecoveryModel{submissions: []json.RawMessage{arguments}}
	accepted, err := architect.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	feature.Design.OutputDigest = digestBytes([]byte(accepted))
	journal := &dependencyRecoveryJournal{}
	pm := dependencyRecoverySession(t, ctx, team, profile("project-manager"), root, journal, http.DefaultClient, nil, []byte(accepted), feature)
	pm.Runner.Model = &dependencyRecoveryModel{submissions: deltas, verify: true}
	result, err := pm.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertDependencyRecoveryJournal(t, journal.entries)
	if err := dependencyRecoveryValidator(feature, []byte(accepted))(ctx, []byte(result)); err != nil {
		t.Fatal(err)
	}
	if pm.Runner.MaxFinalSubmissions != 3 || pm.Runner.MaxTurns != 8 {
		t.Fatal("finite budget changed")
	}
	var final architectureStageResult
	final, err = parsePlanFinalizationStageResult([]byte(result), nil)
	if err != nil || final.Tasks[0].DependsOn == nil || len(final.Tasks[0].DependsOn) != 0 || !slices.Equal(final.Tasks[1].DependsOn, []uint32{0}) || !slices.Equal(final.Tasks[2].DependsOn, []uint32{0}) {
		t.Fatal("new or original edge lost", err)
	}
}

func TestDependencyRecoveryDoesNotRelaxFinalPlanValidation(t *testing.T) {
	source, _, feature := dependencyRecoveryFixture(t)
	proposed, err := parseArchitectureStageResult(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*architectureStageResult)
	}{
		{"valid-baseline", func(*architectureStageResult) {}},
		{"technical-task-change", func(p *architectureStageResult) { p.Tasks[1].Description = "invented new work" }},
		{"architecture-change", func(p *architectureStageResult) { p.Architecture = stageText(`"invented architecture"`) }},
		{"existing-edge-removed", func(p *architectureStageResult) { p.Tasks[2].DependsOn = []uint32{} }},
		{"wrong-source-digest", func(p *architectureStageResult) { p.SourceDesignDigest = repeatedDigest('d') }},
		{"cycle", func(p *architectureStageResult) { p.Tasks[0].DependsOn = []uint32{1} }},
		{"missing-handoff-prerequisite", func(p *architectureStageResult) {
			p.Tasks[1].DependsOn = []uint32{}
			p.Handoffs = []architectureHandoffResult{{ProviderTaskIndex: 0, ConsumerTaskIndex: 1, Capability: "provider.payload.value", Contract: "Read the provider public Value field"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var final architectureStageResult
			raw, _ := json.Marshal(proposed)
			if err := json.Unmarshal(raw, &final); err != nil {
				t.Fatal(err)
			}
			final.ResultType = "FEATURE_EXECUTION_PLAN"
			final.SourceDesignDigest = feature.Design.OutputDigest
			final.Tasks[1].DependsOn = []uint32{0}
			final.Handoffs = []architectureHandoffResult{}
			tc.change(&final)
			// The stage DTO omits empty handoffs; the signed final transport
			// requires the explicit array, as the production binder supplies it.
			workJSON, _ := json.Marshal(final)
			var work map[string]json.RawMessage
			if err := json.Unmarshal(workJSON, &work); err != nil {
				t.Fatal(err)
			}
			work["handoffs"], _ = json.Marshal(final.Handoffs)
			envelope, _ := json.Marshal(map[string]any{"schema_version": "1.0.0", "outcome": "completed", "summary": "Finalization", "evidence": []string{}, "message_proposals": []any{}, "work_product": work})
			err := dependencyRecoveryValidator(feature, source)(t.Context(), []byte(application.OrganizationalResultMarker+"\n"+string(envelope)))
			if (err == nil) != (tc.name == "valid-baseline") {
				t.Fatalf("final plan predicate: %v", err)
			}
		})
	}
}
