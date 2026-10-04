package nativeagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tekroo-ai/teams/application"
)

var planDeltaToolSchema = json.RawMessage(`{"type":"object","description":"Native plan result: supply only new dependency edges and handoffs. Teams carries the accepted architecture, decisions, assumptions, tasks, and source digest into the stored result.","additionalProperties":false,"required":["task_dependencies","handoffs"],"properties":{"task_dependencies":{"type":"array","description":"Additional task dependencies only; use [] when none are needed. Do not repeat existing dependencies.","maxItems":64,"items":{"type":"object","additionalProperties":false,"required":["task_index","depends_on"],"properties":{"task_index":{"type":"integer","minimum":0},"depends_on":{"type":"array","uniqueItems":true,"items":{"type":"integer","minimum":0}}}}},"handoffs":{"type":"array","description":"Cross-task capability handoffs; use [] when none are needed.","maxItems":4096,"items":{"type":"object","additionalProperties":false,"required":["provider_task_index","consumer_task_index","capability","contract"],"properties":{"provider_task_index":{"type":"integer","minimum":0},"consumer_task_index":{"type":"integer","minimum":0},"capability":{"type":"string","minLength":1},"contract":{"type":"string","minLength":1,"maxLength":4096}}}}}}`)

func modelFacingPlanSchema(handlerSchema json.RawMessage, plan *PlanFinalizationBinding) (json.RawMessage, error) {
	_, tasks, err := acceptedPlanWorkProduct(plan)
	if err != nil {
		return nil, err
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(handlerSchema, &schema) != nil {
		return nil, ErrInvalidBinding
	}
	var properties map[string]json.RawMessage
	if json.Unmarshal(schema["properties"], &properties) != nil || properties["work_product"] == nil {
		return nil, ErrInvalidBinding
	}
	var work map[string]json.RawMessage
	if json.Unmarshal(planDeltaToolSchema, &work) != nil {
		return nil, ErrInvalidBinding
	}
	var workProperties map[string]json.RawMessage
	if json.Unmarshal(work["properties"], &workProperties) != nil {
		return nil, ErrInvalidBinding
	}
	implementationTasks := 0
	for _, task := range tasks {
		var purpose string
		if raw := task["purpose"]; raw == nil || json.Unmarshal(raw, &purpose) != nil || purpose == "IMPLEMENTATION" {
			implementationTasks++
		}
	}
	if implementationTasks < 2 {
		var handoffs map[string]json.RawMessage
		if json.Unmarshal(workProperties["handoffs"], &handoffs) != nil {
			return nil, ErrInvalidBinding
		}
		handoffs["maxItems"] = json.RawMessage(`0`)
		handoffs["description"] = json.RawMessage(`"No cross-task handoff is possible with fewer than two implementation tasks; submit []."`)
		workProperties["handoffs"], _ = json.Marshal(handoffs)
	}
	work["properties"], _ = json.Marshal(workProperties)
	properties["work_product"], _ = json.Marshal(work)
	schema["properties"], _ = json.Marshal(properties)
	return json.Marshal(schema)
}

func acceptedPlanWorkProduct(plan *PlanFinalizationBinding) (map[string]json.RawMessage, []map[string]json.RawMessage, error) {
	marker := []byte(application.OrganizationalResultMarker)
	index := bytes.LastIndex(plan.SourceOutput, marker)
	if index < 0 || bytes.Count(plan.SourceOutput, marker) != 1 || index > 0 && plan.SourceOutput[index-1] != '\n' {
		return nil, nil, ErrInvalidBinding
	}
	var envelope struct {
		WorkProduct json.RawMessage `json:"work_product"`
	}
	if json.Unmarshal(bytes.TrimSpace(plan.SourceOutput[index+len(marker):]), &envelope) != nil || len(envelope.WorkProduct) == 0 {
		return nil, nil, ErrInvalidBinding
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(envelope.WorkProduct, &source) != nil || source == nil {
		return nil, nil, ErrInvalidBinding
	}
	var resultType string
	if json.Unmarshal(source["result_type"], &resultType) != nil || resultType != "FEATURE_PLAN" {
		return nil, nil, ErrInvalidBinding
	}
	var tasks []map[string]json.RawMessage
	if json.Unmarshal(source["tasks"], &tasks) != nil || len(tasks) == 0 {
		return nil, nil, ErrInvalidBinding
	}
	return source, tasks, nil
}

func bindHandlerPlanResult(arguments json.RawMessage, plan *PlanFinalizationBinding) (json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(arguments, &envelope) != nil || envelope == nil {
		return nil, ErrInvalidBinding
	}
	var outerOutcome string
	if json.Unmarshal(envelope["outcome"], &outerOutcome) != nil {
		return nil, ErrInvalidBinding
	}
	if outerOutcome != "completed" {
		return arguments, nil
	}
	source, tasks, err := acceptedPlanWorkProduct(plan)
	if err != nil {
		return nil, err
	}
	var delta struct {
		TaskDependencies []struct {
			TaskIndex uint32   `json:"task_index"`
			DependsOn []uint32 `json:"depends_on"`
		} `json:"task_dependencies"`
		Handoffs json.RawMessage `json:"handoffs"`
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope["work_product"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&delta); err != nil {
		return nil, fmt.Errorf("%w: submit_result.work_product: %v", ErrInvalidBinding, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF || delta.TaskDependencies == nil || delta.Handoffs == nil {
		return nil, ErrInvalidBinding
	}
	seen := make(map[uint32]bool, len(delta.TaskDependencies))
	for _, change := range delta.TaskDependencies {
		if int(change.TaskIndex) >= len(tasks) || seen[change.TaskIndex] || change.DependsOn == nil {
			return nil, ErrInvalidBinding
		}
		seen[change.TaskIndex] = true
		task := tasks[change.TaskIndex]
		var existing []uint32
		if json.Unmarshal(task["depends_on"], &existing) != nil {
			return nil, ErrInvalidBinding
		}
		for _, dependency := range change.DependsOn {
			if int(dependency) >= len(tasks) || dependency == change.TaskIndex {
				return nil, ErrInvalidBinding
			}
			for _, prior := range existing {
				if prior == dependency {
					return nil, ErrInvalidBinding
				}
			}
			existing = append(existing, dependency)
		}
		task["depends_on"], _ = json.Marshal(existing)
	}
	var handoffs []json.RawMessage
	if json.Unmarshal(delta.Handoffs, &handoffs) != nil || handoffs == nil {
		return nil, ErrInvalidBinding
	}
	source["schema_version"] = json.RawMessage(`"1.0.0"`)
	source["result_type"] = json.RawMessage(`"FEATURE_EXECUTION_PLAN"`)
	source["source_design_digest"], _ = json.Marshal(plan.SourceDigest)
	source["handoffs"] = delta.Handoffs
	source["tasks"], _ = json.Marshal(tasks)
	envelope["work_product"], _ = json.Marshal(source)
	return json.Marshal(envelope)
}
