package operationalruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

type nativeRetryFixture struct {
	invocations map[kernel.UUIDv7]kernel.WorkInvocation
	journals    map[string][]agentruntime.Entry
}

func (f nativeRetryFixture) LoadOperationalExecution(_ context.Context, id kernel.UUIDv7) (application.OperationalExecutionContext, error) {
	invocation, found := f.invocations[id]
	if !found {
		return application.OperationalExecutionContext{}, nativeagent.ErrInvalidBinding
	}
	return application.OperationalExecutionContext{Invocation: invocation}, nil
}
func (f nativeRetryFixture) Load(_ context.Context, id string) ([]agentruntime.Entry, error) {
	return f.journals[id], nil
}

func TestNativeRetryResolverDistinguishesNeverStartedFromLostHistory(t *testing.T) {
	id := deterministicOperationalUUID("retry-context-regression", "parent")
	digest := repeatedDigest('a')
	finished := time.Now().UTC()
	prior := kernel.WorkInvocation{ID: id, Revision: 4, State: kernel.InvocationStartFailed, AuthorizationEventID: id, ParentEventID: id, TaskID: id, BudgetAccountID: id, LifecycleEpoch: 1, ScopeRevision: 1, TaskRevision: 1,
		WorkProfile: kernel.WorkProfileBinding{ProfileID: id, ProfileRevision: 1, ProfileDigest: digest, LifecycleEpoch: 1, ScopeRevision: 1}, QualifiedAssignmentID: id, Purpose: kernel.PurposeHandoff, AttemptFamily: "design", AttemptOrdinal: 1, ConditionDigest: digest, OutputPredicateDigest: digest,
		AllowedTerminalOutcomes: []kernel.WorkInvocationState{kernel.InvocationStartFailed}, ToolPolicyDigest: digest, EffectPolicyDigest: digest, ActorFQN: "teams::architect-1", Execution: kernel.ExecutionTuple{ExecutionID: id, FencingEpoch: 1}, ModelProfileDigest: digest, RuntimeIdentityDigest: digest, WorkspaceID: "project", DeadlineAt: finished.Add(time.Hour), IdempotencyKey: "parent", AdmissionPolicyRevision: 1, AdmissionPolicyDigest: digest, GlobalDebitOrdinal: 1, PurposeDebitOrdinal: 1, TerminalEvidenceIDs: []kernel.UUIDv7{id}, FinishedAt: &finished, LastEventID: id}
	if !prior.Valid() {
		t.Fatal("invalid fixture")
	}
	child := application.ExecutionBrief{InvocationID: deterministicOperationalUUID("retry-context-regression", "child"), Task: application.TaskExecutionSpecification{TaskID: id}, Purpose: prior.Purpose, ActorFQN: prior.ActorFQN, Scope: kernel.TaskOperationalScope{WorkspaceID: prior.WorkspaceID}, RetryOfInvocationID: &id, AttemptOrdinal: 2, RetryOrdinal: 1, ConditionDigest: repeatedDigest('b'), Evidence: []kernel.EvidenceRef{{EvidenceID: id, SHA256: digest}}}
	fixture := nativeRetryFixture{invocations: map[kernel.UUIDv7]kernel.WorkInvocation{id: prior}, journals: map[string][]agentruntime.Entry{}}
	source, grant, err := nativeRetryContext(t.Context(), fixture, child)
	if err != nil || source != nil || grant == nil || len(grant.PreStartRetries) != 1 {
		t.Fatal("genuine pre-start failure could not start fresh", err)
	}
	fixture.journals[string(id)] = []agentruntime.Entry{{Kind: agentruntime.Started}}
	if _, _, err := nativeRetryContext(t.Context(), fixture, child); !errors.Is(err, nativeagent.ErrInvalidBinding) {
		t.Fatal("discarded an existing journal", err)
	}
	delete(fixture.journals, string(id))
	prior.State, prior.RequestDigest, prior.StartedAt = kernel.InvocationFailed, &digest, &finished
	fixture.invocations[id] = prior
	source, grant, err = nativeRetryContext(t.Context(), fixture, child)
	if err != nil || source == nil || source.ID != id || grant != nil {
		t.Fatal("started failure silently became a fresh start", err)
	}
	// Binding the selected started source will reject its absent journal; the
	// resolver must not erase that evidence failure by returning nil instead.
}

func TestDecisionOwnerReturnDoesNotReplaceStartedAdvisoryRetryHistory(t *testing.T) {
	original := kernel.WorkInvocation{ID: deterministicOperationalUUID("advisory-retry", "design-invocation"), TaskID: deterministicOperationalUUID("advisory-retry", "design-task")}
	advisory := kernel.WorkInvocation{ID: deterministicOperationalUUID("advisory-retry", "advisory-invocation"), TaskID: deterministicOperationalUUID("advisory-retry", "advisory-task")}
	brief := application.ExecutionBrief{Task: application.TaskExecutionSpecification{TaskID: advisory.TaskID}, RetryOfInvocationID: &advisory.ID}
	if !decisionOwnerReturnCompatible(brief, &original, &advisory) {
		t.Fatal("started advisory retry was forced back to the original design history")
	}
	if !decisionOwnerReturnCompatible(brief, &original, &original) {
		t.Fatal("never-started advisory lost its original decision-owner dependency")
	}
	brief.RetryOfInvocationID = nil
	if decisionOwnerReturnCompatible(brief, &original, &advisory) {
		t.Fatal("non-retry substituted advisory history for original decision owner")
	}
	brief.RetryOfInvocationID = &advisory.ID
	advisory.TaskID = deterministicOperationalUUID("advisory-retry", "unrelated-task")
	if decisionOwnerReturnCompatible(brief, &original, &advisory) {
		t.Fatal("retry substituted an unrelated task's history")
	}
}
