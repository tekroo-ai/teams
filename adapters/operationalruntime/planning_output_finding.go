package operationalruntime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

type planningOutputFinding struct {
	SchemaVersion  string               `json:"schema_version"`
	FeatureID      kernel.UUIDv7        `json:"feature_id"`
	TaskID         kernel.UUIDv7        `json:"task_id"`
	InvocationID   kernel.UUIDv7        `json:"invocation_id"`
	Stage          featurePlanningStage `json:"stage"`
	OutputDigest   kernel.Digest        `json:"output_sha256"`
	DesignDigest   *kernel.Digest       `json:"accepted_design_sha256,omitempty"`
	Classification string               `json:"classification"`
	Message        string               `json:"validator_message"`
}

func (service *ProductionService) registerPlanningOutputFinding(ctx context.Context, feature organization.FeatureRequest, stage featurePlanningStage, invocation kernel.WorkInvocation, validationErr error) (kernel.EvidenceRef, error) {
	if validationErr == nil || invocation.OutputDigest == nil || !invocation.OutputDigest.Valid() {
		return kernel.EvidenceRef{}, organization.ErrInvalidFeature
	}
	finding := planningOutputFinding{SchemaVersion: "tekroo.teams.planning-output-finding/1.0.0", FeatureID: feature.ID, TaskID: invocation.TaskID, InvocationID: invocation.ID, Stage: stage, OutputDigest: *invocation.OutputDigest, Classification: invalidPlanningOutputReason, Message: boundedWorkBlockReason(validationErr.Error(), invocation.ID)}
	if feature.Design != nil {
		finding.DesignDigest = &feature.Design.OutputDigest
	}
	raw, err := json.Marshal(finding)
	if err != nil {
		return kernel.EvidenceRef{}, err
	}
	digest := digestBytes(raw)
	id := deterministicOperationalUUID("planning-output-finding", string(feature.ID), string(invocation.ID), string(digest))
	if err := service.retainPlanningEvidenceBlob(ctx, id, raw, digest); err != nil {
		return kernel.EvidenceRef{}, err
	}
	// Keep the stable block classification separate from the exact bounded
	// validator diagnostic. No signed command/schema needs a new field.
	payload := mustJSON(map[string]any{"access_partition": feature.Input.WorkspaceID, "availability": "AVAILABLE", "byte_length": len(raw), "canonical_digest": digest, "computation": nil, "deletion_tombstone": nil, "evidence_kind": "DECISION_RECORD", "integrity_state": "DIGEST_VERIFIED", "locator": "teams://feature/" + string(feature.ID) + "/planning/finding/" + string(digest), "locator_immutable": true, "media_type": "application/json", "producing_component": "tekrood-planning-validator", "producing_version": finding.SchemaVersion, "redacts": nil, "retention_policy": "feature-lifecycle", "sensitivity": "INTERNAL", "sha256": digest, "source_evidence_ids": invocation.TerminalEvidenceIDs, "source_timestamp": invocation.FinishedAt, "transport_provenance": "teams-planning-validation"})
	_, err = service.submitDeterministicCommand(ctx, feature, "tekroo.command.evidence.register", kernel.SchemaVersion, kernel.AggregateEvidence, id, service.serviceAuthority, 0, payload, nil, nil, "planning-output-finding-"+string(digest))
	if err != nil {
		return kernel.EvidenceRef{}, errors.Join(organization.ErrInvalidFeature, err)
	}
	return kernel.EvidenceRef{EvidenceID: id, SHA256: digest}, nil
}
