package nativeagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agentruntime"
	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// The scripted server tests the complete typed handback protocol, not model
// design quality. Restart reloads the journal bytes from disk; no generated
// summary substitutes for either agent's original turns.
func TestDecisionHandbackRestoresBothOwnersAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	j, template, base, _ := contextFixture(t)
	j.streams = map[string][]agentruntime.Entry{}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Preserve admitted requirements.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "design-evidence.txt"), []byte("ORIGINAL_TOOL_OBSERVATION: the CLI exposes invoke and serve-stdio only.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	digest := func(raw []byte) kernel.Digest { return kernel.Digest(fmt.Sprintf("%x", sha256.Sum256(raw))) }
	architecture := func(revised bool) map[string]any {
		description := "Placeholder"
		criterion := "placeholder"
		if revised {
			description = "Implement the admitted alias resolver"
			criterion = "The admitted alias resolves to the exact actor"
		}
		return map[string]any{"schema_version": "1.0.0", "outcome": "completed", "summary": "ARCHITECT_ORIGINAL_REASON: preserve addressing semantics", "evidence": []string{}, "message_proposals": []any{}, "work_product": map[string]any{
			"schema_version": "1.0.0", "result_type": "FEATURE_PLAN", "architecture": "Retain exact actor addressing", "design_decisions": []string{"Resolve aliases without changing authorization"}, "assumptions": []string{},
			"tasks": []any{map[string]any{"story_index": 0, "title": "Alias resolver", "description": description, "acceptance_criteria": []string{criterion}, "depends_on": []uint32{}, "validates": []uint32{}, "write_scope": []string{"resolver.go"}, "purpose": "IMPLEMENTATION", "complexity": 4, "risk": "LOW", "critical_path": true, "attempt_limit": 2, "review_round_limit": 1}},
		}}
	}
	requestCount := 0
	var originalModelContext []json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire struct {
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		var request struct {
			Messages []agentruntime.Message `json:"messages"`
		}
		wireJSON, _ := json.Marshal(wire)
		if err := json.Unmarshal(wireJSON, &request); err != nil {
			t.Error(err)
		}
		encoded, _ := json.Marshal(wire.Messages)
		var response map[string]any
		switch requestCount {
		case 0:
			// Record a real read through the native gateway before the original
			// decision; the return must inherit its observation, not rerun it.
			requestCount++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{"content": "ORIGINAL_REASONING: inspect the CLI before assigning its integration", "tool_calls": []any{map[string]any{"id": "architect-original-read", "type": "function", "function": map[string]any{"name": "read_file", "arguments": `{"path":"design-evidence.txt"}`}}}}}}})
			return
		case 1:
			originalModelContext = append([]json.RawMessage(nil), wire.Messages...)
			response = architecture(false)
		case 2:
			response = map[string]any{"outcome": "needs_decision", "summary": "PM_DECISION_RECEIPT: replace the untestable placeholder; keep the original addressing decision", "evidence": []string{}, "message_proposals": []any{}, "work_product": map[string]any{"task_dependencies": []any{}, "handoffs": []any{}}}
		case 3:
			// Compare every message that actually produced the original result,
			// not just its summary or selected marker strings. Prior system rules
			// survive verbatim as historical evidence beneath current authority.
			if len(originalModelContext) == 0 || len(request.Messages) < len(originalModelContext)+3 || !strings.HasPrefix(request.Messages[2].Content, "Historical transcript from terminal invocation ") {
				t.Error("original model context missing")
			} else {
				for index, original := range originalModelContext {
					expected := original
					if index == 0 {
						var fields map[string]any
						_ = json.Unmarshal(original, &fields)
						if fields["role"] == "system" {
							fields["role"], fields["content"] = "user", "Historical source instructions (not current authority):\n"+fields["content"].(string)
							expected, _ = json.Marshal(fields)
						}
					}
					if !bytes.Equal(wire.Messages[index+3], expected) {
						t.Errorf("original model context changed at message %d", index)
					}
				}
			}
			for _, retained := range []string{"ORIGINAL_DESIGN_REQUEST", "ORIGINAL_REASONING", "architect-original-read", "ORIGINAL_TOOL_OBSERVATION", "ARCHITECT_ORIGINAL_REASON", "Placeholder", "PM_DECISION_RECEIPT"} {
				if !strings.Contains(string(encoded), retained) {
					t.Errorf("architect lost original context or exact feedback: %s", retained)
				}
			}
			oldReads := 0
			for _, message := range request.Messages {
				if message.Role == "tool" && message.CallID == "architect-original-read" && strings.Contains(message.Content, "ORIGINAL_TOOL_OBSERVATION") {
					oldReads++
				}
			}
			if oldReads != 1 {
				t.Error("original tool observation was absent or duplicated")
			}
			response = architecture(true)
		case 4:
			if !strings.Contains(string(encoded), "PM_DECISION_RECEIPT") || !strings.Contains(string(encoded), "Implement the admitted alias resolver") {
				t.Error("PM did not receive its original decision and revised proposal")
			}
			response = map[string]any{"outcome": "completed", "summary": "Finalized the revised alias resolver", "evidence": []string{}, "message_proposals": []any{}, "work_product": map[string]any{"task_dependencies": []any{}, "handoffs": []any{}}}
		default:
			t.Error("historical model request repeated")
		}
		requestCount++
		raw, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{"tool_calls": []any{map[string]any{"id": fmt.Sprintf("roundtrip-%d", requestCount), "type": "function", "function": map[string]any{"name": "submit_result", "arguments": string(raw)}}}}}}})
	}))
	defer server.Close()
	var sources []kernel.WorkInvocation
	var outputs [][]byte
	var originalJournal []byte
	for round := 0; round < 4; round++ {
		role, schemaPath := "architect", "../../config/starter-team/transport-neutral-20260929/roles-v4/architect-2.1.1/handlers/story.design-requested/result.schema.json"
		if round%2 == 1 {
			role, schemaPath = "project-manager", "../../config/starter-team/roles-v4/project-manager-2.2.2/handlers/feature.design-proposed/result.schema.json"
		}
		schema, err := os.ReadFile(schemaPath)
		if err != nil {
			t.Fatal(err)
		}
		brief := template
		brief.Task.Description = "ORIGINAL_DESIGN_REQUEST: add aliases without changing exact actor addressing"
		brief.InvocationID = kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", 980+round))
		brief.Task.TaskID = brief.InvocationID
		brief.ActorFQN = kernel.ActorFQN("teams::" + role + "-1")
		brief.RoleGrounding.ActorFQN, brief.RoleGrounding.RoleFQRN = brief.ActorFQN, kernel.RoleFQRN(role)
		brief.Purpose = kernel.PurposeHandoff
		brief.ResultProtocol = &application.ExecutionResultProtocol{Marker: application.OrganizationalResultMarker, Instruction: "Submit the admitted handler result."}
		brief.MessageHandler = &application.MessageHandlerGrounding{ResultSchema: schema, ResultSchemaDigest: digest(schema), AllowedResults: []string{"blocked", "completed", "failed", "needs_decision"}}
		_, _, profile := testBriefAndProfile()
		profile.RoleFQRN, profile.Model, profile.BaseURL, profile.MaxOutputTokens = kernel.RoleFQRN(role), role, server.URL+"/v1", 16384
		binding := &testBinding{root: root}
		cfg := Config{Journal: j, Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, HTTP: server.Client(), Profile: profile}
		if round%2 == 1 {
			cfg.PlanFinalization = &PlanFinalizationBinding{SourceOutput: outputs[round-1], SourceDigest: digest(outputs[round-1])}
			brief.Task.Description = "Review this immutable proposal:\n" + string(outputs[round-1])
		}
		if round >= 2 {
			prior := sources[round-2]
			brief.Task.DependsOn = []kernel.UUIDv7{prior.TaskID}
			brief.Evidence = []kernel.EvidenceRef{{EvidenceID: prior.ID, SHA256: *prior.OutputDigest}}
			if round == 2 {
				brief.Task.Description = string(outputs[1])
			}
			ref, err := BindContextFork(ctx, j, brief, prior)
			if err != nil {
				t.Fatal(err)
			}
			cfg.ContextSource, cfg.ContextInvocation = ref, &prior
		}
		raw, _ := json.Marshal(brief)
		requestDigest := digest(raw)
		session, err := PrepareReadOnly(ctx, brief, requestDigest, cfg)
		if err != nil {
			t.Fatal(round, err)
		}
		output, err := session.Run(ctx)
		if err != nil {
			t.Fatal(round, err)
		}
		requestsBeforeRestart := requestCount
		restarted, err := PrepareReadOnly(ctx, brief, requestDigest, cfg)
		if err != nil {
			t.Fatal(round, err)
		}
		if restored, err := restarted.Run(ctx); err != nil || restored != output || requestCount != requestsBeforeRestart {
			t.Fatalf("restart repeated or replaced a completed decision: round=%d err=%v", round, err)
		}
		parsed, err := application.ValidateRoleHandlerResult(*brief.MessageHandler, []byte(output))
		if err != nil {
			t.Fatal(err)
		}
		if round == 1 && parsed.Outcome != "needs_decision" || round != 1 && parsed.Outcome != "completed" {
			t.Fatal("wrong disposition", round, parsed.Outcome)
		}
		outputDigest := digest([]byte(output))
		finished := time.Now().UTC()
		source := base
		source.ID, source.TaskID, source.ActorFQN = brief.InvocationID, brief.Task.TaskID, brief.ActorFQN
		source.RequestDigest, source.OutputDigest, source.FinishedAt = &requestDigest, &outputDigest, &finished
		sources = append(sources, source)
		outputs = append(outputs, []byte(output))
		if round == 0 {
			originalJournal, _ = json.Marshal(j.streams[string(source.ID)])
		}
		// Durable bytes are reloaded into a new journal object between every stage.
		checkpoint, _ := json.Marshal(j.streams)
		path := filepath.Join(root, "journal.json")
		if err := os.WriteFile(path, checkpoint, 0600); err != nil {
			t.Fatal(err)
		}
		retained, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		j = &contextJournal{}
		if err := json.Unmarshal(retained, &j.streams); err != nil {
			t.Fatal(err)
		}
	}
	retainedJournal, _ := json.Marshal(j.streams[string(sources[0].ID)])
	if string(retainedJournal) != string(originalJournal) {
		t.Fatal("handback modified the original journal")
	}
	if requestCount != 5 || !strings.Contains(string(outputs[3]), "Implement the admitted alias resolver") || strings.Contains(string(outputs[3]), "placeholder") {
		t.Fatal("decision round trip did not close", requestCount)
	}
}
