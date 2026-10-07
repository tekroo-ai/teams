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

var resultLimitFixture = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["outcome","work_product"],"properties":{"outcome":{"const":"completed"},"work_product":{"type":"object","additionalProperties":false,"required":["tasks"],"properties":{"tasks":{"type":"array","minItems":1,"maxItems":128,"items":{"type":"object","additionalProperties":false,"required":["title"],"properties":{"title":{"type":"string","minLength":1}}}}}}}}`)

func taskResultFixture(n int) json.RawMessage {
	tasks := make([]map[string]string, n)
	for i := range tasks {
		tasks[i] = map[string]string{"title": "Cohesive work"}
	}
	raw, _ := json.Marshal(map[string]any{"outcome": "completed", "work_product": map[string]any{"tasks": tasks}})
	return raw
}

func TestResultArrayLimitsNarrowWithoutEditingSignedContract(t *testing.T) {
	before := string(resultLimitFixture)
	limits := []ResultArrayLimit{{PropertyPath: []string{"work_product", "tasks"}, MaxItems: 7}}
	schema, err := narrowResultArrays(resultLimitFixture, limits)
	if err != nil {
		t.Fatal(err)
	}
	if string(resultLimitFixture) != before || !schemaAccepts(t, schema, taskResultFixture(7)) ||
		schemaAccepts(t, schema, taskResultFixture(8)) || schemaAccepts(t, schema, taskResultFixture(0)) {
		t.Fatalf("admission bound failed or signed schema changed: %s", schema)
	}
	if !schemaAccepts(t, resultLimitFixture, taskResultFixture(8)) {
		t.Fatal("signed source contract was narrowed in place")
	}
	bad := []byte(strings.Replace(string(taskResultFixture(1)), `"completed"`, `"failed"`, 1))
	if schemaAccepts(t, schema, bad) {
		t.Fatal("unrelated outcome constraint was lost")
	}
	limits = append(limits, ResultArrayLimit{PropertyPath: []string{"work_product", "tasks"}, MaxItems: 10})
	repeated, err := narrowResultArrays(schema, limits)
	if err != nil || schemaAccepts(t, repeated, taskResultFixture(8)) {
		t.Fatalf("a later looser limit broadened the schema: %s %v", repeated, err)
	}
}

func TestResultArrayLimitsFailClosedOnInvalidBindings(t *testing.T) {
	for _, limit := range []ResultArrayLimit{
		{PropertyPath: nil, MaxItems: 7},
		{PropertyPath: []string{"work_product", "tasks"}, MaxItems: -1},
		{PropertyPath: []string{"work_product", "tasks"}, MaxItems: 0},
		{PropertyPath: []string{"work_product", "missing"}, MaxItems: 7},
		{PropertyPath: []string{"outcome"}, MaxItems: 7},
	} {
		if _, err := narrowResultArrays(resultLimitFixture, []ResultArrayLimit{limit}); err == nil {
			t.Fatalf("invalid limit accepted: %+v", limit)
		}
	}
	unchanged, err := narrowResultArrays(resultLimitFixture, nil)
	if err != nil || string(unchanged) != string(resultLimitFixture) {
		t.Fatal("unbound schemas changed")
	}
	stricter := json.RawMessage(strings.Replace(string(resultLimitFixture), `"maxItems":128`, `"maxItems":3`, 1))
	schema, err := narrowResultArrays(stricter, []ResultArrayLimit{{PropertyPath: []string{"work_product", "tasks"}, MaxItems: 7}})
	if err != nil || schemaAccepts(t, schema, taskResultFixture(4)) {
		t.Fatal("original stricter limit was broadened")
	}
}

func TestPreparedSessionSendsAdmissionBoundInResponseGrammar(t *testing.T) {
	var wireSchema json.RawMessage
	response, _ := json.Marshal(map[string]json.RawMessage{"submit_result": taskResultFixture(7)})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Format struct {
				Schema struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		wireSchema = body.Format.Schema.Schema
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "admission-bound", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(response)}}}})
	}))
	defer server.Close()
	brief, _, profile := testBriefAndProfile()
	brief.MessageHandler = &application.MessageHandlerGrounding{ResultSchema: resultLimitFixture, AllowedResults: []string{"completed"}}
	brief.ResultProtocol = &application.ExecutionResultProtocol{Marker: application.OrganizationalResultMarker}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	profile.BaseURL, profile.ResponseMode = server.URL+"/v1", ResponseModeJSONSchemaActions
	bindings := &testBinding{root: t.TempDir()}
	config := Config{Bindings: bindings, Gateway: agenttools.Gateway{Bindings: bindings}, Journal: &testJournal{}, HTTP: server.Client(), Profile: profile,
		ResultArrayLimits: []ResultArrayLimit{{PropertyPath: []string{"work_product", "tasks"}, MaxItems: 7}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !schemaAccepts(t, wireSchema, response) {
		t.Fatalf("seven-task result rejected on wire: %s", wireSchema)
	}
	rejected, _ := json.Marshal(map[string]json.RawMessage{"submit_result": taskResultFixture(8)})
	if schemaAccepts(t, wireSchema, rejected) {
		t.Fatalf("eight-task result permitted on wire: %s", wireSchema)
	}
}
