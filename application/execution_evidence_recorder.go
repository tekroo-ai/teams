package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

type ExecutionEvidenceBlobReceipt struct {
	Locator    string
	SHA256     kernel.Digest
	ByteLength uint64
}

type ExecutionEvidenceBlobStore interface {
	Put(context.Context, kernel.UUIDv7, []byte) (ExecutionEvidenceBlobReceipt, error)
}

type CommandEvidenceRecorderPolicy struct {
	PolicyRevision   uint64
	Authority        kernel.PrincipalRef
	Provenance       kernel.ProvenanceBasis
	ProducingVersion string
	RetentionPolicy  string
}

type CommandEvidenceRecorder struct {
	commands ExecutionCommandService
	blobs    ExecutionEvidenceBlobStore
	policy   CommandEvidenceRecorderPolicy
}

func NewCommandEvidenceRecorder(commands ExecutionCommandService, blobs ExecutionEvidenceBlobStore, policy CommandEvidenceRecorderPolicy) (*CommandEvidenceRecorder, error) {
	if commands == nil || blobs == nil || policy.PolicyRevision == 0 || policy.Authority.Kind != kernel.PrincipalService || !policy.Authority.Valid() || !policy.Provenance.Valid() || policy.ProducingVersion == "" || policy.RetentionPolicy == "" {
		return nil, ErrInvalidConfiguration
	}
	return &CommandEvidenceRecorder{commands: commands, blobs: blobs, policy: policy}, nil
}

func (recorder *CommandEvidenceRecorder) RecordExecutionEvidence(ctx context.Context, invocation kernel.WorkInvocation, items []ExecutionEvidence) ([]kernel.EvidenceRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !invocation.Valid() || len(items) == 0 || len(items) > 64 {
		return nil, ErrInvalidOperationalExecution
	}
	ordered := append([]ExecutionEvidence(nil), items...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].EvidenceID < ordered[right].EvidenceID })
	result := make([]kernel.EvidenceRef, 0, len(ordered))
	for index, item := range ordered {
		if !item.EvidenceID.Valid() || !validExecutionEvidenceKind(item.Kind) || item.MediaType == "" || len(item.MediaType) > 255 || item.SourceTimestamp.IsZero() || len(item.Content) > 16<<20 || index > 0 && item.EvidenceID == ordered[index-1].EvidenceID {
			return nil, ErrInvalidOperationalExecution
		}
		blob, err := recorder.blobs.Put(ctx, item.EvidenceID, item.Content)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(item.Content)
		expectedDigest := kernel.Digest(hex.EncodeToString(digest[:]))
		if blob.Locator == "" || blob.SHA256 != expectedDigest || blob.ByteLength != uint64(len(item.Content)) {
			return nil, ErrInvalidOperationalExecution
		}
		payload, err := json.Marshal(map[string]any{
			"access_partition":     invocation.WorkspaceID,
			"availability":         "AVAILABLE",
			"byte_length":          len(item.Content),
			"canonical_digest":     expectedDigest,
			"computation":          nil,
			"deletion_tombstone":   nil,
			"evidence_kind":        item.Kind,
			"integrity_state":      "DIGEST_VERIFIED",
			"locator":              blob.Locator,
			"locator_immutable":    true,
			"media_type":           item.MediaType,
			"producing_component":  "teams-openhands-operational-coordinator",
			"producing_version":    recorder.policy.ProducingVersion,
			"redacts":              nil,
			"retention_policy":     recorder.policy.RetentionPolicy,
			"sensitivity":          "INTERNAL",
			"sha256":               expectedDigest,
			"source_evidence_ids":  []kernel.UUIDv7{},
			"source_timestamp":     item.SourceTimestamp,
			"transport_provenance": "qualified-openhands-execution-boundary",
		})
		if err != nil {
			return nil, err
		}
		commandID := deterministicUUID("execution-evidence-command", string(invocation.ID), string(item.EvidenceID), string(expectedDigest))
		command := kernel.KernelCommand{
			ContractManifest: kernel.ContractIdentity, CommandID: commandID,
			CommandType: "tekroo.command.evidence.register", CommandVersion: kernel.SchemaVersion,
			Target:    kernel.AggregateRef{Kind: kernel.AggregateEvidence, ID: item.EvidenceID},
			Authority: recorder.policy.Authority, ExpectedRevision: kernel.MustNotExist(),
			ExpectedPolicyRevision:    recorder.policy.PolicyRevision,
			ExpectedCatalogueRevision: kernel.CatalogueRevision,
			IdempotencyKey:            fmt.Sprintf("execution-evidence/%s/%s/%s", invocation.ID, item.EvidenceID, expectedDigest),
			CorrelationID:             invocation.ID,
			Causation:                 []kernel.DagParent{{ParentEventID: invocation.LastEventID, EdgeKind: kernel.EdgeCausal}},
			Payload:                   payload,
		}
		receipt, err := recorder.commands.Handle(ctx, command, recorder.policy.Provenance)
		if err != nil {
			return nil, err
		}
		if receipt.OutcomeCode != kernel.OutcomeApplied && receipt.OutcomeCode != kernel.OutcomeNoChange {
			return nil, fmt.Errorf("record execution evidence: %s", receipt.ReasonCode)
		}
		result = append(result, kernel.EvidenceRef{EvidenceID: item.EvidenceID, SHA256: expectedDigest})
	}
	return result, nil
}

func validExecutionEvidenceKind(value string) bool {
	switch value {
	case "SOURCE_SNAPSHOT", "SOURCE_DIFF", "TEST_RESULT", "TOOL_RESULT", "ARTIFACT", "PROVIDER_RECEIPT", "MODEL_OUTPUT", "DECISION_RECORD", "EXTERNAL_OBSERVATION":
		return true
	default:
		return false
	}
}

func stableEvidenceTime(invocation kernel.WorkInvocation) time.Time {
	if invocation.StartedAt != nil {
		return *invocation.StartedAt
	}
	if invocation.ClaimedAt != nil {
		return *invocation.ClaimedAt
	}
	return invocation.DeadlineAt
}

var _ ExecutionEvidenceRecorder = (*CommandEvidenceRecorder)(nil)
