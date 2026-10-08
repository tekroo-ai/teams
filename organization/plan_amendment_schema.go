package organization

import (
	"bytes"
	"encoding/json"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// A single schema source serves the runtime validator, native result catalog
// and generated contract artifact. Structured fields are never string-encoded.
func PlanAmendmentProposalSchema() json.RawMessage {
	type object = map[string]any
	text := func(maximum int) object { return object{"type": "string", "minLength": 1, "maxLength": maximum} }
	integer := func(minimum, maximum uint64) object {
		return object{"type": "integer", "minimum": minimum, "maximum": maximum}
	}
	closed := func(properties object, required []string) object {
		return object{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
	}
	array := func(item object, minimum, maximum int) object {
		var kind any = "array"
		// Historical Go plans may encode empty slices as null. Retained nodes
		// must remain byte-identical, so null is legal only for empty collections.
		if minimum == 0 {
			kind = []string{"array", "null"}
		}
		return object{"type": kind, "items": item, "minItems": minimum, "maxItems": maximum, "uniqueItems": true}
	}
	id := object{"type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"}
	digest := object{"type": "string", "pattern": "^[0-9a-f]{64}$"}
	actor := object{"type": "string", "pattern": "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$", "maxLength": 256}
	timestamp := object{"type": "string", "format": "date-time"}
	execution := closed(object{"execution_id": id, "fencing_epoch": integer(1, 1000000)}, []string{"execution_id", "fencing_epoch"})
	scope := object{"type": "string", "minLength": 1, "maxLength": 1024}
	story := closed(object{"id": id, "title": text(256), "description": text(65536), "acceptance_criteria": array(text(4096), 1, 32), "priority": object{"enum": []string{"LOW", "NORMAL", "HIGH", "CRITICAL"}}, "supersedes_story_id": id}, []string{"id", "title", "description", "acceptance_criteria", "priority"})
	task := closed(object{
		"id": id, "story_id": id, "title": text(256), "description": text(65536), "acceptance_criteria": array(text(4096), 1, 64),
		"depends_on": array(id, 0, 128), "validates": array(id, 0, 128), "write_scope": array(scope, 0, 32), "owner": actor,
		"model_profile_digest": digest, "decision_route": object{"enum": []string{"BOUNDED_EXECUTION", "COMPLEX_REASONING"}},
		"purpose":    object{"enum": []string{"INVESTIGATION", "IMPLEMENTATION", "VALIDATION", "REVIEW", "REPAIR", "HANDOFF", "REPLAN", "PROMOTION", "ESCALATION"}},
		"complexity": integer(1, 10), "risk": object{"enum": []string{"LOW", "MODERATE", "HIGH", "CRITICAL"}}, "critical_path": object{"type": "boolean"}, "attempt_limit": integer(1, 16), "review_round_limit": integer(1, 8),
	}, []string{"id", "story_id", "title", "description", "acceptance_criteria", "depends_on", "owner", "model_profile_digest", "decision_route", "purpose", "complexity", "risk", "critical_path", "attempt_limit", "review_round_limit"})
	handoff := closed(object{"provider_task_id": id, "consumer_task_id": id, "capability": object{"type": "string", "pattern": "^[a-z0-9](?:[a-z0-9.-]{0,126}[a-z0-9])?$"}, "contract": text(4096)}, []string{"provider_task_id", "consumer_task_id", "capability", "contract"})
	plan := closed(object{
		"version": integer(2, 1000000), "prepared_by": actor, "prepared_execution": execution, "architecture": text(65536), "design_decisions": array(text(4096), 0, 64), "assumptions": array(text(4096), 0, 64), "source_design_digest": digest,
		"stories": array(story, 1, 32), "tasks": array(task, 1, 128), "handoffs": array(handoff, 0, 16384), "created_at": timestamp,
	}, []string{"version", "prepared_by", "prepared_execution", "architecture", "design_decisions", "assumptions", "source_design_digest", "stories", "tasks", "created_at"})
	replacement := closed(object{"prior_task_id": id, "successor_task_id": id}, []string{"prior_task_id", "successor_task_id"})
	impact := closed(object{"kind": object{"enum": []string{"PRODUCTION_IMPLEMENTER", "WIRING", "CALLER", "TEST_DOUBLE"}}, "path": scope, "owner_task_id": id, "verification": text(4096)}, []string{"kind", "path", "owner_task_id", "verification"})
	properties := object{
		"schema_version": object{"const": PlanAmendmentContractVersion}, "feature_id": id, "feature_revision": integer(1, 1000000), "plan_version": integer(1, 1000000), "plan_digest": digest, "specification_digest": digest,
		"blocked_task_id": id, "blocked_task_revision": integer(1, 1000000), "blocked_invocation_id": id, "consultation_id": id, "answer_digest": digest, "reason": text(4096), "successor_plan": plan, "replacements": array(replacement, 1, 128), "impacts": array(impact, 1, 256),
	}
	schema := closed(properties, []string{"schema_version", "feature_id", "feature_revision", "plan_version", "plan_digest", "specification_digest", "blocked_task_id", "blocked_task_revision", "blocked_invocation_id", "consultation_id", "answer_digest", "reason", "successor_plan", "replacements", "impacts"})
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["$id"] = "https://tekroo.ai/contracts/plan-amendment/0.15.0/proposal"
	schema["title"] = "Single-use PM execution-plan amendment proposal"
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	} // Only literal schema values enter this builder.
	return raw
}

var amendmentSchemaOnce sync.Once
var amendmentCompiledSchema *jsonschema.Schema
var amendmentSchemaError error

func ValidatePlanAmendmentProposalJSON(raw []byte) error {
	amendmentSchemaOnce.Do(func() {
		var document any
		document, amendmentSchemaError = jsonschema.UnmarshalJSON(bytes.NewReader(PlanAmendmentProposalSchema()))
		if amendmentSchemaError != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.AssertFormat()
		const uri = "https://tekroo.ai/contracts/plan-amendment/0.15.0/proposal"
		amendmentSchemaError = compiler.AddResource(uri, document)
		if amendmentSchemaError == nil {
			amendmentCompiledSchema, amendmentSchemaError = compiler.Compile(uri)
		}
	})
	if amendmentSchemaError != nil {
		return amendmentSchemaError
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return amendmentCompiledSchema.Validate(instance)
}
