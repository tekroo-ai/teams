package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
)

func TestCommandEvidenceRecorderPersistsContentBeforeRegisteringExactMetadata(t *testing.T) {
	runtime := newOperationalRuntime(t)
	commands := &evidenceCommandSink{t: t}
	blobs := &evidenceBlobSink{}
	recorder, err := NewCommandEvidenceRecorder(commands, blobs, CommandEvidenceRecorderPolicy{
		PolicyRevision: 1, Authority: kernel.PrincipalRef{Kind: kernel.PrincipalService, ID: "teams-openhands-runtime"},
		Provenance: validTestProvenance(), ProducingVersion: "step-5", RetentionPolicy: "task-lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	items := []ExecutionEvidence{
		{EvidenceID: testUUID(802), Kind: "MODEL_OUTPUT", MediaType: "text/plain", SourceTimestamp: time.Date(2026, 8, 31, 12, 1, 0, 0, time.UTC), Content: []byte("model")},
		{EvidenceID: testUUID(801), Kind: "TOOL_RESULT", MediaType: "application/json", SourceTimestamp: time.Date(2026, 8, 31, 12, 0, 30, 0, time.UTC), Content: []byte(`{"exit":0}`)},
	}
	references, err := recorder.RecordExecutionEvidence(context.Background(), runtime.context.Invocation, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 2 || references[0].EvidenceID != testUUID(801) || references[1].EvidenceID != testUUID(802) || blobs.putCalls != 2 || len(commands.commands) != 2 {
		t.Fatalf("references=%v puts=%d commands=%d", references, blobs.putCalls, len(commands.commands))
	}
	for _, command := range commands.commands {
		if command.CommandType != "tekroo.command.evidence.register" || !command.ExpectedRevision.MustNotExist || command.ExpectedLifecycleEpoch != nil || len(command.Causation) != 1 || command.Causation[0].ParentEventID != runtime.context.Invocation.LastEventID {
			t.Fatalf("command = %#v", command)
		}
		var payload map[string]any
		if json.Unmarshal(command.Payload, &payload) != nil || payload["availability"] != "AVAILABLE" || payload["integrity_state"] != "DIGEST_VERIFIED" || payload["access_partition"] != runtime.context.Invocation.WorkspaceID || payload["locator_immutable"] != true {
			t.Fatalf("payload = %s", command.Payload)
		}
	}
}

type evidenceCommandSink struct {
	t        *testing.T
	commands []kernel.KernelCommand
}

func (sink *evidenceCommandSink) Handle(_ context.Context, command kernel.KernelCommand, _ kernel.ProvenanceBasis) (kernel.CommandReceipt, error) {
	sink.commands = append(sink.commands, command)
	return kernel.CommandReceipt{OutcomeCode: kernel.OutcomeApplied, ReasonCode: "APPLIED"}, nil
}

type evidenceBlobSink struct{ putCalls int }

func (sink *evidenceBlobSink) Put(_ context.Context, _ kernel.UUIDv7, content []byte) (ExecutionEvidenceBlobReceipt, error) {
	sink.putCalls++
	hash := sha256.Sum256(content)
	digest := kernel.Digest(hex.EncodeToString(hash[:]))
	return ExecutionEvidenceBlobReceipt{Locator: "immutable://" + string(digest), SHA256: digest, ByteLength: uint64(len(content))}, nil
}
