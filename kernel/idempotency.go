package kernel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

type IdempotencyReceipt struct {
	Outcome     string `json:"outcome"`
	Fingerprint string `json:"fingerprint,omitempty"`
	EventCount  int    `json:"eventCount"`
	Reason      string `json:"reason,omitempty"`
}

type IdempotencyLedger struct {
	decisions map[string]IdempotencyReceipt
}

func NewIdempotencyLedger() *IdempotencyLedger {
	return &IdempotencyLedger{decisions: make(map[string]IdempotencyReceipt)}
}

func (l *IdempotencyLedger) Apply(scope, semanticRequest any) (IdempotencyReceipt, error) {
	key, err := canonicalJSON(scope)
	if err != nil {
		return IdempotencyReceipt{}, fmt.Errorf("canonicalize scope: %w", err)
	}
	request, err := canonicalJSON(semanticRequest)
	if err != nil {
		return IdempotencyReceipt{}, fmt.Errorf("canonicalize semantic request: %w", err)
	}
	digest := sha256.Sum256(request)
	fingerprint := hex.EncodeToString(digest[:])

	prior, exists := l.decisions[string(key)]
	if !exists {
		receipt := IdempotencyReceipt{Outcome: "APPLIED", Fingerprint: fingerprint, EventCount: 1}
		l.decisions[string(key)] = receipt
		return receipt, nil
	}
	if prior.Fingerprint == fingerprint {
		return prior, nil
	}
	return IdempotencyReceipt{Outcome: "REJECTED_CONFLICT", EventCount: 0, Reason: "IDEMPOTENCY_KEY_REUSE"}, nil
}

func (l *IdempotencyLedger) DurableDecisionCount() int { return len(l.decisions) }

func CommandFingerprint(command KernelCommand) (Digest, error) {
	parents := append([]DagParent(nil), command.Causation...)
	sort.Slice(parents, func(i, j int) bool {
		if parents[i].EdgeKind != parents[j].EdgeKind {
			return parents[i].EdgeKind < parents[j].EdgeKind
		}
		return parents[i].ParentEventID < parents[j].ParentEventID
	})
	evidence := append([]EvidenceRef(nil), command.EvidenceRefs...)
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i].EvidenceID != evidence[j].EvidenceID {
			return evidence[i].EvidenceID < evidence[j].EvidenceID
		}
		return evidence[i].SHA256 < evidence[j].SHA256
	})
	preconditions := canonicalPreconditions(command.Preconditions)
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(command.Payload))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&payload)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	if decodeErr != nil || !errors.Is(trailingErr, io.EOF) {
		payload = struct {
			RawHex string `json:"raw_hex"`
		}{RawHex: hex.EncodeToString(command.Payload)}
	}
	semantic := struct {
		ContractManifest string                  `json:"contract_manifest"`
		CommandType      string                  `json:"command_type"`
		CommandVersion   string                  `json:"command_version"`
		Target           AggregateRef            `json:"target"`
		Authority        PrincipalRef            `json:"authority"`
		ActorFQN         *ActorFQN               `json:"actor_fqn,omitempty"`
		Execution        *ExecutionTuple         `json:"execution,omitempty"`
		ExpectedRevision ExpectedRevision        `json:"expected_revision"`
		Preconditions    []AggregatePrecondition `json:"preconditions"`
		Causation        []DagParent             `json:"causation"`
		Payload          any                     `json:"payload"`
		EvidenceRefs     []EvidenceRef           `json:"evidence_refs"`
	}{
		ContractManifest: command.ContractManifest,
		CommandType:      command.CommandType,
		CommandVersion:   command.CommandVersion,
		Target:           command.Target,
		Authority:        command.Authority,
		ActorFQN:         command.ActorFQN,
		Execution:        command.Execution,
		ExpectedRevision: command.ExpectedRevision,
		Preconditions:    preconditions,
		Causation:        parents,
		Payload:          payload,
		EvidenceRefs:     evidence,
	}
	canonical, err := canonicalJSON(semantic)
	if err != nil {
		return "", fmt.Errorf("canonicalize command: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return Digest(hex.EncodeToString(digest[:])), nil
}

func IdempotencyScopeDigest(command KernelCommand) (Digest, error) {
	scope := struct {
		ContractManifest string       `json:"contract_manifest"`
		Principal        PrincipalRef `json:"principal_ref"`
		CommandType      string       `json:"command_type"`
		Target           AggregateRef `json:"target"`
		IdempotencyKey   string       `json:"idempotency_key"`
	}{command.ContractManifest, command.Authority, command.CommandType, command.Target, command.IdempotencyKey}
	canonical, err := canonicalJSON(scope)
	if err != nil {
		return "", fmt.Errorf("canonicalize idempotency scope: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return Digest(hex.EncodeToString(digest[:])), nil
}

func canonicalJSON(value any) ([]byte, error) {
	bytesValue, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(bytesValue))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
