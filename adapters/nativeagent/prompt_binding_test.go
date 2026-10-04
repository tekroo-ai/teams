package nativeagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestNativeHandlerPromptRemainsBoundAcrossJournalInspection(t *testing.T) {
	brief, _, _ := testBriefAndProfile()
	brief.Purpose = kernel.PurposeHandoff
	brief.MessageHandler = &application.MessageHandlerGrounding{}
	encoded, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	for _, plan := range []bool{false, true} {
		prompt, err := nativeHandlerPrompt(brief, digest, encoded, plan)
		if err != nil {
			t.Fatal(err)
		}
		started, _ := json.Marshal(map[string]string{"request_digest": string(digest), "prompt": prompt})
		finished, _ := json.Marshal("complete")
		entries := []agentruntime.Entry{
			{InvocationID: string(brief.InvocationID), Sequence: 1, Kind: agentruntime.Started, Payload: started},
			{InvocationID: string(brief.InvocationID), Sequence: 2, Kind: agentruntime.Finished, Payload: finished},
		}
		observed, terminal, err := observeJournal(brief, digest, entries)
		if err != nil || !terminal || observed.State != application.ExternalSucceeded || string(observed.Output) != "complete" {
			t.Fatalf("bound prompt plan=%t: %+v terminal=%t err=%v", plan, observed, terminal, err)
		}
		var envelope map[string]any
		if err := json.Unmarshal([]byte(prompt), &envelope); err != nil {
			t.Fatal(err)
		}
		envelope["native_result_guidance"] = "tampered"
		tampered, _ := json.Marshal(envelope)
		entries[0].Payload, _ = json.Marshal(map[string]string{"request_digest": string(digest), "prompt": string(tampered)})
		if _, _, err := observeJournal(brief, digest, entries); err != ErrInvalidBinding {
			t.Fatalf("tampered prompt accepted plan=%t: %v", plan, err)
		}
	}
}
