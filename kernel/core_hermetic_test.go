package kernel_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestAllFrozenCommandsReachTheirDeclaredEventThroughEvaluator(t *testing.T) {
	type fixture struct {
		FixtureID string `json:"fixtureId"`
		When      struct {
			CommandType string          `json:"commandType"`
			Payload     json.RawMessage `json:"payload"`
		} `json:"when"`
		Then struct {
			Expected struct {
				EventTypes []string `json:"eventTypes"`
			} `json:"expected"`
		} `json:"then"`
	}
	type fixtureDocument struct {
		Fixtures []fixture `json:"fixtures"`
	}
	type entry struct {
		TypeID            string                 `json:"typeId"`
		Kind              string                 `json:"kind"`
		TargetKinds       []kernel.AggregateKind `json:"targetKinds"`
		AuthorityKinds    []kernel.PrincipalKind `json:"authorityKinds"`
		ExecutionRequired bool                   `json:"executionRequired"`
		RootAllowed       bool                   `json:"rootAllowed"`
	}
	type catalogueDocument struct {
		Entries []entry `json:"entries"`
	}

	root := testRepositoryRoot(t)
	fixtureBytes, err := os.ReadFile(filepath.Join(root, "CONTRACTS/tekroo.kernel.contracts/0.11.0/fixtures/catalogue-coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalogueBytes, err := os.ReadFile(filepath.Join(root, "CONTRACTS/tekroo.kernel.contracts/0.11.0/catalogue/kernel-catalogue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures fixtureDocument
	if err := json.Unmarshal(fixtureBytes, &fixtures); err != nil {
		t.Fatal(err)
	}
	var catalogue catalogueDocument
	if err := json.Unmarshal(catalogueBytes, &catalogue); err != nil {
		t.Fatal(err)
	}
	entries := make(map[string]entry)
	for _, definition := range catalogue.Entries {
		if definition.Kind == "COMMAND" {
			entries[definition.TypeID] = definition
		}
	}

	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	executed := 0
	applied := 0
	for _, item := range fixtures.Fixtures {
		if !strings.HasSuffix(item.FixtureID, "-VALID") {
			continue
		}
		if strings.Contains(item.FixtureID, "PHASE4") {
			continue
		}
		item := item
		t.Run(item.FixtureID, func(t *testing.T) {
			definition, found := entries[item.When.CommandType]
			if !found || len(definition.TargetKinds) == 0 || len(definition.AuthorityKinds) == 0 || len(item.Then.Expected.EventTypes) != 1 {
				t.Fatalf("incomplete frozen definition for %s", item.When.CommandType)
			}
			command, snapshot := commandCase(t, item.When.CommandType, item.When.Payload, definition.TargetKinds[0], definition.AuthorityKinds[0], definition.ExecutionRequired, definition.RootAllowed)
			decision := evaluate(t, evaluator, command, snapshot, validDecisionContext(t))
			if decision.Receipt.OutcomeCode != kernel.OutcomeApplied || len(decision.Events) != 1 || decision.Events[0].EventType != item.Then.Expected.EventTypes[0] {
				t.Fatalf("target=%q expected_lifecycle_epoch=%v decision = %#v", command.Target.Kind, command.ExpectedLifecycleEpoch, decision)
			}
			applied++
		})
		executed++
	}
	if executed != 56 {
		t.Fatalf("executed command cases = %d, want 56", executed)
	}
	if applied != 56 {
		t.Fatalf("applied = %d, want 56", applied)
	}
}

func TestExecutionFencingRequiresExactCurrentTuple(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command, snapshot := activeTaskCommand(t)

	applied := evaluate(t, evaluator, command, snapshot, validDecisionContext(t))
	if applied.Receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("current execution outcome = %s, want APPLIED", applied.Receipt.OutcomeCode)
	}

	stale := command
	stale.Execution = &kernel.ExecutionTuple{
		ExecutionID:  mustUUID(t, "00000000-0000-7000-8000-000000000099"),
		FencingEpoch: 1,
	}
	rejected := evaluate(t, evaluator, stale, snapshot, validDecisionContext(t))
	if rejected.Receipt.OutcomeCode != kernel.OutcomeRejectedStaleExecution || len(rejected.Events) != 0 {
		t.Fatalf("stale execution decision = %#v", rejected)
	}
}

func TestDispatchRequiresExactQualifiedAssignmentAuthorization(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	payload := json.RawMessage(`{"destination":"teams::coder-1","routing_mode":"EXACT"}`)
	command, snapshot := commandCase(t, "tekroo.command.task.dispatch", payload, kernel.AggregateTask, kernel.PrincipalPolicy, false, false)
	if decision := evaluate(t, evaluator, command, snapshot, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("authorized dispatch = %#v", decision)
	}

	missing := snapshot
	missing.QualifiedAssignments = map[kernel.AggregateRef]kernel.QualifiedAssignmentAuthorization{}
	if decision := evaluate(t, evaluator, command, missing, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedPolicy || len(decision.Events) != 0 {
		t.Fatalf("dispatch without authorization = %#v", decision)
	}

	stale := snapshot
	authorization := stale.QualifiedAssignments[command.Target]
	stale.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{authorization.SelectedActorFQN: {ExecutionID: authorization.SelectedExecutionID, FencingEpoch: authorization.SelectedFencingEpoch + 1}}
	if decision := evaluate(t, evaluator, command, stale, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedStaleExecution || len(decision.Events) != 0 {
		t.Fatalf("dispatch with stale fence = %#v", decision)
	}
}

func TestEscalationOpenPolicyFencesSemanticDuplicateAndSubjectLifecycle(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	payload := json.RawMessage(`{"adjudicator":{"id":"principal-adjudicator","kind":"HUMAN"},"causal_path_event_ids":["00000000-0000-7000-8000-000000000702"],"deadline_at":"2026-08-12T00:00:00Z","escalation_id":"00000000-0000-7000-8000-000000000701","escalation_policy_revision":1,"evidence_ids":["00000000-0000-7000-8000-000000000703"],"expected_subject_revision":7,"resolution_owner_fqn":"teams::coder-1","resolution_round_limit":1,"route_limit":1,"subject_id":"00000000-0000-7000-8000-000000000101","subject_kind":"task","subject_lifecycle_epoch":1,"timeout_policy":{"id":"escalation-timeout-policy","kind":"POLICY"},"trigger":"HANDOFF_CYCLE_DETECTED","triggering_condition_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","unresolved_question":"Which directed successor resolves the detected handoff cycle?"}`)
	command, snapshot := commandCase(t, "tekroo.command.escalation.open", payload, kernel.AggregateEscalation, kernel.PrincipalPolicy, false, false)
	if decision := evaluate(t, evaluator, command, snapshot, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeApplied || decision.Events[0].EventType != "tekroo.event.escalation.opened" {
		t.Fatalf("opening decision = %#v", decision)
	}

	escalation, err := kernel.EscalationFromOpenPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := snapshot
	duplicate.EscalationKeys = map[kernel.EscalationKey]kernel.AggregateRef{escalation.Key(): command.Target}
	if decision := evaluate(t, evaluator, command, duplicate, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict || decision.Receipt.ReasonCode != "ESCALATION_ALREADY_EXISTS" || len(decision.Events) != 0 {
		t.Fatalf("duplicate decision = %#v", decision)
	}

	stale := snapshot
	stale.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{}
	for subject, related := range snapshot.Related {
		copy := *related.State
		copy.LifecycleEpoch++
		related.State = &copy
		stale.Related[subject] = related
	}
	if decision := evaluate(t, evaluator, command, stale, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedConflict || decision.Receipt.ReasonCode != "STALE_LIFECYCLE_EPOCH" || len(decision.Events) != 0 {
		t.Fatalf("stale lifecycle decision = %#v", decision)
	}
}

func TestEscalationResolutionUsesTrustedDecisionTimeAndExactAdjudicator(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	payload := json.RawMessage(`{"decided_at":"2026-08-11T12:00:00Z","escalation_id":"00000000-0000-7000-8000-000000000701","evidence_ids":["00000000-0000-7000-8000-000000000703"],"expected_escalation_revision":1,"outcome":"RESOLVED","reasons":["A directed successor was selected."],"round":1,"source_role":"ADJUDICATOR","subject_id":"00000000-0000-7000-8000-000000000101","subject_kind":"task","subject_lifecycle_epoch":1}`)
	command, snapshot := commandCase(t, "tekroo.command.escalation.resolve", payload, kernel.AggregateEscalation, kernel.PrincipalActor, false, false)
	if decision := evaluate(t, evaluator, command, snapshot, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeApplied || decision.Events[0].EventType != "tekroo.event.escalation.resolved" {
		t.Fatalf("resolution decision = %#v", decision)
	}

	wrongAuthority := snapshot
	wrongAuthority.Escalations = make(map[kernel.AggregateRef]kernel.EscalationSnapshot, len(snapshot.Escalations))
	for ref, escalation := range snapshot.Escalations {
		escalation.Adjudicator = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "other-adjudicator"}
		wrongAuthority.Escalations[ref] = escalation
	}
	if decision := evaluate(t, evaluator, command, wrongAuthority, validDecisionContext(t)); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedPolicy || decision.Receipt.ReasonCode != "ADJUDICATOR_MISMATCH" || len(decision.Events) != 0 {
		t.Fatalf("wrong adjudicator decision = %#v", decision)
	}

	afterDeadline := validDecisionContext(t)
	afterDeadline.DecidedAt = time.Date(2026, time.August, 12, 0, 0, 0, 1, time.UTC)
	if decision := evaluate(t, evaluator, command, snapshot, afterDeadline); decision.Receipt.OutcomeCode != kernel.OutcomeRejectedPolicy || decision.Receipt.ReasonCode != "ADJUDICATOR_DEADLINE_EXCEEDED" || len(decision.Events) != 0 {
		t.Fatalf("late adjudication decision = %#v", decision)
	}
}

func TestDAGParentsMustExistAndAreCanonicalized(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := storyAuthorizeCommand(t)
	first := mustUUID(t, "00000000-0000-7000-8000-000000000011")
	second := mustUUID(t, "00000000-0000-7000-8000-000000000012")
	command.Causation = []kernel.DagParent{
		{ParentEventID: second, EdgeKind: kernel.EdgeResponse},
		{ParentEventID: first, EdgeKind: kernel.EdgeCausal},
	}
	snapshot := storySnapshot(command.Target, kernel.PhaseDraft, 1)
	snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{
		first:  {EventType: "tekroo.event.story.created"},
		second: {EventType: "tekroo.event.evidence.registered"},
	}

	decision := evaluate(t, evaluator, command, snapshot, validDecisionContext(t))
	if decision.Receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("valid DAG decision = %#v", decision.Receipt)
	}
	want := []kernel.DagParent{
		{ParentEventID: first, EdgeKind: kernel.EdgeCausal},
		{ParentEventID: second, EdgeKind: kernel.EdgeResponse},
	}
	if !reflect.DeepEqual(decision.Events[0].Parents, want) {
		t.Fatalf("parents = %#v, want %#v", decision.Events[0].Parents, want)
	}

	delete(snapshot.AcceptedEvents, first)
	rejected := evaluate(t, evaluator, command, snapshot, validDecisionContext(t))
	if rejected.Receipt.OutcomeCode != kernel.OutcomeRejectedInvalid || rejected.Receipt.ReasonCode != "INVALID_DAG_PARENT" {
		t.Fatalf("missing-parent receipt = %#v", rejected.Receipt)
	}
}

func TestEvidenceReferenceRequiresExactAvailableRegistration(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := storyAuthorizeCommand(t)
	parentID := mustUUID(t, "00000000-0000-7000-8000-000000000011")
	evidenceID := mustUUID(t, "00000000-0000-7000-8000-000000000021")
	digest := mustDigest(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	command.Causation = []kernel.DagParent{{ParentEventID: parentID, EdgeKind: kernel.EdgeCausal}}
	command.EvidenceRefs = []kernel.EvidenceRef{{EvidenceID: evidenceID, SHA256: digest}}
	snapshot := storySnapshot(command.Target, kernel.PhaseDraft, 1)
	snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{parentID: {EventType: "tekroo.event.story.created"}}
	snapshot.Evidence = map[kernel.UUIDv7]kernel.EvidenceMetadata{evidenceID: {SHA256: digest, Available: false}}

	rejected := evaluate(t, evaluator, command, snapshot, validDecisionContext(t))
	if rejected.Receipt.OutcomeCode != kernel.OutcomeRejectedInvalid || rejected.Receipt.ReasonCode != "INVALID_EVIDENCE_REFERENCE" {
		t.Fatalf("unavailable evidence receipt = %#v", rejected.Receipt)
	}
	snapshot.Evidence[evidenceID] = kernel.EvidenceMetadata{SHA256: digest, Available: true}
	applied := evaluate(t, evaluator, command, snapshot, validDecisionContext(t))
	if applied.Receipt.OutcomeCode != kernel.OutcomeApplied {
		t.Fatalf("registered evidence outcome = %s", applied.Receipt.OutcomeCode)
	}
}

func TestUnknownTypeAndUnsupportedVersionHaveDistinctStableReasons(t *testing.T) {
	evaluator := kernel.Evaluator{Catalogue: loadCatalogue(t)}
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.unknown.operation"
	unknown := evaluate(t, evaluator, command, kernel.Snapshot{}, validDecisionContext(t))
	if unknown.Receipt.ReasonCode != "UNKNOWN_COMMAND_TYPE" || len(unknown.Events) != 0 {
		t.Fatalf("unknown command receipt = %#v", unknown.Receipt)
	}
	command = validStoryCreateCommand(t)
	command.CommandVersion = "9.9.9"
	unsupported := evaluate(t, evaluator, command, kernel.Snapshot{}, validDecisionContext(t))
	if unsupported.Receipt.ReasonCode != "UNSUPPORTED_COMMAND_VERSION" || len(unsupported.Events) != 0 {
		t.Fatalf("unsupported version receipt = %#v", unsupported.Receipt)
	}
}

func TestSemanticFingerprintIgnoresTransportFieldsAndSetOrder(t *testing.T) {
	command := validStoryCreateCommand(t)
	firstParent := kernel.DagParent{ParentEventID: mustUUID(t, "00000000-0000-7000-8000-000000000011"), EdgeKind: kernel.EdgeCausal}
	secondParent := kernel.DagParent{ParentEventID: mustUUID(t, "00000000-0000-7000-8000-000000000012"), EdgeKind: kernel.EdgeResponse}
	command.Causation = []kernel.DagParent{secondParent, firstParent}
	first, err := kernel.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	changedTransport := command
	changedTransport.CommandID = mustUUID(t, "00000000-0000-7000-8000-000000000031")
	changedTransport.CorrelationID = mustUUID(t, "00000000-0000-7000-8000-000000000032")
	issued := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	changedTransport.IssuedAt = &issued
	changedTransport.Causation = []kernel.DagParent{firstParent, secondParent}
	second, err := kernel.CommandFingerprint(changedTransport)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("transport-only changes altered fingerprint: %s != %s", first, second)
	}
	changedTransport.Payload = json.RawMessage(`{"acceptance_criteria":["changed"],"description":"Exact ownership.","title":"Ownership"}`)
	third, err := kernel.CommandFingerprint(changedTransport)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("semantic payload change did not alter fingerprint")
	}
	contextChanges := []kernel.KernelCommand{command, command, command}
	contextChanges[0].ExpectedPolicyRevision++
	contextChanges[1].ExpectedCatalogueRevision++
	epoch := uint64(1)
	contextChanges[2].ExpectedLifecycleEpoch = &epoch
	for index, changed := range contextChanges {
		digest, fingerprintErr := kernel.CommandFingerprint(changed)
		if fingerprintErr != nil {
			t.Fatal(fingerprintErr)
		}
		if digest == first {
			t.Fatalf("expected decision context change %d did not alter fingerprint", index)
		}
	}
	malformedA := command
	malformedA.Payload = json.RawMessage(`{} trailing-a`)
	malformedB := command
	malformedB.Payload = json.RawMessage(`{} trailing-b`)
	fourth, err := kernel.CommandFingerprint(malformedA)
	if err != nil {
		t.Fatal(err)
	}
	fifth, err := kernel.CommandFingerprint(malformedB)
	if err != nil {
		t.Fatal(err)
	}
	if fourth == fifth {
		t.Fatal("distinct malformed raw inputs collapsed to one fingerprint")
	}
}

func TestLifecycleTransitionTableIsExhaustive(t *testing.T) {
	forwardActions := []kernel.LifecycleAction{
		kernel.ActionAuthorize,
		kernel.ActionBeginPlanning,
		kernel.ActionMarkReady,
		kernel.ActionActivate,
		kernel.ActionComplete,
		kernel.ActionAccept,
	}
	storyPhases := []kernel.Phase{kernel.PhaseDraft, kernel.PhaseReady, kernel.PhasePlanning, kernel.PhaseActive, kernel.PhaseCompleted, kernel.PhaseAccepted, kernel.PhaseClosed}
	taskPhases := []kernel.Phase{kernel.PhasePlanned, kernel.PhaseReady, kernel.PhaseActive, kernel.PhaseCompleted, kernel.PhaseClosed}
	storyLegal := map[[2]string]kernel.Phase{
		{string(kernel.PhaseDraft), string(kernel.ActionAuthorize)}:     kernel.PhaseReady,
		{string(kernel.PhaseReady), string(kernel.ActionBeginPlanning)}: kernel.PhasePlanning,
		{string(kernel.PhasePlanning), string(kernel.ActionActivate)}:   kernel.PhaseActive,
		{string(kernel.PhaseActive), string(kernel.ActionComplete)}:     kernel.PhaseCompleted,
		{string(kernel.PhaseCompleted), string(kernel.ActionAccept)}:    kernel.PhaseAccepted,
	}
	taskLegal := map[[2]string]kernel.Phase{
		{string(kernel.PhasePlanned), string(kernel.ActionMarkReady)}: kernel.PhaseReady,
		{string(kernel.PhaseReady), string(kernel.ActionActivate)}:    kernel.PhaseActive,
		{string(kernel.PhaseActive), string(kernel.ActionComplete)}:   kernel.PhaseCompleted,
	}
	assertTransitionMatrix(t, kernel.LifecycleStory, storyPhases, forwardActions, storyLegal)
	assertTransitionMatrix(t, kernel.LifecycleTask, taskPhases, forwardActions, taskLegal)
}

func TestGeneratedLifecycleHistoriesMatchIndependentModel(t *testing.T) {
	const seed uint64 = 0x5eedc0de
	generator := seed
	actions := []kernel.LifecycleAction{
		kernel.ActionAuthorize, kernel.ActionBeginPlanning, kernel.ActionMarkReady,
		kernel.ActionActivate, kernel.ActionComplete, kernel.ActionAccept,
		kernel.ActionBlock, kernel.ActionUnblock, kernel.ActionReopen, kernel.ActionClose,
	}
	for history := 0; history < 4096; history++ {
		model := kernel.LifecycleStory
		initial := kernel.LifecycleState{Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, LifecycleEpoch: 1}
		if nextRandom(&generator)&1 == 1 {
			model = kernel.LifecycleTask
			initial.Phase = kernel.PhasePlanned
		}
		length := int(nextRandom(&generator)%24) + 1
		sequence := make([]kernel.LifecycleAction, length)
		for index := range sequence {
			sequence[index] = actions[nextRandom(&generator)%uint64(len(actions))]
		}
		got := kernel.ApplyLifecycleActions(model, initial, sequence)
		want := referenceLifecycle(model, initial, sequence)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed=%x history=%d actions=%v\ngot=%#v\nwant=%#v", seed, history, sequence, got, want)
		}
	}
}

func TestGeneratedDAGsAcceptOnlyAcyclicExistingNodes(t *testing.T) {
	const seed uint64 = 0xda6ac1c
	generator := seed
	for history := 0; history < 2048; history++ {
		nodeCount := int(nextRandom(&generator)%48) + 2
		nodes := make([]string, nodeCount)
		for index := range nodes {
			nodes[index] = fmt.Sprintf("node-%03d", index)
		}
		edges := make([]kernel.GraphEdge, 0, nodeCount*2)
		for child := 1; child < nodeCount; child++ {
			parent := int(nextRandom(&generator) % uint64(child))
			edges = append(edges, kernel.GraphEdge{Parent: nodes[parent], Child: nodes[child]})
		}
		if result := kernel.ValidateDAG(nodes, edges); !result.Valid {
			t.Fatalf("seed=%x history=%d generated DAG rejected: %#v", seed, history, result)
		}
		cycle := append(append([]kernel.GraphEdge(nil), edges...), kernel.GraphEdge{Parent: nodes[nodeCount-1], Child: nodes[0]})
		if result := kernel.ValidateDAG(nodes, cycle); result.Valid || result.Reason == nil || *result.Reason != "CYCLE" {
			t.Fatalf("seed=%x history=%d cycle accepted: %#v", seed, history, result)
		}
		missing := append(append([]kernel.GraphEdge(nil), edges...), kernel.GraphEdge{Parent: "missing", Child: nodes[0]})
		if result := kernel.ValidateDAG(nodes, missing); result.Valid || result.Reason == nil || *result.Reason != "MISSING_NODE" {
			t.Fatalf("seed=%x history=%d missing parent accepted: %#v", seed, history, result)
		}
	}
}

func activeTaskCommand(t *testing.T) (kernel.KernelCommand, kernel.Snapshot) {
	t.Helper()
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.task.activate"
	command.Target.Kind = kernel.AggregateTask
	command.ExpectedRevision = kernel.NewExpectedRevision(2)
	epoch := uint64(1)
	command.ExpectedLifecycleEpoch = &epoch
	actor := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: mustUUID(t, "00000000-0000-7000-8000-000000000041"), FencingEpoch: 7}
	command.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)}
	command.ActorFQN = &actor
	command.Execution = &execution
	command.Payload = json.RawMessage(`{"owner_fqn":"teams::coder-1","ownership_version":1}`)
	parent := mustUUID(t, "00000000-0000-7000-8000-000000000042")
	command.Causation = []kernel.DagParent{{ParentEventID: parent, EdgeKind: kernel.EdgeCausal}}
	state := &kernel.AggregateState{
		Kind: kernel.AggregateTask, ID: command.Target.ID, Revision: 2,
		LifecycleEpoch: 1, Phase: kernel.PhaseReady, Condition: kernel.ConditionRunnable,
		Ownership: kernel.Ownership{OwnerFQN: &actor, OwnershipVersion: 1},
	}
	return command, kernel.Snapshot{
		Exists: true, Revision: 2, State: state,
		AcceptedEvents:    map[kernel.UUIDv7]kernel.AcceptedEvent{parent: {EventType: "tekroo.event.task.readied"}},
		CurrentExecutions: map[kernel.ActorFQN]kernel.ExecutionTuple{actor: execution},
	}
}

func commandCase(t *testing.T, commandType string, payload json.RawMessage, targetKind kernel.AggregateKind, authorityKind kernel.PrincipalKind, executionRequired, rootAllowed bool) (kernel.KernelCommand, kernel.Snapshot) {
	t.Helper()
	target := kernel.AggregateRef{Kind: targetKind, ID: mustUUID(t, "00000000-0000-7000-8000-000000000071")}
	command := kernel.KernelCommand{
		ContractManifest:          kernel.ContractIdentity,
		CommandID:                 mustUUID(t, "00000000-0000-7000-8000-000000000072"),
		CommandType:               commandType,
		CommandVersion:            kernel.SchemaVersion,
		Target:                    target,
		Authority:                 kernel.PrincipalRef{Kind: authorityKind, ID: "principal"},
		ExpectedRevision:          kernel.NewExpectedRevision(1),
		ExpectedPolicyRevision:    1,
		ExpectedCatalogueRevision: kernel.CatalogueRevision,
		IdempotencyKey:            "all-command-semantics",
		CorrelationID:             mustUUID(t, "00000000-0000-7000-8000-000000000073"),
		Payload:                   append(json.RawMessage(nil), payload...),
	}
	snapshot := kernel.Snapshot{Exists: true, Revision: 1}
	parent := mustUUID(t, "00000000-0000-7000-8000-000000000074")
	if rootAllowed {
		command.ExpectedRevision = kernel.MustNotExist()
		snapshot.Exists = false
		snapshot.Revision = 0
	} else {
		command.Causation = []kernel.DagParent{{ParentEventID: parent, EdgeKind: kernel.EdgeCausal}}
		snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{parent: {EventType: "tekroo.event.story.created"}}
	}
	if commandType == "tekroo.command.release-plan.create" {
		command.ExpectedRevision = kernel.MustNotExist()
		snapshot.Exists = false
		snapshot.Revision = 0
	}
	actor := kernel.ActorFQN("teams::coder-1")
	execution := kernel.ExecutionTuple{ExecutionID: mustUUID(t, "00000000-0000-7000-8000-000000000075"), FencingEpoch: 7}
	if authorityKind == kernel.PrincipalActor {
		command.Authority.ID = string(actor)
		command.ActorFQN = &actor
	}
	if executionRequired {
		command.ActorFQN = &actor
		command.Execution = &execution
		snapshot.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{actor: execution}
	}
	if targetKind == kernel.AggregateStory || targetKind == kernel.AggregateTask {
		snapshot.State = stateForCommand(commandType, target)
		if commandType != "tekroo.command.record.correct" && !command.ExpectedRevision.MustNotExist {
			epoch := snapshot.State.LifecycleEpoch
			command.ExpectedLifecycleEpoch = &epoch
		}
	}
	if commandType == "tekroo.command.execution.replace" {
		snapshot.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{
			actor: {ExecutionID: mustUUID(t, "00000000-0000-7000-8000-000000000001"), FencingEpoch: 1},
		}
	}
	var object map[string]any
	if json.Unmarshal(payload, &object) == nil {
		if commandType == "tekroo.command.human-interaction.open" {
			command.Target.ID = kernel.UUIDv7(object["interaction_id"].(string))
		}
		evidenceValues, hasEvidence := object["evidence_ids"].([]any)
		if commandType == "tekroo.command.task.bind-work-profile" {
			evidenceValues, hasEvidence = object["classification_evidence_ids"].([]any)
		}
		if hasEvidence {
			snapshot.Evidence = make(map[kernel.UUIDv7]kernel.EvidenceMetadata, len(evidenceValues))
			for _, value := range evidenceValues {
				id := kernel.UUIDv7(value.(string))
				digest := kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
				command.EvidenceRefs = append(command.EvidenceRefs, kernel.EvidenceRef{EvidenceID: id, SHA256: digest})
				snapshot.Evidence[id] = kernel.EvidenceMetadata{SHA256: digest, Available: true}
			}
		}
		if reviewValue, ok := object["completion_review_id"].(string); ok {
			reviewRef := kernel.AggregateRef{Kind: kernel.AggregateCompletionReview, ID: kernel.UUIDv7(reviewValue)}
			finalizedID := kernel.UUIDv7(object["validation_finalized_event_id"].(string))
			reviewRevision := uint64(object["completion_review_revision"].(float64))
			policyRevision := uint64(object["branch_policy_revision"].(float64))
			criteriaRevision := uint64(object["criteria_revision"].(float64))
			snapshot.Reviews = map[kernel.AggregateRef]kernel.CompletionReviewSnapshot{reviewRef: {
				Subject: target, LifecycleEpoch: 1, CriteriaRevision: criteriaRevision, BranchPolicyRevision: policyRevision, ReviewRevision: reviewRevision,
				Finalization: &kernel.ReviewFinalization{EventID: finalizedID, ReviewRevision: reviewRevision, TerminalStatus: "PASS"},
			}}
			if snapshot.AcceptedEvents == nil {
				snapshot.AcceptedEvents = make(map[kernel.UUIDv7]kernel.AcceptedEvent)
			}
			snapshot.AcceptedEvents[finalizedID] = kernel.AcceptedEvent{EventType: "tekroo.event.completion-review.finalized", Qualification: "PASS"}
		}
		if value, ok := object["target_event_id"].(string); ok {
			if snapshot.AcceptedEvents == nil {
				snapshot.AcceptedEvents = make(map[kernel.UUIDv7]kernel.AcceptedEvent)
			}
			snapshot.AcceptedEvents[kernel.UUIDv7(value)] = kernel.AcceptedEvent{EventType: "tekroo.event.story.completed"}
		}
		if commandType == "tekroo.command.completion-review.open" {
			subject := kernel.AggregateRef{Kind: kernel.AggregateKind(object["subject_kind"].(string)), ID: kernel.UUIDv7(object["subject_id"].(string))}
			review, err := kernel.CompletionReviewFromPayload(payload)
			if err != nil {
				t.Fatal(err)
			}
			command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: subject, Expected: kernel.NewExpectedRevision(1)}}
			snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{subject: {
				Exists: true, Revision: 1,
				State: &kernel.AggregateState{Kind: subject.Kind, ID: subject.ID, Revision: 1, LifecycleEpoch: uint64(object["lifecycle_epoch"].(float64)), ScopeRevision: review.ScopeRevision, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
			}}
			if subject.Kind == kernel.AggregateTask {
				profile := variantFixtureWorkProfile(subject.ID)
				profile.Profile.ProfileID = review.WorkProfile.ProfileID
				profile.Profile.ProfileRevision = review.WorkProfile.ProfileRevision
				profile.Profile.ProfileDigest = review.WorkProfile.ProfileDigest
				profile.Profile.LifecycleEpoch = review.WorkProfile.LifecycleEpoch
				profile.Profile.ScopeRevision = review.WorkProfile.ScopeRevision
				snapshot.WorkProfiles = map[kernel.AggregateRef]kernel.WorkProfileSnapshot{subject: profile}
			}
		}
		if commandType == "tekroo.command.completion-review.record-result" {
			reviewID := kernel.UUIDv7(object["review_id"].(string))
			command.Target.ID = reviewID
			policyRevision := uint64(object["branch_policy_revision"].(float64))
			branchID := object["branch_id"].(string)
			deadline := time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
			snapshot.Reviews = map[kernel.AggregateRef]kernel.CompletionReviewSnapshot{command.Target: {
				BranchPolicyRevision: policyRevision,
				RequiredBranchIDs:    []string{branchID},
				Branches:             map[string]kernel.ReviewBranchSpec{branchID: {BranchID: branchID, Validator: command.Authority, DeadlineAt: deadline, RoundLimit: 2}},
				Adjudication:         kernel.ReviewAdjudication{Adjudicator: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "adjudicator"}, DeadlineAt: deadline, RoundLimit: 2},
				PartialResultPolicy:  "WAIT_ALL",
				Results:              map[string]string{},
				ResultRecords:        map[string]kernel.ReviewBranchResult{},
				KnownResultEvents:    map[kernel.UUIDv7]kernel.ReviewBranchResult{},
				ReviewRevision:       1,
				Join:                 kernel.ReviewJoinResult{Status: "PENDING"},
			}}
		}
		if commandType == "tekroo.command.completion-review.finalize" {
			reviewID := kernel.UUIDv7(object["review_id"].(string))
			command.Target.ID = reviewID
			resultID := kernel.UUIDv7(object["result_event_ids"].([]any)[0].(string))
			branchID := "tests"
			result := kernel.ReviewBranchResult{BranchID: branchID, Result: "PASS", EventID: resultID}
			subject := kernel.AggregateRef{Kind: kernel.AggregateKind(object["subject_kind"].(string)), ID: kernel.UUIDv7(object["subject_id"].(string))}
			snapshot.Reviews = map[kernel.AggregateRef]kernel.CompletionReviewSnapshot{command.Target: {
				Subject: subject, LifecycleEpoch: uint64(object["lifecycle_epoch"].(float64)), BranchPolicyRevision: uint64(object["branch_policy_revision"].(float64)),
				RequiredBranchIDs: []string{branchID}, Results: map[string]string{branchID: "PASS"}, ResultRecords: map[string]kernel.ReviewBranchResult{branchID: result}, KnownResultEvents: map[kernel.UUIDv7]kernel.ReviewBranchResult{resultID: result}, ReviewRevision: uint64(object["expected_review_revision"].(float64)), Join: kernel.ReviewJoinResult{Complete: true, Status: "PASS"},
			}}
		}
		if commandType == "tekroo.command.escalation.open" {
			escalation, err := kernel.EscalationFromOpenPayload(payload)
			if err != nil {
				t.Fatal(err)
			}
			command.Target.ID = escalation.EscalationID
			command.ExpectedRevision = kernel.MustNotExist()
			command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: escalation.Subject, Expected: kernel.NewExpectedRevision(escalation.ExpectedSubjectRevision)}}
			command.Causation = make([]kernel.DagParent, len(escalation.CausalPathEventIDs))
			snapshot.Exists = false
			snapshot.Revision = 0
			snapshot.AcceptedEvents = make(map[kernel.UUIDv7]kernel.AcceptedEvent, len(escalation.CausalPathEventIDs))
			for index, eventID := range escalation.CausalPathEventIDs {
				command.Causation[index] = kernel.DagParent{ParentEventID: eventID, EdgeKind: kernel.EdgeCausal}
				snapshot.AcceptedEvents[eventID] = kernel.AcceptedEvent{EventType: "tekroo.event.task.handoff-recorded"}
			}
			snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{escalation.Subject: {
				Exists: true, Revision: escalation.ExpectedSubjectRevision,
				State: &kernel.AggregateState{Kind: escalation.Subject.Kind, ID: escalation.Subject.ID, Revision: escalation.ExpectedSubjectRevision, LifecycleEpoch: escalation.SubjectLifecycleEpoch, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
			}}
			snapshot.Escalations = map[kernel.AggregateRef]kernel.EscalationSnapshot{}
			snapshot.EscalationKeys = map[kernel.EscalationKey]kernel.AggregateRef{}
		}
		if commandType == "tekroo.command.escalation.resolve" {
			transition, err := kernel.EscalationTransitionFromResolvePayload(payload)
			if err != nil {
				t.Fatal(err)
			}
			openingEventID := mustUUID(t, "00000000-0000-7000-8000-000000000704")
			subjectRevision := uint64(7)
			escalation := kernel.EscalationSnapshot{
				EscalationID: transition.EscalationID, OpeningEventID: openingEventID, Revision: transition.ExpectedRevision, State: kernel.EscalationOpen,
				Subject: transition.Subject, SubjectLifecycleEpoch: transition.SubjectLifecycleEpoch, ExpectedSubjectRevision: subjectRevision,
				Trigger: kernel.EscalationHandoffCycleDetected, ConditionDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
				Adjudicator: command.Authority, TimeoutPolicy: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "escalation-timeout-policy"},
				ResolutionOwnerFQN: actor, DeadlineAt: time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC), ResolutionRoundLimit: 1, RouteLimit: 1, PolicyRevision: 1,
				CausalPathEventIDs: []kernel.UUIDv7{mustUUID(t, "00000000-0000-7000-8000-000000000702")}, UnresolvedQuestion: "Which directed successor resolves the detected handoff cycle?", EvidenceIDs: transition.EvidenceIDs,
			}
			if !escalation.Valid() {
				t.Fatal("invalid escalation fixture snapshot")
			}
			command.Target.ID = transition.EscalationID
			command.ExpectedRevision = kernel.NewExpectedRevision(transition.ExpectedRevision)
			command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: transition.Subject, Expected: kernel.NewExpectedRevision(subjectRevision)}}
			command.Causation = []kernel.DagParent{{ParentEventID: openingEventID, EdgeKind: kernel.EdgeResponse}}
			snapshot.Exists = true
			snapshot.Revision = transition.ExpectedRevision
			snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{openingEventID: {EventType: "tekroo.event.escalation.opened"}}
			snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{transition.Subject: {
				Exists: true, Revision: subjectRevision,
				State: &kernel.AggregateState{Kind: transition.Subject.Kind, ID: transition.Subject.ID, Revision: subjectRevision, LifecycleEpoch: transition.SubjectLifecycleEpoch, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
			}}
			snapshot.Escalations = map[kernel.AggregateRef]kernel.EscalationSnapshot{command.Target: escalation}
			snapshot.EscalationKeys = map[kernel.EscalationKey]kernel.AggregateRef{escalation.Key(): command.Target}
		}
		configureModelCapabilityCommandCase(t, &command, &snapshot, object)
		configureReleaseCommandCase(t, &command, &snapshot, object)
		configureOperatorHumanContinuityCommandCase(t, &command, &snapshot, object)
	}
	return command, snapshot
}

func configureModelCapabilityCommandCase(t *testing.T, command *kernel.KernelCommand, snapshot *kernel.Snapshot, object map[string]any) {
	t.Helper()
	if command.CommandType == "tekroo.command.task.bind-work-profile" {
		profile, err := kernel.WorkRiskProfileFromPayload(command.Payload)
		if err != nil {
			t.Fatal(err)
		}
		command.Target.ID = profile.TaskID
		command.ExpectedRevision = kernel.NewExpectedRevision(1)
		epoch := profile.LifecycleEpoch
		command.ExpectedLifecycleEpoch = &epoch
		snapshot.Exists = true
		snapshot.Revision = 1
		snapshot.State = &kernel.AggregateState{Kind: kernel.AggregateTask, ID: profile.TaskID, Revision: 1, LifecycleEpoch: profile.LifecycleEpoch, ScopeRevision: profile.ScopeRevision, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable}
		snapshot.WorkProfiles = map[kernel.AggregateRef]kernel.WorkProfileSnapshot{}
		return
	}
	if command.CommandType == "tekroo.command.task.authorize-qualified-assignment" {
		authorization, err := kernel.QualifiedAssignmentAuthorizationFromPayload(command.Payload, command.CommandID)
		if err != nil {
			t.Fatal(err)
		}
		command.Target.ID = authorization.TaskID
		command.ExpectedRevision = kernel.NewExpectedRevision(authorization.ExpectedTaskRevision)
		epoch := authorization.WorkProfile.LifecycleEpoch
		command.ExpectedLifecycleEpoch = &epoch
		snapshot.Exists = true
		snapshot.Revision = authorization.ExpectedTaskRevision
		snapshot.State = &kernel.AggregateState{Kind: kernel.AggregateTask, ID: authorization.TaskID, Revision: authorization.ExpectedTaskRevision, LifecycleEpoch: authorization.WorkProfile.LifecycleEpoch, ScopeRevision: authorization.WorkProfile.ScopeRevision, Phase: kernel.PhaseReady, Condition: kernel.ConditionRunnable}
		snapshot.WorkProfiles = map[kernel.AggregateRef]kernel.WorkProfileSnapshot{command.Target: variantFixtureWorkProfile(authorization.TaskID)}
		snapshot.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{authorization.SelectedActorFQN: authorization.SelectedExecution()}
		return
	}
	if command.CommandType == "tekroo.command.task.dispatch" {
		profile := variantFixtureWorkProfile(command.Target.ID)
		authorizationEventID := kernel.UUIDv7("00000000-0000-7000-8000-000000000076")
		authorization := qualifiedAssignmentFixture(command.Target.ID, authorizationEventID)
		snapshot.Revision = 2
		snapshot.State.Revision = 2
		command.ExpectedRevision = kernel.NewExpectedRevision(2)
		command.Causation = []kernel.DagParent{{ParentEventID: authorizationEventID, EdgeKind: kernel.EdgeCausal}}
		snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{authorizationEventID: {EventType: "tekroo.event.task.qualified-assignment-authorized"}}
		snapshot.WorkProfiles = map[kernel.AggregateRef]kernel.WorkProfileSnapshot{command.Target: profile}
		snapshot.QualifiedAssignments = map[kernel.AggregateRef]kernel.QualifiedAssignmentAuthorization{command.Target: authorization}
		snapshot.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{authorization.SelectedActorFQN: authorization.SelectedExecution()}
		return
	}
	if !strings.HasPrefix(command.CommandType, "tekroo.command.variant-group.") {
		return
	}
	groupID := kernel.UUIDv7(object["variant_group_id"].(string))
	command.Target = kernel.AggregateRef{Kind: kernel.AggregateVariantGroup, ID: groupID}
	group := variantFixtureGroup(t, groupID)
	snapshot.VariantGroups = map[kernel.AggregateRef]kernel.VariantGroupSnapshot{}
	snapshot.VariantGroupKeys = map[kernel.VariantGroupKey]kernel.AggregateRef{}
	if command.CommandType == "tekroo.command.variant-group.open" {
		opened, err := kernel.VariantGroupFromOpenPayload(command.Payload, command.CommandID)
		if err != nil {
			t.Fatal(err)
		}
		task := kernel.AggregateRef{Kind: kernel.AggregateTask, ID: opened.TaskID}
		command.ExpectedRevision = kernel.MustNotExist()
		command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: task, Expected: kernel.NewExpectedRevision(1)}}
		snapshot.Exists = false
		snapshot.Revision = 0
		snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{task: {Exists: true, Revision: 1, State: &kernel.AggregateState{Kind: kernel.AggregateTask, ID: task.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhasePlanned, Condition: kernel.ConditionRunnable}}}
		snapshot.WorkProfiles = map[kernel.AggregateRef]kernel.WorkProfileSnapshot{task: variantFixtureWorkProfile(task.ID)}
		return
	}
	group.Revision = uint64(object["expected_group_revision"].(float64))
	command.ExpectedRevision = kernel.NewExpectedRevision(group.Revision)
	snapshot.Exists = true
	snapshot.Revision = group.Revision
	if command.CommandType != "tekroo.command.variant-group.submit-candidate" {
		group.Candidates = variantFixtureCandidates()
	}
	if command.CommandType == "tekroo.command.variant-group.record-comparison" {
		command.Authority = group.Comparator
		actor := kernel.ActorFQN(group.Comparator.ID)
		execution := kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7(object["comparator_execution_id"].(string)), FencingEpoch: 1}
		command.ActorFQN = &actor
		command.Execution = &execution
		snapshot.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{actor: execution}
	}
	if command.CommandType == "tekroo.command.variant-group.select" {
		group.State = kernel.VariantCompared
		group.Comparison = &kernel.VariantComparison{EventID: kernel.UUIDv7(object["comparison_event_id"].(string)), CandidateIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000808", "00000000-0000-7000-8000-000000000809"}, Comparator: group.Comparator, ComparatorExecutionID: "00000000-0000-7000-8000-000000000811", Classification: kernel.VariantMaterialAgreement, ComparisonMethodID: group.ComparisonMethodID, ComparisonReceiptDigest: "6666666666666666666666666666666666666666666666666666666666666666", Reasons: []string{"behavior and architecture agree"}, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}}
		command.Authority = group.Adjudicator
	}
	if command.CommandType == "tekroo.command.variant-group.submit-candidate" {
		actor := kernel.ActorFQN(object["actor_fqn"].(string))
		execution := kernel.ExecutionTuple{ExecutionID: kernel.UUIDv7(object["execution_id"].(string)), FencingEpoch: uint64(object["fencing_epoch"].(float64))}
		command.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: string(actor)}
		command.ActorFQN = &actor
		command.Execution = &execution
		snapshot.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{actor: execution}
	}
	snapshot.VariantGroups[command.Target] = group
}

func qualifiedAssignmentFixture(taskID kernel.UUIDv7, eventID kernel.UUIDv7) kernel.QualifiedAssignmentAuthorization {
	return kernel.QualifiedAssignmentAuthorization{
		AssignmentID: "00000000-0000-7000-8000-000000000803", TaskID: taskID, ExpectedTaskRevision: 1,
		WorkProfile:           kernel.WorkProfileBinding{ProfileID: "00000000-0000-7000-8000-000000000802", ProfileRevision: 1, ProfileDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LifecycleEpoch: 1, ScopeRevision: 1},
		RequiredDecisionRoute: kernel.RouteBoundedExecution, SelectedDecisionRoute: kernel.RouteBoundedExecution, SelectedActorFQN: "teams::coder-1", SelectedExecutionID: "00000000-0000-7000-8000-000000000804", SelectedFencingEpoch: 1,
		ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", RuntimeIdentityDigest: "1111111111111111111111111111111111111111111111111111111111111111",
		Qualification:           kernel.AssignmentQualificationReceipt{QualificationID: "00000000-0000-7000-8000-000000000805", QualificationDigest: "2222222222222222222222222222222222222222222222222222222222222222", QualificationCorpusDigest: "3333333333333333333333333333333333333333333333333333333333333333", ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", DecisionRoute: kernel.RouteBoundedExecution, QualifiedRole: "programmer", Status: kernel.QualificationPass, ObservedAt: time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)},
		SelectionPolicyRevision: 1, SelectionPolicyDigest: "4444444444444444444444444444444444444444444444444444444444444444", HardConstraintResults: []kernel.HardConstraintResult{{ConstraintID: "data-residency", Outcome: kernel.ConstraintPass, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}}}, SelectionReasons: []string{"least-cost qualified profile"}, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}, AuthorizationEventID: eventID,
	}
}

func variantFixtureWorkProfile(taskID kernel.UUIDv7) kernel.WorkProfileSnapshot {
	return kernel.WorkProfileSnapshot{BoundEventID: "00000000-0000-7000-8000-000000000806", TaskRevision: 1, Profile: kernel.WorkRiskProfile{
		TaskID: taskID, ProfileID: "00000000-0000-7000-8000-000000000802", ProfileRevision: 1, ProfileDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LifecycleEpoch: 1, ScopeRevision: 1,
		WorkKind: kernel.WorkImplementation, Ambiguity: kernel.AmbiguityLow, Novelty: kernel.NoveltyRoutine, BlastRadius: kernel.BlastLocal, SecuritySensitivity: kernel.SecurityOrdinary,
		MinimumDecisionRoute: kernel.RouteBoundedExecution, AcceptanceCriteriaDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", RequiredDeterministicGateIDs: []string{"go-test"}, RequiredValidationBranches: 1,
		RequiredIndependenceDimensions: []kernel.IndependenceDimension{kernel.IndependencePrincipal, kernel.IndependenceActor, kernel.IndependenceExecution, kernel.IndependenceContext, kernel.IndependenceWorkspace, kernel.IndependenceMethod},
		ImplementationVariantCount:     1, ValidCandidateQuorum: 1, VerificationTopologyDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ClassificationPolicyRevision: 1, ClassificationPolicyDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", PromotionPolicyRevision: 1, PromotionPolicyDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		Budgets: kernel.FiniteWorkBudgets{AttemptLimit: 2, ReviewRoundLimit: 2, PromotionLimit: 1, EscalationLimit: 1, DeadlineAt: time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)}, ClassificationAuthority: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal"}, ClassificationEvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"},
	}}
}

func variantFixtureGroup(t *testing.T, groupID kernel.UUIDv7) kernel.VariantGroupSnapshot {
	t.Helper()
	payload := json.RawMessage(`{"acceptance_manifest_digest":"9999999999999999999999999999999999999999999999999999999999999999","adjudicator":{"id":"principal","kind":"HUMAN"},"base_artifact_digest":"5555555555555555555555555555555555555555555555555555555555555555","candidate_count":2,"comparator":{"id":"teams::reviewer-1","kind":"ACTOR"},"comparison_method_id":"structured-diff-v1","comparison_policy_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","decision_deadline_at":"2026-08-15T00:00:00Z","dependency_lock_digest":"8888888888888888888888888888888888888888888888888888888888888888","evidence_ids":["00000000-0000-7000-8000-000000000806"],"input_evidence_set_digest":"6666666666666666666666666666666666666666666666666666666666666666","materiality_policy_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","replacement_budget":1,"required_independence_dimensions":["ACTOR","EXECUTION","CONTEXT","WORKSPACE"],"submission_deadline_at":"2026-08-14T00:00:00Z","task_id":"00000000-0000-7000-8000-000000000801","toolchain_digest":"7777777777777777777777777777777777777777777777777777777777777777","valid_candidate_quorum":2,"variant_group_id":"00000000-0000-7000-8000-000000000807","work_profile":{"lifecycle_epoch":1,"profile_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","profile_id":"00000000-0000-7000-8000-000000000802","profile_revision":1,"scope_revision":1}}`)
	group, err := kernel.VariantGroupFromOpenPayload(payload, "00000000-0000-7000-8000-000000000807")
	if err != nil {
		t.Fatal(err)
	}
	group.VariantGroupID = groupID
	return group
}

func variantFixtureCandidates() map[kernel.UUIDv7]kernel.VariantCandidate {
	return map[kernel.UUIDv7]kernel.VariantCandidate{
		"00000000-0000-7000-8000-000000000808": {CandidateID: "00000000-0000-7000-8000-000000000808", ActorFQN: "teams::coder-1", Execution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000804", FencingEpoch: 1}, ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", RuntimeIdentityDigest: "1111111111111111111111111111111111111111111111111111111111111111", ContextDigest: "2222222222222222222222222222222222222222222222222222222222222222", WorkspaceDigest: "3333333333333333333333333333333333333333333333333333333333333333", ArtifactDigest: "4444444444444444444444444444444444444444444444444444444444444444", ChangedFileInventoryDigest: "5555555555555555555555555555555555555555555555555555555555555555", DeterministicGateReceiptIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}, SubmissionEventID: "00000000-0000-7000-8000-000000000808"},
		"00000000-0000-7000-8000-000000000809": {CandidateID: "00000000-0000-7000-8000-000000000809", ActorFQN: "teams::coder-2", Execution: kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000805", FencingEpoch: 1}, ModelProfileDigest: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", RuntimeIdentityDigest: "1111111111111111111111111111111111111111111111111111111111111111", ContextDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", WorkspaceDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ArtifactDigest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ChangedFileInventoryDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", DeterministicGateReceiptIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000806"}, SubmissionEventID: "00000000-0000-7000-8000-000000000809"},
	}
}

func configureReleaseCommandCase(t *testing.T, command *kernel.KernelCommand, snapshot *kernel.Snapshot, object map[string]any) {
	t.Helper()
	if command.CommandType == "tekroo.command.story.approve-release" {
		storyID := kernel.UUIDv7(object["story_id"].(string))
		revision := uint64(object["expected_story_revision"].(float64))
		command.Target.ID = storyID
		command.ExpectedRevision = kernel.NewExpectedRevision(revision)
		epoch := uint64(object["lifecycle_epoch"].(float64))
		command.ExpectedLifecycleEpoch = &epoch
		command.Authority = kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal-author"}
		command.ActorFQN = nil
		command.Execution = nil
		snapshot.Exists = true
		snapshot.Revision = revision
		snapshot.State = &kernel.AggregateState{Kind: kernel.AggregateStory, ID: storyID, Revision: revision, LifecycleEpoch: epoch, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable}
		return
	}

	if command.CommandType == "tekroo.command.release-plan.create" {
		plan, err := kernel.ReleasePlanFromCreatePayload(command.Payload, mustUUID(t, "00000000-0000-7000-8000-000000000748"))
		if err != nil {
			t.Fatal(err)
		}
		command.Target.ID = plan.ReleasePlanID
		command.ExpectedRevision = kernel.MustNotExist()
		command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: plan.Story, Expected: kernel.NewExpectedRevision(plan.ExpectedStoryRevision)}}
		command.Causation = []kernel.DagParent{{ParentEventID: plan.AuthorApprovalEventID, EdgeKind: kernel.EdgeResponse}}
		snapshot.Exists = false
		snapshot.Revision = 0
		snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{plan.AuthorApprovalEventID: {EventType: "tekroo.event.story.release-approved"}}
		snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{plan.Story: {
			Exists: true, Revision: plan.ExpectedStoryRevision,
			State: &kernel.AggregateState{Kind: kernel.AggregateStory, ID: plan.Story.ID, Revision: plan.ExpectedStoryRevision, LifecycleEpoch: plan.StoryLifecycleEpoch, Phase: kernel.PhaseCompleted, Condition: kernel.ConditionRunnable},
		}}
		snapshot.ReleasePlanKeys = map[kernel.ReleasePlanKey]kernel.AggregateRef{}
		return
	}

	if !strings.HasPrefix(command.CommandType, "tekroo.command.release-plan.") && command.CommandType != "tekroo.command.story.request-acceptance" {
		return
	}

	plan := releaseFixturePlan(t)
	if command.CommandType == "tekroo.command.story.request-acceptance" {
		plan.Revision = uint64(object["release_plan_revision"].(float64))
		plan.State = kernel.ReleaseReadyForAcceptance
		plan.Qualification = releaseFixtureQualification(plan, mustUUID(t, "00000000-0000-7000-8000-000000000758"))
		plan.NextMergeIndex = uint64(len(plan.OrderedMerges))
		plan.FinalizationEventID = kernel.UUIDv7(object["release_finalized_event_id"].(string))
		plan.ProviderTreeDigest = object["qualified_tree_digest"].(string)
		plan.Story = command.Target
		planRef := kernel.AggregateRef{Kind: kernel.AggregateReleasePlan, ID: plan.ReleasePlanID}
		command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: planRef, Expected: kernel.NewExpectedRevision(plan.Revision)}}
		command.Causation = []kernel.DagParent{{ParentEventID: plan.FinalizationEventID, EdgeKind: kernel.EdgeResponse}}
		snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{plan.FinalizationEventID: {EventType: "tekroo.event.release-plan.finalized"}}
		snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{planRef: {Exists: true, Revision: plan.Revision}}
		snapshot.ReleasePlans = map[kernel.AggregateRef]kernel.ReleasePlanSnapshot{planRef: plan}
		return
	}

	command.Target.ID = plan.ReleasePlanID
	plan.Revision = uint64(object["expected_release_revision"].(float64))
	command.ExpectedRevision = kernel.NewExpectedRevision(plan.Revision)
	snapshot.Exists = true
	snapshot.Revision = plan.Revision
	switch command.CommandType {
	case "tekroo.command.release-plan.record-qualification":
		plan.State = kernel.ReleasePlanned
	case "tekroo.command.release-plan.request-execution":
		plan.State = kernel.ReleaseQualified
		plan.Qualification = releaseFixtureQualification(plan, mustUUID(t, "00000000-0000-7000-8000-000000000758"))
	case "tekroo.command.release-plan.record-result":
		plan.State = kernel.ReleaseExecuting
		plan.Qualification = releaseFixtureQualification(plan, mustUUID(t, "00000000-0000-7000-8000-000000000758"))
		plan.ActiveAttempt = &kernel.ReleaseAttempt{MergeID: kernel.UUIDv7(object["merge_id"].(string)), AttemptID: kernel.UUIDv7(object["attempt_id"].(string)), Round: 1, ProviderIdempotencyKey: "release-751-merge-752-round-1", RequestEventID: mustUUID(t, "00000000-0000-7000-8000-000000000758")}
	case "tekroo.command.release-plan.record-reconciliation":
		plan.State = kernel.ReleaseReconciling
		plan.Qualification = releaseFixtureQualification(plan, mustUUID(t, "00000000-0000-7000-8000-000000000758"))
		plan.ActiveAttempt = &kernel.ReleaseAttempt{MergeID: kernel.UUIDv7(object["merge_id"].(string)), AttemptID: kernel.UUIDv7(object["attempt_id"].(string)), Round: 1, ProviderIdempotencyKey: "release-751-merge-752-round-1", RequestEventID: mustUUID(t, "00000000-0000-7000-8000-000000000758")}
		priorID := kernel.UUIDv7(object["supersedes_result_event_id"].(string))
		plan.Results[plan.ActiveAttempt.MergeID] = kernel.ReleaseResult{MergeID: plan.ActiveAttempt.MergeID, AttemptID: plan.ActiveAttempt.AttemptID, Outcome: kernel.ReleaseOutcomeUnknown, ResultEventID: priorID}
	case "tekroo.command.release-plan.finalize":
		plan.State = kernel.ReleaseQualified
		plan.Qualification = releaseFixtureQualification(plan, kernel.UUIDv7(object["qualification_event_id"].(string)))
		plan.NextMergeIndex = uint64(len(plan.OrderedMerges))
		plan.ProviderTreeDigest = plan.ExpectedQualifiedTree
		resultID := kernel.UUIDv7(object["result_event_ids"].([]any)[0].(string))
		merge := plan.OrderedMerges[0]
		plan.Results[merge.MergeID] = kernel.ReleaseResult{MergeID: merge.MergeID, AttemptID: mustUUID(t, "00000000-0000-7000-8000-000000000755"), Outcome: kernel.ReleaseOutcomeMerged, ResultEventID: resultID, ObservedTree: plan.ExpectedQualifiedTree, Reconciled: true, ReconciliationEventID: resultID}
	}
	snapshot.ReleasePlans = map[kernel.AggregateRef]kernel.ReleasePlanSnapshot{command.Target: plan}
}

func configureOperatorHumanContinuityCommandCase(t *testing.T, command *kernel.KernelCommand, snapshot *kernel.Snapshot, object map[string]any) {
	t.Helper()
	continuity := func(revision uint64, state kernel.ContinuityControlState, power uint64, inFlight, unresolved []kernel.UUIDv7) *kernel.AggregateState {
		value := kernel.TeamContinuitySnapshot{Revision: revision, OperatingPosture: "CONTINUOUS", ControlState: state, PowerEpoch: power, AdmissionOpen: state == kernel.ContinuityActive, ContinuityPolicyRevision: 1, ContinuityPolicyDigest: kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"), HealthRequirementDigest: kernel.Digest("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"), InFlightExecutionIDs: inFlight, UnresolvedExecutionIDs: unresolved, LastTransitionEventID: mustUUID(t, "00000000-0000-7000-8000-000000000906")}
		return &kernel.AggregateState{Kind: kernel.AggregateSystem, ID: command.Target.ID, Revision: revision, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, Continuity: &value}
	}
	switch command.CommandType {
	case "tekroo.command.system.request-quiescence":
		command.ExpectedLifecycleEpoch = nil
		command.ExpectedRevision = kernel.NewExpectedRevision(1)
		snapshot.Revision = 1
		snapshot.State = continuity(1, kernel.ContinuityActive, 1, nil, nil)
	case "tekroo.command.system.record-suspended":
		command.ExpectedLifecycleEpoch = nil
		ids := []kernel.UUIDv7{mustUUID(t, "00000000-0000-7000-8000-000000000904"), mustUUID(t, "00000000-0000-7000-8000-000000000905")}
		command.ExpectedRevision = kernel.NewExpectedRevision(2)
		snapshot.Revision = 2
		snapshot.State = continuity(2, kernel.ContinuityQuiescing, 2, ids, nil)
	case "tekroo.command.system.record-unexpected-outage":
		command.ExpectedLifecycleEpoch = nil
		command.ExpectedRevision = kernel.NewExpectedRevision(1)
		snapshot.Revision = 1
		snapshot.State = continuity(1, kernel.ContinuityActive, 1, nil, nil)
	case "tekroo.command.system.begin-reconciliation":
		command.ExpectedLifecycleEpoch = nil
		ids := []kernel.UUIDv7{mustUUID(t, "00000000-0000-7000-8000-000000000905")}
		command.ExpectedRevision = kernel.NewExpectedRevision(3)
		snapshot.Revision = 3
		snapshot.State = continuity(3, kernel.ContinuitySuspended, 2, nil, ids)
	case "tekroo.command.system.resume":
		command.ExpectedLifecycleEpoch = nil
		ids := []kernel.UUIDv7{mustUUID(t, "00000000-0000-7000-8000-000000000905")}
		command.ExpectedRevision = kernel.NewExpectedRevision(4)
		snapshot.Revision = 4
		snapshot.State = continuity(4, kernel.ContinuityReconciling, 2, nil, ids)
	case "tekroo.command.human-participant.revoke":
		participantObject := object["participant"].(map[string]any)
		participant := kernel.PrincipalRef{Kind: kernel.PrincipalKind(participantObject["kind"].(string)), ID: participantObject["id"].(string)}
		profileID := kernel.UUIDv7(object["expected_profile_id"].(string))
		recipient := kernel.HumanInteractionRecipient{Principal: participant, ParticipantProfileID: profileID, ParticipantProfileRevision: 1, ParticipantProfileDigest: "6666666666666666666666666666666666666666666666666666666666666666", RoleBindingID: "00000000-0000-7000-8000-000000000910", DeliveryBindingID: "00000000-0000-7000-8000-000000000912"}
		profile := participantFixture(recipient)
		command.ExpectedLifecycleEpoch = nil
		command.ExpectedRevision = kernel.NewExpectedRevision(profile.Revision)
		snapshot.Revision = profile.Revision
		snapshot.State = &kernel.AggregateState{Kind: kernel.AggregateHumanParticipant, ID: command.Target.ID, Revision: profile.Revision, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, Participant: &profile}
	case "tekroo.command.human-interaction.open":
		interaction, err := kernel.HumanInteractionFromOpenPayload(command.Payload, 1)
		if err != nil {
			t.Fatal(err)
		}
		command.Target.ID = interaction.InteractionID
		command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: interaction.Subject, Expected: kernel.NewExpectedRevision(interaction.ExpectedSubjectRevision)}}
		command.Causation = []kernel.DagParent{{ParentEventID: mustUUID(t, "00000000-0000-7000-8000-000000000903"), EdgeKind: kernel.EdgeCausal}}
		command.ActorFQN = interaction.OriginActorFQN
		command.Execution = interaction.OriginExecution
		snapshot.CurrentExecutions = map[kernel.ActorFQN]kernel.ExecutionTuple{*interaction.OriginActorFQN: *interaction.OriginExecution}
		snapshot.AcceptedEvents = map[kernel.UUIDv7]kernel.AcceptedEvent{command.Causation[0].ParentEventID: {EventType: "tekroo.event.task.handoff-recorded"}}
		snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{interaction.Subject: {Exists: true, Revision: interaction.ExpectedSubjectRevision, State: &kernel.AggregateState{Kind: interaction.Subject.Kind, ID: interaction.Subject.ID, Revision: interaction.ExpectedSubjectRevision, LifecycleEpoch: interaction.SubjectLifecycleEpoch, Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable}}}
		for index, recipient := range interaction.Recipients {
			ref := kernel.AggregateRef{Kind: kernel.AggregateHumanParticipant, ID: kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", 920+index))}
			profile := participantFixture(recipient)
			snapshot.Related[ref] = kernel.RelatedSnapshot{Exists: true, Revision: profile.Revision, State: &kernel.AggregateState{Kind: ref.Kind, ID: ref.ID, Revision: profile.Revision, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, Participant: &profile}}
			command.Preconditions = append(command.Preconditions, kernel.AggregatePrecondition{Aggregate: ref, Expected: kernel.NewExpectedRevision(profile.Revision)})
		}
		sort.Slice(command.Preconditions, func(i, j int) bool {
			return command.Preconditions[i].Aggregate.Kind < command.Preconditions[j].Aggregate.Kind || command.Preconditions[i].Aggregate.Kind == command.Preconditions[j].Aggregate.Kind && command.Preconditions[i].Aggregate.ID < command.Preconditions[j].Aggregate.ID
		})
	case "tekroo.command.human-interaction.record-delivery", "tekroo.command.human-interaction.respond", "tekroo.command.human-interaction.close", "tekroo.command.human-interaction.expire":
		configureHumanInteractionContinuation(t, command, snapshot, object)
	}
}

func participantFixture(recipient kernel.HumanInteractionRecipient) kernel.HumanParticipantSnapshot {
	validFrom := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	return kernel.HumanParticipantSnapshot{Participant: recipient.Principal, Revision: 1, ProfileID: recipient.ParticipantProfileID, ProfileRevision: recipient.ParticipantProfileRevision, ProfileDigest: recipient.ParticipantProfileDigest, DisplayLabel: "Alice", PrivacyClassification: kernel.ConfidentialityInternal, ParticipantPolicyRevision: 1, ParticipantPolicyDigest: kernel.Digest("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), Active: true,
		RoleBindings:           []kernel.HumanRoleBinding{{RoleBindingID: recipient.RoleBindingID, RoleClass: "SME", RoleID: "sme", ScopeKind: "PROJECT", ScopeID: "tekroo-v4", AuthorityPolicyRevision: 1, AuthorityPolicyDigest: kernel.Digest("7777777777777777777777777777777777777777777777777777777777777777"), ValidFrom: validFrom, Active: true, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000903"}}},
		AuthenticationBindings: []kernel.HumanAuthenticationBinding{{AuthenticationBindingID: "00000000-0000-7000-8000-000000000911", Method: "OIDC", IssuerDigest: "8888888888888888888888888888888888888888888888888888888888888888", SubjectDigest: "9999999999999999999999999999999999999999999999999999999999999999", Assurance: "STANDARD", BindingRevision: 1, Active: true, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000903"}}},
		DeliveryBindings:       []kernel.HumanDeliveryBinding{{DeliveryBindingID: recipient.DeliveryBindingID, Channel: "WEB_PORTAL", EndpointDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", AdapterProfileDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ConfidentialityCeiling: kernel.ConfidentialityConfidential, AuthenticationBindingIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000911"}, BindingRevision: 1, Active: true, EvidenceIDs: []kernel.UUIDv7{"00000000-0000-7000-8000-000000000903"}}},
	}
}

func configureHumanInteractionContinuation(t *testing.T, command *kernel.KernelCommand, snapshot *kernel.Snapshot, object map[string]any) {
	t.Helper()
	command.ExpectedLifecycleEpoch = nil
	interactionID := kernel.UUIDv7(object["interaction_id"].(string))
	revision := uint64(object["expected_interaction_revision"].(float64))
	questionRevision := uint64(object["question_revision"].(float64))
	respondent := kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "human:alice"}
	if raw, ok := object["recipient"].(map[string]any); ok {
		respondent.ID = raw["id"].(string)
	}
	if raw, ok := object["respondent"].(map[string]any); ok {
		respondent.ID = raw["id"].(string)
		command.Authority = respondent
		command.ActorFQN = nil
		command.Execution = nil
	}
	profileID := kernel.UUIDv7("00000000-0000-7000-8000-000000000909")
	if value, ok := object["participant_profile_id"].(string); ok {
		profileID = kernel.UUIDv7(value)
	}
	roleID := kernel.UUIDv7("00000000-0000-7000-8000-000000000910")
	if value, ok := object["role_binding_id"].(string); ok {
		roleID = kernel.UUIDv7(value)
	}
	deliveryBindingID := kernel.UUIDv7("00000000-0000-7000-8000-000000000912")
	if value, ok := object["delivery_binding_id"].(string); ok {
		deliveryBindingID = kernel.UUIDv7(value)
	}
	recipient := kernel.HumanInteractionRecipient{Principal: respondent, ParticipantProfileID: profileID, ParticipantProfileRevision: 1, ParticipantProfileDigest: "6666666666666666666666666666666666666666666666666666666666666666", RoleBindingID: roleID, DeliveryBindingID: deliveryBindingID}
	interaction := kernel.HumanInteractionSnapshot{InteractionID: interactionID, Revision: revision, Phase: kernel.HumanInteractionOpen, Subject: kernel.AggregateRef{Kind: kernel.AggregateTask, ID: "00000000-0000-7000-8000-000000000901"}, SubjectLifecycleEpoch: 1, ExpectedSubjectRevision: 2, QuestionRevision: questionRevision, CanonicalQuestionDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", ResponseSpecificationDigest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", OriginPrincipal: kernel.PrincipalRef{Kind: kernel.PrincipalActor, ID: "teams::coder-1"}, OriginActorFQN: actorPointer("teams::coder-1"), OriginExecution: &kernel.ExecutionTuple{ExecutionID: "00000000-0000-7000-8000-000000000904", FencingEpoch: 1}, Recipients: []kernel.HumanInteractionRecipient{recipient}, ResponsePolicy: kernel.HumanResponsePolicy{Kind: kernel.HumanResponseExactOne}, Purpose: "ADVISORY_CONSULTATION", DeclaredEffect: "ADVISORY_ONLY", Confidentiality: kernel.ConfidentialityInternal, DeadlineAt: time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC), TimeoutPolicy: kernel.PrincipalRef{Kind: kernel.PrincipalPolicy, ID: "human-interaction-timeout"}, DisclosureScopeDigest: "1212121212121212121212121212121212121212121212121212121212121212", InteractionPolicyRevision: 1, InteractionPolicyDigest: "1313131313131313131313131313131313131313131313131313131313131313"}
	if command.CommandType == "tekroo.command.human-interaction.respond" {
		deliveryID := kernel.UUIDv7(object["delivery_id"].(string))
		interaction.Deliveries = []kernel.HumanDeliveryRecord{{DeliveryID: deliveryID, EventID: kernel.UUIDv7(object["delivery_event_id"].(string)), Recipient: respondent, DeliveryBindingID: deliveryBindingID, Outcome: "DELIVERED"}}
		interaction.Phase = kernel.HumanInteractionCollecting
	}
	if command.CommandType == "tekroo.command.human-interaction.close" {
		responseID := kernel.UUIDv7(object["accepted_response_event_ids"].([]any)[0].(string))
		interaction.Deliveries = []kernel.HumanDeliveryRecord{{DeliveryID: "00000000-0000-7000-8000-000000000914", EventID: "00000000-0000-7000-8000-000000000915", Recipient: respondent, DeliveryBindingID: deliveryBindingID, Outcome: "DELIVERED"}}
		interaction.Responses = []kernel.HumanResponseRecord{{EventID: responseID, Respondent: respondent, ParticipantProfileID: profileID, ParticipantProfileRevision: 1, RoleBindingID: roleID, AuthenticationBindingID: "00000000-0000-7000-8000-000000000911", DeliveryID: "00000000-0000-7000-8000-000000000914", ResponseArtifactDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResponseSpecificationDigest: interaction.ResponseSpecificationDigest}}
		interaction.Phase = kernel.HumanInteractionSatisfied
	}
	command.Target.ID = interactionID
	command.ExpectedRevision = kernel.NewExpectedRevision(revision)
	snapshot.Exists = true
	snapshot.Revision = revision
	snapshot.State = &kernel.AggregateState{Kind: kernel.AggregateHumanInteraction, ID: interactionID, Revision: revision, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, Interaction: &interaction}
	if command.CommandType == "tekroo.command.human-interaction.respond" {
		profile := participantFixture(recipient)
		ref := kernel.AggregateRef{Kind: kernel.AggregateHumanParticipant, ID: "00000000-0000-7000-8000-000000000920"}
		snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{ref: {Exists: true, Revision: 1, State: &kernel.AggregateState{Kind: ref.Kind, ID: ref.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Phase: kernel.PhaseDraft, Condition: kernel.ConditionRunnable, Participant: &profile}}}
		command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: ref, Expected: kernel.NewExpectedRevision(1)}}
	}
}

func actorPointer(value kernel.ActorFQN) *kernel.ActorFQN { return &value }

func releaseFixtureQualification(plan kernel.ReleasePlanSnapshot, eventID kernel.UUIDv7) *kernel.ReleaseQualification {
	return &kernel.ReleaseQualification{
		EventID: eventID, QualificationID: "00000000-0000-7000-8000-000000000754", QualifiedBaseCommit: plan.BaseCommit,
		OrderedHeadCommits: []string{plan.OrderedMerges[0].HeadCommit}, QualifiedTreeDigest: plan.ExpectedQualifiedTree,
		GateDefinition: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Toolchain: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		DependencyLock: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", ArtifactDigests: []kernel.Digest{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
	}
}

func releaseFixturePlan(t *testing.T) kernel.ReleasePlanSnapshot {
	t.Helper()
	return kernel.ReleasePlanSnapshot{
		ReleasePlanID: mustUUID(t, "00000000-0000-7000-8000-000000000751"), OpeningEventID: mustUUID(t, "00000000-0000-7000-8000-000000000748"), Revision: 1, State: kernel.ReleasePlanned, Mode: kernel.ReleaseModeCode,
		Story: kernel.AggregateRef{Kind: kernel.AggregateStory, ID: mustUUID(t, "00000000-0000-7000-8000-000000000101")}, StoryLifecycleEpoch: 1, ExpectedStoryRevision: 8,
		Author: kernel.PrincipalRef{Kind: kernel.PrincipalHuman, ID: "principal-author"}, AuthorApprovalEventID: mustUUID(t, "00000000-0000-7000-8000-000000000749"), AuthorApprovalRevision: 1,
		PolicyRevision: 1, PlanDigest: kernel.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), EvidenceIDs: []kernel.UUIDv7{mustUUID(t, "00000000-0000-7000-8000-000000000750")},
		RepositoryURL: "https://example.invalid/tekroo/teams.git", BaseRef: "main", BaseCommit: "1111111111111111111111111111111111111111",
		OrderedMerges: []kernel.ReleaseMergePlan{{MergeID: mustUUID(t, "00000000-0000-7000-8000-000000000752"), ChangeRef: "refs/heads/story-1", HeadCommit: "2222222222222222222222222222222222222222", Role: "story"}},
		MergeStrategy: "FF_ONLY_ORDERED", GitVersion: "git version 2.51.0", ConflictPolicy: "FAIL_NO_IMPROVISATION", ContractManifest: kernel.ContractIdentity,
		ManifestSHA256: kernel.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), RequiredProfiles: []string{"contract-structure", "core-hermetic", "mongo-integration", "synthesized-merge"},
		ExpectedQualifiedTree: "3333333333333333333333333333333333333333", ExecutionRoundLimit: 2, NextRound: 1, Results: map[kernel.UUIDv7]kernel.ReleaseResult{},
	}
}

func stateForCommand(commandType string, target kernel.AggregateRef) *kernel.AggregateState {
	state := &kernel.AggregateState{Kind: target.Kind, ID: target.ID, Revision: 1, LifecycleEpoch: 1, ScopeRevision: 1, Condition: kernel.ConditionRunnable}
	if target.Kind == kernel.AggregateStory {
		state.Phase = kernel.PhaseActive
		switch commandType {
		case "tekroo.command.story.authorize":
			state.Phase = kernel.PhaseDraft
		case "tekroo.command.story.begin-planning":
			state.Phase = kernel.PhaseReady
		case "tekroo.command.story.activate":
			state.Phase = kernel.PhasePlanning
		case "tekroo.command.story.request-acceptance":
			state.Phase = kernel.PhaseCompleted
		case "tekroo.command.work.reopen":
			state.Phase = kernel.PhaseCompleted
		case "tekroo.command.work.create-successor":
			state.Phase = kernel.PhaseCompleted
		case "tekroo.command.work.unblock":
			state.Condition = kernel.ConditionBlocked
		}
		return state
	}
	state.Phase = kernel.PhasePlanned
	actor := kernel.ActorFQN("teams::coder-1")
	switch commandType {
	case "tekroo.command.task.mark-ready":
	case "tekroo.command.task.dispatch", "tekroo.command.task.acquire-ownership":
	case "tekroo.command.task.release-ownership", "tekroo.command.task.handoff", "tekroo.command.task.force-reassign":
		state.Ownership = kernel.Ownership{OwnerFQN: &actor, OwnershipVersion: 1}
	case "tekroo.command.task.activate":
		state.Phase = kernel.PhaseReady
		state.Ownership = kernel.Ownership{OwnerFQN: &actor, OwnershipVersion: 1}
	case "tekroo.command.task.request-completion":
		state.Phase = kernel.PhaseActive
		state.Ownership = kernel.Ownership{OwnerFQN: &actor, OwnershipVersion: 1}
	}
	return state
}

func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
}

func storyAuthorizeCommand(t *testing.T) kernel.KernelCommand {
	t.Helper()
	command := validStoryCreateCommand(t)
	command.CommandType = "tekroo.command.story.authorize"
	command.ExpectedRevision = kernel.NewExpectedRevision(1)
	epoch := uint64(1)
	command.ExpectedLifecycleEpoch = &epoch
	command.Payload = json.RawMessage(`{"scope_revision":1,"reason":"approved"}`)
	return command
}

func storySnapshot(target kernel.AggregateRef, phase kernel.Phase, revision uint64) kernel.Snapshot {
	return kernel.Snapshot{Exists: true, Revision: revision, State: &kernel.AggregateState{
		Kind: target.Kind, ID: target.ID, Revision: revision,
		LifecycleEpoch: 1, Phase: phase, Condition: kernel.ConditionRunnable,
	}}
}

func assertTransitionMatrix(t *testing.T, model kernel.LifecycleModel, phases []kernel.Phase, actions []kernel.LifecycleAction, legal map[[2]string]kernel.Phase) {
	t.Helper()
	for _, phase := range phases {
		for _, action := range actions {
			result := kernel.ApplyLifecycleActions(model, kernel.LifecycleState{Phase: phase, Condition: kernel.ConditionRunnable, LifecycleEpoch: 1}, []kernel.LifecycleAction{action})
			want, ok := legal[[2]string{string(phase), string(action)}]
			if ok {
				if result.Rejected != nil || result.Phase != want {
					t.Fatalf("%s %s/%s rejected or reached %s", model, phase, action, result.Phase)
				}
			} else if result.Rejected == nil {
				t.Fatalf("unlisted %s transition %s/%s was accepted", model, phase, action)
			}
		}
	}
}

func nextRandom(state *uint64) uint64 {
	value := *state
	value ^= value << 13
	value ^= value >> 7
	value ^= value << 17
	*state = value
	return value
}

func referenceLifecycle(model kernel.LifecycleModel, initial kernel.LifecycleState, actions []kernel.LifecycleAction) kernel.LifecycleState {
	state := initial
	state.Rejected = nil
	for _, action := range actions {
		accepted := true
		switch action {
		case kernel.ActionBlock:
			accepted = state.Condition == kernel.ConditionRunnable && state.Phase != kernel.PhaseClosed
			if accepted {
				state.Condition = kernel.ConditionBlocked
			}
		case kernel.ActionUnblock:
			accepted = state.Condition == kernel.ConditionBlocked && state.Phase != kernel.PhaseClosed
			if accepted {
				state.Condition = kernel.ConditionRunnable
			}
		case kernel.ActionClose:
			accepted = state.Phase != kernel.PhaseClosed
			if accepted {
				state.Phase = kernel.PhaseClosed
			}
		case kernel.ActionReopen:
			accepted = (model == kernel.LifecycleStory && (state.Phase == kernel.PhaseCompleted || state.Phase == kernel.PhaseAccepted)) || (model == kernel.LifecycleTask && state.Phase == kernel.PhaseCompleted)
			if accepted {
				state.Phase = kernel.PhaseActive
				state.LifecycleEpoch++
			}
		default:
			var next kernel.Phase
			next, accepted = referenceForwardTransition(model, state.Phase, action)
			if accepted {
				state.Phase = next
			}
		}
		if !accepted {
			reason := kernel.ReasonRejectedPolicy
			state.Rejected = &reason
			return state
		}
	}
	return state
}

func referenceForwardTransition(model kernel.LifecycleModel, phase kernel.Phase, action kernel.LifecycleAction) (kernel.Phase, bool) {
	if model == kernel.LifecycleStory {
		legal := map[[2]string]kernel.Phase{
			{string(kernel.PhaseDraft), string(kernel.ActionAuthorize)}:     kernel.PhaseReady,
			{string(kernel.PhaseReady), string(kernel.ActionBeginPlanning)}: kernel.PhasePlanning,
			{string(kernel.PhasePlanning), string(kernel.ActionActivate)}:   kernel.PhaseActive,
			{string(kernel.PhaseActive), string(kernel.ActionComplete)}:     kernel.PhaseCompleted,
			{string(kernel.PhaseCompleted), string(kernel.ActionAccept)}:    kernel.PhaseAccepted,
		}
		next, ok := legal[[2]string{string(phase), string(action)}]
		return next, ok
	}
	legal := map[[2]string]kernel.Phase{
		{string(kernel.PhasePlanned), string(kernel.ActionMarkReady)}: kernel.PhaseReady,
		{string(kernel.PhaseReady), string(kernel.ActionActivate)}:    kernel.PhaseActive,
		{string(kernel.PhaseActive), string(kernel.ActionComplete)}:   kernel.PhaseCompleted,
	}
	next, ok := legal[[2]string{string(phase), string(action)}]
	return next, ok
}
