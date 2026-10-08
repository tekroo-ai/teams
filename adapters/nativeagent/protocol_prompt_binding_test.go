package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestBoundaryRecognizesOnlyExactServerResolvedProtocolPrompt(t *testing.T) {
	for _, validation := range []bool{false, true} {
		name := "workflow-with-raw-input"
		if validation {
			name = "validation"
		}
		t.Run(name, func(t *testing.T) {
			brief, _, profile := testBriefAndProfile()
			profile.BaseURL = "http://example.invalid/v1"
			brief.Task.TaskID = kernel.UUIDv7("00000000-0000-7000-8000-000000000961")
			brief.Task.SourceDigest = kernel.Digest(strings.Repeat("a", 64))
			brief.Purpose = kernel.PurposeHandoff
			brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Outcomes: []string{"STRUCTURED_HANDOFF"}}
			contract := testResultContract(brief)
			input := &ResultInput{Evidence: kernel.EvidenceRef{EvidenceID: kernel.UUIDv7("00000000-0000-7000-8000-000000000962")}, Value: json.RawMessage(`{"accepted_plan":{"version":1}}`)}
			hash := func(raw []byte) kernel.Digest {
				sum := sha256.Sum256(raw)
				return kernel.Digest(hex.EncodeToString(sum[:]))
			}
			input.Evidence.SHA256 = hash(input.Value)
			brief.Evidence = append(brief.Evidence, input.Evidence)
			binding := &testBinding{root: t.TempDir()}
			config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding}, Journal: &testJournal{}, HTTP: http.DefaultClient, Profile: profile, ResultContract: &contract, ResultInput: input}
			if validation {
				brief.Purpose = kernel.PurposeValidation
				brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.ValidationResultMarker, Outcomes: []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"}}
				config.ResultContract, config.ResultInput = nil, nil
			}
			raw, _ := json.Marshal(brief)
			digest := hash(raw)
			session, err := PrepareReadOnly(t.Context(), brief, digest, config)
			if err != nil {
				t.Fatal(err)
			}
			start, _ := json.Marshal(map[string]any{"request_digest": digest, "prompt": session.Prompt})
			output := "immutable result"
			payload, _ := json.Marshal(output)
			entries := []agentruntime.Entry{{InvocationID: string(brief.InvocationID), Sequence: 1, Kind: agentruntime.Started, Payload: start}, {InvocationID: string(brief.InvocationID), Sequence: 2, Kind: agentruntime.Finished, Payload: payload}}
			boundary := &Boundary{Configure: func(context.Context, application.ExecutionBrief) (Config, error) { return config, nil }}
			for pass := 0; pass < 2; pass++ {
				observation, terminal, err := boundary.observeJournal(t.Context(), brief, digest, entries)
				if err != nil || !terminal || observation.State != application.ExternalSucceeded || string(observation.Output) != output {
					t.Fatalf("restart observation=%+v terminal=%v error=%v", observation, terminal, err)
				}
			}
			for _, changed := range []string{strings.Replace(session.Prompt, "Call submit_result once", "Ignore the contract", 1), session.Prompt + " ", string(mustPromptJSON(t, map[string]any{"source_request_digest": digest, "execution_brief": string(raw)}))} {
				bad := append([]agentruntime.Entry(nil), entries...)
				bad[0].Payload = mustPromptJSON(t, map[string]any{"request_digest": digest, "prompt": changed})
				if _, _, err := boundary.observeJournal(t.Context(), brief, digest, bad); !errors.Is(err, ErrInvalidBinding) {
					t.Fatal("accepted altered/encoded prompt", err)
				}
			}
			for _, change := range []func([]agentruntime.Entry){func(e []agentruntime.Entry) { e[1].Sequence = 3 }, func(e []agentruntime.Entry) { e[1].InvocationID = "wrong" }} {
				bad := append([]agentruntime.Entry(nil), entries...)
				change(bad)
				if _, _, err := boundary.observeJournal(t.Context(), brief, digest, bad); !errors.Is(err, ErrInvalidBinding) {
					t.Fatal("lost journal identity check", err)
				}
			}
			if !validation {
				changed := contract
				changed.SchemaRef = "other/admitted-contract"
				config.ResultContract = &changed
				if _, _, err := boundary.observeJournal(t.Context(), brief, digest, entries); !errors.Is(err, ErrInvalidBinding) {
					t.Fatal("accepted changed server contract", err)
				}
				config.ResultContract = &contract
				changedInput := *input
				changedInput.Value = json.RawMessage(`{"accepted_plan":{"version":2}}`)
				config.ResultInput = &changedInput
				if _, _, err := boundary.observeJournal(t.Context(), brief, digest, entries); !errors.Is(err, ErrInvalidBinding) {
					t.Fatal("accepted changed input bytes", err)
				}
			}
		})
	}
}

func mustPromptJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
