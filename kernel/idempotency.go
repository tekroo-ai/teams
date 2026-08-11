package kernel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	canonical, err := canonicalJSON(command)
	if err != nil {
		return "", fmt.Errorf("canonicalize command: %w", err)
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
