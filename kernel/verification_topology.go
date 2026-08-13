package kernel

type WorkExecutionIdentity struct {
	Principal          PrincipalRef `json:"principal"`
	ActorFQN           ActorFQN     `json:"actor_fqn"`
	ExecutionID        UUIDv7       `json:"execution_id"`
	FencingEpoch       uint64       `json:"fencing_epoch"`
	ModelProfileDigest Digest       `json:"model_profile_digest"`
	WorkspaceDigest    Digest       `json:"workspace_digest"`
	ContextDigest      Digest       `json:"context_digest"`
}

func (identity WorkExecutionIdentity) Valid() bool {
	return identity.Principal.Valid() && identity.ActorFQN.Valid() && identity.ExecutionID.Valid() && identity.FencingEpoch > 0 && identity.ModelProfileDigest.Valid() && identity.WorkspaceDigest.Valid() && identity.ContextDigest.Valid()
}

func (identity WorkExecutionIdentity) Execution() ExecutionTuple {
	return ExecutionTuple{ExecutionID: identity.ExecutionID, FencingEpoch: identity.FencingEpoch}
}

type IndependenceReceipt struct {
	ProvenDimensions         []IndependenceDimension `json:"proven_dimensions"`
	IdentityComparisonDigest Digest                  `json:"identity_comparison_digest"`
	MethodIDs                []string                `json:"method_ids"`
	EvidenceIDs              []UUIDv7                `json:"evidence_ids"`
}

func (receipt IndependenceReceipt) Valid() bool {
	return validUniqueDimensions(receipt.ProvenDimensions) && receipt.IdentityComparisonDigest.Valid() && validUniqueStrings(receipt.MethodIDs, 1, 64) && validUniqueUUIDs(receipt.EvidenceIDs, 1, 64)
}

func (receipt IndependenceReceipt) Proves(required []IndependenceDimension, methods []string) bool {
	if !receipt.Valid() {
		return false
	}
	dimensions := make(map[IndependenceDimension]struct{}, len(receipt.ProvenDimensions))
	for _, value := range receipt.ProvenDimensions {
		dimensions[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := dimensions[value]; !ok {
			return false
		}
	}
	observedMethods := make(map[string]struct{}, len(receipt.MethodIDs))
	for _, value := range receipt.MethodIDs {
		observedMethods[value] = struct{}{}
	}
	for _, value := range methods {
		if _, ok := observedMethods[value]; !ok {
			return false
		}
	}
	return true
}

func RequiredSeparationProven(implementer WorkExecutionIdentity, validatorPrincipal PrincipalRef, validatorActor *ActorFQN, validatorExecution *ExecutionTuple, required []IndependenceDimension, receipt IndependenceReceipt) bool {
	if !implementer.Valid() || !validatorPrincipal.Valid() || !receipt.Proves(required, nil) {
		return false
	}
	for _, dimension := range required {
		switch dimension {
		case IndependencePrincipal:
			if validatorPrincipal == implementer.Principal {
				return false
			}
		case IndependenceActor:
			if validatorActor == nil || !validatorActor.Valid() || *validatorActor == implementer.ActorFQN {
				return false
			}
		case IndependenceExecution:
			if validatorExecution == nil || !validatorExecution.Valid() || *validatorExecution == implementer.Execution() {
				return false
			}
		}
	}
	return true
}
