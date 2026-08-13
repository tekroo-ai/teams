package kernel

import (
	"encoding/json"
	"errors"
	"strings"
)

type OperatorRoleProfile struct {
	BindingID                      UUIDv7   `json:"binding_id" bson:"binding_id"`
	RoleID                         string   `json:"role_id" bson:"role_id"`
	OperatorActorFQN               ActorFQN `json:"operator_actor_fqn" bson:"operator_actor_fqn"`
	RoleBundleVersion              string   `json:"role_bundle_version" bson:"role_bundle_version"`
	RoleBundleDigest               Digest   `json:"role_bundle_digest" bson:"role_bundle_digest"`
	RoleBundleSignatureEvidenceIDs []UUIDv7 `json:"role_bundle_signature_evidence_ids" bson:"role_bundle_signature_evidence_ids"`
	RoleDefinitionDigest           Digest   `json:"role_definition_digest" bson:"role_definition_digest"`
	CapabilityIDs                  []string `json:"capability_ids" bson:"capability_ids"`
	HumanSelectionPolicyRevision   uint64   `json:"human_selection_policy_revision" bson:"human_selection_policy_revision"`
	HumanSelectionPolicyDigest     Digest   `json:"human_selection_policy_digest" bson:"human_selection_policy_digest"`
	AuthorityPolicyRevision        uint64   `json:"authority_policy_revision" bson:"authority_policy_revision"`
	AuthorityPolicyDigest          Digest   `json:"authority_policy_digest" bson:"authority_policy_digest"`
	ReplacesBindingID              *UUIDv7  `json:"replaces_binding_id,omitempty" bson:"replaces_binding_id,omitempty"`
}

func (profile OperatorRoleProfile) Valid() bool {
	return profile.BindingID.Valid() && profile.RoleID == "operator" &&
		profile.OperatorActorFQN.Valid() && strings.Contains(string(profile.OperatorActorFQN), "::operator-") && profile.RoleBundleVersion != "" && len(profile.RoleBundleVersion) <= 128 &&
		profile.RoleBundleDigest.Valid() && profile.RoleDefinitionDigest.Valid() &&
		validUniqueUUIDs(profile.RoleBundleSignatureEvidenceIDs, 1, 16) && validOperatorCapabilities(profile.CapabilityIDs) &&
		profile.HumanSelectionPolicyRevision > 0 && profile.HumanSelectionPolicyDigest.Valid() &&
		profile.AuthorityPolicyRevision > 0 && profile.AuthorityPolicyDigest.Valid() &&
		(profile.ReplacesBindingID == nil || profile.ReplacesBindingID.Valid())
}

func (profile OperatorRoleProfile) Clone() OperatorRoleProfile {
	copy := profile
	copy.CapabilityIDs = append([]string(nil), profile.CapabilityIDs...)
	copy.RoleBundleSignatureEvidenceIDs = append([]UUIDv7(nil), profile.RoleBundleSignatureEvidenceIDs...)
	if profile.ReplacesBindingID != nil {
		value := *profile.ReplacesBindingID
		copy.ReplacesBindingID = &value
	}
	return copy
}

func validOperatorCapabilities(values []string) bool {
	if !validUniqueStrings(values, 1, 10) {
		return false
	}
	for _, value := range values {
		switch value {
		case "COORDINATE_DAG", "INSPECT_ORGANIZATION", "INSPECT_EXECUTION", "INVOKE_AUTHORIZED_COMMAND", "PREPARE_DECISION", "PROPOSE_ASSIGNMENT", "PROPOSE_DECOMPOSITION", "PROPOSE_ESCALATION", "ROUTE_HUMAN_REQUIRED", "SURFACE_OPERATIONAL_CONDITION":
		default:
			return false
		}
	}
	return true
}

func OperatorRoleFromPayload(payload json.RawMessage) (OperatorRoleProfile, error) {
	var value OperatorRoleProfile
	if json.Unmarshal(payload, &value) != nil || !value.Valid() {
		return OperatorRoleProfile{}, errors.New("invalid operator role profile")
	}
	return value, nil
}

type AuthorityPresentationInput struct {
	SourceKind           string
	ClaimedKind          string
	CurrentExecution     bool
	ActorCommandAdmitted bool
	ExplicitlyAuthorized bool
}

type PolicyResult struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

func EvaluateAuthorityPresentation(input AuthorityPresentationInput) PolicyResult {
	if input.SourceKind == "CONTROL_SURFACE" {
		return PolicyResult{Reason: "ADAPTER_NOT_PRINCIPAL"}
	}
	if input.SourceKind == "ACTOR" && input.ClaimedKind == "HUMAN" {
		return PolicyResult{Reason: "HUMAN_IMPERSONATION"}
	}
	if input.SourceKind != input.ClaimedKind {
		return PolicyResult{Reason: "PRINCIPAL_KIND_MISMATCH"}
	}
	if input.SourceKind == "ACTOR" {
		if !input.CurrentExecution {
			return PolicyResult{Reason: "STALE_EXECUTION"}
		}
		if !input.ActorCommandAdmitted || !input.ExplicitlyAuthorized {
			return PolicyResult{Reason: "AUTHORITY_REQUIRED"}
		}
	}
	if !input.ExplicitlyAuthorized {
		return PolicyResult{Reason: "AUTHORITY_REQUIRED"}
	}
	return PolicyResult{Accepted: true, Reason: "ACCEPTED"}
}

func EvaluateHumanRequiredTarget(target PrincipalRef) PolicyResult {
	if target.Kind != PrincipalHuman {
		return PolicyResult{Reason: "HUMAN_PRINCIPAL_REQUIRED"}
	}
	return PolicyResult{Accepted: true, Reason: "ACCEPTED"}
}
