package nativeagent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/internal/rolefixtures"
	"github.com/tekroo-ai/teams/kernel"
)

func TestPlanFinalizationOptionalSourceDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		invalid     bool
	}{
		{name: "omitted"},
		{name: "empty", field: `,"depends_on":[]`},
		{name: "existing", field: `,"depends_on":[0]`},
		// Preserve legacy decoder behavior; signed source-schema validation
		// remains responsible for rejecting an explicit null architectural array.
		{name: "legacy-null", field: `,"depends_on":null`},
		{name: "string", field: `,"depends_on":"[]"`, invalid: true},
		{name: "object", field: `,"depends_on":{}`, invalid: true},
		{name: "scalar", field: `,"depends_on":0`, invalid: true},
		{name: "negative", field: `,"depends_on":[-1]`, invalid: true},
		{name: "fraction", field: `,"depends_on":[0.5]`, invalid: true},
		{name: "string-item", field: `,"depends_on":["0"]`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte(application.OrganizationalResultMarker + `
{"work_product":{"result_type":"FEATURE_PLAN","architecture":"preserved","tasks":[{"title":"provider"},{"title":"consumer"` + tc.field + `},{"title":"existing consumer","depends_on":[0]}]}}`)
			before := slices.Clone(source)
			plan := &PlanFinalizationBinding{SourceOutput: source}
			delta := json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":[{"task_index":1,"depends_on":[0]}],"handoffs":[]}}`)
			bound, err := bindHandlerPlanResult(delta, plan)
			if tc.invalid {
				if !errors.Is(err, ErrInvalidBinding) {
					t.Fatalf("malformed source accepted: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					Work struct {
						Tasks []struct {
							DependsOn []uint32 `json:"depends_on"`
						} `json:"tasks"`
					} `json:"work_product"`
				}
				if json.Unmarshal(bound, &got) != nil || fmt.Sprint(got.Work.Tasks[1].DependsOn) != "[0]" || fmt.Sprint(got.Work.Tasks[2].DependsOn) != "[0]" {
					t.Fatalf("edge missing or duplicated: %s", bound)
				}
			}
			if !bytes.Equal(plan.SourceOutput, before) {
				t.Fatal("accepted source mutated")
			}
		})
	}
}

func TestPlanFinalizationMissingSourceDoesNotDefaultInvalidDelta(t *testing.T) {
	plan := &PlanFinalizationBinding{SourceOutput: []byte(application.OrganizationalResultMarker + `
{"work_product":{"result_type":"FEATURE_PLAN","tasks":[{},{}]}}`)}
	for _, dependency := range []string{`{}`, `{"depends_on":null}`, `{"depends_on":1}`, `{"depends_on":[1]}`, `{"depends_on":[2]}`, `{"depends_on":[0,0]}`} {
		delta := json.RawMessage(`{"outcome":"completed","work_product":{"task_dependencies":[{"task_index":1` + func() string {
			if dependency == `{}` {
				return ""
			}
			return "," + dependency[1:len(dependency)-1]
		}() + `}],"handoffs":[]}}`)
		if _, err := bindHandlerPlanResult(delta, plan); !errors.Is(err, ErrInvalidPlanDelta) {
			t.Fatalf("bad model delta accepted or misclassified: %s: %v", delta, err)
		}
	}
}

func TestSignedArchitectureOptionalDependencyContract(t *testing.T) {
	handler := rolefixtures.Handler(t, rolefixtures.Current(t), "architect", "tekroo.message.story.design-requested")
	for _, tc := range []struct {
		field string
		valid bool
	}{{"", true}, {`,"depends_on":[]`, true}, {`,"depends_on":null`, false}, {`,"depends_on":"[]"`, false}} {
		output := []byte(application.OrganizationalResultMarker + `
{"schema_version":"1.0.0","outcome":"completed","summary":"Bounded design","evidence":[],"message_proposals":[],"work_product":{"schema_version":"1.0.0","result_type":"FEATURE_PLAN","architecture":"Preserve unrelated behavior","tasks":[{"story_index":0,"title":"Provider","description":"Extend the existing provider and its tests","acceptance_criteria":["Provider tests pass"],"purpose":"IMPLEMENTATION","complexity":1,"risk":"LOW","attempt_limit":2,"review_round_limit":1` + tc.field + `}]}}`)
		_, err := application.ValidateRoleHandlerResult(handler, output)
		if (err == nil) != tc.valid {
			t.Fatalf("signed optional-field contract changed for %s: %v", tc.field, err)
		}
	}
}

func TestPlanFinalizationOmittedDependenciesWithEmptyDelta(t *testing.T) {
	for _, fixture := range signedPlanFixtures(t) {
		t.Run(fixture.name, func(t *testing.T) {
			handler := fixture.handler
			source := []byte(application.OrganizationalResultMarker + `
{"work_product":{"schema_version":"1.0.0","result_type":"FEATURE_PLAN","architecture":"Preserve existing behavior","tasks":[{"story_index":0,"title":"Provider","description":"Extend the existing provider and its tests","acceptance_criteria":["Provider tests pass"],"purpose":"IMPLEMENTATION","complexity":1,"risk":"LOW","attempt_limit":2,"review_round_limit":1}]}}`)
			digest := sha256.Sum256(source)
			plan := &PlanFinalizationBinding{SourceOutput: source, SourceDigest: kernel.Digest(fmt.Sprintf("%x", digest[:]))}
			delta := json.RawMessage(`{"outcome":"completed","summary":"Ready","evidence":[],"message_proposals":[],"work_product":{"task_dependencies":[{"task_index":0,"depends_on":[]}],"handoffs":[]}}`)
			assembled, err := bindHandlerPlanResult(bindHandlerResultVersion(delta, handler), plan)
			if err != nil {
				t.Fatal(err)
			}
			if !schemaAccepts(t, handler.ResultSchema, assembled) {
				t.Fatalf("empty confirmation must remain a signed-schema-valid array: %s", assembled)
			}
			_, tasks, err := acceptedPlanWorkProduct(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, present := tasks[0]["depends_on"]; present {
				t.Fatal("source was normalized in place")
			}
		})
	}
}
