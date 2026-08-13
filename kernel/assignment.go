package kernel

import (
	"sort"
	"time"
)

type AssignmentStatus string

const (
	AssignmentReady           AssignmentStatus = "READY_TO_ASSIGN"
	AssignmentBlocked         AssignmentStatus = "BLOCKED_DEPENDENCY"
	AssignmentNoEligibleActor AssignmentStatus = "NO_ELIGIBLE_ACTOR"
	AssignmentAlreadyOwned    AssignmentStatus = "ALREADY_OWNED"
	AssignmentNotRunnable     AssignmentStatus = "NOT_RUNNABLE"
	AssignmentInvalid         AssignmentStatus = "INVALID_INPUT"
)

type DependencyRequirement struct {
	Aggregate       AggregateRef
	RequiredPhase   Phase
	ObservedState   *AggregateState
	EvidenceEventID UUIDv7
}

type AssignmentCandidate struct {
	ActorFQN              ActorFQN
	Execution             ExecutionTuple
	ActiveAssignments     uint32
	ModelProfileDigest    Digest
	RuntimeIdentityDigest Digest
	Qualification         ModelProfileQualification
	HardConstraints       []HardConstraintResult
	SelectionEvidence     []EvidenceRef
	SelectionReasons      []string
	ExpectedTotalCost     CostObservation
}

type AssignmentInput struct {
	Task                    AggregateState
	Dependencies            []DependencyRequirement
	ReadinessParents        []DagParent
	ReadiedEventID          UUIDv7
	ReadinessPolicyRevision uint64
	WorkProfile             WorkProfileSnapshot
	SelectionPolicyRevision uint64
	SelectionPolicyDigest   Digest
	DecidedAt               time.Time
	Candidates              []AssignmentCandidate
}

type AssignmentDecision struct {
	Status                  AssignmentStatus
	Reason                  string
	Task                    AggregateState
	NeedsReadiness          bool
	ReadinessPolicyRevision uint64
	DependencyEventIDs      []UUIDv7
	ReadinessParents        []DagParent
	ActorFQN                *ActorFQN
	Execution               *ExecutionTuple
	WorkProfile             WorkProfileBinding
	RequiredDecisionRoute   DecisionRoute
	SelectedDecisionRoute   DecisionRoute
	ModelProfileDigest      Digest
	RuntimeIdentityDigest   Digest
	Qualification           *ModelProfileQualification
	SelectionPolicyRevision uint64
	SelectionPolicyDigest   Digest
	HardConstraints         []HardConstraintResult
	SelectionEvidence       []EvidenceRef
	SelectionReasons        []string
	ExpectedTotalCost       CostObservation
}

func PlanAssignment(input AssignmentInput) AssignmentDecision {
	decision := AssignmentDecision{Status: AssignmentInvalid, Reason: "INVALID_ASSIGNMENT_INPUT", Task: input.Task.Clone()}
	if !validAssignmentTask(input.Task) || input.ReadinessPolicyRevision == 0 || input.SelectionPolicyRevision == 0 || !input.SelectionPolicyDigest.Valid() || input.DecidedAt.IsZero() || !input.WorkProfile.Valid() || input.WorkProfile.Profile.TaskID != input.Task.ID || input.WorkProfile.Profile.LifecycleEpoch != input.Task.LifecycleEpoch || input.WorkProfile.Profile.ScopeRevision != input.Task.ScopeRevision || !input.WorkProfile.Profile.MinimumDecisionRoute.ModelExecutable() || len(input.Dependencies) > 64 || len(input.ReadinessParents) == 0 || len(input.ReadinessParents) > 64 || len(input.Candidates) > 64 {
		return decision
	}
	decision.WorkProfile = input.WorkProfile.Profile.Binding()
	decision.RequiredDecisionRoute = input.WorkProfile.Profile.MinimumDecisionRoute
	decision.SelectionPolicyRevision = input.SelectionPolicyRevision
	decision.SelectionPolicyDigest = input.SelectionPolicyDigest
	parents, ok := canonicalAssignmentParents(input.ReadinessParents)
	if !ok {
		return decision
	}
	decision.ReadinessParents = parents
	decision.ReadinessPolicyRevision = input.ReadinessPolicyRevision
	parentIDs := make(map[UUIDv7]struct{}, len(parents))
	for _, parent := range parents {
		parentIDs[parent.ParentEventID] = struct{}{}
	}
	if input.Task.Ownership.OwnerFQN != nil {
		decision.Status, decision.Reason = AssignmentAlreadyOwned, "TASK_ALREADY_OWNED"
		return decision
	}
	if input.Task.Condition != ConditionRunnable || (input.Task.Phase != PhasePlanned && input.Task.Phase != PhaseReady) {
		decision.Status, decision.Reason = AssignmentNotRunnable, "TASK_NOT_RUNNABLE"
		return decision
	}
	if input.Task.Phase == PhaseReady && !input.ReadiedEventID.Valid() {
		return decision
	}

	dependencies := append([]DependencyRequirement(nil), input.Dependencies...)
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].Aggregate.Kind != dependencies[j].Aggregate.Kind {
			return dependencies[i].Aggregate.Kind < dependencies[j].Aggregate.Kind
		}
		return dependencies[i].Aggregate.ID < dependencies[j].Aggregate.ID
	})
	for index, dependency := range dependencies {
		if !dependency.Aggregate.Valid() || !terminalDependencyPhase(dependency.RequiredPhase) || !dependency.EvidenceEventID.Valid() || (index > 0 && dependencies[index-1].Aggregate == dependency.Aggregate) {
			return decision
		}
		decision.DependencyEventIDs = append(decision.DependencyEventIDs, dependency.EvidenceEventID)
		if _, linked := parentIDs[dependency.EvidenceEventID]; !linked {
			return decision
		}
		if dependency.ObservedState == nil || dependency.ObservedState.Kind != dependency.Aggregate.Kind || dependency.ObservedState.ID != dependency.Aggregate.ID || dependency.ObservedState.Revision == 0 || dependency.ObservedState.LifecycleEpoch == 0 || dependency.ObservedState.Phase != dependency.RequiredPhase {
			decision.Status, decision.Reason = AssignmentBlocked, "DEPENDENCY_PREDICATE_UNSATISFIED"
			return decision
		}
	}
	sort.Slice(decision.DependencyEventIDs, func(i, j int) bool { return decision.DependencyEventIDs[i] < decision.DependencyEventIDs[j] })
	for index := 1; index < len(decision.DependencyEventIDs); index++ {
		if decision.DependencyEventIDs[index-1] == decision.DependencyEventIDs[index] {
			decision.Status, decision.Reason = AssignmentInvalid, "INVALID_ASSIGNMENT_INPUT"
			return decision
		}
	}

	eligible := make([]AssignmentCandidate, 0, len(input.Candidates))
	seenActors := make(map[ActorFQN]struct{}, len(input.Candidates))
	for _, candidate := range input.Candidates {
		if !candidate.ActorFQN.Valid() || !candidate.Execution.Valid() || !candidate.ModelProfileDigest.Valid() || !candidate.RuntimeIdentityDigest.Valid() {
			return decision
		}
		if _, duplicate := seenActors[candidate.ActorFQN]; duplicate {
			return decision
		}
		seenActors[candidate.ActorFQN] = struct{}{}
		if candidateEligible(candidate, input.WorkProfile.Profile, input.DecidedAt) {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		decision.Status, decision.Reason = AssignmentNoEligibleActor, "NO_ELIGIBLE_ACTOR"
		return decision
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].ExpectedTotalCost.Reported != eligible[j].ExpectedTotalCost.Reported {
			return eligible[i].ExpectedTotalCost.Reported
		}
		if eligible[i].ExpectedTotalCost.Reported && eligible[i].ExpectedTotalCost.Microunits != eligible[j].ExpectedTotalCost.Microunits {
			return eligible[i].ExpectedTotalCost.Microunits < eligible[j].ExpectedTotalCost.Microunits
		}
		if eligible[i].ActiveAssignments != eligible[j].ActiveAssignments {
			return eligible[i].ActiveAssignments < eligible[j].ActiveAssignments
		}
		return eligible[i].ActorFQN < eligible[j].ActorFQN
	})
	actor, execution := eligible[0].ActorFQN, eligible[0].Execution
	selected := eligible[0]
	decision.ActorFQN, decision.Execution = &actor, &execution
	decision.SelectedDecisionRoute = selected.Qualification.DecisionRoute
	decision.ModelProfileDigest = selected.ModelProfileDigest
	decision.RuntimeIdentityDigest = selected.RuntimeIdentityDigest
	qualification := selected.Qualification.Clone()
	decision.Qualification = &qualification
	decision.HardConstraints = cloneHardConstraints(selected.HardConstraints)
	decision.SelectionEvidence = append([]EvidenceRef(nil), selected.SelectionEvidence...)
	decision.SelectionReasons = append([]string(nil), selected.SelectionReasons...)
	decision.ExpectedTotalCost = selected.ExpectedTotalCost
	decision.NeedsReadiness = input.Task.Phase == PhasePlanned
	if !decision.NeedsReadiness {
		decision.ReadinessParents = []DagParent{{ParentEventID: input.ReadiedEventID, EdgeKind: EdgeCausal}}
	}
	decision.Status, decision.Reason = AssignmentReady, "READY_TO_ASSIGN"
	return decision
}

func validAssignmentTask(task AggregateState) bool {
	return task.Kind == AggregateTask && task.ID.Valid() && task.Revision > 0 && task.LifecycleEpoch > 0 && task.ScopeRevision > 0
}

func candidateEligible(candidate AssignmentCandidate, profile WorkRiskProfile, decidedAt time.Time) bool {
	if !candidate.Qualification.EligibleAt(decidedAt) || candidate.Qualification.ModelProfileDigest != candidate.ModelProfileDigest || !candidate.Qualification.DecisionRoute.ModelExecutable() || !candidate.Qualification.DecisionRoute.Satisfies(profile.MinimumDecisionRoute) || !containsWorkKind(candidate.Qualification.QualifiedWorkKinds, profile.WorkKind) || !validEvidenceSet(candidate.SelectionEvidence) || !validUniqueStrings(candidate.SelectionReasons, 1, 64) || len(candidate.HardConstraints) == 0 || len(candidate.HardConstraints) > 64 {
		return false
	}
	for _, constraint := range candidate.HardConstraints {
		if !constraint.Valid() || constraint.Outcome != ConstraintPass {
			return false
		}
	}
	return candidate.ExpectedTotalCost.Valid()
}

func validEvidenceSet(values []EvidenceRef) bool {
	if len(values) == 0 || len(values) > 64 {
		return false
	}
	seen := make(map[UUIDv7]struct{}, len(values))
	for _, value := range values {
		if !value.EvidenceID.Valid() || !value.SHA256.Valid() {
			return false
		}
		if _, duplicate := seen[value.EvidenceID]; duplicate {
			return false
		}
		seen[value.EvidenceID] = struct{}{}
	}
	return true
}

func containsWorkKind(values []WorkKind, target WorkKind) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func terminalDependencyPhase(phase Phase) bool {
	return phase == PhaseCompleted || phase == PhaseAccepted || phase == PhaseClosed
}

func canonicalAssignmentParents(values []DagParent) ([]DagParent, bool) {
	parents := append([]DagParent(nil), values...)
	for _, parent := range parents {
		if !parent.ParentEventID.Valid() || !parent.EdgeKind.Valid() {
			return nil, false
		}
	}
	sort.Slice(parents, func(i, j int) bool {
		if parents[i].EdgeKind != parents[j].EdgeKind {
			return parents[i].EdgeKind < parents[j].EdgeKind
		}
		return parents[i].ParentEventID < parents[j].ParentEventID
	})
	for index := 1; index < len(parents); index++ {
		if parents[index-1] == parents[index] {
			return nil, false
		}
	}
	return parents, true
}
