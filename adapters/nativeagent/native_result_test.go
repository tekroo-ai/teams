package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestNativeValidationResultIsStructuredAndPreservesSourceDigest(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeValidation
	brief.ResultProtocol = &application.ExecutionResultProtocol{
		SchemaVersion: "1.0.0", Marker: application.ValidationResultMarker,
		Outcomes:    []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"},
		Instruction: "The OpenHands finish tool message is the result consumed by Teams.",
	}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name       string          `json:"name"`
					Parameters json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[1].Content, string(digest)) || strings.Contains(body.Messages[1].Content, "The OpenHands finish tool message") || !strings.Contains(body.Messages[1].Content, "Call submit_result once") {
			t.Errorf("unbound or contradictory native prompt: %+v", body.Messages)
		}
		found := false
		for _, tool := range body.Tools {
			if tool.Function.Name == "submit_result" {
				found = strings.Contains(string(tool.Function.Parameters), `"additionalProperties":false`)
			}
		}
		if !found {
			t.Error("structured validation result tool is missing")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"final-1","function":{"name":"submit_result","arguments":"{\"schema_version\":\"1.0.0\",\"outcome\":\"PASS\",\"reasons\":[\"The check passed.\"]}"}}]}}]}`))
	}))
	defer server.Close()
	profile.BaseURL = server.URL + "/v1"
	binding := &testBinding{root: t.TempDir()}
	journal := &testJournal{}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: journal, HTTP: server.Client(), Profile: profile}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.Run(ctx)
	if err != nil || output != application.ValidationResultMarker+"\n"+`{"schema_version":"1.0.0","outcome":"PASS","reasons":["The check passed."]}` {
		t.Fatalf("output=%q err=%v entries=%+v", output, err, journal.entries)
	}
}

func TestNativeValidationResultRejectsInvalidShapes(t *testing.T) {
	brief := application.ExecutionBrief{Purpose: kernel.PurposeReview, ResultProtocol: &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.ValidationResultMarker, Outcomes: []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"}}}
	_, finalize, ok := nativeValidationResult(brief)
	if !ok {
		t.Fatal("missing review result tool")
	}
	for _, raw := range []string{
		`"{\"schema_version\":\"1.0.0\",\"outcome\":\"PASS\",\"reasons\":[\"ok\"]}"`,
		`{"schema_version":"1.0.0","outcome":"PASS","reasons":[]}`,
		`{"schema_version":"1.0.0","outcome":"PASS","reasons":["ok"],"extra":true}`,
		`{"schema_version":"1.0.0","outcome":"PASS","reasons":["ok","ok"]}`,
		`{"schema_version":"1.0.0","outcome":"PASS","reasons":["ok"],"candidate_id":"00000000-0000-7000-8000-000000000001"}`,
	} {
		if output, err := finalize(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid result %s as %q", raw, output)
		}
	}
}

func TestNativeValidationAcceptsNormalTestOutputWhitespace(t *testing.T) {
	value := json.RawMessage(`{"schema_version":"1.0.0","outcome":"PASS","reasons":["The test passed."],"test_evidence":["ok  example.test/nativequalification\t1.203s\nPASS"]}`)
	if err := validateNativeValidationPayload(value); err != nil {
		t.Fatalf("JSON Schema-valid test output was rejected by the host: %v", err)
	}
}
