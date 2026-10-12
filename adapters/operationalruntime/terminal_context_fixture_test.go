package operationalruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/tekroo-ai/teams/internal/rolefixtures"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

// Unlike historical single-stream qualification fixtures, this journal binds
// loads and appends to the exact invocation ID. Frozen ancestors are never
// substituted for a child or silently rewritten by a restart.
type terminalQualificationJournal struct {
	streams   map[string][]agentruntime.Entry
	directory string
}

func (j *terminalQualificationJournal) Load(_ context.Context, id string) ([]agentruntime.Entry, error) {
	return slices.Clone(j.streams[id]), nil
}

func (j *terminalQualificationJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, raw json.RawMessage) (agentruntime.Entry, error) {
	if expected != uint64(len(j.streams[id])) || !json.Valid(raw) {
		return agentruntime.Entry{}, agentruntime.ErrConflict
	}
	e := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: slices.Clone(raw)}
	j.streams[id] = append(j.streams[id], e)
	if j.directory != "" {
		encoded, err := json.MarshalIndent(j.streams[id], "", "  ")
		if err != nil {
			return agentruntime.Entry{}, err
		}
		if err := os.WriteFile(filepath.Join(j.directory, id+"-journal.json"), encoded, 0600); err != nil {
			return agentruntime.Entry{}, err
		}
	}
	return e, nil
}

type terminalQualificationModel struct {
	calls []agentruntime.ToolCall
	next  int
}

func (m *terminalQualificationModel) Complete(context.Context, []agentruntime.Message) (agentruntime.Completion, error) {
	if m.next >= len(m.calls) {
		return agentruntime.Completion{}, errors.New("unexpected fixture model turn")
	}
	c := m.calls[m.next]
	m.next++
	return agentruntime.Completion{ToolCalls: []agentruntime.ToolCall{c}}, nil
}

func terminalQualificationBrief(t testing.TB, team organization.LoadedTeam, p nativeagent.Profile, id kernel.UUIDv7, root, description string) application.ExecutionBrief {
	t.Helper()
	i := slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(p.RoleFQRN) })
	if i < 0 {
		t.Fatal("missing signed role")
	}
	r := team.Roles[i]
	actor := kernel.ActorFQN("teams::" + string(p.RoleFQRN) + "-1")
	b := application.ExecutionBrief{ContractManifest: kernel.ContractIdentity, InvocationID: id, ActorFQN: actor, Purpose: kernel.PurposeHandoff,
		ModelProfileDigest: p.ModelProfileDigest, RuntimeIdentityDigest: p.RuntimeIdentityDigest, ToolPolicyDigest: p.ToolPolicyDigest, EffectPolicyDigest: p.EffectPolicyDigest,
		RoleGrounding:  application.RoleExecutionGrounding{ActorFQN: actor, RoleFQRN: p.RoleFQRN, BundleVersion: r.Bundle.Version, BundleDigest: p.RoleBundleDigest, Capabilities: r.Bundle.Capabilities, Permissions: r.Bundle.Permissions, Instructions: r.Package.Charter},
		Task:           application.TaskExecutionSpecification{TaskID: id, SourceDigest: digestBytes([]byte("terminal handback qualification")), Title: "Frozen completed-submission handback qualification", Description: description, AcceptanceCriteria: []string{"Read the exact frozen completion, retain its decision, preserve original bytes and finite budgets."}},
		ResultProtocol: &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Instruction: "Submit the structured result through submit_result."}, DeadlineAt: time.Now().UTC().Add(time.Hour), RemainingGlobalBudget: 3, RemainingPurposeBudget: 3}
	b.Scope.WorkspaceID = root
	return b
}

func terminalQualificationConfig(team organization.LoadedTeam, p nativeagent.Profile, root string, j agentruntime.Journal, client *http.Client, policy *agentruntime.ContextDeliveryPolicy) nativeagent.Config {
	i := slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(p.RoleFQRN) })
	binding := dependencyRecoveryBinding{agenttools.Authority{WorkspaceRoot: root, Permissions: team.Roles[i].Bundle.Permissions, Purpose: kernel.PurposeHandoff, EffectPolicyDigest: p.EffectPolicyDigest}}
	return nativeagent.Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Minute}}, Journal: j, HTTP: client, ContextDelivery: policy, Profile: p}
}

func terminalQualificationSession(t testing.TB, ctx context.Context, b application.ExecutionBrief, cfg nativeagent.Config) nativeagent.Session {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	s, err := nativeagent.PrepareReadOnly(ctx, b, digestBytes(raw), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// Kernel-valid completed-source fixture. It grants only same-actor, same-lane
// history access after the native binding independently checks the actual
// Finished output and the child's admitted evidence/return lineage.
func terminalQualificationInvocation(t testing.TB, b application.ExecutionBrief, s nativeagent.Session, output string) kernel.WorkInvocation {
	t.Helper()
	id, digest, finished := b.InvocationID, kernel.Digest(s.RequestDigest), time.Now().UTC()
	out := digestBytes([]byte(output))
	v := kernel.WorkInvocation{ID: id, Revision: 4, State: kernel.InvocationSucceeded, AuthorizationEventID: id, ParentEventID: id,
		TaskID: b.Task.TaskID, BudgetAccountID: id, LifecycleEpoch: 1, ScopeRevision: 1, TaskRevision: 1,
		WorkProfile:           kernel.WorkProfileBinding{ProfileID: id, ProfileRevision: 1, ProfileDigest: digest, LifecycleEpoch: 1, ScopeRevision: 1},
		QualifiedAssignmentID: id, Purpose: b.Purpose, AttemptFamily: "qualification", AttemptOrdinal: 1, ConditionDigest: digest, OutputPredicateDigest: digest,
		AllowedTerminalOutcomes: []kernel.WorkInvocationState{kernel.InvocationSucceeded}, ToolPolicyDigest: b.ToolPolicyDigest, EffectPolicyDigest: b.EffectPolicyDigest,
		ActorFQN: b.ActorFQN, Execution: kernel.ExecutionTuple{ExecutionID: id, FencingEpoch: 1}, ModelProfileDigest: b.ModelProfileDigest, RuntimeIdentityDigest: b.RuntimeIdentityDigest,
		WorkspaceID: b.Scope.WorkspaceID, DeadlineAt: b.DeadlineAt, IdempotencyKey: "terminal-source", AdmissionPolicyRevision: 1, AdmissionPolicyDigest: digest,
		GlobalDebitOrdinal: 1, PurposeDebitOrdinal: 1, RequestDigest: &digest, OutputDigest: &out, FinishedAt: &finished, LastEventID: id}
	if !v.Valid() {
		t.Fatal("invalid completed-source fixture")
	}
	return v
}

func terminalQualificationReturn(t testing.TB, ctx context.Context, j agentruntime.Journal, b *application.ExecutionBrief, source kernel.WorkInvocation, correction kernel.EvidenceRef, cfg *nativeagent.Config) {
	t.Helper()
	b.Evidence = []kernel.EvidenceRef{{EvidenceID: source.ID, SHA256: *source.OutputDigest}, correction}
	returned := &nativeagent.ContextReturnBinding{SourceInvocationID: source.ID, SourceTaskID: source.TaskID, ChildInvocationID: b.InvocationID, ChildTaskID: b.Task.TaskID, CorrectionEvidence: []kernel.EvidenceRef{correction}}
	ref, err := nativeagent.BindContextFork(ctx, j, *b, source, returned)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ContextSource, cfg.ContextInvocation, cfg.ContextReturn = ref, &source, returned
}

func terminalQualificationObservation(t testing.TB, fork *agentruntime.ContextFork) agentruntime.Message {
	t.Helper()
	for i := len(fork.WorkingHistory) - 1; i >= 0; i-- {
		m := fork.WorkingHistory[i]
		if m.Role == "tool" && m.Name == "submit_result" && m.Receipt != nil {
			return m
		}
	}
	t.Fatal("completed submission lacks source reference")
	return agentruntime.Message{}
}

func terminalQualificationRead(t testing.TB, ref *agentruntime.RecordedResultRef, pointer, id string) agentruntime.ToolCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"invocation_id": ref.InvocationID, "sequence": ref.Sequence, "sha256": ref.SHA256, "json_pointer": pointer})
	if err != nil {
		t.Fatal(err)
	}
	return agentruntime.ToolCall{ID: id, Name: "read_recorded_result", Arguments: raw}
}

func assertTerminalQualificationReads(t testing.TB, entries []agentruntime.Entry, ref *agentruntime.RecordedResultRef, pointers []string, expectedErrors int) {
	t.Helper()
	requests, observed := map[string]string{}, map[string]bool{}
	failures, finish := 0, 0
	for _, e := range entries {
		switch e.Kind {
		case agentruntime.ModelTurn:
			var c agentruntime.Completion
			if json.Unmarshal(e.Payload, &c) != nil {
				t.Fatal("invalid model turn")
			}
			for _, call := range c.ToolCalls {
				if call.Name != "read_recorded_result" {
					continue
				}
				var a struct {
					InvocationID string `json:"invocation_id"`
					Sequence     uint64
					SHA256       string
					Pointer      string `json:"json_pointer"`
				}
				if json.Unmarshal(call.Arguments, &a) != nil {
					t.Fatal("invalid read")
				}
				if a.InvocationID == ref.InvocationID && a.Sequence == ref.Sequence && a.SHA256 == ref.SHA256 {
					requests[call.ID] = a.Pointer
				}
			}
		case agentruntime.ToolDone:
			var d agentruntime.ToolResult
			if json.Unmarshal(e.Payload, &d) != nil {
				t.Fatal("invalid receipt")
			}
			if d.Error != "" {
				failures++
				continue
			}
			if pointer, ok := requests[d.CallID]; ok {
				observed[pointer] = true
			}
		case agentruntime.Finished:
			finish++
		case agentruntime.Failed, agentruntime.ModelRejected, agentruntime.ModelPartial:
			t.Fatal("unexpected qualification failure/continuation", string(e.Payload))
		}
	}
	if failures != expectedErrors || finish != 1 {
		t.Fatalf("wrong failure/completion count: %d/%d", failures, finish)
	}
	for _, p := range pointers {
		if !observed[p] {
			t.Fatal("exact terminal evidence was not read", p)
		}
	}
}

func TestArchitectPMDecisionReturnWithLargeFinishedReceipt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	team := rolefixtures.Current(t)
	profile := func(name kernel.RoleFQRN) nativeagent.Profile {
		r := team.Roles[slices.IndexFunc(team.Roles, func(r organization.LoadedRole) bool { return r.Binding.Role == string(name) })]
		return nativeagent.Profile{RoleFQRN: name, RoleBundleDigest: r.Binding.BundleDigest, ModelProfileDigest: r.Binding.ModelProfileDigest, RuntimeIdentityDigest: repeatedDigest('a'), ToolPolicyDigest: repeatedDigest('b'), EffectPolicyDigest: repeatedDigest('c'), BaseURL: "http://127.0.0.1:1/v1", Model: "scripted-not-live", MaxOutputTokens: 32768, MaxTurns: 8}
	}
	runTerminalArchitectPMQualification(t, ctx, team, profile("architect"), profile("project-manager"), nil, t.TempDir(), &terminalQualificationJournal{streams: map[string][]agentruntime.Entry{}}, http.DefaultClient, false)
}

// Shared deterministic/live gate: real signed result handlers, PM disposition,
// workflow routing, structured feedback and native same-owner context binding.
// Parent seeding is explicitly synthetic; it is never a historical run retry.
func runTerminalArchitectPMQualification(t testing.TB, ctx context.Context, team organization.LoadedTeam, architect, pm nativeagent.Profile, policy *agentruntime.ContextDeliveryPolicy, root string, j *terminalQualificationJournal, client *http.Client, live bool) {
	t.Helper()
	source, _, feature := dependencyRecoveryFixture(t)
	var plan map[string]any
	if json.Unmarshal(bytes.TrimPrefix(source, []byte(application.OrganizationalResultMarker+"\n")), &plan) != nil {
		t.Fatal("invalid plan fixture")
	}
	marker := string(digestBytes([]byte(root + " unique original architect decision")))
	// Large accepted completion, not a large ordinary ToolDone. Padding is
	// synthetic prior summary text, never a required output or current authority.
	plan["summary"] = "Original source marker=" + marker + "; " + strings.Repeat("retained-fixture ", 1100)
	parentArgs, _ := json.Marshal(plan)
	id := deterministicOperationalUUID("terminal-architect-parent", root)
	b := terminalQualificationBrief(t, team, architect, id, root, "Synthetic completed architect fixture; preserve the supplied typed proposal. No edits or test execution.")
	handler := rolefixtures.Handler(t, team, "architect", "tekroo.message.story.design-requested")
	b.MessageHandler = &handler
	cfg := terminalQualificationConfig(team, architect, root, j, client, policy)
	cfg.ResultArrayLimits = featureStageResultArrayLimits(stageArchitecture, 16)
	cfg.ValidateFinalResult = func(_ context.Context, output []byte) error {
		return (&ProductionService{}).validateFeatureStageOutput(feature, stageArchitecture, output)
	}
	parent := terminalQualificationSession(t, ctx, b, cfg)
	parent.Runner.Model = &terminalQualificationModel{calls: []agentruntime.ToolCall{{ID: "accepted-architect-fixture", Name: "submit_result", Arguments: parentArgs}}}
	accepted, err := parent.Run(ctx)
	if err != nil {
		t.Fatal("synthetic signed architect fixture", err)
	}
	prior, _ := json.Marshal(j.streams[parent.InvocationID])
	feature.Design.OutputDigest = digestBytes([]byte(accepted))
	pmID := deterministicOperationalUUID("terminal-pm-disposition", root)
	pmBrief := terminalQualificationBrief(t, team, pm, pmID, root, "Synthetic typed needs_decision boundary qualification, not feature work. Return outcome needs_decision and summary 'Please clarify the original architecture decision using the retained source marker'. Follow the declared plan-finalization tool contract: work_product.task_dependencies=[] and work_product.handoffs=[] for this non-completion disposition. No completed plan, edits, tests or alternate architecture is authorized.")
	pmHandler := rolefixtures.Handler(t, team, "project-manager", "tekroo.message.feature.design-proposed")
	pmBrief.MessageHandler = &pmHandler
	pmCfg := terminalQualificationConfig(team, pm, root, j, client, policy)
	pmCfg.PlanFinalization = &nativeagent.PlanFinalizationBinding{SourceOutput: []byte(accepted), SourceDigest: feature.Design.OutputDigest}
	pmSession := terminalQualificationSession(t, ctx, pmBrief, pmCfg)
	if !live {
		pmSession.Runner.Model = &terminalQualificationModel{calls: []agentruntime.ToolCall{{ID: "pm-needs-decision", Name: "submit_result", Arguments: json.RawMessage(`{"outcome":"needs_decision","summary":"Please clarify the original architecture decision using the retained source marker","evidence":[],"message_proposals":[],"work_product":{"task_dependencies":[],"handoffs":[]}}`)}}}
	}
	decision, err := pmSession.Run(ctx)
	if err != nil {
		raw, _ := json.Marshal(j.streams[pmSession.InvocationID])
		t.Fatal("PM disposition", err, string(raw))
	}
	parsed, err := application.ValidateRoleHandlerResult(pmHandler, []byte(decision))
	var disposition struct {
		Summary string `json:"summary"`
	}
	if err != nil || json.Unmarshal(parsed.Raw, &disposition) != nil || !mayRoutePMDesignRevision(feature, stagePlanFinalization, parsed.Outcome, disposition.Summary) {
		t.Fatal("PM decision not legitimately routable", err, decision)
	}
	feature.DesignRevision = &organization.FeatureDesignRevision{PriorDesignDigest: feature.Design.OutputDigest, DecisionDigest: digestBytes([]byte(decision)), Reason: disposition.Summary, RequestedAt: time.Now().UTC()}
	feature.Status = organization.FeatureSpecified
	definition := terminalFixtureWorkflow(t)
	if stage, ok := workflowStageForFeature(feature, definition); !ok || stage != "revise-design" {
		t.Fatal("needs_decision did not route to architect revision")
	}
	if _, err := designRevisionFeedback(*feature.DesignRevision, []byte(accepted)); err != nil {
		t.Fatal("structured revision feedback", err)
	}
	childID := deterministicOperationalUUID("terminal-architect-return", root)
	child := terminalQualificationBrief(t, team, architect, childID, root, "Synthetic same-owner handback qualification. The original completed architect context is retained. Read /historical_result/summary from its frozen Finished reference with read_recorded_result. Your new summary MUST be exactly the 64 lowercase hexadecimal characters after 'Original source marker=' and before the semicolon. Do not include that label, a semicolon, quotation marks, whitespace or explanation in the summary value. The marker is not in this assignment or schema. Submit the same technical work_product as the original; do not echo synthetic prior summary padding. No edits or test execution. PM reason: "+disposition.Summary)
	child.MessageHandler = &handler
	childCfg := terminalQualificationConfig(team, architect, root, j, client, policy)
	childCfg.ResultArrayLimits = featureStageResultArrayLimits(stageArchitecture, 16)
	childCfg.ValidateFinalResult = func(_ context.Context, output []byte) error {
		if err := (&ProductionService{}).validateFeatureStageOutput(feature, stageArchitecture, output); err != nil {
			return err
		}
		var result map[string]json.RawMessage
		if json.Unmarshal(bytes.TrimPrefix(output, []byte(application.OrganizationalResultMarker+"\n")), &result) != nil {
			return errors.New("invalid corrected fixture")
		}
		var summary string
		if json.Unmarshal(result["summary"], &summary) != nil || summary != marker {
			return errors.New("original marker was not restored exactly")
		}
		var product any
		if json.Unmarshal(result["work_product"], &product) != nil {
			return errors.New("missing technical product")
		}
		want, _ := json.Marshal(plan["work_product"])
		got, _ := json.Marshal(product)
		if !bytes.Equal(want, got) {
			return errors.New("original technical content changed")
		}
		return nil
	}
	invocation := terminalQualificationInvocation(t, b, parent, accepted)
	terminalQualificationReturn(t, ctx, j, &child, invocation, kernel.EvidenceRef{EvidenceID: pmID, SHA256: digestBytes([]byte(decision))}, &childCfg)
	returned := terminalQualificationSession(t, ctx, child, childCfg)
	observation := terminalQualificationObservation(t, returned.Runner.ContextFork)
	if len(observation.Content) <= agentruntime.DefaultContextDeliveryPolicy().ResultBytes {
		t.Fatal("qualification missed oversized completed submission")
	}
	if !live {
		plan["summary"] = marker
		corrected, _ := json.Marshal(plan)
		returned.Runner.Model = &terminalQualificationModel{calls: []agentruntime.ToolCall{terminalQualificationRead(t, observation.Receipt, "/historical_result/summary", "read-original-summary"), {ID: "corrected-architect", Name: "submit_result", Arguments: corrected}}}
	}
	if _, err := returned.Run(ctx); err != nil {
		t.Fatal("returned architect", err)
	}
	assertTerminalQualificationReads(t, j.streams[returned.InvocationID], observation.Receipt, []string{"/historical_result/summary"}, 0)
	after, _ := json.Marshal(j.streams[parent.InvocationID])
	if !bytes.Equal(prior, after) {
		t.Fatal("accepted architect bytes changed")
	}
	if parent.Runner.MaxFinalSubmissions != 3 || returned.Runner.MaxFinalSubmissions != 3 || returned.Runner.MaxTurns != architect.MaxTurns {
		t.Fatal("finite allowances changed")
	}
	// Completed-child restart must return the recorded result, not ask the
	// provider for another decision or execute any historical submission.
	returned.Runner.Model = &terminalQualificationModel{}
	if _, err := returned.Run(ctx); err != nil {
		t.Fatal("completed handback restart", err)
	}
}

func terminalFixtureWorkflow(t testing.TB) kernel.WorkflowDefinition {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "config", "workflows", "software-development.v2.1.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var declared kernel.WorkflowDefinition
	if json.Unmarshal(raw, &declared) != nil {
		t.Fatal("invalid workflow fixture")
	}
	definition, err := organization.LoadWorkflowDefinition(path, declared.ContentDigest)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
