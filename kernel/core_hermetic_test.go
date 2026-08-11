package kernel_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	fixtureBytes, err := os.ReadFile(filepath.Join(root, "CONTRACTS/tekroo.kernel.contracts/0.2.0/fixtures/catalogue-coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalogueBytes, err := os.ReadFile(filepath.Join(root, "CONTRACTS/tekroo.kernel.contracts/0.2.0/catalogue/kernel-catalogue.json"))
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
	for _, item := range fixtures.Fixtures {
		if !strings.HasSuffix(item.FixtureID, "-VALID") {
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
				t.Fatalf("decision = %#v", decision)
			}
		})
		executed++
	}
	if executed != 26 {
		t.Fatalf("executed command cases = %d, want 26", executed)
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
		if values, ok := object["evidence_ids"].([]any); ok {
			snapshot.Evidence = make(map[kernel.UUIDv7]kernel.EvidenceMetadata, len(values))
			for _, value := range values {
				id := kernel.UUIDv7(value.(string))
				digest := kernel.Digest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")
				command.EvidenceRefs = append(command.EvidenceRefs, kernel.EvidenceRef{EvidenceID: id, SHA256: digest})
				snapshot.Evidence[id] = kernel.EvidenceMetadata{SHA256: digest, Available: true}
			}
		}
		if values, ok := object["validation_event_ids"].([]any); ok {
			if snapshot.AcceptedEvents == nil {
				snapshot.AcceptedEvents = make(map[kernel.UUIDv7]kernel.AcceptedEvent)
			}
			for _, value := range values {
				snapshot.AcceptedEvents[kernel.UUIDv7(value.(string))] = kernel.AcceptedEvent{EventType: "tekroo.event.completion-review.result-recorded", Qualification: "PASS"}
			}
		}
		if value, ok := object["target_event_id"].(string); ok {
			if snapshot.AcceptedEvents == nil {
				snapshot.AcceptedEvents = make(map[kernel.UUIDv7]kernel.AcceptedEvent)
			}
			snapshot.AcceptedEvents[kernel.UUIDv7(value)] = kernel.AcceptedEvent{EventType: "tekroo.event.story.completed"}
		}
		if commandType == "tekroo.command.completion-review.open" {
			subject := kernel.AggregateRef{Kind: kernel.AggregateKind(object["subject_kind"].(string)), ID: kernel.UUIDv7(object["subject_id"].(string))}
			command.Preconditions = []kernel.AggregatePrecondition{{Aggregate: subject, Expected: kernel.NewExpectedRevision(1)}}
			snapshot.Related = map[kernel.AggregateRef]kernel.RelatedSnapshot{subject: {
				Exists: true, Revision: 1,
				State: &kernel.AggregateState{Kind: subject.Kind, ID: subject.ID, Revision: 1, LifecycleEpoch: uint64(object["lifecycle_epoch"].(float64)), Phase: kernel.PhaseActive, Condition: kernel.ConditionRunnable},
			}}
		}
		if commandType == "tekroo.command.completion-review.record-result" {
			reviewID := kernel.UUIDv7(object["review_id"].(string))
			command.Target.ID = reviewID
			policyRevision := uint64(object["branch_policy_revision"].(float64))
			branchID := object["branch_id"].(string)
			snapshot.Reviews = map[kernel.AggregateRef]kernel.CompletionReviewSnapshot{command.Target: {
				BranchPolicyRevision: policyRevision,
				RequiredBranchIDs:    []string{branchID},
				PartialResultPolicy:  "WAIT_ALL",
				Results:              map[string]string{},
				Join:                 kernel.ReviewJoinResult{Status: "PENDING"},
			}}
		}
	}
	return command, snapshot
}

func stateForCommand(commandType string, target kernel.AggregateRef) *kernel.AggregateState {
	state := &kernel.AggregateState{Kind: target.Kind, ID: target.ID, Revision: 1, LifecycleEpoch: 1, Condition: kernel.ConditionRunnable}
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
