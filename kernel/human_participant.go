package kernel

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var humanAuthorizedCommandPattern = regexp.MustCompile(`^tekroo\.command\.[a-z0-9]+(?:[.-][a-z0-9]+)*$`)

type Confidentiality string

const (
	ConfidentialityPublic       Confidentiality = "PUBLIC"
	ConfidentialityInternal     Confidentiality = "INTERNAL"
	ConfidentialityConfidential Confidentiality = "CONFIDENTIAL"
	ConfidentialityRestricted   Confidentiality = "RESTRICTED"
)

func (value Confidentiality) Valid() bool {
	switch value {
	case ConfidentialityPublic, ConfidentialityInternal, ConfidentialityConfidential, ConfidentialityRestricted:
		return true
	default:
		return false
	}
}

func confidentialityRank(value Confidentiality) int {
	switch value {
	case ConfidentialityPublic:
		return 0
	case ConfidentialityInternal:
		return 1
	case ConfidentialityConfidential:
		return 2
	case ConfidentialityRestricted:
		return 3
	default:
		return -1
	}
}

type HumanRoleBinding struct {
	RoleBindingID           UUIDv7     `json:"role_binding_id" bson:"role_binding_id"`
	RoleClass               string     `json:"role_class" bson:"role_class"`
	RoleID                  string     `json:"role_id" bson:"role_id"`
	ScopeKind               string     `json:"scope_kind" bson:"scope_kind"`
	ScopeID                 string     `json:"scope_id" bson:"scope_id"`
	AdvisoryTopicIDs        []string   `json:"advisory_topic_ids" bson:"advisory_topic_ids"`
	AuthorizedCommandTypes  []string   `json:"authorized_command_types" bson:"authorized_command_types"`
	AuthorityPolicyRevision uint64     `json:"authority_policy_revision" bson:"authority_policy_revision"`
	AuthorityPolicyDigest   Digest     `json:"authority_policy_digest" bson:"authority_policy_digest"`
	ValidFrom               time.Time  `json:"valid_from" bson:"valid_from"`
	ValidUntil              *time.Time `json:"valid_until" bson:"valid_until,omitempty"`
	Active                  bool       `json:"active" bson:"active"`
	EvidenceIDs             []UUIDv7   `json:"evidence_ids" bson:"evidence_ids"`
}

func (binding HumanRoleBinding) Current(at time.Time) bool {
	return binding.Active && !at.Before(binding.ValidFrom) && (binding.ValidUntil == nil || at.Before(*binding.ValidUntil))
}

type HumanAuthenticationBinding struct {
	AuthenticationBindingID UUIDv7   `json:"authentication_binding_id" bson:"authentication_binding_id"`
	Method                  string   `json:"method" bson:"method"`
	IssuerDigest            Digest   `json:"issuer_digest" bson:"issuer_digest"`
	SubjectDigest           Digest   `json:"subject_digest" bson:"subject_digest"`
	Assurance               string   `json:"assurance" bson:"assurance"`
	BindingRevision         uint64   `json:"binding_revision" bson:"binding_revision"`
	Active                  bool     `json:"active" bson:"active"`
	EvidenceIDs             []UUIDv7 `json:"evidence_ids" bson:"evidence_ids"`
}

type HumanDeliveryBinding struct {
	DeliveryBindingID        UUIDv7          `json:"delivery_binding_id" bson:"delivery_binding_id"`
	Channel                  string          `json:"channel" bson:"channel"`
	EndpointDigest           Digest          `json:"endpoint_digest" bson:"endpoint_digest"`
	AdapterProfileDigest     Digest          `json:"adapter_profile_digest" bson:"adapter_profile_digest"`
	ConfidentialityCeiling   Confidentiality `json:"confidentiality_ceiling" bson:"confidentiality_ceiling"`
	AuthenticationBindingIDs []UUIDv7        `json:"authentication_binding_ids" bson:"authentication_binding_ids"`
	BindingRevision          uint64          `json:"binding_revision" bson:"binding_revision"`
	Active                   bool            `json:"active" bson:"active"`
	EvidenceIDs              []UUIDv7        `json:"evidence_ids" bson:"evidence_ids"`
}

type HumanParticipantSnapshot struct {
	Participant               PrincipalRef                 `json:"participant" bson:"participant"`
	Revision                  uint64                       `json:"revision" bson:"revision"`
	ProfileID                 UUIDv7                       `json:"profile_id" bson:"profile_id"`
	ProfileRevision           uint64                       `json:"profile_revision" bson:"profile_revision"`
	ProfileDigest             Digest                       `json:"profile_digest" bson:"profile_digest"`
	DisplayLabel              string                       `json:"display_label" bson:"display_label"`
	PrivacyClassification     Confidentiality              `json:"privacy_classification" bson:"privacy_classification"`
	RoleBindings              []HumanRoleBinding           `json:"role_bindings" bson:"role_bindings"`
	AuthenticationBindings    []HumanAuthenticationBinding `json:"authentication_bindings" bson:"authentication_bindings"`
	DeliveryBindings          []HumanDeliveryBinding       `json:"delivery_bindings" bson:"delivery_bindings"`
	ParticipantPolicyRevision uint64                       `json:"participant_policy_revision" bson:"participant_policy_revision"`
	ParticipantPolicyDigest   Digest                       `json:"participant_policy_digest" bson:"participant_policy_digest"`
	SupersedesProfileID       *UUIDv7                      `json:"supersedes_profile_id,omitempty" bson:"supersedes_profile_id,omitempty"`
	Active                    bool                         `json:"active" bson:"active"`
}

func (snapshot HumanParticipantSnapshot) Clone() HumanParticipantSnapshot {
	copy := snapshot
	copy.RoleBindings = append([]HumanRoleBinding(nil), snapshot.RoleBindings...)
	for index := range copy.RoleBindings {
		copy.RoleBindings[index].AdvisoryTopicIDs = append([]string(nil), snapshot.RoleBindings[index].AdvisoryTopicIDs...)
		copy.RoleBindings[index].AuthorizedCommandTypes = append([]string(nil), snapshot.RoleBindings[index].AuthorizedCommandTypes...)
		copy.RoleBindings[index].EvidenceIDs = append([]UUIDv7(nil), snapshot.RoleBindings[index].EvidenceIDs...)
		if snapshot.RoleBindings[index].ValidUntil != nil {
			value := *snapshot.RoleBindings[index].ValidUntil
			copy.RoleBindings[index].ValidUntil = &value
		}
	}
	copy.AuthenticationBindings = append([]HumanAuthenticationBinding(nil), snapshot.AuthenticationBindings...)
	for index := range copy.AuthenticationBindings {
		copy.AuthenticationBindings[index].EvidenceIDs = append([]UUIDv7(nil), snapshot.AuthenticationBindings[index].EvidenceIDs...)
	}
	copy.DeliveryBindings = append([]HumanDeliveryBinding(nil), snapshot.DeliveryBindings...)
	for index := range copy.DeliveryBindings {
		copy.DeliveryBindings[index].AuthenticationBindingIDs = append([]UUIDv7(nil), snapshot.DeliveryBindings[index].AuthenticationBindingIDs...)
		copy.DeliveryBindings[index].EvidenceIDs = append([]UUIDv7(nil), snapshot.DeliveryBindings[index].EvidenceIDs...)
	}
	if snapshot.SupersedesProfileID != nil {
		value := *snapshot.SupersedesProfileID
		copy.SupersedesProfileID = &value
	}
	return copy
}

func HumanParticipantFromPayload(payload json.RawMessage, revision uint64) (HumanParticipantSnapshot, error) {
	var value struct {
		Participant                 PrincipalRef                 `json:"participant"`
		ExpectedParticipantRevision uint64                       `json:"expected_participant_revision"`
		ProfileID                   UUIDv7                       `json:"profile_id"`
		ProfileRevision             uint64                       `json:"profile_revision"`
		ProfileDigest               Digest                       `json:"profile_digest"`
		DisplayLabel                string                       `json:"display_label"`
		PrivacyClassification       Confidentiality              `json:"privacy_classification"`
		RoleBindings                []HumanRoleBinding           `json:"role_bindings"`
		AuthenticationBindings      []HumanAuthenticationBinding `json:"authentication_bindings"`
		DeliveryBindings            []HumanDeliveryBinding       `json:"delivery_bindings"`
		ParticipantPolicyRevision   uint64                       `json:"participant_policy_revision"`
		ParticipantPolicyDigest     Digest                       `json:"participant_policy_digest"`
		SupersedesProfileID         *UUIDv7                      `json:"supersedes_profile_id"`
	}
	if json.Unmarshal(payload, &value) != nil || value.Participant.Kind != PrincipalHuman || value.ExpectedParticipantRevision+1 != revision {
		return HumanParticipantSnapshot{}, errors.New("invalid human participant profile")
	}
	snapshot := HumanParticipantSnapshot{Participant: value.Participant, Revision: revision, ProfileID: value.ProfileID, ProfileRevision: value.ProfileRevision, ProfileDigest: value.ProfileDigest, DisplayLabel: value.DisplayLabel, PrivacyClassification: value.PrivacyClassification, RoleBindings: value.RoleBindings, AuthenticationBindings: value.AuthenticationBindings, DeliveryBindings: value.DeliveryBindings, ParticipantPolicyRevision: value.ParticipantPolicyRevision, ParticipantPolicyDigest: value.ParticipantPolicyDigest, SupersedesProfileID: value.SupersedesProfileID, Active: true}
	if !snapshot.Valid() {
		return HumanParticipantSnapshot{}, errors.New("invalid human participant profile")
	}
	return snapshot, nil
}

func (snapshot HumanParticipantSnapshot) Valid() bool {
	if snapshot.Participant.Kind != PrincipalHuman || !snapshot.Participant.Valid() || snapshot.Revision == 0 || !snapshot.ProfileID.Valid() || snapshot.ProfileRevision == 0 || !snapshot.ProfileDigest.Valid() || snapshot.DisplayLabel == "" || len(snapshot.DisplayLabel) > 256 || !snapshot.PrivacyClassification.Valid() || snapshot.ParticipantPolicyRevision == 0 || !snapshot.ParticipantPolicyDigest.Valid() || snapshot.SupersedesProfileID != nil && (!snapshot.SupersedesProfileID.Valid() || *snapshot.SupersedesProfileID == snapshot.ProfileID) || len(snapshot.RoleBindings) == 0 || len(snapshot.RoleBindings) > 64 || len(snapshot.AuthenticationBindings) == 0 || len(snapshot.AuthenticationBindings) > 32 || len(snapshot.DeliveryBindings) == 0 || len(snapshot.DeliveryBindings) > 32 {
		return false
	}
	auth := make(map[UUIDv7]bool, len(snapshot.AuthenticationBindings))
	for _, binding := range snapshot.AuthenticationBindings {
		if !binding.AuthenticationBindingID.Valid() || !validAuthenticationMethod(binding.Method) || !binding.IssuerDigest.Valid() || !binding.SubjectDigest.Valid() || binding.Assurance != "HIGH" && binding.Assurance != "STANDARD" || binding.BindingRevision == 0 || !binding.Active || !validUniqueUUIDs(binding.EvidenceIDs, 1, 64) || auth[binding.AuthenticationBindingID] {
			return false
		}
		auth[binding.AuthenticationBindingID] = true
	}
	roles := make(map[UUIDv7]bool, len(snapshot.RoleBindings))
	for _, binding := range snapshot.RoleBindings {
		if !binding.RoleBindingID.Valid() || !validHumanRoleClass(binding.RoleClass) || binding.RoleID == "" || len(binding.RoleID) > 128 || !validHumanScopeKind(binding.ScopeKind) || binding.ScopeID == "" || len(binding.ScopeID) > 256 || !validUniqueStrings(binding.AdvisoryTopicIDs, 0, 64) || !validAuthorizedCommandTypes(binding.AuthorizedCommandTypes) || binding.AuthorityPolicyRevision == 0 || !binding.AuthorityPolicyDigest.Valid() || binding.ValidFrom.IsZero() || binding.ValidUntil != nil && !binding.ValidUntil.After(binding.ValidFrom) || !binding.Active || !validUniqueUUIDs(binding.EvidenceIDs, 1, 64) || roles[binding.RoleBindingID] {
			return false
		}
		roles[binding.RoleBindingID] = true
	}
	deliveries := make(map[UUIDv7]bool, len(snapshot.DeliveryBindings))
	for _, binding := range snapshot.DeliveryBindings {
		if !binding.DeliveryBindingID.Valid() || !validDeliveryChannel(binding.Channel) || !binding.EndpointDigest.Valid() || !binding.AdapterProfileDigest.Valid() || !binding.ConfidentialityCeiling.Valid() || binding.BindingRevision == 0 || !binding.Active || !validUniqueUUIDs(binding.AuthenticationBindingIDs, 1, 16) || !validUniqueUUIDs(binding.EvidenceIDs, 1, 64) || deliveries[binding.DeliveryBindingID] {
			return false
		}
		for _, id := range binding.AuthenticationBindingIDs {
			if !auth[id] {
				return false
			}
		}
		deliveries[binding.DeliveryBindingID] = true
	}
	return true
}

func validHumanRoleClass(value string) bool {
	return value == "APPROVER" || value == "CLIENT" || value == "END_USER" || value == "PRINCIPAL" || value == "PROJECT_DEFINED" || value == "SME"
}

func validHumanScopeKind(value string) bool {
	return value == "DOMAIN" || value == "PROJECT" || value == "SYSTEM" || value == "WORK"
}

func validAuthenticationMethod(value string) bool {
	return value == "API_CREDENTIAL" || value == "CHANNEL_IDENTITY" || value == "IN_PERSON_ATTESTATION" || value == "MAGIC_LINK" || value == "OIDC" || value == "WEBAUTHN"
}

func validDeliveryChannel(value string) bool {
	return value == "EMAIL" || value == "OTHER" || value == "SLACK" || value == "SMS" || value == "TEAMS" || value == "WEB_PORTAL"
}

func validAuthorizedCommandTypes(values []string) bool {
	if !validUniqueStrings(values, 0, 64) {
		return false
	}
	for _, value := range values {
		if !humanAuthorizedCommandPattern.MatchString(value) {
			return false
		}
	}
	return true
}

func (snapshot HumanParticipantSnapshot) Authorizes(bindingID UUIDv7, commandType, scopeKind, scopeID string, at time.Time) bool {
	if !snapshot.Active {
		return false
	}
	for _, binding := range snapshot.RoleBindings {
		if binding.RoleBindingID != bindingID || !binding.Current(at) || binding.ScopeKind != scopeKind || binding.ScopeID != scopeID {
			continue
		}
		for _, admitted := range binding.AuthorizedCommandTypes {
			if admitted == commandType {
				return true
			}
		}
	}
	return false
}

func (snapshot HumanParticipantSnapshot) Authentication(id UUIDv7) (HumanAuthenticationBinding, bool) {
	if !snapshot.Active {
		return HumanAuthenticationBinding{}, false
	}
	for _, binding := range snapshot.AuthenticationBindings {
		if binding.AuthenticationBindingID == id && binding.Active {
			return binding, true
		}
	}
	return HumanAuthenticationBinding{}, false
}

func (snapshot HumanParticipantSnapshot) Delivery(id UUIDv7, requested Confidentiality) (HumanDeliveryBinding, bool) {
	if !snapshot.Active {
		return HumanDeliveryBinding{}, false
	}
	for _, binding := range snapshot.DeliveryBindings {
		if binding.DeliveryBindingID == id && binding.Active && confidentialityRank(requested) <= confidentialityRank(binding.ConfidentialityCeiling) {
			return binding, true
		}
	}
	return HumanDeliveryBinding{}, false
}
