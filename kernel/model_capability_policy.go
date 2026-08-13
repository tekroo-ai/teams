package kernel

type ModelCapabilityPolicyAction string

const (
	CapabilityBindProfile         ModelCapabilityPolicyAction = "BIND_PROFILE"
	CapabilityAuthorizeAssignment ModelCapabilityPolicyAction = "AUTHORIZE_ASSIGNMENT"
	CapabilityFinalizeReview      ModelCapabilityPolicyAction = "FINALIZE_REVIEW"
	CapabilityEscalate            ModelCapabilityPolicyAction = "ESCALATE"
	CapabilitySelect              ModelCapabilityPolicyAction = "SELECT"
)

type ModelCapabilityPolicyInput struct {
	Action                ModelCapabilityPolicyAction
	ScopeRevision         uint64
	ProfileScopeRevision  uint64
	RequiredRoute         DecisionRoute
	SelectedRoute         DecisionRoute
	QualificationStatus   QualificationStatus
	QualificationRevoked  bool
	HardConstraintsPass   bool
	RequiredDimensions    []IndependenceDimension
	ProvenDimensions      []IndependenceDimension
	VariantClassification VariantClassification
	SubmittedCandidateIDs []UUIDv7
	SelectedCandidateID   UUIDv7
}

type ModelCapabilityPolicyResult struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

// EvaluateModelCapabilityPolicy is the provider-neutral reference model for
// the 0.6.0 capability-policy fixtures. It performs no registry or provider I/O.
func EvaluateModelCapabilityPolicy(input ModelCapabilityPolicyInput) ModelCapabilityPolicyResult {
	reject := func(reason string) ModelCapabilityPolicyResult {
		return ModelCapabilityPolicyResult{Accepted: false, Reason: reason}
	}
	switch input.Action {
	case CapabilityBindProfile:
		if input.ScopeRevision == 0 || input.ProfileScopeRevision != input.ScopeRevision {
			return reject("STALE_WORK_PROFILE")
		}
	case CapabilityAuthorizeAssignment:
		if input.QualificationRevoked {
			return reject("QUALIFICATION_REVOKED")
		}
		if input.QualificationStatus != QualificationPass {
			return reject("QUALIFICATION_NOT_PASSING")
		}
		if !input.HardConstraintsPass {
			return reject("HARD_CONSTRAINT_FAILED")
		}
		if !input.SelectedRoute.Satisfies(input.RequiredRoute) {
			return reject("CAPABILITY_MISMATCH")
		}
	case CapabilityFinalizeReview:
		for _, required := range input.RequiredDimensions {
			if !containsIndependenceDimension(input.ProvenDimensions, required) {
				return reject("INDEPENDENCE_NOT_PROVEN")
			}
		}
	case CapabilityEscalate:
		if input.VariantClassification != VariantMaterialDisagreement {
			return reject("ESCALATION_NOT_REQUIRED")
		}
		return ModelCapabilityPolicyResult{Accepted: true, Reason: "ESCALATION_REQUIRED"}
	case CapabilitySelect:
		if input.VariantClassification == VariantMaterialDisagreement {
			return reject("MATERIAL_DISAGREEMENT")
		}
		if !capabilityContainsUUID(input.SubmittedCandidateIDs, input.SelectedCandidateID) {
			return reject("CANDIDATE_NOT_SUBMITTED")
		}
	default:
		return reject("UNKNOWN_ACTION")
	}
	return ModelCapabilityPolicyResult{Accepted: true, Reason: "ACCEPTED"}
}

func containsIndependenceDimension(values []IndependenceDimension, target IndependenceDimension) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func capabilityContainsUUID(values []UUIDv7, target UUIDv7) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
