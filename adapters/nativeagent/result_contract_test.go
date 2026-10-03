package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func testResultContract(brief application.ExecutionBrief) ResultContract {
	schema := json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["schema_version","result_type","stories"],"properties":{"schema_version":{"const":"1.0.0"},"result_type":{"const":"FEATURE_SPECIFICATION"},"stories":{"type":"array","minItems":1,"items":{"type":"object","additionalProperties":false,"required":["title"],"properties":{"title":{"type":"string","minLength":1}}}}}}`)
	sum := sha256.Sum256(schema)
	return ResultContract{TaskID: brief.Task.TaskID, TaskSourceDigest: brief.Task.SourceDigest,
		SchemaRef: "tekroo.feature-specification/1.0.0", SchemaSHA256: kernel.Digest(hex.EncodeToString(sum[:])), Schema: schema}
}

func TestNativeOrganizationalResultBindsSchemaAndRejectsMalformedOutput(t *testing.T) {
	brief, _, _ := testBriefAndProfile()
	brief.Task.TaskID = kernel.UUIDv7("00000000-0000-7000-8000-000000000952")
	brief.Task.SourceDigest = kernel.Digest(strings.Repeat("a", 64))
	brief.Purpose = kernel.PurposeHandoff
	brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Outcomes: []string{"STRUCTURED_HANDOFF"}}
	contract := testResultContract(brief)
	definition, finalize, err := nativeOrganizationalResult(brief, contract)
	if err != nil || definition.Name != "submit_result" || string(definition.Parameters) != string(contract.Schema) {
		t.Fatalf("definition=%+v err=%v", definition, err)
	}
	valid := json.RawMessage(`{"schema_version":"1.0.0","result_type":"FEATURE_SPECIFICATION","stories":[{"title":"One story"}]}`)
	if output, err := finalize(valid); err != nil || output != application.OrganizationalResultMarker+"\n"+string(valid) {
		t.Fatalf("valid output=%q err=%v", output, err)
	}
	brief.Purpose = kernel.PurposeReplan
	if _, replanningFinalize, err := nativeOrganizationalResult(brief, contract); err != nil {
		t.Fatalf("replan result channel: %v", err)
	} else if output, err := replanningFinalize(valid); err != nil || output != application.OrganizationalResultMarker+"\n"+string(valid) {
		t.Fatalf("replan output=%q err=%v", output, err)
	}
	for _, raw := range []string{
		`"{\"schema_version\":\"1.0.0\"}"`,
		`{"schema_version":"1.0.0","result_type":"FEATURE_SPECIFICATION","stories":[]}`,
		`{"schema_version":"1.0.0","result_type":"FEATURE_SPECIFICATION","stories":[{"title":"One"}],"extra":true}`,
	} {
		if _, err := finalize(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid result: %s", raw)
		}
	}
	contract.SchemaSHA256 = kernel.Digest(strings.Repeat("b", 64))
	if _, _, err := nativeOrganizationalResult(brief, contract); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("accepted changed schema digest: %v", err)
	}
	contract = testResultContract(brief)
	contract.TaskID = kernel.UUIDv7("00000000-0000-7000-8000-000000000953")
	if _, _, err := nativeOrganizationalResult(brief, contract); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("accepted different task: %v", err)
	}
	contract = testResultContract(brief)
	contract.TaskSourceDigest = kernel.Digest(strings.Repeat("b", 64))
	if _, _, err := nativeOrganizationalResult(brief, contract); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("accepted different task source: %v", err)
	}
	contract = testResultContract(brief)
	contract.Schema = json.RawMessage(`{"$ref":"https://example.invalid/remote-schema"}`)
	remoteSum := sha256.Sum256(contract.Schema)
	contract.SchemaSHA256 = kernel.Digest(hex.EncodeToString(remoteSum[:]))
	if _, _, err := nativeOrganizationalResult(brief, contract); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("accepted external schema reference: %v", err)
	}
}

func TestPreparedHandoffSessionUsesBoundSchema(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	brief.Task.TaskID = kernel.UUIDv7("00000000-0000-7000-8000-000000000954")
	brief.Task.SourceDigest = kernel.Digest(strings.Repeat("a", 64))
	brief.Purpose = kernel.PurposeHandoff
	brief.ResultProtocol = &application.ExecutionResultProtocol{SchemaVersion: "1.0.0", Marker: application.OrganizationalResultMarker, Outcomes: []string{"STRUCTURED_HANDOFF"}, Instruction: "The OpenHands finish tool message is the result consumed by Teams."}
	encoded, _ := json.Marshal(brief)
	sum := sha256.Sum256(encoded)
	digest := kernel.Digest(hex.EncodeToString(sum[:]))
	contract := testResultContract(brief)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[1].Content, contract.SchemaRef) || strings.Contains(body.Messages[1].Content, "The OpenHands finish tool message") || !strings.Contains(body.Messages[1].Content, string(digest)) {
			t.Error("native result contract was not exposed with source identity")
		}
		found := false
		for _, tool := range body.Tools {
			found = found || tool.Function.Name == "submit_result"
		}
		if !found {
			t.Error("structured result tool not exposed")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"final-1","function":{"name":"submit_result","arguments":"{\"schema_version\":\"1.0.0\",\"result_type\":\"FEATURE_SPECIFICATION\",\"stories\":[{\"title\":\"One story\"}]}"}}]}}]}`))
	}))
	defer server.Close()
	profile.BaseURL = server.URL + "/v1"
	binding := &testBinding{root: t.TempDir()}
	config := Config{Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding, Host: agenttools.Host{Timeout: time.Second}}, Journal: &testJournal{}, HTTP: server.Client(), Profile: profile, ResultContract: &contract}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	withoutContract := config
	withoutContract.ResultContract = nil
	if _, err := PrepareReadOnly(ctx, brief, digest, withoutContract); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("accepted handoff without resolved schema: %v", err)
	}
	session, err := PrepareReadOnly(ctx, brief, digest, config)
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.Run(ctx)
	if err != nil || !strings.Contains(output, `"result_type":"FEATURE_SPECIFICATION"`) {
		t.Fatalf("output=%q err=%v", output, err)
	}
}
