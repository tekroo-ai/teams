package kernel

import (
	"errors"
	"regexp"
	"strings"
)

const (
	ContractIdentity         = "tekroo.kernel.contracts/0.11.0"
	SchemaVersion            = "1.6.0"
	OperationalSchemaVersion = "1.7.0"
	CatalogueRevision        = uint64(8)
)

var (
	actorFQNPattern = regexp.MustCompile(`^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$`)
	roleFQRNPattern = regexp.MustCompile(`^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])$`)
	uuidV7Pattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	digestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type ActorFQN string

func ParseActorFQN(value string) (ActorFQN, error) {
	if !actorFQNPattern.MatchString(value) {
		return "", errors.New("invalid actor FQN")
	}
	return ActorFQN(value), nil
}

func (f ActorFQN) Valid() bool { return actorFQNPattern.MatchString(string(f)) }

// RoleFQRN identifies a role definition (role). ActorFQN identifies one
// running instance of that role definition (team::role-N).
type RoleFQRN string

func ParseRoleFQRN(value string) (RoleFQRN, error) {
	if !validRoleFQRN(value) {
		return "", errors.New("invalid role FQRN")
	}
	return RoleFQRN(value), nil
}

func (f RoleFQRN) Valid() bool { return validRoleFQRN(string(f)) }

func validRoleFQRN(value string) bool {
	return roleFQRNPattern.MatchString(value)
}

func RoleFQRNFromActor(actor ActorFQN) (RoleFQRN, error) {
	if !actor.Valid() {
		return "", errors.New("invalid actor FQN")
	}
	_, roleAndInstance, found := strings.Cut(string(actor), "::")
	if !found {
		return "", errors.New("invalid actor FQN")
	}
	separator := strings.LastIndexByte(roleAndInstance, '-')
	if separator < 0 {
		return "", errors.New("invalid actor FQN")
	}
	return ParseRoleFQRN(roleAndInstance[:separator])
}

type UUIDv7 string

func ParseUUIDv7(value string) (UUIDv7, error) {
	if !uuidV7Pattern.MatchString(value) {
		return "", errors.New("invalid UUIDv7")
	}
	return UUIDv7(value), nil
}

func (id UUIDv7) Valid() bool { return uuidV7Pattern.MatchString(string(id)) }

type Digest string

func ParseDigest(value string) (Digest, error) {
	if !digestPattern.MatchString(value) {
		return "", errors.New("invalid SHA-256 digest")
	}
	return Digest(value), nil
}

func (d Digest) Valid() bool { return digestPattern.MatchString(string(d)) }

type AggregateKind string

const (
	AggregateStory            AggregateKind = "story"
	AggregateTask             AggregateKind = "task"
	AggregateCompletionReview AggregateKind = "completion-review"
	AggregateEscalation       AggregateKind = "escalation"
	AggregateReleasePlan      AggregateKind = "release-plan"
	AggregateVariantGroup     AggregateKind = "variant-group"
	AggregateHumanParticipant AggregateKind = "human-participant"
	AggregateHumanInteraction AggregateKind = "human-interaction"
	AggregateEvidence         AggregateKind = "evidence"
	AggregateExecution        AggregateKind = "execution"
	AggregateSystem           AggregateKind = "system"
	AggregateWorkBudget       AggregateKind = "work-budget-account"
	AggregateWorkInvocation   AggregateKind = "work-invocation"
	AggregateWorkflowInstance AggregateKind = "workflow-instance"
)

func (k AggregateKind) Valid() bool {
	switch k {
	case AggregateStory, AggregateTask, AggregateCompletionReview, AggregateEscalation, AggregateReleasePlan, AggregateVariantGroup, AggregateHumanParticipant, AggregateHumanInteraction, AggregateEvidence, AggregateExecution, AggregateSystem, AggregateWorkBudget, AggregateWorkInvocation, AggregateWorkflowInstance:
		return true
	default:
		return false
	}
}

type AggregateRef struct {
	Kind AggregateKind `json:"kind"`
	ID   UUIDv7        `json:"id"`
}

func (r AggregateRef) Valid() bool { return r.Kind.Valid() && r.ID.Valid() }

type PrincipalKind string

const (
	PrincipalActor   PrincipalKind = "ACTOR"
	PrincipalHuman   PrincipalKind = "HUMAN"
	PrincipalService PrincipalKind = "SERVICE"
	PrincipalPolicy  PrincipalKind = "POLICY"
)

type PrincipalRef struct {
	Kind PrincipalKind `json:"kind"`
	ID   string        `json:"id"`
}

func (p PrincipalRef) Valid() bool {
	if len(p.ID) < 1 || len(p.ID) > 256 {
		return false
	}
	switch p.Kind {
	case PrincipalActor, PrincipalHuman, PrincipalService, PrincipalPolicy:
		return true
	default:
		return false
	}
}

type ExecutionTuple struct {
	ExecutionID  UUIDv7 `json:"execution_id"`
	FencingEpoch uint64 `json:"fencing_epoch"`
}

func (e ExecutionTuple) Valid() bool { return e.ExecutionID.Valid() && e.FencingEpoch > 0 }

type EdgeKind string

const (
	EdgeCausal       EdgeKind = "CAUSAL"
	EdgeResponse     EdgeKind = "RESPONSE"
	EdgeRetry        EdgeKind = "RETRY"
	EdgeDerivation   EdgeKind = "DERIVATION"
	EdgeSupersession EdgeKind = "SUPERSESSION"
)

func (k EdgeKind) Valid() bool {
	switch k {
	case EdgeCausal, EdgeResponse, EdgeRetry, EdgeDerivation, EdgeSupersession:
		return true
	default:
		return false
	}
}

type DagParent struct {
	ParentEventID UUIDv7   `json:"parent_event_id"`
	EdgeKind      EdgeKind `json:"edge_kind"`
}

type EvidenceRef struct {
	EvidenceID UUIDv7 `json:"evidence_id"`
	SHA256     Digest `json:"sha256"`
}

type Phase string

const (
	PhaseDraft     Phase = "DRAFT"
	PhasePlanned   Phase = "PLANNED"
	PhaseReady     Phase = "READY"
	PhasePlanning  Phase = "PLANNING"
	PhaseActive    Phase = "ACTIVE"
	PhaseCompleted Phase = "COMPLETED"
	PhaseAccepted  Phase = "ACCEPTED"
	PhaseClosed    Phase = "CLOSED"
)

type Condition string

const (
	ConditionRunnable Condition = "RUNNABLE"
	ConditionBlocked  Condition = "BLOCKED"
)

type Ownership struct {
	OwnerFQN         *ActorFQN `json:"owner_fqn"`
	OwnershipVersion uint64    `json:"ownership_version"`
	AssignedEventID  *UUIDv7   `json:"assigned_event_id"`
}

type AggregateState struct {
	Kind           AggregateKind             `json:"kind"`
	ID             UUIDv7                    `json:"id"`
	Revision       uint64                    `json:"revision"`
	LifecycleEpoch uint64                    `json:"lifecycle_epoch"`
	ScopeRevision  uint64                    `json:"scope_revision"`
	Phase          Phase                     `json:"phase"`
	Condition      Condition                 `json:"condition"`
	Ownership      Ownership                 `json:"ownership"`
	OperatorRole   *OperatorRoleProfile      `json:"operator_role,omitempty"`
	Participant    *HumanParticipantSnapshot `json:"human_participant,omitempty"`
	Interaction    *HumanInteractionSnapshot `json:"human_interaction,omitempty"`
	Continuity     *TeamContinuitySnapshot   `json:"team_continuity,omitempty"`
}

func (s AggregateState) Clone() AggregateState {
	copy := s
	if s.Ownership.OwnerFQN != nil {
		owner := *s.Ownership.OwnerFQN
		copy.Ownership.OwnerFQN = &owner
	}
	if s.Ownership.AssignedEventID != nil {
		eventID := *s.Ownership.AssignedEventID
		copy.Ownership.AssignedEventID = &eventID
	}
	if s.OperatorRole != nil {
		value := s.OperatorRole.Clone()
		copy.OperatorRole = &value
	}
	if s.Participant != nil {
		value := s.Participant.Clone()
		copy.Participant = &value
	}
	if s.Interaction != nil {
		value := s.Interaction.Clone()
		copy.Interaction = &value
	}
	if s.Continuity != nil {
		value := s.Continuity.Clone()
		copy.Continuity = &value
	}
	return copy
}
