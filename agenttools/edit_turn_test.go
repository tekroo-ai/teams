package agenttools

import (
	"context"
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
)

type editTurnJournal struct{ entries []agentruntime.Entry }

func (j *editTurnJournal) Load(context.Context, string) ([]agentruntime.Entry, error) {
	return append([]agentruntime.Entry(nil), j.entries...), nil
}
func (j *editTurnJournal) Append(_ context.Context, id string, expected uint64, kind agentruntime.Kind, payload json.RawMessage) (agentruntime.Entry, error) {
	if expected != uint64(len(j.entries)) {
		return agentruntime.Entry{}, fmt.Errorf("journal revision mismatch")
	}
	entry := agentruntime.Entry{InvocationID: id, Sequence: expected + 1, Kind: kind, Payload: append(json.RawMessage(nil), payload...)}
	j.entries = append(j.entries, entry)
	return entry, nil
}

func TestStructuredEditToolRoundTripWithoutCorrection(t *testing.T) {
	gateway, _, root, request := editEffectFixture(t)
	definitions := MutationDefinitions([]string{"repository.edit"})
	definitions = append(definitions, agentruntime.ToolDefinition{Name: "submit_result", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["outcome"],"properties":{"outcome":{"type":"string","enum":["completed"]}}}`)})
	contract, err := agentruntime.NewStructuredTurnContract(definitions, "submit_result")
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		format := body["response_format"].(map[string]any)
		if format["type"] != "json_schema" || body["tool_choice"] != "none" {
			t.Error("formal schema transport missing")
		}
		encodedSchema, _ := json.Marshal(format)
		if !strings.Contains(string(encodedSchema), `"edit_file"`) || !strings.Contains(string(encodedSchema), `"old_text"`) {
			t.Error("edit schema missing from first request")
		}
		content := `{"submit_result":{"outcome":"completed"}}`
		if requests == 1 {
			encoded, _ := json.Marshal(map[string]any{"calls": []any{map[string]json.RawMessage{"edit_file": request.Call.Arguments}}})
			content = string(encoded)
		}
		if requests > 2 {
			t.Error("unnecessary correction/model turn")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("edit-response-%d", requests), "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": content}}}})
	}))
	defer server.Close()
	journal := &editTurnJournal{}
	runner := agentruntime.Runner{Journal: journal,
		Tools:     ReadOnlyTurnAdapter{Gateway: Gateway{Bindings: gateway.Bindings, Host: gateway.Host}, InvocationID: request.InvocationID, RequestDigest: request.RequestDigest},
		Effects:   MutationTurnAdapter{Gateway: gateway, InvocationID: request.InvocationID, RequestDigest: request.RequestDigest},
		Model:     agentruntime.OpenAIModel{Client: server.Client(), BaseURL: server.URL + "/v1", Model: "fixture", MaxTokens: 512, FinalTool: "submit_result", TurnContract: contract},
		FinalTool: "submit_result", Finalize: func(raw json.RawMessage) (string, error) { return string(raw), nil }, MaxTurns: 2}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if _, err := runner.Run(ctx, string(request.InvocationID), string(request.RequestDigest), "Make a precise edit"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if requests != 2 || string(got) != "after\nkeep\n" {
		t.Fatalf("round trip/replay: requests=%d content=%q", requests, got)
	}
	for _, entry := range journal.entries {
		if entry.Kind == agentruntime.ModelRejected {
			t.Fatal("schema correction during valid edit")
		}
		if entry.Kind == agentruntime.ToolDone {
			var result agentruntime.ToolResult
			if err := json.Unmarshal(entry.Payload, &result); err != nil || result.Error != "" {
				t.Fatalf("tool error: %+v %v", result, err)
			}
		}
	}
}
