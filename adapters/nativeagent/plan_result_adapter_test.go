package nativeagent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

// The real PM contract requires complete plan fields in a conditional branch.
// The model owns only the delta; Teams supplies the immutable design fields.
func TestPlanDeltaAndAssembledResultAgainstSignedConditionalSchema(t *testing.T) {
	signed, err := os.ReadFile("../../config/starter-team/roles-v4/project-manager-2.2.1/handlers/feature.design-proposed/result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(signed)
	source := []byte(application.OrganizationalResultMarker + `
{"work_product":{"schema_version":"1.0.0","result_type":"FEATURE_PLAN","architecture":"Change greeting behavior without changing its public API.","tasks":[{"story_index":0,"title":"Implement greeting","description":"Update greeting behavior.","acceptance_criteria":["Greeting returns the requested text."],"purpose":"IMPLEMENTATION","complexity":1,"risk":"LOW","attempt_limit":1,"review_round_limit":1,"depends_on":[]}]}}`)
	digest := sha256.Sum256(source)
	plan := &PlanFinalizationBinding{SourceOutput: source, SourceDigest: kernel.Digest(fmt.Sprintf("%x", digest[:]))}
	handler := application.MessageHandlerGrounding{ResultSchema: signed}
	modelSchema, err := modelFacingHandlerSchema(handler, kernel.PurposeHandoff)
	if err != nil {
		t.Fatal(err)
	}
	modelSchema, err = modelFacingPlanSchema(modelSchema, plan)
	if err != nil {
		t.Fatal(err)
	}
	delta := json.RawMessage(`{"outcome":"completed","summary":"Ready for implementation.","evidence":["Accepted design reviewed."],"message_proposals":[],"work_product":{"task_dependencies":[],"handoffs":[]}}`)
	if !schemaAccepts(t, modelSchema, delta) {
		t.Fatalf("conditional schema rejects admitted model delta: %s", modelSchema)
	}
	if schemaAccepts(t, signed, delta) {
		t.Fatal("signed full-result schema must not accept an unassembled delta")
	}
	assembled, err := bindHandlerPlanResult(bindHandlerResultVersion(delta, handler), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !schemaAccepts(t, signed, assembled) {
		t.Fatalf("assembled result violates signed schema: %s", assembled)
	}
	if sha256.Sum256(signed) != originalDigest {
		t.Fatal("signed source contract changed")
	}
	for _, sample := range []string{
		`{"outcome":"completed","summary":"Ready","evidence":[],"message_proposals":[],"work_product":{"task_dependencies":"[]","handoffs":[]}}`,
		`{"outcome":"completed","summary":"Ready","evidence":[],"message_proposals":[],"work_product":{"task_dependencies":[],"handoffs":[],"architecture":"model changed design"}}`,
	} {
		if schemaAccepts(t, modelSchema, []byte(sample)) {
			t.Fatalf("invalid model delta accepted: %s", sample)
		}
	}
}

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
	input := json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":[{"task_index":1,"depends_on":[0]}],"handoffs":[{"provider_task_index":0,"consumer_task_index":1,"capability":"api","contract":{"provided_interface":"Public function","required_inputs":[],"consumer_usage":"Call the public function","verification":"Invoke the actual public function"}}]}}`)
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

// r45: the PM repeated an edge already present in the accepted architecture.
// Reasserting the same prerequisite must not change the graph or the binding.
func TestPlanFinalizationRepeatedDependenciesAreIdempotent(t *testing.T) {
	source := []byte(application.OrganizationalResultMarker + `
{"work_product":{"result_type":"FEATURE_PLAN","architecture":"unchanged","tasks":[{"depends_on":[]},{"depends_on":[0]},{"depends_on":[0,1]}]}}`)
	plan := &PlanFinalizationBinding{SourceOutput: source}
	for _, dependencies := range []string{
		`[{"task_index":2,"depends_on":[0]}]`,
		`[{"task_index":2,"depends_on":[0,1]}]`,
	} {
		raw := json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":` + dependencies + `,"handoffs":[]}}`)
		bound, err := bindHandlerPlanResult(raw, plan)
		if err != nil {
			t.Fatalf("repeated dependency rejected: %v", err)
		}
		var result struct {
			WorkProduct struct {
				Architecture string `json:"architecture"`
				Tasks        []struct {
					DependsOn []uint32 `json:"depends_on"`
				} `json:"tasks"`
			} `json:"work_product"`
		}
		if json.Unmarshal(bound, &result) != nil || result.WorkProduct.Architecture != "unchanged" || fmt.Sprint(result.WorkProduct.Tasks[2].DependsOn) != "[0 1]" {
			t.Fatalf("repeated edge changed accepted graph: %s", bound)
		}
		if string(plan.SourceOutput) != string(source) {
			t.Fatal("accepted architecture mutated")
		}
	}
	for _, dependencies := range []string{
		`[{"task_index":2,"depends_on":[2]}]`,
		`[{"task_index":2,"depends_on":[3]}]`,
		`[{"task_index":2,"depends_on":[0,0]}]`,
		`[{"task_index":2,"depends_on":null}]`,
		`[{"task_index":2,"depends_on":[0]},{"task_index":2,"depends_on":[1]}]`,
	} {
		_, err := bindHandlerPlanResult(json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":`+dependencies+`,"handoffs":[]}}`), plan)
		if !errors.Is(err, ErrInvalidPlanDelta) || errors.Is(err, ErrInvalidBinding) {
			t.Fatalf("invalid delta misclassified: %s: %v", dependencies, err)
		}
	}
}

// Opt-in, read-only replay of retained model submissions. No model calls,
// journal writes, or invocation state changes are made by this test.
func TestPlanFinalizationRetainedSubmissionReplay(t *testing.T) {
	if os.Getenv("TEKROO_PLAN_REPLAY_CONFIG") == "" {
		t.Skip("requires an explicit config and architecture/PM invocation IDs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	script := `const fs=require("fs");const c=JSON.parse(fs.readFileSync(process.env.TEKROO_PLAN_REPLAY_CONFIG));const d=new Mongo(fs.readFileSync(c.mongo.uri_file,"utf8").trim()).getDB(c.mongo.database);const source=d.agent_turn_entries.findOne({invocation_id:process.env.TEKROO_PLAN_REPLAY_ARCHITECT,kind:"finished"});if(!source)throw new Error("architecture result absent");const submissions=[];for(const r of d.agent_turn_entries.find({invocation_id:process.env.TEKROO_PLAN_REPLAY_PM,kind:"model_turn"}).sort({sequence:1}).toArray()){const p=JSON.parse(r.payload.toString("utf8"));for(const t of p.tool_calls||[]){if(t.name==="submit_result")submissions.push(t.arguments);}}print(JSON.stringify({source:JSON.parse(source.payload.toString("utf8")),submissions}));`
	output, err := exec.CommandContext(ctx, "mongosh", "--nodb", "--quiet", "--eval", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	var retained struct {
		Source      string            `json:"source"`
		Submissions []json.RawMessage `json:"submissions"`
	}
	if err := json.Unmarshal(output, &retained); err != nil || len(retained.Submissions) == 0 {
		t.Fatalf("read retained submissions: %v", err)
	}
	signed, err := os.ReadFile("../../config/starter-team/roles-v4/project-manager-2.2.1/handlers/feature.design-proposed/result.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	sourceHash := sha256.Sum256([]byte(retained.Source))
	plan := &PlanFinalizationBinding{SourceOutput: []byte(retained.Source), SourceDigest: kernel.Digest(fmt.Sprintf("%x", sourceHash[:]))}
	handler := application.MessageHandlerGrounding{ResultSchema: signed}
	for index, raw := range retained.Submissions {
		bound, err := bindHandlerPlanResult(bindHandlerResultVersion(raw, handler), plan)
		if err != nil {
			t.Fatalf("retained submission %d: %v", index+1, err)
		}
		if !schemaAccepts(t, signed, bound) {
			t.Fatalf("retained submission %d violates signed schema", index+1)
		}
		_, sourceTasks, err := acceptedPlanWorkProduct(plan)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Outcome     string `json:"outcome"`
			WorkProduct struct {
				Tasks []map[string]json.RawMessage `json:"tasks"`
			} `json:"work_product"`
		}
		if err := json.Unmarshal(bound, &result); err != nil {
			t.Fatal(err)
		}
		if result.Outcome == "completed" && !reflect.DeepEqual(sourceTasks, result.WorkProduct.Tasks) {
			t.Fatal("retained submission changed accepted tasks")
		}
		if result.Outcome != "completed" && result.WorkProduct.Tasks != nil {
			t.Fatal("non-completion assembled an executable task graph")
		}
	}
	t.Logf("%d retained PM submissions accepted; signed schema and accepted tasks preserved", len(retained.Submissions))
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
