package nativeagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
)

func TestConstructionContractRequiresAccessibleInputsAndConsumerUsage(t *testing.T) {
	valid := json.RawMessage(`{"provided_interface":"OpenAdapter(Config) (Adapter, error)","required_inputs":[{"input":"Config","source":"Public configuration already supplied to the consumer"}],"consumer_usage":"Call OpenAdapter from the consumer package without provider-file edits","verification":"Provider's external-package test constructs and invokes the real adapter with valid configuration"}`)
	if !schemaAccepts(t, constructionContractSchema, valid) {
		t.Fatal("valid construction contract rejected")
	}
	text, err := decodeConstructionContract(valid)
	if err != nil || !strings.Contains(text, "Required inputs:") || !strings.Contains(text, "external-package test") || strings.HasPrefix(text, "{") {
		t.Fatalf("lost usable boundary: %q %v", text, err)
	}
	for _, invalid := range []string{
		`"NewAdapter(db) exists"`,
		`{"provided_interface":"NewAdapter(db)","required_inputs":[{"input":"db"}],"consumer_usage":"construct","verification":"test"}`,
		`{"provided_interface":"NewAdapter(db)","required_inputs":null,"consumer_usage":"construct","verification":"test"}`,
		`{"provided_interface":"NewAdapter(db)","required_inputs":[],"consumer_usage":"","verification":"test"}`,
		`{"provided_interface":"NewAdapter(db)","required_inputs":[],"consumer_usage":"construct","verification":"test","extra":1}`,
	} {
		if schemaAccepts(t, constructionContractSchema, []byte(invalid)) {
			t.Fatalf("schema accepted %s", invalid)
		}
		if _, err := decodeConstructionContract([]byte(invalid)); err == nil {
			t.Fatalf("local validator accepted %s", invalid)
		}
	}
}

func TestPlanModelContractIsStructuredAndAcceptedStorageRemainsNarrative(t *testing.T) {
	plan := &PlanFinalizationBinding{SourceOutput: []byte(application.OrganizationalResultMarker + `{"work_product":{"result_type":"FEATURE_PLAN","architecture":"accepted","tasks":[{"title":"provider","depends_on":[]},{"title":"consumer","depends_on":[]}]}}`)}
	schema, err := modelFacingPlanSchema(json.RawMessage(`{"type":"object","properties":{"outcome":{"type":"string"},"work_product":{"type":"object"}}}`), plan)
	if err != nil {
		t.Fatal(err)
	}
	valid := json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":[{"task_index":1,"depends_on":[0]}],"handoffs":[{"provider_task_index":0,"consumer_task_index":1,"capability":"storage","contract":{"provided_interface":"Open(Config)","required_inputs":[{"input":"Config","source":"public runtime configuration"}],"consumer_usage":"Open and invoke the returned adapter","verification":"external consumer-path test"}}]}}`)
	if !schemaAccepts(t, schema, valid) {
		t.Fatalf("structured delta rejected: %s", schema)
	}
	bound, err := bindHandlerPlanResult(valid, plan)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Work struct {
			Handoffs []struct {
				Contract string `json:"contract"`
			} `json:"handoffs"`
		} `json:"work_product"`
	}
	if json.Unmarshal(bound, &result) != nil || len(result.Work.Handoffs) != 1 || !strings.Contains(result.Work.Handoffs[0].Contract, "public runtime configuration") {
		t.Fatalf("accepted representation changed: %s", bound)
	}
	wrong := json.RawMessage(strings.Replace(string(valid), `{"provided_interface":"Open(Config)","required_inputs":[{"input":"Config","source":"public runtime configuration"}],"consumer_usage":"Open and invoke the returned adapter","verification":"external consumer-path test"}`, `"Open(Config) exists"`, 1))
	if schemaAccepts(t, schema, wrong) {
		t.Fatal("model contract still accepts an opaque string")
	}
}
