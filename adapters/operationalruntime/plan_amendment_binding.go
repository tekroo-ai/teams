package operationalruntime

import (
	"encoding/json"

	"github.com/tekroo-ai/teams/adapters/nativeagent"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/organization"
)

const amendmentAssessmentGuidance = `Read the admitted amendment input record, accepted PM plan and architect answer. Propose the smallest execution ownership/dependency/scope repair within the unchanged product specification and technical design. The architect answer is evidence, not permission to override the frozen amendment policy.
The supplied schema pins harness-owned identities. Top-level plan_version and plan_digest identify the accepted SOURCE plan; successor_plan.version is the next version. Use the exact const values, including the reservation timestamp and current prepared_execution.
Preserve architecture, stories, design decisions, assumptions and source_design_digest exactly. Preserve every retained task definition byte-for-byte, including descriptions, dependencies and validation targets. COMPLETED or CLOSED tasks cannot be replaced or rewritten, even under a remaining repair allowance. Only the blocked task and its eligible, unexecuted downstream closure can be replaced; use fresh IDs and explicitly enumerate replacements. If a dependency changes, replace the affected unexecuted dependent rather than rewriting its retained ID. The successor must itself be a closed DAG: no dependency, validates, required_validation_tasks or handoff may refer to a removed node. The replacements array does not perform implicit graph remapping.
Use remaining_task_allowances from input, not the original task limits. Retired plus successor tasks count toward maximum_tasks. Preserve existing handoff contracts with only mapped endpoint IDs; an architect answer does not authorize changing retained contracts. Each impact must belong to a fresh successor task and be covered by its write_scope; every fresh write_scope path needs exact impact coverage. Do not attach impacts to retained tasks or claim completed work is preserved while replacing it.
Do not implement, edit the repository, broaden product behavior, ask another agent, or approve your own proposal. Return a raw structured amendment proposal under the supplied JSON Schema. Do not invent a compliant-looking proposal if the repair cannot satisfy these bounds: a rejection or failure is terminal and requires the operator; there is no second assessment.`

// Specialize the request, not the frozen contract. Identity is supplied by the
// harness; the model still owns the proposal and the independent policy decides
// whether its task graph is admissible. Stable reservation time permits exact
// reconstruction after restart without inventing a model-generated timestamp.
func boundPlanAmendmentSchema(feature organization.FeatureRequest, brief application.ExecutionBrief) (json.RawMessage, error) {
	a := feature.PlanAmendment
	if a == nil || a.AssessmentID != brief.Task.TaskID || a.PriorPlan.PreparedBy != brief.ActorFQN ||
		!brief.Execution.Valid() || a.RequestedAt.IsZero() {
		return nil, nativeagent.ErrInvalidBinding
	}
	var schema map[string]any
	if err := json.Unmarshal(organization.PlanAmendmentProposalSchema(), &schema); err != nil {
		return nil, err
	}
	properties := schema["properties"].(map[string]any)
	for name, value := range map[string]any{
		"feature_id": feature.ID, "feature_revision": a.FeatureRevision,
		"plan_version": a.PriorPlan.Version, "plan_digest": a.PriorDigest,
		"specification_digest": a.SpecificationDigest, "blocked_task_id": a.BlockedTaskID,
		"blocked_task_revision": a.BlockedTaskRevision, "blocked_invocation_id": a.BlockedInvocationID,
		"consultation_id": a.ConsultationID, "answer_digest": a.AnswerDigest,
	} {
		properties[name].(map[string]any)["const"] = value
	}
	properties["plan_version"].(map[string]any)["description"] = "The accepted SOURCE plan version, not the successor version. Harness-owned; copy the exact const."
	properties["plan_digest"].(map[string]any)["description"] = "The accepted SOURCE plan digest from the reservation. Never compute or invent a successor digest here."
	next := properties["successor_plan"].(map[string]any)["properties"].(map[string]any)
	next["version"].(map[string]any)["const"] = a.PriorPlan.Version + 1
	next["prepared_by"].(map[string]any)["const"] = brief.ActorFQN
	next["created_at"].(map[string]any)["const"] = a.RequestedAt
	next["created_at"].(map[string]any)["description"] = "Harness-owned reservation timestamp; do not invent a wall-clock time."
	execution := next["prepared_execution"].(map[string]any)["properties"].(map[string]any)
	execution["execution_id"].(map[string]any)["const"] = brief.Execution.ExecutionID
	execution["fencing_epoch"].(map[string]any)["const"] = brief.Execution.FencingEpoch
	return json.Marshal(schema)
}
