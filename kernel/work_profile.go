package kernel

import (
	"encoding/json"
	"sort"
	"time"
)

type DecisionRoute string

const (
	RouteDeterministic    DecisionRoute = "DETERMINISTIC"
	RouteBoundedExecution DecisionRoute = "BOUNDED_EXECUTION"
	RouteComplexReasoning DecisionRoute = "COMPLEX_REASONING"
	RouteNovelReasoning   DecisionRoute = "NOVEL_REASONING"
	RouteHumanRequired    DecisionRoute = "HUMAN_REQUIRED"
)

func (route DecisionRoute) Valid() bool {
	_, ok := routeRank(route)
	return ok
}

func (route DecisionRoute) ModelExecutable() bool {
	return route == RouteBoundedExecution || route == RouteComplexReasoning || route == RouteNovelReasoning
}

func (route DecisionRoute) Satisfies(required DecisionRoute) bool {
	selectedRank, selectedOK := routeRank(route)
	requiredRank, requiredOK := routeRank(required)
	return selectedOK && requiredOK && selectedRank >= requiredRank
}

func routeRank(route DecisionRoute) (uint8, bool) {
	switch route {
	case RouteDeterministic:
		return 0, true
	case RouteBoundedExecution:
		return 1, true
	case RouteComplexReasoning:
		return 2, true
	case RouteNovelReasoning:
		return 3, true
	case RouteHumanRequired:
		return 4, true
	default:
		return 0, false
	}
}

type WorkKind string

const (
	WorkInvestigation  WorkKind = "INVESTIGATION"
	WorkDesign         WorkKind = "DESIGN"
	WorkImplementation WorkKind = "IMPLEMENTATION"
	WorkDebugging      WorkKind = "DEBUGGING"
	WorkValidation     WorkKind = "VALIDATION"
	WorkSecurityReview WorkKind = "SECURITY_REVIEW"
	WorkRelease        WorkKind = "RELEASE"
)

func (kind WorkKind) Valid() bool {
	switch kind {
	case WorkInvestigation, WorkDesign, WorkImplementation, WorkDebugging, WorkValidation, WorkSecurityReview, WorkRelease:
		return true
	default:
		return false
	}
}

type Ambiguity string

const (
	AmbiguityLow     Ambiguity = "LOW"
	AmbiguityMedium  Ambiguity = "MEDIUM"
	AmbiguityHigh    Ambiguity = "HIGH"
	AmbiguityUnknown Ambiguity = "UNKNOWN"
)

func (value Ambiguity) Valid() bool {
	return value == AmbiguityLow || value == AmbiguityMedium || value == AmbiguityHigh || value == AmbiguityUnknown
}

type Novelty string

const (
	NoveltyRoutine    Novelty = "ROUTINE"
	NoveltyUnfamiliar Novelty = "UNFAMILIAR"
	NoveltyNovel      Novelty = "NOVEL"
	NoveltyUnknown    Novelty = "UNKNOWN"
)

func (value Novelty) Valid() bool {
	return value == NoveltyRoutine || value == NoveltyUnfamiliar || value == NoveltyNovel || value == NoveltyUnknown
}

type BlastRadius string

const (
	BlastLocal          BlastRadius = "LOCAL"
	BlastMultiComponent BlastRadius = "MULTI_COMPONENT"
	BlastArchitectural  BlastRadius = "ARCHITECTURAL"
	BlastExternalEffect BlastRadius = "EXTERNAL_EFFECT"
	BlastUnknown        BlastRadius = "UNKNOWN"
)

func (value BlastRadius) Valid() bool {
	return value == BlastLocal || value == BlastMultiComponent || value == BlastArchitectural || value == BlastExternalEffect || value == BlastUnknown
}

type SecuritySensitivity string

const (
	SecurityOrdinary  SecuritySensitivity = "ORDINARY"
	SecuritySensitive SecuritySensitivity = "SENSITIVE"
	SecurityCritical  SecuritySensitivity = "CRITICAL"
	SecurityUnknown   SecuritySensitivity = "UNKNOWN"
)

func (value SecuritySensitivity) Valid() bool {
	return value == SecurityOrdinary || value == SecuritySensitive || value == SecurityCritical || value == SecurityUnknown
}

type IndependenceDimension string

const (
	IndependencePrincipal    IndependenceDimension = "PRINCIPAL"
	IndependenceActor        IndependenceDimension = "ACTOR"
	IndependenceExecution    IndependenceDimension = "EXECUTION"
	IndependenceContext      IndependenceDimension = "CONTEXT"
	IndependenceWorkspace    IndependenceDimension = "WORKSPACE"
	IndependenceModelProfile IndependenceDimension = "MODEL_PROFILE"
	IndependenceProvider     IndependenceDimension = "PROVIDER"
	IndependenceMethod       IndependenceDimension = "METHOD"
	IndependenceHuman        IndependenceDimension = "HUMAN"
)

func (value IndependenceDimension) Valid() bool {
	switch value {
	case IndependencePrincipal, IndependenceActor, IndependenceExecution, IndependenceContext, IndependenceWorkspace, IndependenceModelProfile, IndependenceProvider, IndependenceMethod, IndependenceHuman:
		return true
	default:
		return false
	}
}

type WorkProfileBinding struct {
	ProfileID       UUIDv7 `json:"profile_id"`
	ProfileRevision uint64 `json:"profile_revision"`
	ProfileDigest   Digest `json:"profile_digest"`
	LifecycleEpoch  uint64 `json:"lifecycle_epoch"`
	ScopeRevision   uint64 `json:"scope_revision"`
}

func (binding WorkProfileBinding) TaskBindingMatches(task AggregateState) bool {
	return binding.Valid() && task.Kind == AggregateTask && task.ID.Valid() && binding.LifecycleEpoch == task.LifecycleEpoch && binding.ScopeRevision == task.ScopeRevision
}

func (binding WorkProfileBinding) Valid() bool {
	return binding.ProfileID.Valid() && binding.ProfileRevision > 0 && binding.ProfileDigest.Valid() && binding.LifecycleEpoch > 0 && binding.ScopeRevision > 0
}

type FiniteWorkBudgets struct {
	AttemptLimit     uint64    `json:"attempt_limit"`
	ReviewRoundLimit uint64    `json:"review_round_limit"`
	PromotionLimit   uint64    `json:"promotion_limit"`
	EscalationLimit  uint64    `json:"escalation_limit"`
	DeadlineAt       time.Time `json:"deadline_at"`
}

func (budgets FiniteWorkBudgets) Valid() bool {
	return budgets.AttemptLimit > 0 && budgets.AttemptLimit <= 1000 && budgets.ReviewRoundLimit > 0 && budgets.ReviewRoundLimit <= 1000 && budgets.PromotionLimit <= 1000 && budgets.EscalationLimit > 0 && budgets.EscalationLimit <= 1000 && !budgets.DeadlineAt.IsZero()
}

type WorkRiskProfile struct {
	TaskID                         UUIDv7                  `json:"task_id"`
	ProfileID                      UUIDv7                  `json:"profile_id"`
	ProfileRevision                uint64                  `json:"profile_revision"`
	ProfileDigest                  Digest                  `json:"profile_digest"`
	LifecycleEpoch                 uint64                  `json:"lifecycle_epoch"`
	ScopeRevision                  uint64                  `json:"scope_revision"`
	WorkKind                       WorkKind                `json:"work_kind"`
	Ambiguity                      Ambiguity               `json:"ambiguity"`
	Novelty                        Novelty                 `json:"novelty"`
	BlastRadius                    BlastRadius             `json:"blast_radius"`
	SecuritySensitivity            SecuritySensitivity     `json:"security_sensitivity"`
	MinimumDecisionRoute           DecisionRoute           `json:"minimum_decision_route"`
	AcceptanceCriteriaDigest       Digest                  `json:"acceptance_criteria_digest"`
	RequiredDeterministicGateIDs   []string                `json:"required_deterministic_gate_ids"`
	RequiredValidationBranches     uint64                  `json:"required_validation_branches"`
	RequiredIndependenceDimensions []IndependenceDimension `json:"required_independence_dimensions"`
	ImplementationVariantCount     uint64                  `json:"implementation_variant_count"`
	ValidCandidateQuorum           uint64                  `json:"valid_candidate_quorum"`
	VerificationTopologyDigest     Digest                  `json:"verification_topology_digest"`
	ClassificationPolicyRevision   uint64                  `json:"classification_policy_revision"`
	ClassificationPolicyDigest     Digest                  `json:"classification_policy_digest"`
	PromotionPolicyRevision        uint64                  `json:"promotion_policy_revision"`
	PromotionPolicyDigest          Digest                  `json:"promotion_policy_digest"`
	Budgets                        FiniteWorkBudgets       `json:"budgets"`
	ClassificationAuthority        PrincipalRef            `json:"classification_authority"`
	ClassificationEvidenceIDs      []UUIDv7                `json:"classification_evidence_ids"`
	SupersedesProfileID            *UUIDv7                 `json:"supersedes_profile_id"`
}

func (profile WorkRiskProfile) Binding() WorkProfileBinding {
	return WorkProfileBinding{ProfileID: profile.ProfileID, ProfileRevision: profile.ProfileRevision, ProfileDigest: profile.ProfileDigest, LifecycleEpoch: profile.LifecycleEpoch, ScopeRevision: profile.ScopeRevision}
}

func (profile WorkRiskProfile) Valid() bool {
	if !profile.TaskID.Valid() || !profile.Binding().Valid() || !profile.WorkKind.Valid() || !profile.Ambiguity.Valid() || !profile.Novelty.Valid() || !profile.BlastRadius.Valid() || !profile.SecuritySensitivity.Valid() || !profile.MinimumDecisionRoute.Valid() || !profile.AcceptanceCriteriaDigest.Valid() || !profile.VerificationTopologyDigest.Valid() || profile.RequiredValidationBranches == 0 || profile.RequiredValidationBranches > 32 || profile.ImplementationVariantCount == 0 || profile.ImplementationVariantCount > 32 || profile.ValidCandidateQuorum == 0 || profile.ValidCandidateQuorum > profile.ImplementationVariantCount || profile.ClassificationPolicyRevision == 0 || !profile.ClassificationPolicyDigest.Valid() || profile.PromotionPolicyRevision == 0 || !profile.PromotionPolicyDigest.Valid() || !profile.Budgets.Valid() || !profile.ClassificationAuthority.Valid() {
		return false
	}
	if profile.SupersedesProfileID != nil && !profile.SupersedesProfileID.Valid() {
		return false
	}
	if !validUniqueStrings(profile.RequiredDeterministicGateIDs, 1, 64) || !validUniqueDimensions(profile.RequiredIndependenceDimensions) || !validUniqueUUIDs(profile.ClassificationEvidenceIDs, 1, 64) {
		return false
	}
	return true
}

func (profile WorkRiskProfile) Clone() WorkRiskProfile {
	copy := profile
	copy.RequiredDeterministicGateIDs = append([]string(nil), profile.RequiredDeterministicGateIDs...)
	copy.RequiredIndependenceDimensions = append([]IndependenceDimension(nil), profile.RequiredIndependenceDimensions...)
	copy.ClassificationEvidenceIDs = append([]UUIDv7(nil), profile.ClassificationEvidenceIDs...)
	if profile.SupersedesProfileID != nil {
		value := *profile.SupersedesProfileID
		copy.SupersedesProfileID = &value
	}
	return copy
}

func WorkRiskProfileFromPayload(payload json.RawMessage) (WorkRiskProfile, error) {
	var profile WorkRiskProfile
	if err := json.Unmarshal(payload, &profile); err != nil {
		return WorkRiskProfile{}, err
	}
	if !profile.Valid() {
		return WorkRiskProfile{}, ErrInvalidPayload
	}
	return profile, nil
}

type WorkProfileSnapshot struct {
	Profile      WorkRiskProfile `json:"profile"`
	BoundEventID UUIDv7          `json:"bound_event_id"`
	TaskRevision uint64          `json:"task_revision"`
}

func (snapshot WorkProfileSnapshot) Valid() bool {
	return snapshot.Profile.Valid() && snapshot.BoundEventID.Valid() && snapshot.TaskRevision > 0
}

func (snapshot WorkProfileSnapshot) Clone() WorkProfileSnapshot {
	copy := snapshot
	copy.Profile = snapshot.Profile.Clone()
	return copy
}

type WorkProfileBindingStatus string

const (
	WorkProfileReady    WorkProfileBindingStatus = "READY_TO_BIND"
	WorkProfileStale    WorkProfileBindingStatus = "STALE_WORK_PROFILE"
	WorkProfileConflict WorkProfileBindingStatus = "PROFILE_REVISION_CONFLICT"
	WorkProfileInvalid  WorkProfileBindingStatus = "INVALID_INPUT"
)

type WorkProfileBindingInput struct {
	Task    AggregateState
	Profile WorkRiskProfile
	Current *WorkProfileSnapshot
}

type WorkProfileBindingDecision struct {
	Status  WorkProfileBindingStatus
	Reason  string
	Task    AggregateState
	Profile WorkRiskProfile
}

func PlanWorkProfileBinding(input WorkProfileBindingInput) WorkProfileBindingDecision {
	decision := WorkProfileBindingDecision{Status: WorkProfileInvalid, Reason: "INVALID_WORK_PROFILE", Task: input.Task.Clone(), Profile: input.Profile.Clone()}
	if input.Task.Kind != AggregateTask || !input.Task.ID.Valid() || input.Task.Revision == 0 || input.Task.LifecycleEpoch == 0 || input.Task.ScopeRevision == 0 || !input.Profile.Valid() || input.Profile.TaskID != input.Task.ID {
		return decision
	}
	if input.Profile.LifecycleEpoch != input.Task.LifecycleEpoch || input.Profile.ScopeRevision != input.Task.ScopeRevision {
		decision.Status, decision.Reason = WorkProfileStale, "STALE_WORK_PROFILE"
		return decision
	}
	if input.Current == nil {
		if input.Profile.ProfileRevision != 1 || input.Profile.SupersedesProfileID != nil {
			decision.Status, decision.Reason = WorkProfileConflict, "PROFILE_REVISION_CONFLICT"
			return decision
		}
	} else {
		current := input.Current.Profile
		if !input.Current.Valid() || current.TaskID != input.Task.ID || input.Profile.ProfileRevision != current.ProfileRevision+1 || input.Profile.SupersedesProfileID == nil || *input.Profile.SupersedesProfileID != current.ProfileID || input.Profile.ProfileDigest == current.ProfileDigest {
			decision.Status, decision.Reason = WorkProfileConflict, "PROFILE_REVISION_CONFLICT"
			return decision
		}
	}
	decision.Status, decision.Reason = WorkProfileReady, "READY_TO_BIND"
	return decision
}

func validUniqueStrings(values []string, minimum, maximum int) bool {
	if len(values) < minimum || len(values) > maximum {
		return false
	}
	copy := append([]string(nil), values...)
	for _, value := range copy {
		if value == "" || len(value) > 4096 {
			return false
		}
	}
	sort.Strings(copy)
	for index := 1; index < len(copy); index++ {
		if copy[index-1] == copy[index] {
			return false
		}
	}
	return true
}

func validUniqueDimensions(values []IndependenceDimension) bool {
	if len(values) == 0 || len(values) > 9 {
		return false
	}
	seen := make(map[IndependenceDimension]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validUniqueUUIDs(values []UUIDv7, minimum, maximum int) bool {
	if len(values) < minimum || len(values) > maximum {
		return false
	}
	seen := make(map[UUIDv7]struct{}, len(values))
	for _, value := range values {
		if !value.Valid() {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
