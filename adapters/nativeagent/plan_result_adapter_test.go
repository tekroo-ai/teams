package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func TestPlanFinalizationAuthorsOnlyDependenciesAndHandoffs(t *testing.T) {
	source := []byte(application.OrganizationalResultMarker + `
{"outcome":"completed","work_product":{"schema_version":"1.0.0","result_type":"FEATURE_PLAN","architecture":"accepted design","design_decisions":["accepted decision"],"assumptions":["accepted assumption"],"tasks":[{"title":"first","depends_on":[]},{"title":"second","depends_on":[]}]}}`)
	hash := sha256.Sum256(source)
	plan := &PlanFinalizationBinding{SourceOutput: source, SourceDigest: kernel.Digest(fmt.Sprintf("%x", hash[:]))}
	modelSchema, err := modelFacingPlanSchema(json.RawMessage(`{"type":"object","properties":{"work_product":{"type":"object"},"outcome":{"type":"string"}}}`), plan)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var work struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(modelSchema, &schema) != nil || json.Unmarshal(schema.Properties["work_product"], &work) != nil ||
		work.Properties["architecture"] != nil || work.Properties["tasks"] != nil || work.Properties["task_dependencies"] == nil {
		t.Fatalf("model still authors immutable design: %s", modelSchema)
	}
	input := json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":[{"task_index":1,"depends_on":[0]}],"handoffs":[{"provider_task_index":0,"consumer_task_index":1,"capability":"api","contract":"function exists"}]}}`)
	bound, err := bindHandlerPlanResult(input, plan)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		WorkProduct struct {
			ResultType         string        `json:"result_type"`
			Architecture       string        `json:"architecture"`
			SourceDesignDigest kernel.Digest `json:"source_design_digest"`
			Tasks              []struct {
				DependsOn []uint32 `json:"depends_on"`
			} `json:"tasks"`
			Handoffs []json.RawMessage `json:"handoffs"`
		} `json:"work_product"`
	}
	if json.Unmarshal(bound, &result) != nil || result.WorkProduct.ResultType != "FEATURE_EXECUTION_PLAN" ||
		result.WorkProduct.Architecture != "accepted design" || result.WorkProduct.SourceDesignDigest != plan.SourceDigest ||
		len(result.WorkProduct.Tasks) != 2 || len(result.WorkProduct.Tasks[1].DependsOn) != 1 || result.WorkProduct.Tasks[1].DependsOn[0] != 0 || len(result.WorkProduct.Handoffs) != 1 {
		t.Fatalf("accepted design not preserved in finalized plan: %s", bound)
	}
	for _, invalid := range []string{
		`{"outcome":"completed","work_product":{"task_dependencies":[{"task_index":1,"depends_on":[2]}],"handoffs":[]}}`,
		`{"outcome":"completed","work_product":{"task_dependencies":[],"handoffs":[],"architecture":"changed"}}`,
		`{"outcome":"completed","work_product":{"task_dependencies":[],"handoffs":null}}`,
	} {
		if _, err := bindHandlerPlanResult(json.RawMessage(invalid), plan); err == nil {
			t.Fatalf("invalid plan delta accepted: %s", invalid)
		}
	}
	if _, err := bindHandlerPlanResult(json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":[],"handoffs":[],"architecture":"changed"}}`), plan); err == nil || !strings.Contains(err.Error(), `unknown field "architecture"`) {
		t.Fatalf("invalid plan field was not identified: %v", err)
	}
}

func TestPlanFinalizationSchemaForbidsHandoffsWithOneImplementationTask(t *testing.T) {
	source := []byte(application.OrganizationalResultMarker + `
{"work_product":{"result_type":"FEATURE_PLAN","tasks":[{"purpose":"IMPLEMENTATION","depends_on":[]},{"purpose":"VALIDATION","depends_on":[0]}]}}`)
	plan := &PlanFinalizationBinding{SourceOutput: source}
	modelSchema, err := modelFacingPlanSchema(json.RawMessage(`{"type":"object","properties":{"work_product":{"type":"object"}}}`), plan)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var work struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	var handoffs struct {
		MaxItems *int `json:"maxItems"`
	}
	if json.Unmarshal(modelSchema, &schema) != nil || json.Unmarshal(schema.Properties["work_product"], &work) != nil || json.Unmarshal(work.Properties["handoffs"], &handoffs) != nil || handoffs.MaxItems == nil || *handoffs.MaxItems != 0 {
		t.Fatalf("single implementation task must admit no cross-task handoffs: %s", modelSchema)
	}
}

func TestPlanFinalizationPreservesSourceBriefAndExplainsNativeDelta(t *testing.T) {
	brief, _, profile := testBriefAndProfile()
	brief.Purpose = kernel.PurposeHandoff
	brief.MessageHandler = &application.MessageHandlerGrounding{ResultSchema: json.RawMessage(`{"type":"object","properties":{"work_product":{"type":"object"}}}`)}
	brief.ResultProtocol = &application.ExecutionResultProtocol{Marker: application.OrganizationalResultMarker}
	encodedBrief, _ := json.Marshal(brief)
	requestHash := sha256.Sum256(encodedBrief)
	source := []byte(application.OrganizationalResultMarker + `
{"work_product":{"result_type":"FEATURE_PLAN","tasks":[{"depends_on":[]}]}}`)
	sourceHash := sha256.Sum256(source)
	profile.BaseURL = "http://127.0.0.1:1/v1"
	binding := &testBinding{root: t.TempDir()}
	config := Config{
		Bindings: binding, Gateway: agenttools.Gateway{Bindings: binding}, Journal: &testJournal{},
		HTTP: http.DefaultClient, Profile: profile,
		PlanFinalization: &PlanFinalizationBinding{SourceOutput: source, SourceDigest: kernel.Digest(fmt.Sprintf("%x", sourceHash[:]))},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := PrepareReadOnly(ctx, brief, kernel.Digest(fmt.Sprintf("%x", requestHash[:])), config)
	if err != nil {
		t.Fatal(err)
	}
	var prompt struct {
		SourceRequestDigest  kernel.Digest   `json:"source_request_digest"`
		ExecutionBrief       json.RawMessage `json:"execution_brief"`
		NativeResultGuidance string          `json:"native_result_guidance"`
	}
	if json.Unmarshal([]byte(session.Prompt), &prompt) != nil || prompt.SourceRequestDigest != kernel.Digest(fmt.Sprintf("%x", requestHash[:])) || string(prompt.ExecutionBrief) != string(encodedBrief) || !strings.Contains(prompt.NativeResultGuidance, "only task_dependencies and handoffs") {
		t.Fatalf("native prompt did not preserve source and explain plan delta: %q", session.Prompt)
	}
}
