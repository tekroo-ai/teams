#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const source = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.10.0");
const target = path.join(root, "CONTRACTS/tekroo.kernel.contracts/0.11.0");
const oldIdentity = "tekroo.kernel.contracts/0.10.0";
const newIdentity = "tekroo.kernel.contracts/0.11.0";

const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const sortValue = (value) => Array.isArray(value)
  ? value.map(sortValue)
  : value && typeof value === "object"
    ? Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])]))
    : value;
const canonical = (value) => JSON.stringify(sortValue(value));
const uuid = { type: "string", pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$" };
const digest = { type: "string", pattern: "^[0-9a-f]{64}$" };
const fullDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const actor = { type: "string", pattern: "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$" };
const fqrn = { type: "string", pattern: "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])$" };
const stageID = { type: "string", pattern: "^[a-z0-9](?:[a-z0-9.-]{0,126}[a-z0-9])?$" };
const text = (maximum = 4096) => ({ type: "string", minLength: 1, maxLength: maximum });

function writeJson(relative, value) {
  const absolute = path.join(target, relative);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, `${JSON.stringify(sortValue(value), null, 2)}\n`);
}

function readJson(relative) {
  return JSON.parse(fs.readFileSync(path.join(target, relative), "utf8"));
}

function walk(directory, prefix = "") {
  const values = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) values.push(...walk(absolute, relative));
    else values.push(relative);
  }
  return values.sort();
}

const workflowDefinitionSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `${newIdentity}/schemas/workflow-definition.schema.json`,
  title: "Tekroo versioned workflow definition",
  type: "object",
  additionalProperties: false,
  required: ["schema_version", "name", "version", "content_digest", "trigger_types", "stages", "root_budgets", "projection_rules"],
  properties: {
    schema_version: { const: "1.0.0" },
    name: stageID,
    version: { type: "string", pattern: "^[0-9]+\\.[0-9]+\\.[0-9]+$" },
    content_digest: digest,
    trigger_types: { type: "array", minItems: 1, maxItems: 64, uniqueItems: true, items: text(256) },
    stages: {
      type: "array", minItems: 1, maxItems: 256,
      items: {
        type: "object", additionalProperties: false,
        required: ["stage_id", "depends_on", "input_schema", "output_schema", "required_capabilities", "purpose", "risk", "concurrency_group", "maximum_parallelism", "attempt_limit", "allowed_outgoing_purposes", "target_selection", "validation_policy"],
        properties: {
          stage_id: stageID,
          depends_on: { type: "array", maxItems: 255, uniqueItems: true, items: stageID },
          input_schema: text(1024), output_schema: text(1024),
          required_capabilities: { type: "array", minItems: 1, maxItems: 64, uniqueItems: true, items: stageID },
          preferred_fqrns: { type: "array", maxItems: 32, uniqueItems: true, items: fqrn },
          purpose: { enum: ["HANDOFF", "IMPLEMENTATION", "VALIDATION", "REVIEW", "REPAIR", "REPLAN", "PROMOTION"] },
          risk: { enum: ["LOW", "MODERATE", "HIGH", "CRITICAL"] },
          complexity_minimum: { type: "integer", minimum: 1, maximum: 5 },
          complexity_maximum: { type: "integer", minimum: 1, maximum: 5 },
          concurrency_group: stageID,
          maximum_parallelism: { type: "integer", minimum: 1, maximum: 1024 },
          attempt_limit: { type: "integer", minimum: 1, maximum: 32 },
          review_round_limit: { type: "integer", minimum: 0, maximum: 16 },
          timeout_seconds: { type: "integer", minimum: 1, maximum: 604800 },
          token_budget: { type: "integer", minimum: 1 },
          allowed_outgoing_purposes: { type: "array", maxItems: 5, uniqueItems: true, items: { enum: ["REQUEST", "HANDOFF", "EVIDENCE", "RESPONSE", "NOTIFICATION"] } },
          target_selection: { enum: ["EXACT_ACTOR", "CAPABILITY", "OPERATOR", "NONE"] },
          validation_policy: { enum: ["DETERMINISTIC_ONLY", "RISK_SELECTED", "SPECIALIZED", "OPERATOR_APPROVED"] },
          completion_condition: text(4096), failure_condition: text(4096)
        }
      }
    },
    root_budgets: {
      type: "object", additionalProperties: false,
      required: ["maximum_model_invocations", "maximum_hops", "maximum_attempts"],
      properties: {
        maximum_model_invocations: { type: "integer", minimum: 0 },
        maximum_hops: { type: "integer", minimum: 1 },
        maximum_attempts: { type: "integer", minimum: 1 },
        maximum_tokens: { type: "integer", minimum: 0 },
        maximum_elapsed_seconds: { type: "integer", minimum: 1 }
      }
    },
    projection_rules: { type: "array", maxItems: 64, uniqueItems: true, items: { enum: ["FEATURE", "STORY", "TASK", "MESSAGE", "STATUS"] } }
  }
};

const workflowInstanceSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `${newIdentity}/schemas/workflow-instance.schema.json`,
  title: "Tekroo durable workflow instance",
  type: "object", additionalProperties: false,
  required: ["schema_version", "instance_id", "revision", "definition_name", "definition_version", "definition_digest", "root_request", "budget_account_id", "state", "nodes"],
  properties: {
    schema_version: { const: "1.0.0" }, instance_id: uuid, revision: { type: "integer", minimum: 1 },
    definition_name: stageID, definition_version: { type: "string", pattern: "^[0-9]+\\.[0-9]+\\.[0-9]+$" }, definition_digest: digest,
    root_request: { $ref: "core.schema.json#/$defs/AggregateRef" }, budget_account_id: uuid,
    state: { enum: ["PENDING", "ACTIVE", "BLOCKED", "COMPLETED", "FAILED", "CANCELLED"] },
    nodes: {
      type: "array", maxItems: 4096,
      items: {
        type: "object", additionalProperties: false,
        required: ["node_id", "stage_id", "predecessor_node_ids", "state", "attempt", "input_evidence_ids", "output_evidence_ids", "failure_classification"],
        properties: {
          node_id: uuid, stage_id: stageID,
          predecessor_node_ids: { type: "array", maxItems: 255, uniqueItems: true, items: uuid },
          state: { enum: ["PENDING", "READY", "ADMITTED", "RUNNING", "BLOCKED", "COMPLETED", "FAILED", "CANCELLED"] },
          attempt: { type: "integer", minimum: 0 }, actor_fqn: actor, invocation_id: uuid,
          input_evidence_ids: { type: "array", maxItems: 1024, uniqueItems: true, items: uuid },
          output_evidence_ids: { type: "array", maxItems: 1024, uniqueItems: true, items: uuid },
          progress_digest: digest, checkpoint_id: uuid,
          failure_classification: { enum: ["NONE", "RECOVERABLE_TRANSPORT", "CORRECTABLE_WORK", "TERMINAL_POLICY", "CANCELLED"] },
          continuation_node_id: uuid,
          blocked_from: { enum: ["READY", "ADMITTED", "RUNNING"] }
        }
      }
    },
    proposed_message_ids: { type: "array", maxItems: 4096, uniqueItems: true, items: uuid },
    accepted_message_ids: { type: "array", maxItems: 4096, uniqueItems: true, items: uuid },
    last_event_id: uuid
  }
};

const workProposalSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `${newIdentity}/schemas/work-proposal.schema.json`,
  title: "Tekroo message-to-work proposal",
  type: "object", additionalProperties: false,
  required: ["schema_version", "proposal_id", "message_id", "workflow_instance_id", "stage_id", "node_id", "actor_fqn", "execution", "budget_account_id", "causation_event_ids", "input_evidence_ids", "proposed_at"],
  properties: {
    schema_version: { const: "1.0.0" }, proposal_id: uuid, message_id: uuid, workflow_instance_id: uuid,
    stage_id: stageID, node_id: uuid, actor_fqn: actor,
    execution: { $ref: "core.schema.json#/$defs/ExecutionTuple" }, budget_account_id: uuid,
    causation_event_ids: { type: "array", minItems: 1, maxItems: 64, uniqueItems: true, items: uuid },
    input_evidence_ids: { type: "array", maxItems: 1024, uniqueItems: true, items: uuid },
    proposed_at: { type: "string", format: "date-time" }
  }
};

const admissionResultSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `${newIdentity}/schemas/admission-result.schema.json`,
  title: "Tekroo deterministic work-admission result",
  type: "object", additionalProperties: false,
  required: ["schema_version", "proposal_id", "workflow_instance_id", "node_id", "outcome", "reason_code", "recorded_event_id", "recorded_at"],
  properties: {
    schema_version: { const: "1.0.0" }, proposal_id: uuid, workflow_instance_id: uuid, node_id: uuid,
    outcome: { enum: ["ADMITTED", "REJECTED"] },
    reason_code: { enum: ["ADMITTED", "UNDECLARED_TRANSITION", "PREDECESSOR_INCOMPLETE", "DUPLICATE_NODE", "CAUSATION_INVALID", "ACTOR_INELIGIBLE", "BUDGET_EXHAUSTED", "NO_PROGRESS", "WORKFLOW_TERMINAL"] },
    recorded_event_id: uuid, authorized_invocation_id: uuid, recorded_at: { type: "string", format: "date-time" }
  },
  allOf: [
    { if: { properties: { outcome: { const: "ADMITTED" } } }, then: { required: ["authorized_invocation_id"], properties: { reason_code: { const: "ADMITTED" } } } },
    { if: { properties: { outcome: { const: "REJECTED" } } }, then: { not: { required: ["authorized_invocation_id"] } } }
  ]
};

const workflowFixtures = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  fixtures: [
    {
      fixtureId: "P10-WF-001", kind: "WORKFLOW_MODEL", classification: "NORMATIVE_EXAMPLE", model: "PARALLEL_READY",
      given: { nodes: [{ id: "design", dependsOn: [] }, { id: "code-a", dependsOn: ["design"] }, { id: "code-b", dependsOn: ["design"] }, { id: "code-c", dependsOn: ["design"] }, { id: "code-d", dependsOn: ["design"] }, { id: "join", dependsOn: ["code-a", "code-b", "code-c", "code-d"] }], completedNodeIds: ["design"], capacity: 4 },
      when: { action: "ADMIT_READY" }, then: { expected: { readyNodeIds: ["code-a", "code-b", "code-c", "code-d"], admittedNodeIds: ["code-a", "code-b", "code-c", "code-d"] } }
    },
    {
      fixtureId: "P10-WF-002", kind: "WORKFLOW_MODEL", classification: "NORMATIVE_EXAMPLE", model: "BOUNDED_REPAIR",
      given: { completedNodeIds: ["design", "code-a"], failedNodeId: "validation", attempt: 1, attemptLimit: 2, budgetAccountId: "root-budget" },
      when: { action: "CREATE_REPAIR_SUCCESSOR", changedConditionEvidenceIds: ["failure-receipt"], requestedBudgetAccountId: "root-budget" },
      then: { expected: { admitted: true, nextAttempt: 2, completedNodeIds: ["design", "code-a"], budgetPreserved: true } }
    },
    {
      fixtureId: "P10-WF-003", kind: "WORKFLOW_MODEL", classification: "NORMATIVE_EXAMPLE", model: "ROLE_REUSE",
      given: { nodes: [{ id: "architecture", role: "architect", dependsOn: [] }, { id: "implementation", role: "coder", dependsOn: ["architecture"] }, { id: "architecture-review", role: "architect", dependsOn: ["implementation"] }] },
      when: { action: "VALIDATE_TOPOLOGY" }, then: { expected: { valid: true, reason: null, distinctNodeCount: 3, reusedRoles: ["architect"] } }
    },
    {
      fixtureId: "P10-WF-004", kind: "WORKFLOW_MODEL", classification: "BOUNDARY_NEGATIVE", model: "NODE_CYCLE",
      given: { nodes: [{ id: "a", dependsOn: ["c"] }, { id: "b", dependsOn: ["a"] }, { id: "c", dependsOn: ["b"] }] },
      when: { action: "VALIDATE_TOPOLOGY" }, then: { expected: { valid: false, reason: "CYCLE" } }
    },
    {
      fixtureId: "P10-WF-005", kind: "WORKFLOW_MODEL", classification: "BOUNDARY_NEGATIVE", model: "MESSAGE_ADMISSION",
      given: { invocationCount: 7 },
      when: { messages: [{ purpose: "EVIDENCE", admission: "NOT_APPLICABLE" }, { purpose: "REQUEST", admission: "REJECTED" }] },
      then: { expected: { invocationCount: 7, createdInvocationCount: 0 } }
    },
    {
      fixtureId: "P10-WF-006", kind: "WORKFLOW_MODEL", classification: "NORMATIVE_EXAMPLE", model: "CHECKPOINT_RESUME",
      given: { completedNodeIds: ["intake", "design", "code-a"], frontierNodeIds: ["code-b", "code-c"], stateDigest: fullDigest },
      when: { action: "RESUME" }, then: { expected: { resumeNodeIds: ["code-b", "code-c"], replayNodeIds: [], preservedCompletedNodeIds: ["intake", "design", "code-a"] } }
    },
    {
      fixtureId: "P10-WF-007", kind: "WORKFLOW_MODEL", classification: "NORMATIVE_EXAMPLE", model: "CONFIG_GENERALITY",
      given: { workflowDomain: "content-publication", configuredRoles: ["editor", "fact-checker", "publisher"], genericRuntimeRoleNames: [] },
      when: { action: "LOAD" }, then: { expected: { loaded: true, sourceChangeRequired: false } }
    },
    {
      fixtureId: "P10-WF-008", kind: "WORKFLOW_MODEL", classification: "BOUNDARY_NEGATIVE", model: "VALIDATION_CEILING",
      given: { designSeconds: 120, implementationSeconds: 240, validationSeconds: 330, requestedAdditionalSeconds: 60, priorEvidenceDigest: fullDigest, currentInputDigest: fullDigest },
      when: { action: "ADMIT_VALIDATION", changedConditionEvidenceIds: [] },
      then: { expected: { ceilingSeconds: 360, reusePriorEvidence: true, additionalValidationAdmitted: false } }
    }
  ]
};

const workflowInvariants = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  invariants: [
    { invariantId: "P10-INV-001", statement: "Workflow definitions are content-addressed, versioned, acyclic, and contain no runtime-owned role semantics.", testIds: ["P10-CAT-001-P", "P10-CAT-001-N", "P10-WF-007"] },
    { invariantId: "P10-INV-002", statement: "A message proposes work but only deterministic admission can authorize one exact workflow node and invocation.", testIds: ["P10-CAT-002-P", "P10-CAT-002-N", "P10-WF-005"] },
    { invariantId: "P10-INV-003", statement: "Node identity and causal topology prevent cycles independently of role, actor, message type, thread, or recipient.", testIds: ["P10-WF-003", "P10-WF-004"] },
    { invariantId: "P10-INV-004", statement: "Repair and reconsideration use bounded successor nodes with changed-condition evidence and cannot reset the root budget.", testIds: ["P10-WF-002", "P10-CAT-002-N"] },
    { invariantId: "P10-INV-005", statement: "Stage results advance only the exact admitted node and preserve content-addressed evidence.", testIds: ["P10-CAT-003-P", "P10-CAT-003-N"] },
    { invariantId: "P10-INV-006", statement: "Durable checkpoints resume the failed or interrupted stage without repeating successful predecessors.", testIds: ["P10-CAT-004-P", "P10-CAT-004-N", "P10-WF-006"] },
    { invariantId: "P10-INV-007", statement: "All dependency-ready nodes may run concurrently up to declared capacity; index or role order cannot add dependencies.", testIds: ["P10-WF-001"] },
    { invariantId: "P10-INV-008", statement: "Validation reuses unchanged passing evidence and cannot exceed design-plus-implementation time without changed-condition evidence or explicit escalation.", testIds: ["P10-WF-008"] }
  ]
};
const workflowTraceability = {
  schemaVersion: "1.0.0", contractIdentity: newIdentity,
  requirements: workflowInvariants.invariants.map((item) => ({
    requirementId: item.invariantId.replace("INV", "REQ"),
    kind: item.invariantId === "P10-INV-008" ? "NEGATIVE_REQUIREMENT" : "DECISION",
    statement: item.statement,
    testIds: item.testIds
  }))
};

fs.rmSync(target, { recursive: true, force: true });
fs.cpSync(source, target, { recursive: true });
for (const relative of walk(target)) {
  if (["manifest.json", "manifest.sha256"].includes(relative)) continue;
  const absolute = path.join(target, relative);
  const raw = fs.readFileSync(absolute, "utf8");
  fs.writeFileSync(absolute, raw.split(oldIdentity).join(newIdentity));
}
fs.rmSync(path.join(target, "manifest.json"), { force: true });
fs.rmSync(path.join(target, "manifest.sha256"), { force: true });

writeJson("schemas/workflow-definition.schema.json", workflowDefinitionSchema);
writeJson("schemas/workflow-instance.schema.json", workflowInstanceSchema);
writeJson("schemas/work-proposal.schema.json", workProposalSchema);
writeJson("schemas/admission-result.schema.json", admissionResultSchema);
writeJson("fixtures/workflow-runtime.json", workflowFixtures);
writeJson("invariants/workflow-invariants.json", workflowInvariants);
writeJson("traceability/workflow-traceability.json", workflowTraceability);
writeJson("compatibility/from-0.10.0.json", {
  schemaVersion: "1.0.0", contractName: "tekroo.kernel.contracts", contractVersion: "0.11.0",
  predecessor: { contractName: "tekroo.kernel.contracts", contractVersion: "0.10.0", manifestSha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
  compatibility: "ADDITIVE",
  directions: { reader: "COMPATIBLE", writer: "SUCCESSOR_IDENTITY_REQUIRED", adapter: "WORKFLOW_COMPATIBILITY_ADAPTER_REQUIRED" },
  additions: ["versioned workflow definitions", "durable workflow instances", "message-to-work proposals", "deterministic admission results", "node-based loop prevention", "risk-selected validation and evidence reuse"],
  preserved: ["all 0.10.0 kernel, organization, federation, task, story, feature, budget, execution, evidence, human, operator, OpenHands, and SMA boundaries"]
});

const core = readJson("schemas/core.schema.json");
const aggregateKinds = core.$defs.AggregateRef.properties.kind.enum;
if (!aggregateKinds.includes("workflow-instance")) aggregateKinds.push("workflow-instance");
aggregateKinds.sort();
writeJson("schemas/core.schema.json", core);

const payloads = readJson("schemas/payloads.schema.json");
payloads.$defs.tekroo_command_workflow_create_1_0_0 = { type: "object", additionalProperties: false, required: ["definition", "root_request", "budget_account_id"], properties: { definition: { type: "object" }, root_request: { type: "object" }, budget_account_id: uuid } };
payloads.$defs.tekroo_command_workflow_propose_work_1_0_0 = { type: "object", additionalProperties: false, required: ["proposal"], properties: { proposal: { type: "object" } } };
payloads.$defs.tekroo_command_workflow_record_stage_result_1_0_0 = { type: "object", additionalProperties: false, required: ["node_id", "invocation_id", "outcome", "output_evidence_ids", "progress_digest"], properties: { node_id: uuid, invocation_id: uuid, outcome: { type: "string", enum: ["COMPLETED", "FAILED", "CANCELLED"] }, output_evidence_ids: { type: "array", uniqueItems: true, items: uuid }, progress_digest: digest, failure_classification: text(128) } };
payloads.$defs.tekroo_command_workflow_record_checkpoint_1_0_0 = { type: "object", additionalProperties: false, required: ["checkpoint_id", "completed_node_ids", "frontier_node_ids", "state_digest"], properties: { checkpoint_id: uuid, completed_node_ids: { type: "array", uniqueItems: true, items: uuid }, frontier_node_ids: { type: "array", uniqueItems: true, items: uuid }, state_digest: digest } };
payloads.$defs.tekroo_event_workflow_created_1_0_0 = {
  type: "object", additionalProperties: false,
  required: ["instance_id", "definition_name", "definition_version", "definition_digest", "root_request", "budget_account_id", "nodes"],
  properties: {
    instance_id: uuid, definition_name: stageID, definition_version: { type: "string", pattern: "^[0-9]+\\.[0-9]+\\.[0-9]+$" }, definition_digest: digest,
    root_request: { type: "object", additionalProperties: false, required: ["kind", "id"], properties: { kind: { type: "string", minLength: 1, maxLength: 128 }, id: uuid } },
    budget_account_id: uuid,
    nodes: {
      type: "array", minItems: 1, maxItems: 256,
      items: { type: "object", additionalProperties: false, required: ["node_id", "stage_id", "predecessor_node_ids"], properties: { node_id: uuid, stage_id: stageID, predecessor_node_ids: { type: "array", maxItems: 255, uniqueItems: true, items: uuid } } }
    }
  }
};
payloads.$defs.tekroo_event_workflow_work_admitted_1_0_0 = { $ref: "admission-result.schema.json" };
payloads.$defs.tekroo_event_workflow_work_rejected_1_0_0 = { $ref: "admission-result.schema.json" };
payloads.$defs.tekroo_event_workflow_stage_result_recorded_1_0_0 = payloads.$defs.tekroo_command_workflow_record_stage_result_1_0_0;
payloads.$defs.tekroo_event_workflow_checkpoint_recorded_1_0_0 = payloads.$defs.tekroo_command_workflow_record_checkpoint_1_0_0;
writeJson("schemas/payloads.schema.json", payloads);

const coverage = readJson("fixtures/catalogue-coverage.json");
const id = (suffix) => `00000000-0000-7000-8000-${suffix}`;
const catalogueFixture = (fixtureId, classification, commandType, payload, outcomeCode, eventTypes = []) => ({
  fixtureId, kind: "CATALOGUE_COMMAND", classification,
  given: { contractManifest: newIdentity }, when: { commandType, payload },
  then: { expected: { outcomeCode, eventTypes } }, sourceDecisionIds: ["P10-INV-001", "P10-INV-002"]
});
coverage.fixtures.push(
  catalogueFixture("P10-CAT-001-P", "NORMATIVE_EXAMPLE", "tekroo.command.workflow.create", { definition: {}, root_request: {}, budget_account_id: id("000000000001") }, "APPLIED", ["tekroo.event.workflow.created"]),
  catalogueFixture("P10-CAT-001-N", "BOUNDARY_NEGATIVE", "tekroo.command.workflow.create", {}, "REJECTED_INVALID"),
  catalogueFixture("P10-CAT-002-P", "NORMATIVE_EXAMPLE", "tekroo.command.workflow.propose-work", { proposal: {} }, "APPLIED", ["tekroo.event.workflow.work-admitted", "tekroo.event.workflow.work-rejected"]),
  catalogueFixture("P10-CAT-002-N", "BOUNDARY_NEGATIVE", "tekroo.command.workflow.propose-work", {}, "REJECTED_INVALID"),
  catalogueFixture("P10-CAT-003-P", "NORMATIVE_EXAMPLE", "tekroo.command.workflow.record-stage-result", { node_id: id("000000000002"), invocation_id: id("000000000003"), outcome: "COMPLETED", output_evidence_ids: [id("000000000004")], progress_digest: fullDigest }, "APPLIED", ["tekroo.event.workflow.stage-result-recorded"]),
  catalogueFixture("P10-CAT-003-N", "BOUNDARY_NEGATIVE", "tekroo.command.workflow.record-stage-result", {}, "REJECTED_INVALID"),
  catalogueFixture("P10-CAT-004-P", "NORMATIVE_EXAMPLE", "tekroo.command.workflow.record-checkpoint", { checkpoint_id: id("000000000005"), completed_node_ids: [id("000000000002")], frontier_node_ids: [id("000000000006")], state_digest: fullDigest }, "APPLIED", ["tekroo.event.workflow.checkpoint-recorded"]),
  catalogueFixture("P10-CAT-004-N", "BOUNDARY_NEGATIVE", "tekroo.command.workflow.record-checkpoint", {}, "REJECTED_INVALID")
);
writeJson("fixtures/catalogue-coverage.json", coverage);

const rootTraceability = readJson("traceability/traceability.json");
rootTraceability.requirements.push(...workflowTraceability.requirements);
writeJson("traceability/traceability.json", rootTraceability);

const commandEntry = (typeId, schema, emits, rootAllowed = false) => ({
  typeId, version: "1.0.0", kind: "COMMAND", lifecycle: "ACTIVE", owner: "tekroo-kernel", aliases: [],
  routingMode: "KERNEL_DIRECT", targetKinds: ["workflow-instance"], authorityKinds: rootAllowed ? ["HUMAN", "POLICY"] : ["ACTOR", "SERVICE", "POLICY"],
  executionRequired: false, rootAllowed, allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
  payloadSchema: `schemas/payloads.schema.json#/$defs/${schema}`, emits, compatibility: { acceptedSourceVersions: ["1.0.0"], transforms: [] }
});
const eventEntry = (typeId, schema, acceptedCommandTypes) => ({
  typeId, version: "1.0.0", kind: "EVENT", lifecycle: "ACTIVE", owner: "tekroo-kernel", aliases: [],
  routingMode: "COMMITTED_EVENT", targetKinds: ["workflow-instance"], authorityKinds: [], executionRequired: false, rootAllowed: false,
  allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
  payloadSchema: `schemas/payloads.schema.json#/$defs/${schema}`, acceptedCommandTypes, compatibility: { acceptedSourceVersions: ["1.0.0"], transforms: [] }
});
const catalogue = readJson("catalogue/kernel-catalogue.json");
catalogue.entries.push(
  commandEntry("tekroo.command.workflow.create", "tekroo_command_workflow_create_1_0_0", ["tekroo.event.workflow.created"], true),
  commandEntry("tekroo.command.workflow.propose-work", "tekroo_command_workflow_propose_work_1_0_0", ["tekroo.event.workflow.work-admitted", "tekroo.event.workflow.work-rejected"]),
  commandEntry("tekroo.command.workflow.record-stage-result", "tekroo_command_workflow_record_stage_result_1_0_0", ["tekroo.event.workflow.stage-result-recorded"]),
  commandEntry("tekroo.command.workflow.record-checkpoint", "tekroo_command_workflow_record_checkpoint_1_0_0", ["tekroo.event.workflow.checkpoint-recorded"]),
  eventEntry("tekroo.event.workflow.created", "tekroo_event_workflow_created_1_0_0", ["tekroo.command.workflow.create"]),
  eventEntry("tekroo.event.workflow.work-admitted", "tekroo_event_workflow_work_admitted_1_0_0", ["tekroo.command.workflow.propose-work"]),
  eventEntry("tekroo.event.workflow.work-rejected", "tekroo_event_workflow_work_rejected_1_0_0", ["tekroo.command.workflow.propose-work"]),
  eventEntry("tekroo.event.workflow.stage-result-recorded", "tekroo_event_workflow_stage_result_recorded_1_0_0", ["tekroo.command.workflow.record-stage-result"]),
  eventEntry("tekroo.event.workflow.checkpoint-recorded", "tekroo_event_workflow_checkpoint_recorded_1_0_0", ["tekroo.command.workflow.record-checkpoint"])
);
catalogue.entries.sort((left, right) => left.typeId.localeCompare(right.typeId));
writeJson("catalogue/kernel-catalogue.json", catalogue);

const validatorPath = path.join(target, "runner/validate-package.mjs");
let validator = fs.readFileSync(validatorPath, "utf8");
validator = validator.replace('manifest.contract.version === "0.10.0"', 'manifest.contract.version === "0.11.0"');
validator = validator.replace('const compatibility = readJson("compatibility/from-0.9.0.json");', 'const compatibility = readJson("compatibility/from-0.10.0.json");');
validator = validator.replace('compatibility.predecessor?.contractVersion === "0.9.0" && compatibility.contractVersion === "0.10.0"', 'compatibility.predecessor?.contractVersion === "0.10.0" && compatibility.contractVersion === "0.11.0"');
validator = validator.replace('check("phase7-federation-schema"', `
const workflowFixtures = readJson("fixtures/workflow-runtime.json");
const workflowInvariants = readJson("invariants/workflow-invariants.json");
const workflowTraceability = readJson("traceability/workflow-traceability.json");
const workflowTestIds = new Set([...workflowFixtures.fixtures.map((item) => item.fixtureId), ...fixtures.filter((item) => item.fixtureId.startsWith("P10-")).map((item) => item.fixtureId), ...workflowInvariants.invariants.map((item) => item.invariantId)]);
check("phase10-workflow-schemas", ["schemas/workflow-definition.schema.json", "schemas/workflow-instance.schema.json", "schemas/work-proposal.schema.json", "schemas/admission-result.schema.json"].every((file) => listedPayloadFiles.includes(file)));
check("phase10-workflow-catalogue", ["tekroo.command.workflow.create", "tekroo.command.workflow.propose-work", "tekroo.command.workflow.record-stage-result", "tekroo.command.workflow.record-checkpoint", "tekroo.event.workflow.created", "tekroo.event.workflow.work-admitted", "tekroo.event.workflow.work-rejected", "tekroo.event.workflow.stage-result-recorded", "tekroo.event.workflow.checkpoint-recorded"].every((id) => typeIds.includes(id)));
check("phase10-workflow-fixture-cardinality", workflowFixtures.fixtures.length === 8);
check("phase10-workflow-invariant-cardinality", workflowInvariants.invariants.length === 8);
check("phase10-workflow-traceability", workflowTraceability.requirements.every((item) => item.testIds.length > 0 && item.testIds.every((id) => workflowTestIds.has(id))));
check("phase10-required-scenarios", ["P10-WF-001", "P10-WF-002", "P10-WF-003", "P10-WF-004", "P10-WF-005", "P10-WF-006", "P10-WF-007", "P10-WF-008"].every((id) => workflowFixtures.fixtures.some((item) => item.fixtureId === id)));
check("phase7-federation-schema"`);
validator = validator.replace('check("decision-trace-count", decisions.length === 50, decisions.length);', 'check("decision-trace-count", decisions.length === 57, decisions.length);');
validator = validator.replace('check("negative-requirement-trace-count", negatives.length === 26, negatives.length);', 'check("negative-requirement-trace-count", negatives.length === 27, negatives.length);');
fs.writeFileSync(validatorPath, validator);

const referenceRunnerPath = path.join(target, "runner/reference-runner.mjs");
let referenceRunner = fs.readFileSync(referenceRunnerPath, "utf8");
referenceRunner = referenceRunner.replace("const manifest = readJson(\"manifest.json\");", `function workflowTopology(nodes) {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  if (byId.size !== nodes.length) return { valid: false, reason: "DUPLICATE_NODE" };
  const visiting = new Set();
  const visited = new Set();
  function cycle(nodeId) {
    if (visiting.has(nodeId)) return true;
    if (visited.has(nodeId)) return false;
    const node = byId.get(nodeId);
    if (!node) return true;
    visiting.add(nodeId);
    for (const predecessor of node.dependsOn) if (!byId.has(predecessor) || cycle(predecessor)) return true;
    visiting.delete(nodeId);
    visited.add(nodeId);
    return false;
  }
  for (const nodeId of byId.keys()) if (cycle(nodeId)) return { valid: false, reason: "CYCLE" };
  return { valid: true, reason: null };
}

function runWorkflowModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (fixture.model === "PARALLEL_READY") {
    const completed = new Set(current.completedNodeIds);
    const readyNodeIds = current.nodes
      .filter((node) => !completed.has(node.id) && node.dependsOn.every((id) => completed.has(id)))
      .map((node) => node.id).sort();
    return { readyNodeIds, admittedNodeIds: readyNodeIds.slice(0, current.capacity) };
  }
  if (fixture.model === "BOUNDED_REPAIR") {
    const budgetPreserved = action.requestedBudgetAccountId === current.budgetAccountId;
    const admitted = action.changedConditionEvidenceIds.length > 0 && current.attempt < current.attemptLimit && budgetPreserved;
    return { admitted, nextAttempt: admitted ? current.attempt + 1 : current.attempt, completedNodeIds: current.completedNodeIds, budgetPreserved };
  }
  if (fixture.model === "ROLE_REUSE") {
    const topology = workflowTopology(current.nodes);
    const counts = new Map();
    for (const node of current.nodes) counts.set(node.role, (counts.get(node.role) ?? 0) + 1);
    return { ...topology, distinctNodeCount: new Set(current.nodes.map((node) => node.id)).size, reusedRoles: [...counts].filter(([, count]) => count > 1).map(([role]) => role).sort() };
  }
  if (fixture.model === "NODE_CYCLE") return workflowTopology(current.nodes);
  if (fixture.model === "MESSAGE_ADMISSION") {
    const createdInvocationCount = action.messages.filter((message) => ["REQUEST", "HANDOFF"].includes(message.purpose) && message.admission === "ADMITTED").length;
    return { invocationCount: current.invocationCount + createdInvocationCount, createdInvocationCount };
  }
  if (fixture.model === "CHECKPOINT_RESUME") return { resumeNodeIds: current.frontierNodeIds, replayNodeIds: [], preservedCompletedNodeIds: current.completedNodeIds };
  if (fixture.model === "CONFIG_GENERALITY") {
    const runtimeRoles = new Set(current.genericRuntimeRoleNames);
    return { loaded: current.configuredRoles.length > 0, sourceChangeRequired: current.configuredRoles.some((role) => runtimeRoles.has(role)) };
  }
  if (fixture.model === "VALIDATION_CEILING") {
    const ceilingSeconds = current.designSeconds + current.implementationSeconds;
    const reusePriorEvidence = current.priorEvidenceDigest === current.currentInputDigest && action.changedConditionEvidenceIds.length === 0;
    return { ceilingSeconds, reusePriorEvidence, additionalValidationAdmitted: !reusePriorEvidence && current.validationSeconds + current.requestedAdditionalSeconds <= ceilingSeconds };
  }
  return { unsupportedWorkflowModel: fixture.model };
}

const manifest = readJson("manifest.json");`);
referenceRunner = referenceRunner.replace('.filter((file) => file.role === "fixtures")', '.filter((file) => ["fixtures", "workflow-fixtures"].includes(file.role))');
referenceRunner = referenceRunner.replace('  } else if (fixture.kind === "STATE_MODEL") {', '  } else if (fixture.kind === "WORKFLOW_MODEL") {\n    actual = runWorkflowModel(fixture);\n  } else if (fixture.kind === "STATE_MODEL") {');
fs.writeFileSync(referenceRunnerPath, referenceRunner);

const sourceManifest = JSON.parse(fs.readFileSync(path.join(source, "manifest.json"), "utf8"));
const sourceByPath = new Map(sourceManifest.files.map((entry) => [entry.path, entry]));
const roles = new Map([
  ["schemas/workflow-definition.schema.json", "workflow-schemas"], ["schemas/workflow-instance.schema.json", "workflow-schemas"],
  ["schemas/work-proposal.schema.json", "workflow-schemas"], ["schemas/admission-result.schema.json", "workflow-schemas"],
  ["fixtures/workflow-runtime.json", "fixtures"], ["invariants/workflow-invariants.json", "workflow-invariants"],
  ["traceability/workflow-traceability.json", "workflow-traceability"], ["compatibility/from-0.10.0.json", "compatibility"]
]);
const dependencyOverrides = new Map([
  ["schemas/workflow-instance.schema.json", ["schemas/core.schema.json"]],
  ["schemas/work-proposal.schema.json", ["schemas/core.schema.json"]],
  ["schemas/payloads.schema.json", ["schemas/core.schema.json", "schemas/workflow-definition.schema.json", "schemas/work-proposal.schema.json", "schemas/admission-result.schema.json"]],
  ["fixtures/workflow-runtime.json", ["catalogue/kernel-catalogue.json"]],
  ["invariants/workflow-invariants.json", ["fixtures/workflow-runtime.json"]],
  ["traceability/workflow-traceability.json", ["fixtures/workflow-runtime.json", "invariants/workflow-invariants.json"]],
  ["compatibility/from-0.10.0.json", ["CONTRACTS/tekroo.kernel.contracts/0.10.0/manifest.json"]]
]);
const paths = walk(target).filter((value) => !["manifest.json", "manifest.sha256"].includes(value));
const files = paths.map((relative) => {
  const bytes = fs.readFileSync(path.join(target, relative));
  const json = relative.endsWith(".json") ? JSON.parse(bytes.toString("utf8")) : null;
  const sourceEntry = sourceByPath.get(relative);
  return {
    path: relative,
    role: roles.get(relative) ?? sourceEntry?.role ?? "contract-asset",
    mediaType: json ? "application/json" : sourceEntry?.mediaType ?? "text/javascript",
    bytes: bytes.length,
    sha256: sha256(bytes),
    canonicalJsonSha256: json ? sha256(canonical(json)) : null,
    dependencies: dependencyOverrides.get(relative) ?? sourceEntry?.dependencies ?? []
  };
});
const fixtureCount = paths.filter((value) => value.startsWith("fixtures/") && value.endsWith(".json")).reduce((sum, value) => sum + (readJson(value).fixtures?.length ?? 0), 0);
const invariantCount = paths.filter((value) => value.startsWith("invariants/") && value.endsWith(".json")).reduce((sum, value) => sum + (readJson(value).invariants?.length ?? 0), 0);
const traceabilityCount = paths.filter((value) => value.startsWith("traceability/") && value.endsWith(".json")).reduce((sum, value) => sum + (readJson(value).requirements?.length ?? 0), 0);
const manifest = {
  ...sourceManifest,
  contract: { name: "tekroo.kernel.contracts", version: "0.11.0", status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-09-13T00:00:00Z",
  files,
  counts: {
    catalogueEntries: catalogue.entries.length,
    commandTypes: catalogue.entries.filter((entry) => entry.kind === "COMMAND").length,
    eventTypes: catalogue.entries.filter((entry) => entry.kind === "EVENT").length,
    fixtures: fixtureCount,
    invariants: invariantCount,
    schemas: paths.filter((value) => value.startsWith("schemas/") && value.endsWith(".json")).length,
    traceabilityRequirements: traceabilityCount
  },
  knownLimitations: [
    "Contract structure does not by itself qualify the Go implementation or a deployment.",
    "The supplied software workflow and a non-software demonstration workflow are implementation deliverables, not embedded contract policy.",
    "V3 data migration and public-network deployment are not authorized."
  ],
  sourceLineage: [
    { path: "CONTRACTS/tekroo.kernel.contracts/0.10.0/manifest.json", sha256: sha256(fs.readFileSync(path.join(source, "manifest.json"))) },
    { path: "docs/architecture/128-phase-10-hybrid-organizational-runtime-plan.md", sha256: sha256(fs.readFileSync(path.join(root, "docs/architecture/128-phase-10-hybrid-organizational-runtime-plan.md"))) },
    { path: "OUTPUT/phase-10/step-1/baseline-separation-receipt.json", sha256: sha256(fs.readFileSync(path.join(root, "OUTPUT/phase-10/step-1/baseline-separation-receipt.json"))) },
    { path: "scripts/generate_phase10_contract_0_11.mjs", sha256: sha256(fs.readFileSync(fileURLToPath(import.meta.url))) }
  ]
};
writeJson("manifest.json", manifest);
const manifestBytes = fs.readFileSync(path.join(target, "manifest.json"));
fs.writeFileSync(path.join(target, "manifest.sha256"), `${sha256(manifestBytes)}  manifest.json\n`);
process.stdout.write(`${newIdentity} ${sha256(manifestBytes)} ${files.length}\n`);
