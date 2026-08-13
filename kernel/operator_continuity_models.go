package kernel

// HumanParticipantPolicyAction identifies one deterministic participant-policy decision.
type HumanParticipantPolicyAction string

const (
	HumanParticipantResolveRecipients HumanParticipantPolicyAction = "RESOLVE_RECIPIENTS"
	HumanParticipantAuthorize         HumanParticipantPolicyAction = "AUTHORIZE"
	HumanParticipantAuthenticate      HumanParticipantPolicyAction = "AUTHENTICATE"
	HumanParticipantValidateBindings  HumanParticipantPolicyAction = "VALIDATE_PROFILE_BINDINGS"
	HumanParticipantSelectRoute       HumanParticipantPolicyAction = "SELECT_DELIVERY_ROUTE"
)

type HumanParticipantPolicyInput struct {
	Action                           HumanParticipantPolicyAction
	ParticipantIDs                   []string
	Active                           bool
	RoleCurrent                      bool
	RoleCurrentSpecified             bool
	ScopeMatches                     bool
	CommandAuthorized                bool
	BindingActive                    bool
	SubjectMatches                   bool
	AssuranceSufficient              bool
	ActiveAuthenticationBindingIDs   []UUIDv7
	DeliveryAuthenticationBindingIDs []UUIDv7
	RequestedConfidentiality         Confidentiality
	RouteCeiling                     Confidentiality
}

type HumanParticipantPolicyResult struct {
	Accepted     bool     `json:"accepted"`
	Reason       string   `json:"reason"`
	RecipientIDs []string `json:"recipientIds,omitempty"`
}

func EvaluateHumanParticipantPolicy(input HumanParticipantPolicyInput) HumanParticipantPolicyResult {
	switch input.Action {
	case HumanParticipantResolveRecipients:
		seen := make(map[string]bool, len(input.ParticipantIDs))
		for _, id := range input.ParticipantIDs {
			if id == "" || seen[id] {
				return HumanParticipantPolicyResult{Reason: "DUPLICATE_HUMAN_IDENTITY"}
			}
			seen[id] = true
		}
		return HumanParticipantPolicyResult{Accepted: true, Reason: "ACCEPTED", RecipientIDs: append([]string(nil), input.ParticipantIDs...)}
	case HumanParticipantAuthorize:
		if !input.Active {
			return HumanParticipantPolicyResult{Reason: "PARTICIPANT_REVOKED"}
		}
		if input.RoleCurrentSpecified && !input.RoleCurrent {
			return HumanParticipantPolicyResult{Reason: "ROLE_BINDING_EXPIRED"}
		}
		if !input.ScopeMatches {
			return HumanParticipantPolicyResult{Reason: "ROLE_SCOPE_MISMATCH"}
		}
		if !input.CommandAuthorized {
			return HumanParticipantPolicyResult{Reason: "SCOPED_AUTHORITY_REQUIRED"}
		}
		return HumanParticipantPolicyResult{Accepted: true, Reason: "ACCEPTED"}
	case HumanParticipantAuthenticate:
		if !input.BindingActive {
			return HumanParticipantPolicyResult{Reason: "AUTHENTICATION_BINDING_INACTIVE"}
		}
		if !input.SubjectMatches {
			return HumanParticipantPolicyResult{Reason: "AUTHENTICATION_SUBJECT_MISMATCH"}
		}
		if !input.AssuranceSufficient {
			return HumanParticipantPolicyResult{Reason: "AUTHENTICATION_ASSURANCE_INSUFFICIENT"}
		}
		return HumanParticipantPolicyResult{Accepted: true, Reason: "ACCEPTED"}
	case HumanParticipantValidateBindings:
		active := make(map[UUIDv7]bool, len(input.ActiveAuthenticationBindingIDs))
		for _, id := range input.ActiveAuthenticationBindingIDs {
			active[id] = true
		}
		for _, id := range input.DeliveryAuthenticationBindingIDs {
			if !active[id] {
				return HumanParticipantPolicyResult{Reason: "AUTHENTICATION_BINDING_REFERENCE_INVALID"}
			}
		}
		return HumanParticipantPolicyResult{Accepted: true, Reason: "ACCEPTED"}
	case HumanParticipantSelectRoute:
		if !input.RequestedConfidentiality.Valid() || !input.RouteCeiling.Valid() || confidentialityRank(input.RequestedConfidentiality) > confidentialityRank(input.RouteCeiling) {
			return HumanParticipantPolicyResult{Reason: "CONFIDENTIALITY_CEILING_EXCEEDED"}
		}
		return HumanParticipantPolicyResult{Accepted: true, Reason: "ACCEPTED"}
	default:
		return HumanParticipantPolicyResult{Reason: "INVALID_ACTION"}
	}
}

type HumanInteractionPolicyAction string

const (
	HumanInteractionOpenPolicy       HumanInteractionPolicyAction = "OPEN"
	HumanInteractionRespondPolicy    HumanInteractionPolicyAction = "RESPOND"
	HumanInteractionDeliveryPolicy   HumanInteractionPolicyAction = "RECORD_DELIVERY"
	HumanInteractionCapabilityPolicy HumanInteractionPolicyAction = "ACCEPT_RESPONSE_CAPABILITY"
	HumanInteractionResponsePolicy   HumanInteractionPolicyAction = "EVALUATE_RESPONSE_POLICY"
	HumanInteractionEffectPolicy     HumanInteractionPolicyAction = "EVALUATE_EFFECT"
	HumanInteractionExpirePolicy     HumanInteractionPolicyAction = "EXPIRE"
)

type HumanInteractionPolicyInput struct {
	Action                     HumanInteractionPolicyAction
	OriginKind                 PrincipalKind
	OriginActorPresent         bool
	OriginExecutionPresent     bool
	SourceKind                 PrincipalKind
	ActorFQNPresent            bool
	SelectedHumanIDs           []string
	AcceptedHumanIDs           []string
	RespondentID               string
	QuestionRevision           uint64
	PresentedQuestionRevision  uint64
	ParticipantActive          bool
	AuthenticationValid        bool
	ResponseSpecificationMatch bool
	Duplicate                  bool
	RenderedDiffers            bool
	MaterialEquivalenceProven  bool
	CredentialVerified         bool
	CommitSucceeded            bool
	ResponsePolicy             HumanResponsePolicy
	DeclaredEffect             string
	ScopedCommandAuthority     bool
	DeadlineExpired            bool
	SilenceIsConsent           bool
}

type HumanInteractionPolicyResult struct {
	Accepted                bool   `json:"accepted"`
	Reason                  string `json:"reason"`
	ResponsePolicySatisfied *bool  `json:"responsePolicySatisfied,omitempty"`
	CapabilityConsumed      *bool  `json:"capabilityConsumed,omitempty"`
	ResolvedEffect          string `json:"resolvedEffect,omitempty"`
	State                   string `json:"state,omitempty"`
}

func EvaluateHumanInteractionPolicy(input HumanInteractionPolicyInput) HumanInteractionPolicyResult {
	switch input.Action {
	case HumanInteractionOpenPolicy:
		if input.OriginKind == PrincipalHuman && (input.OriginActorPresent || input.OriginExecutionPresent) {
			return HumanInteractionPolicyResult{Reason: "HUMAN_ACTOR_ATTRIBUTION"}
		}
		if input.OriginKind == PrincipalActor && (!input.OriginActorPresent || !input.OriginExecutionPresent) {
			return HumanInteractionPolicyResult{Reason: "ACTOR_EXECUTION_REQUIRED"}
		}
		return HumanInteractionPolicyResult{Accepted: true, Reason: "ACCEPTED"}
	case HumanInteractionRespondPolicy:
		if input.SourceKind != PrincipalHuman {
			return HumanInteractionPolicyResult{Reason: "HUMAN_AUTHORITY_REQUIRED"}
		}
		if input.ActorFQNPresent {
			return HumanInteractionPolicyResult{Reason: "HUMAN_ACTOR_ATTRIBUTION"}
		}
		if !containsString(input.SelectedHumanIDs, input.RespondentID) {
			return HumanInteractionPolicyResult{Reason: "RECIPIENT_MISMATCH"}
		}
		if input.PresentedQuestionRevision != input.QuestionRevision {
			return HumanInteractionPolicyResult{Reason: "QUESTION_REVISION_MISMATCH"}
		}
		if !input.ParticipantActive {
			return HumanInteractionPolicyResult{Reason: "PARTICIPANT_REVOKED"}
		}
		if !input.AuthenticationValid {
			return HumanInteractionPolicyResult{Reason: "AUTHENTICATION_REQUIRED"}
		}
		if !input.ResponseSpecificationMatch {
			return HumanInteractionPolicyResult{Reason: "RESPONSE_SPECIFICATION_MISMATCH"}
		}
		if input.Duplicate {
			return HumanInteractionPolicyResult{Reason: "DUPLICATE_RESPONSE"}
		}
		return HumanInteractionPolicyResult{Accepted: true, Reason: "ACCEPTED"}
	case HumanInteractionDeliveryPolicy:
		if input.RenderedDiffers && !input.MaterialEquivalenceProven {
			return HumanInteractionPolicyResult{Reason: "PRESENTATION_EQUIVALENCE_REQUIRED"}
		}
		value := false
		return HumanInteractionPolicyResult{Accepted: true, Reason: "DELIVERY_RECORDED", ResponsePolicySatisfied: &value}
	case HumanInteractionCapabilityPolicy:
		value := false
		if !input.CredentialVerified {
			return HumanInteractionPolicyResult{Reason: "AUTHENTICATION_REQUIRED", CapabilityConsumed: &value}
		}
		if !input.CommitSucceeded {
			return HumanInteractionPolicyResult{Reason: "COMMIT_FAILED", CapabilityConsumed: &value}
		}
		value = true
		return HumanInteractionPolicyResult{Accepted: true, Reason: "ACCEPTED", CapabilityConsumed: &value}
	case HumanInteractionResponsePolicy:
		result := EvaluateHumanResponsePolicy(input.ResponsePolicy, input.SelectedHumanIDs, input.AcceptedHumanIDs)
		return HumanInteractionPolicyResult{Accepted: result.Accepted, Reason: result.Reason}
	case HumanInteractionEffectPolicy:
		switch input.DeclaredEffect {
		case "ADVISORY_ONLY":
			return HumanInteractionPolicyResult{Accepted: true, Reason: "RECORDED_AS_ADVISORY", ResolvedEffect: "ADVISORY_ONLY"}
		case "EVIDENCE_ONLY":
			return HumanInteractionPolicyResult{Accepted: true, Reason: "RECORDED_AS_EVIDENCE", ResolvedEffect: "EVIDENCE_ONLY"}
		default:
			if !input.ScopedCommandAuthority {
				return HumanInteractionPolicyResult{Reason: "SCOPED_AUTHORITY_REQUIRED", ResolvedEffect: "EVIDENCE_ONLY"}
			}
			return HumanInteractionPolicyResult{Accepted: true, Reason: "AUTHORIZED_EFFECT", ResolvedEffect: "AUTHORIZED_EFFECT"}
		}
	case HumanInteractionExpirePolicy:
		if !input.DeadlineExpired {
			return HumanInteractionPolicyResult{Reason: "DEADLINE_NOT_EXPIRED"}
		}
		if input.SilenceIsConsent {
			return HumanInteractionPolicyResult{Reason: "SILENCE_CANNOT_AUTHORIZE"}
		}
		return HumanInteractionPolicyResult{Accepted: true, Reason: "EXPIRED_WITHOUT_CONSENT", State: "EXPIRED"}
	default:
		return HumanInteractionPolicyResult{Reason: "INVALID_ACTION"}
	}
}

type TeamContinuityPolicyAction string

const (
	TeamContinuityRequestQuiescence TeamContinuityPolicyAction = "REQUEST_QUIESCENCE"
	TeamContinuityDispatch          TeamContinuityPolicyAction = "DISPATCH"
	TeamContinuityAcceptResult      TeamContinuityPolicyAction = "ACCEPT_RESULT"
	TeamContinuityRecordSuspended   TeamContinuityPolicyAction = "RECORD_SUSPENDED"
	TeamContinuityBeginReconcile    TeamContinuityPolicyAction = "BEGIN_RECONCILIATION"
	TeamContinuityUnexpectedOutage  TeamContinuityPolicyAction = "RECORD_UNEXPECTED_OUTAGE"
	TeamContinuityResume            TeamContinuityPolicyAction = "RESUME"
	TeamContinuityObserveContinuous TeamContinuityPolicyAction = "OBSERVE_CONTINUOUS"
)

type TeamContinuityPolicyInput struct {
	Action                 TeamContinuityPolicyAction
	State                  ContinuityControlState
	PowerEpoch             uint64
	AdmissionOpen          bool
	OperatingPosture       string
	UnresolvedExecutionIDs []UUIDv7
	RecordedUnresolvedIDs  []UUIDv7
	PresentedPowerEpoch    uint64
	NextPowerEpoch         uint64
	AllExecutionsRecorded  bool
	ReconciledExecutionIDs []UUIDv7
	KnownInFlightIDs       []UUIDv7
	ServicesHealthy        bool
	OutboxReconciled       bool
	ChangeStreamReconciled bool
}

type TeamContinuityPolicyResult struct {
	Accepted               bool                    `json:"accepted"`
	Reason                 string                  `json:"reason"`
	State                  *ContinuityControlState `json:"state,omitempty"`
	PowerEpoch             *uint64                 `json:"powerEpoch,omitempty"`
	AdmissionOpen          *bool                   `json:"admissionOpen,omitempty"`
	UnresolvedExecutionIDs *[]UUIDv7               `json:"unresolvedExecutionIds,omitempty"`
}

func EvaluateTeamContinuityPolicy(input TeamContinuityPolicyInput) TeamContinuityPolicyResult {
	stateResult := func(accepted bool, reason string, state ContinuityControlState) TeamContinuityPolicyResult {
		return TeamContinuityPolicyResult{Accepted: accepted, Reason: reason, State: &state}
	}
	switch input.Action {
	case TeamContinuityRequestQuiescence:
		state, epoch, admission := input.State, input.PowerEpoch, input.AdmissionOpen
		if state != ContinuityActive {
			return TeamContinuityPolicyResult{Reason: "INVALID_CONTROL_STATE", State: &state, PowerEpoch: &epoch, AdmissionOpen: &admission}
		}
		if input.NextPowerEpoch != input.PowerEpoch+1 {
			return TeamContinuityPolicyResult{Reason: "POWER_EPOCH_MISMATCH", State: &state, PowerEpoch: &epoch, AdmissionOpen: &admission}
		}
		state, admission = ContinuityQuiescing, false
		return TeamContinuityPolicyResult{Accepted: true, Reason: "ACCEPTED", State: &state, PowerEpoch: &input.NextPowerEpoch, AdmissionOpen: &admission}
	case TeamContinuityDispatch, TeamContinuityAcceptResult:
		action := ContinuityDispatch
		if input.Action == TeamContinuityAcceptResult {
			action = ContinuityAcceptResult
		}
		return continuityAdmissionResult(EvaluateContinuityAdmission(TeamContinuitySnapshot{PowerEpoch: input.PowerEpoch, AdmissionOpen: input.AdmissionOpen}, action, input.PresentedPowerEpoch))
	case TeamContinuityRecordSuspended:
		if input.State != ContinuityQuiescing {
			return stateResult(false, "INVALID_CONTROL_STATE", input.State)
		}
		if !input.AllExecutionsRecorded {
			return stateResult(false, "EXECUTION_INVENTORY_INCOMPLETE", input.State)
		}
		state, admission := ContinuitySuspended, false
		unresolved := append([]UUIDv7(nil), input.RecordedUnresolvedIDs...)
		return TeamContinuityPolicyResult{Accepted: true, Reason: "ACCEPTED", State: &state, AdmissionOpen: &admission, UnresolvedExecutionIDs: &unresolved}
	case TeamContinuityBeginReconcile:
		if input.State != ContinuitySuspended || input.PresentedPowerEpoch != input.PowerEpoch {
			return stateResult(false, "RECONCILIATION_PRECONDITION_FAILED", input.State)
		}
		state, admission := ContinuityReconciling, false
		unresolved := append([]UUIDv7(nil), input.UnresolvedExecutionIDs...)
		return TeamContinuityPolicyResult{Accepted: true, Reason: "ACCEPTED", State: &state, AdmissionOpen: &admission, UnresolvedExecutionIDs: &unresolved}
	case TeamContinuityUnexpectedOutage:
		if input.NextPowerEpoch != input.PowerEpoch+1 {
			return stateResult(false, "POWER_EPOCH_MISMATCH", input.State)
		}
		state, admission := ContinuityReconciling, false
		unresolved := append([]UUIDv7(nil), input.KnownInFlightIDs...)
		return TeamContinuityPolicyResult{Accepted: true, Reason: "ACCEPTED", State: &state, PowerEpoch: &input.NextPowerEpoch, AdmissionOpen: &admission, UnresolvedExecutionIDs: &unresolved}
	case TeamContinuityResume:
		if input.State != ContinuityReconciling {
			return stateResult(false, "RECONCILIATION_REQUIRED", input.State)
		}
		if input.PresentedPowerEpoch != input.PowerEpoch {
			return stateResult(false, "POWER_EPOCH_MISMATCH", input.State)
		}
		if !sameUUIDSet(input.UnresolvedExecutionIDs, input.ReconciledExecutionIDs) {
			return stateResult(false, "UNRESOLVED_EXECUTIONS", input.State)
		}
		if !input.ServicesHealthy || !input.OutboxReconciled || !input.ChangeStreamReconciled {
			return stateResult(false, "RESUME_PRECONDITION_FAILED", input.State)
		}
		state, admission, epoch := ContinuityActive, true, input.PowerEpoch
		unresolved := []UUIDv7{}
		return TeamContinuityPolicyResult{Accepted: true, Reason: "ACCEPTED", State: &state, PowerEpoch: &epoch, AdmissionOpen: &admission, UnresolvedExecutionIDs: &unresolved}
	case TeamContinuityObserveContinuous:
		if !input.ServicesHealthy {
			return TeamContinuityPolicyResult{Reason: "HOST_CONTINUITY_LOST"}
		}
		return TeamContinuityPolicyResult{Accepted: true, Reason: "ACCEPTED"}
	default:
		return TeamContinuityPolicyResult{Reason: "INVALID_ACTION"}
	}
}

func continuityAdmissionResult(result PolicyResult) TeamContinuityPolicyResult {
	return TeamContinuityPolicyResult{Accepted: result.Accepted, Reason: result.Reason}
}
