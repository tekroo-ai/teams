#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const predecessorVersion = "0.5.0";
const contractVersion = "0.6.0";
const predecessorSchemaVersion = "1.4.0";
const schemaVersion = "1.5.0";
const contractIdentity = `tekroo.kernel.contracts/${contractVersion}`;
const predecessorIdentity = `tekroo.kernel.contracts/${predecessorVersion}`;
const predecessorRoot = path.join(repoRoot, `CONTRACTS/${predecessorIdentity}`);
const packageRoot = path.join(repoRoot, `CONTRACTS/${contractIdentity}`);
const manifestPath = path.join(packageRoot, "manifest.json");
const checksumPath = path.join(packageRoot, "manifest.sha256");

if (fs.existsSync(packageRoot)) throw new Error(`contract ${contractVersion} already exists; refusing in-place regeneration`);

const uuid7Pattern = "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$";
const shaPattern = "^[0-9a-f]{64}$";
const actorPattern = "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$";
const routes = ["DETERMINISTIC", "BOUNDED_EXECUTION", "COMPLEX_REASONING", "NOVEL_REASONING", "HUMAN_REQUIRED"];
const modelRoutes = ["BOUNDED_EXECUTION", "COMPLEX_REASONING", "NOVEL_REASONING"];
const independenceDimensions = ["PRINCIPAL", "ACTOR", "EXECUTION", "CONTEXT", "WORKSPACE", "MODEL_PROFILE", "PROVIDER", "METHOD", "HUMAN"];
const escalationTriggers = [
  "RETRY_EXHAUSTED", "VALIDATION_CONFLICT", "VALIDATION_INCONCLUSIVE", "VALIDATION_BUDGET_EXHAUSTED",
  "VALIDATION_DEADLINE_EXPIRED", "HANDOFF_CYCLE_DETECTED", "HANDOFF_BUDGET_EXHAUSTED",
  "REQUIREMENT_CONTRADICTION", "ARCHITECTURE_AMBIGUITY", "MATERIAL_VARIANT_DISAGREEMENT",
  "CAPABILITY_MISMATCH", "RISK_PROFILE_BREACH", "BLAST_RADIUS_EXCEEDED",
  "SECURITY_CLASSIFICATION_ELEVATED", "ROOT_CAUSE_UNRESOLVED", "REQUIRED_TOOL_UNAVAILABLE",
  "NOVELTY_RECLASSIFIED",
];

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])]));
  return value;
}

const canonical = (value) => JSON.stringify(sortValue(value));
const pretty = (value) => `${JSON.stringify(sortValue(value), null, 2)}\n`;
const sha256 = (value) => crypto.createHash("sha256").update(value).digest("hex");
const read = (relativePath) => fs.readFileSync(path.join(repoRoot, relativePath));
const readJson = (relativePath) => JSON.parse(read(relativePath).toString("utf8"));

function writeJson(relativePath, value) {
  const absolute = path.join(packageRoot, relativePath);
  fs.mkdirSync(path.dirname(absolute), { recursive: true });
  fs.writeFileSync(absolute, pretty(value), { flag: "wx" });
}

function overwriteJson(relativePath, value) {
  fs.writeFileSync(path.join(packageRoot, relativePath), pretty(value));
}

function advanceString(value) {
  return value
    .replaceAll(predecessorIdentity, contractIdentity)
    .replaceAll("_1_4_0", "_1_5_0")
    .replaceAll(predecessorSchemaVersion, schemaVersion);
}

function advance(value) {
  if (typeof value === "string") return advanceString(value);
  if (Array.isArray(value)) return value.map(advance);
  if (value && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([key, child]) => [advanceString(key), advance(child)]));
  return value;
}

const string = (options = {}) => ({ type: "string", ...options });
const integer = (options = {}) => ({ type: "integer", ...options });
const array = (items, options = {}) => ({ type: "array", items, ...options });
const object = (properties, required = Object.keys(properties)) => ({ type: "object", additionalProperties: false, properties, required });
const nullable = (schema) => ({ anyOf: [schema, { type: "null" }] });
const uuid = () => string({ pattern: uuid7Pattern });
const digest = () => string({ pattern: shaPattern });
const actor = () => string({ pattern: actorPattern });
const nonempty = (maxLength = 4096) => string({ minLength: 1, maxLength });
const uuidList = (options = {}) => array(uuid(), { maxItems: 64, uniqueItems: true, ...options });
const stringList = (options = {}) => array(nonempty(), { maxItems: 64, uniqueItems: true, ...options });
const principal = () => object({ kind: string({ enum: ["ACTOR", "HUMAN", "SERVICE", "POLICY"] }), id: nonempty(256) });
const route = () => string({ enum: routes });
const modelRoute = () => string({ enum: modelRoutes });
const dimensionList = (options = {}) => array(string({ enum: independenceDimensions }), { maxItems: independenceDimensions.length, uniqueItems: true, ...options });

function walk(directory, prefix = "") {
  const result = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const relative = prefix ? `${prefix}/${entry.name}` : entry.name;
    const absolute = path.join(directory, entry.name);
    if (entry.isDirectory()) result.push(...walk(absolute, relative));
    else result.push(relative);
  }
  return result.sort();
}

const skipped = new Set([
  "manifest.json",
  "manifest.sha256",
  "compatibility/from-0.4.0.json",
  "runner/reference-runner.mjs",
  "runner/validate-package.mjs",
]);

for (const relativePath of walk(predecessorRoot)) {
  if (skipped.has(relativePath)) continue;
  const source = path.join(predecessorRoot, relativePath);
  if (relativePath.endsWith(".json")) writeJson(relativePath, advance(JSON.parse(fs.readFileSync(source, "utf8"))));
  else {
    const target = path.join(packageRoot, relativePath);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, advanceString(fs.readFileSync(source, "utf8")), { flag: "wx" });
  }
}

const corePath = "schemas/core.schema.json";
const core = readJson(`CONTRACTS/${contractIdentity}/${corePath}`);
if (!core.$defs.AggregateRef.properties.kind.enum.includes("variant-group")) core.$defs.AggregateRef.properties.kind.enum.push("variant-group");
core.$defs.AggregateRef.properties.kind.enum.sort();
overwriteJson(corePath, core);

const aggregatePath = "schemas/aggregate-state.schema.json";
const aggregate = readJson(`CONTRACTS/${contractIdentity}/${aggregatePath}`);
for (const variant of aggregate.oneOf) {
  variant.properties.scope_revision = integer({ minimum: 1 });
  if (!variant.required.includes("scope_revision")) variant.required.push("scope_revision");
  variant.required.sort();
}
overwriteJson(aggregatePath, aggregate);

const profileBinding = () => object({
  profile_id: uuid(),
  profile_revision: integer({ minimum: 1 }),
  profile_digest: digest(),
  lifecycle_epoch: integer({ minimum: 1 }),
  scope_revision: integer({ minimum: 1 }),
});
const finiteBudgets = () => object({
  attempt_limit: integer({ minimum: 1, maximum: 1000 }),
  review_round_limit: integer({ minimum: 1, maximum: 1000 }),
  promotion_limit: integer({ minimum: 0, maximum: 1000 }),
  escalation_limit: integer({ minimum: 1, maximum: 1000 }),
  deadline_at: string({ format: "date-time" }),
});
const workProfile = object({
  task_id: uuid(),
  ...profileBinding().properties,
  work_kind: string({ enum: ["INVESTIGATION", "DESIGN", "IMPLEMENTATION", "DEBUGGING", "VALIDATION", "SECURITY_REVIEW", "RELEASE"] }),
  ambiguity: string({ enum: ["LOW", "MEDIUM", "HIGH", "UNKNOWN"] }),
  novelty: string({ enum: ["ROUTINE", "UNFAMILIAR", "NOVEL", "UNKNOWN"] }),
  blast_radius: string({ enum: ["LOCAL", "MULTI_COMPONENT", "ARCHITECTURAL", "EXTERNAL_EFFECT", "UNKNOWN"] }),
  security_sensitivity: string({ enum: ["ORDINARY", "SENSITIVE", "CRITICAL", "UNKNOWN"] }),
  minimum_decision_route: route(),
  acceptance_criteria_digest: digest(),
  required_deterministic_gate_ids: stringList({ minItems: 1 }),
  required_validation_branches: integer({ minimum: 1, maximum: 32 }),
  required_independence_dimensions: dimensionList({ minItems: 1 }),
  implementation_variant_count: integer({ minimum: 1, maximum: 32 }),
  valid_candidate_quorum: integer({ minimum: 1, maximum: 32 }),
  verification_topology_digest: digest(),
  classification_policy_revision: integer({ minimum: 1 }),
  classification_policy_digest: digest(),
  promotion_policy_revision: integer({ minimum: 1 }),
  promotion_policy_digest: digest(),
  budgets: finiteBudgets(),
  classification_authority: principal(),
  classification_evidence_ids: uuidList({ minItems: 1 }),
  supersedes_profile_id: nullable(uuid()),
});

writeJson("schemas/work-profile.schema.json", {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `https://contracts.tekroo.ai/${contractIdentity}/schemas/work-profile.schema.json`,
  title: "Content-addressed task work-risk profile",
  ...workProfile,
});

const qualificationRef = () => object({
  qualification_id: uuid(),
  qualification_digest: digest(),
  qualification_corpus_digest: digest(),
  model_profile_digest: digest(),
  decision_route: modelRoute(),
  qualified_role: nonempty(256),
  status: { const: "PASS" },
  observed_at: string({ format: "date-time" }),
});
const hardConstraint = () => object({
  constraint_id: nonempty(256),
  outcome: { const: "PASS" },
  evidence_ids: uuidList({ minItems: 1 }),
});
const assignmentAuthorization = object({
  assignment_id: uuid(),
  task_id: uuid(),
  expected_task_revision: integer({ minimum: 1 }),
  work_profile: profileBinding(),
  required_decision_route: modelRoute(),
  selected_decision_route: modelRoute(),
  selected_actor_fqn: actor(),
  selected_execution_id: uuid(),
  selected_fencing_epoch: integer({ minimum: 1 }),
  model_profile_digest: digest(),
  runtime_identity_digest: digest(),
  qualification: qualificationRef(),
  selection_policy_revision: integer({ minimum: 1 }),
  selection_policy_digest: digest(),
  hard_constraint_results: array(hardConstraint(), { minItems: 1, maxItems: 64 }),
  selection_reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
});

const implementerIdentity = () => object({
  principal: principal(),
  actor_fqn: actor(),
  execution_id: uuid(),
  fencing_epoch: integer({ minimum: 1 }),
  model_profile_digest: digest(),
  workspace_digest: digest(),
  context_digest: digest(),
});
const independenceReceipt = () => object({
  proven_dimensions: dimensionList({ minItems: 1 }),
  identity_comparison_digest: digest(),
  method_ids: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
});

const variantOpen = object({
  variant_group_id: uuid(),
  task_id: uuid(),
  work_profile: profileBinding(),
  base_artifact_digest: digest(),
  input_evidence_set_digest: digest(),
  toolchain_digest: digest(),
  dependency_lock_digest: digest(),
  acceptance_manifest_digest: digest(),
  candidate_count: integer({ minimum: 2, maximum: 32 }),
  valid_candidate_quorum: integer({ minimum: 2, maximum: 32 }),
  required_independence_dimensions: dimensionList({ minItems: 1 }),
  comparison_method_id: nonempty(256),
  comparison_policy_digest: digest(),
  materiality_policy_digest: digest(),
  comparator: principal(),
  adjudicator: principal(),
  submission_deadline_at: string({ format: "date-time" }),
  decision_deadline_at: string({ format: "date-time" }),
  replacement_budget: integer({ minimum: 0, maximum: 32 }),
  evidence_ids: uuidList({ minItems: 1 }),
});
const variantCandidate = object({
  variant_group_id: uuid(),
  expected_group_revision: integer({ minimum: 1 }),
  candidate_id: uuid(),
  actor_fqn: actor(),
  execution_id: uuid(),
  fencing_epoch: integer({ minimum: 1 }),
  model_profile_digest: digest(),
  runtime_identity_digest: digest(),
  context_digest: digest(),
  workspace_digest: digest(),
  artifact_digest: digest(),
  changed_file_inventory_digest: digest(),
  deterministic_gate_receipt_ids: uuidList({ minItems: 1 }),
  assumptions: stringList(),
  unresolved_exceptions: stringList(),
  evidence_ids: uuidList({ minItems: 1 }),
});
const variantComparison = object({
  variant_group_id: uuid(),
  expected_group_revision: integer({ minimum: 1 }),
  comparator: principal(),
  comparator_execution_id: uuid(),
  candidate_ids: uuidList({ minItems: 2 }),
  classification: string({ enum: ["MATERIAL_AGREEMENT", "NON_MATERIAL_VARIATION", "MATERIAL_DISAGREEMENT", "INSUFFICIENT_VALID_CANDIDATES", "INCONCLUSIVE"] }),
  comparison_method_id: nonempty(256),
  comparison_receipt_digest: digest(),
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
});
const selectionCommon = {
  variant_group_id: uuid(),
  expected_group_revision: integer({ minimum: 1 }),
  comparison_event_id: uuid(),
  adjudicator: principal(),
  reasons: stringList({ minItems: 1 }),
  evidence_ids: uuidList({ minItems: 1 }),
};
const variantSelection = {
  oneOf: [
    object({ ...selectionCommon, outcome: { const: "SELECT_CANDIDATE" }, selected_candidate_id: uuid() }),
    object({ ...selectionCommon, outcome: string({ enum: ["REJECT_ALL", "REDESIGN", "SPLIT", "HUMAN_REQUIRED", "ESCALATE"] }) }),
  ],
};

const payloadPath = "schemas/payloads.schema.json";
const payloads = readJson(`CONTRACTS/${contractIdentity}/${payloadPath}`);
const bindName = "tekroo_command_task_bind_work_profile_1_5_0";
const bindEventName = "tekroo_event_task_work_profile_bound_1_5_0";
const assignmentName = "tekroo_command_task_authorize_qualified_assignment_1_5_0";
const assignmentEventName = "tekroo_event_task_qualified_assignment_authorized_1_5_0";
payloads.$defs[bindName] = workProfile;
payloads.$defs[bindEventName] = workProfile;
payloads.$defs[assignmentName] = assignmentAuthorization;
payloads.$defs[assignmentEventName] = assignmentAuthorization;
payloads.$defs.tekroo_command_variant_group_open_1_5_0 = variantOpen;
payloads.$defs.tekroo_event_variant_group_opened_1_5_0 = variantOpen;
payloads.$defs.tekroo_command_variant_group_submit_candidate_1_5_0 = variantCandidate;
payloads.$defs.tekroo_event_variant_group_candidate_submitted_1_5_0 = variantCandidate;
payloads.$defs.tekroo_command_variant_group_record_comparison_1_5_0 = variantComparison;
payloads.$defs.tekroo_event_variant_group_comparison_recorded_1_5_0 = variantComparison;
payloads.$defs.tekroo_command_variant_group_select_1_5_0 = variantSelection;
payloads.$defs.tekroo_event_variant_group_selected_1_5_0 = variantSelection;

const completionNames = {
  open: ["tekroo_command_completion_review_open_1_5_0", "tekroo_event_completion_review_opened_1_5_0"],
  result: ["tekroo_command_completion_review_record_result_1_5_0", "tekroo_event_completion_review_result_recorded_1_5_0"],
  finalize: ["tekroo_command_completion_review_finalize_1_5_0", "tekroo_event_completion_review_finalized_1_5_0"],
};
for (const name of completionNames.open) {
  const schema = payloads.$defs[name];
  schema.properties.scope_revision = integer({ minimum: 1 });
  schema.properties.work_profile = profileBinding();
  schema.properties.candidate_artifact_digest = digest();
  schema.properties.implementer = implementerIdentity();
  schema.properties.verification_topology_digest = digest();
  schema.properties.variant_group_id = nullable(uuid());
  schema.required.push("scope_revision", "work_profile", "candidate_artifact_digest", "implementer", "verification_topology_digest", "variant_group_id");
  for (const branch of schema.properties.branches.items ? [schema.properties.branches.items] : []) {
    branch.properties.required_independence_dimensions = dimensionList({ minItems: 1 });
    branch.properties.required_method_ids = stringList({ minItems: 1 });
    branch.required.push("required_independence_dimensions", "required_method_ids");
  }
}
for (const name of completionNames.result) {
  const schema = payloads.$defs[name];
  schema.properties.candidate_artifact_digest = digest();
  schema.properties.independence_receipt = independenceReceipt();
  schema.required.push("candidate_artifact_digest", "independence_receipt");
}
for (const name of completionNames.finalize) {
  const schema = payloads.$defs[name];
  schema.properties.scope_revision = integer({ minimum: 1 });
  schema.properties.work_profile = profileBinding();
  schema.properties.candidate_artifact_digest = digest();
  schema.properties.verification_topology_digest = digest();
  schema.required.push("scope_revision", "work_profile", "candidate_artifact_digest", "verification_topology_digest");
}
for (const name of ["tekroo_command_escalation_open_1_5_0", "tekroo_event_escalation_opened_1_5_0"]) {
  const trigger = payloads.$defs[name].properties.trigger;
  trigger.enum = [...new Set([...trigger.enum, ...escalationTriggers])].sort();
}
overwriteJson(payloadPath, payloads);

const cataloguePath = "catalogue/kernel-catalogue.json";
const catalogue = readJson(`CONTRACTS/${contractIdentity}/${cataloguePath}`);
catalogue.revision = 6;
const predecessorTypeIds = catalogue.entries.map((entry) => entry.typeId).sort();
const changedCompletionTypes = [
  "tekroo.command.completion-review.open", "tekroo.event.completion-review.opened",
  "tekroo.command.completion-review.record-result", "tekroo.event.completion-review.result-recorded",
  "tekroo.command.completion-review.finalize", "tekroo.event.completion-review.finalized",
];
for (const entry of catalogue.entries) {
  entry.compatibility = changedCompletionTypes.includes(entry.typeId)
    ? { acceptedSourceVersions: [schemaVersion], transforms: [] }
    : { acceptedSourceVersions: [predecessorSchemaVersion, schemaVersion], transforms: [{ sourceVersion: predecessorSchemaVersion, targetVersion: schemaVersion, mode: "IDENTITY" }] };
}
const commonEntry = {
  version: schemaVersion,
  lifecycle: "ACTIVE",
  owner: "tekroo-kernel",
  executionRequired: false,
  allowedParentEdges: ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"],
  rootAllowed: false,
  aliases: [],
  compatibility: { acceptedSourceVersions: [schemaVersion], transforms: [] },
};
function addPair(commandType, eventType, targetKinds, authorityKinds, commandSchema, eventSchema) {
  catalogue.entries.push(
    { ...commonEntry, typeId: commandType, kind: "COMMAND", targetKinds, authorityKinds, routingMode: "KERNEL_DIRECT", payloadSchema: `schemas/payloads.schema.json#/$defs/${commandSchema}`, emits: [eventType] },
    { ...commonEntry, typeId: eventType, kind: "EVENT", targetKinds, authorityKinds: [], routingMode: "COMMITTED_EVENT", payloadSchema: `schemas/payloads.schema.json#/$defs/${eventSchema}`, acceptedCommandTypes: [commandType] },
  );
}
addPair("tekroo.command.task.bind-work-profile", "tekroo.event.task.work-profile-bound", ["task"], ["POLICY", "HUMAN"], bindName, bindEventName);
addPair("tekroo.command.task.authorize-qualified-assignment", "tekroo.event.task.qualified-assignment-authorized", ["task"], ["POLICY"], assignmentName, assignmentEventName);
addPair("tekroo.command.variant-group.open", "tekroo.event.variant-group.opened", ["variant-group"], ["POLICY"], "tekroo_command_variant_group_open_1_5_0", "tekroo_event_variant_group_opened_1_5_0");
addPair("tekroo.command.variant-group.submit-candidate", "tekroo.event.variant-group.candidate-submitted", ["variant-group"], ["ACTOR", "SERVICE"], "tekroo_command_variant_group_submit_candidate_1_5_0", "tekroo_event_variant_group_candidate_submitted_1_5_0");
addPair("tekroo.command.variant-group.record-comparison", "tekroo.event.variant-group.comparison-recorded", ["variant-group"], ["ACTOR", "HUMAN", "SERVICE"], "tekroo_command_variant_group_record_comparison_1_5_0", "tekroo_event_variant_group_comparison_recorded_1_5_0");
addPair("tekroo.command.variant-group.select", "tekroo.event.variant-group.selected", ["variant-group"], ["POLICY", "HUMAN"], "tekroo_command_variant_group_select_1_5_0", "tekroo_event_variant_group_selected_1_5_0");
catalogue.entries.sort((left, right) => left.typeId.localeCompare(right.typeId));
overwriteJson(cataloguePath, catalogue);

const ids = {
  task: "00000000-0000-7000-8000-000000000801",
  profile: "00000000-0000-7000-8000-000000000802",
  assignment: "00000000-0000-7000-8000-000000000803",
  execution: "00000000-0000-7000-8000-000000000804",
  qualification: "00000000-0000-7000-8000-000000000805",
  evidence: "00000000-0000-7000-8000-000000000806",
  group: "00000000-0000-7000-8000-000000000807",
  candidateA: "00000000-0000-7000-8000-000000000808",
  candidateB: "00000000-0000-7000-8000-000000000809",
  comparison: "00000000-0000-7000-8000-000000000810",
};
const d = (character) => character.repeat(64);
const bindingPayload = {
  task_id: ids.task, profile_id: ids.profile, profile_revision: 1, profile_digest: d("a"), lifecycle_epoch: 1, scope_revision: 1,
  work_kind: "IMPLEMENTATION", ambiguity: "LOW", novelty: "ROUTINE", blast_radius: "LOCAL", security_sensitivity: "ORDINARY",
  minimum_decision_route: "BOUNDED_EXECUTION", acceptance_criteria_digest: d("b"), required_deterministic_gate_ids: ["go-test"],
  required_validation_branches: 1, required_independence_dimensions: ["PRINCIPAL", "ACTOR", "EXECUTION", "CONTEXT", "WORKSPACE", "METHOD"],
  implementation_variant_count: 1, valid_candidate_quorum: 1, verification_topology_digest: d("c"), classification_policy_revision: 1,
  classification_policy_digest: d("d"), promotion_policy_revision: 1, promotion_policy_digest: d("e"),
  budgets: { attempt_limit: 2, review_round_limit: 2, promotion_limit: 1, escalation_limit: 1, deadline_at: "2026-08-14T00:00:00Z" },
  classification_authority: { kind: "HUMAN", id: "principal" }, classification_evidence_ids: [ids.evidence], supersedes_profile_id: null,
};
const bindingRefPayload = { profile_id: ids.profile, profile_revision: 1, profile_digest: d("a"), lifecycle_epoch: 1, scope_revision: 1 };
const assignmentPayload = {
  assignment_id: ids.assignment, task_id: ids.task, expected_task_revision: 2, work_profile: bindingRefPayload,
  required_decision_route: "BOUNDED_EXECUTION", selected_decision_route: "BOUNDED_EXECUTION", selected_actor_fqn: "teams::coder-1",
  selected_execution_id: ids.execution, selected_fencing_epoch: 1, model_profile_digest: d("f"), runtime_identity_digest: d("1"),
  qualification: { qualification_id: ids.qualification, qualification_digest: d("2"), qualification_corpus_digest: d("3"), model_profile_digest: d("f"), decision_route: "BOUNDED_EXECUTION", qualified_role: "programmer", status: "PASS", observed_at: "2026-08-13T12:00:00Z" },
  selection_policy_revision: 1, selection_policy_digest: d("4"),
  hard_constraint_results: [{ constraint_id: "data-residency", outcome: "PASS", evidence_ids: [ids.evidence] }],
  selection_reasons: ["least-cost qualified profile"], evidence_ids: [ids.evidence],
};
const variantOpenPayload = {
  variant_group_id: ids.group, task_id: ids.task, work_profile: bindingRefPayload, base_artifact_digest: d("5"), input_evidence_set_digest: d("6"),
  toolchain_digest: d("7"), dependency_lock_digest: d("8"), acceptance_manifest_digest: d("9"), candidate_count: 2, valid_candidate_quorum: 2,
  required_independence_dimensions: ["ACTOR", "EXECUTION", "CONTEXT", "WORKSPACE"], comparison_method_id: "structured-diff-v1",
  comparison_policy_digest: d("a"), materiality_policy_digest: d("b"), comparator: { kind: "ACTOR", id: "teams::reviewer-1" },
  adjudicator: { kind: "HUMAN", id: "principal" }, submission_deadline_at: "2026-08-14T00:00:00Z", decision_deadline_at: "2026-08-15T00:00:00Z",
  replacement_budget: 1, evidence_ids: [ids.evidence],
};
const candidatePayload = {
  variant_group_id: ids.group, expected_group_revision: 1, candidate_id: ids.candidateA, actor_fqn: "teams::coder-1", execution_id: ids.execution,
  fencing_epoch: 1, model_profile_digest: d("f"), runtime_identity_digest: d("1"), context_digest: d("2"), workspace_digest: d("3"),
  artifact_digest: d("4"), changed_file_inventory_digest: d("5"), deterministic_gate_receipt_ids: [ids.evidence], assumptions: [], unresolved_exceptions: [], evidence_ids: [ids.evidence],
};
const comparisonPayload = {
  variant_group_id: ids.group, expected_group_revision: 3, comparator: { kind: "ACTOR", id: "teams::reviewer-1" }, comparator_execution_id: "00000000-0000-7000-8000-000000000811",
  candidate_ids: [ids.candidateA, ids.candidateB], classification: "MATERIAL_AGREEMENT", comparison_method_id: "structured-diff-v1", comparison_receipt_digest: d("6"),
  reasons: ["behavior and architecture agree"], evidence_ids: [ids.evidence],
};
const selectionPayload = { variant_group_id: ids.group, expected_group_revision: 4, comparison_event_id: ids.comparison, adjudicator: { kind: "HUMAN", id: "principal" }, outcome: "SELECT_CANDIDATE", selected_candidate_id: ids.candidateA, reasons: ["candidate satisfies frozen policy"], evidence_ids: [ids.evidence] };

const catalogueFixturesPath = "fixtures/catalogue-coverage.json";
const catalogueFixtures = readJson(`CONTRACTS/${contractIdentity}/${catalogueFixturesPath}`);
function catalogueFixture(fixtureId, classification, commandType, payload, outcomeCode, eventTypes) {
  return { classification, fixtureId, kind: "CATALOGUE_COMMAND", sourceDecisionIds: ["MC-DEC-008"], given: { contractManifest: contractIdentity }, when: { commandType, payload }, then: { expected: { outcomeCode, eventTypes } } };
}
for (const fixture of catalogueFixtures.fixtures) {
  if (fixture.classification !== "NORMATIVE_EXAMPLE") continue;
  const payload = fixture.when?.payload;
  if (fixture.when?.commandType === "tekroo.command.completion-review.open") {
    Object.assign(payload, { scope_revision: 1, work_profile: bindingRefPayload, candidate_artifact_digest: d("7"), implementer: { principal: { kind: "ACTOR", id: "teams::coder-1" }, actor_fqn: "teams::coder-1", execution_id: ids.execution, fencing_epoch: 1, model_profile_digest: d("f"), workspace_digest: d("8"), context_digest: d("9") }, verification_topology_digest: d("c"), variant_group_id: null });
    for (const branch of payload.branches) Object.assign(branch, { required_independence_dimensions: ["PRINCIPAL", "ACTOR", "EXECUTION", "CONTEXT", "WORKSPACE", "METHOD"], required_method_ids: ["go-test"] });
  }
  if (fixture.when?.commandType === "tekroo.command.completion-review.record-result") Object.assign(payload, { candidate_artifact_digest: d("7"), independence_receipt: { proven_dimensions: ["PRINCIPAL", "ACTOR", "EXECUTION", "CONTEXT", "WORKSPACE", "METHOD"], identity_comparison_digest: d("d"), method_ids: ["go-test"], evidence_ids: [ids.evidence] } });
  if (fixture.when?.commandType === "tekroo.command.completion-review.finalize") Object.assign(payload, { scope_revision: 1, work_profile: bindingRefPayload, candidate_artifact_digest: d("7"), verification_topology_digest: d("c") });
}
catalogueFixtures.fixtures.push(
  catalogueFixture("CAT-037-WORK-PROFILE-BIND-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.task.bind-work-profile", bindingPayload, "APPLIED", ["tekroo.event.task.work-profile-bound"]),
  catalogueFixture("CAT-037-WORK-PROFILE-BIND-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.task.bind-work-profile", { ...bindingPayload, scope_revision: 0 }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-038-QUALIFIED-ASSIGNMENT-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.task.authorize-qualified-assignment", assignmentPayload, "APPLIED", ["tekroo.event.task.qualified-assignment-authorized"]),
  catalogueFixture("CAT-038-QUALIFIED-ASSIGNMENT-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.task.authorize-qualified-assignment", { ...assignmentPayload, hard_constraint_results: [] }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-039-VARIANT-GROUP-OPEN-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.variant-group.open", variantOpenPayload, "APPLIED", ["tekroo.event.variant-group.opened"]),
  catalogueFixture("CAT-039-VARIANT-GROUP-OPEN-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.variant-group.open", { ...variantOpenPayload, candidate_count: 1 }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-040-VARIANT-CANDIDATE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.variant-group.submit-candidate", candidatePayload, "APPLIED", ["tekroo.event.variant-group.candidate-submitted"]),
  catalogueFixture("CAT-040-VARIANT-CANDIDATE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.variant-group.submit-candidate", { ...candidatePayload, deterministic_gate_receipt_ids: [] }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-041-VARIANT-COMPARISON-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.variant-group.record-comparison", comparisonPayload, "APPLIED", ["tekroo.event.variant-group.comparison-recorded"]),
  catalogueFixture("CAT-041-VARIANT-COMPARISON-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.variant-group.record-comparison", { ...comparisonPayload, candidate_ids: [ids.candidateA] }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-042-VARIANT-SELECTION-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.variant-group.select", selectionPayload, "APPLIED", ["tekroo.event.variant-group.selected"]),
  catalogueFixture("CAT-042-VARIANT-SELECTION-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.variant-group.select", { ...selectionPayload, selected_candidate_id: undefined }, "REJECTED_INVALID", []),
);
catalogueFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(catalogueFixturesPath, catalogueFixtures);

const modelFixturesPath = "fixtures/model-and-invariant-scenarios.json";
const modelFixtures = readJson(`CONTRACTS/${contractIdentity}/${modelFixturesPath}`);
function policyFixture(fixtureId, given, when, expected) {
  return { classification: expected.accepted ? "NORMATIVE_EXAMPLE" : "COUNTEREXAMPLE", fixtureId, kind: "MODEL_CAPABILITY_POLICY_MODEL", sourceDecisionIds: ["MC-DEC-002", "MC-DEC-003", "MC-DEC-004", "MC-DEC-005", "MC-DEC-007", "MC-DEC-008"], given, when, then: { expected } };
}
modelFixtures.fixtures.push(
  policyFixture("MC-SCOPE-BINDING-EXACT", { scopeRevision: 2, profileScopeRevision: 2 }, { action: "BIND_PROFILE" }, { accepted: true, reason: "ACCEPTED" }),
  policyFixture("MC-SCOPE-BINDING-STALE", { scopeRevision: 2, profileScopeRevision: 1 }, { action: "BIND_PROFILE" }, { accepted: false, reason: "STALE_WORK_PROFILE" }),
  policyFixture("MC-ROUTINE-REVISION-PRESERVES-PROFILE", { scopeRevision: 2, profileScopeRevision: 2, aggregateRevision: 19 }, { action: "BIND_PROFILE" }, { accepted: true, reason: "ACCEPTED" }),
  policyFixture("MC-ASSIGNMENT-EXACT-QUALIFICATION", { requiredRoute: "COMPLEX_REASONING", selectedRoute: "COMPLEX_REASONING", qualificationStatus: "PASS", revoked: false, hardConstraintsPass: true }, { action: "AUTHORIZE_ASSIGNMENT" }, { accepted: true, reason: "ACCEPTED" }),
  policyFixture("MC-ASSIGNMENT-DOWNGRADE-REJECTED", { requiredRoute: "COMPLEX_REASONING", selectedRoute: "BOUNDED_EXECUTION", qualificationStatus: "PASS", revoked: false, hardConstraintsPass: true }, { action: "AUTHORIZE_ASSIGNMENT" }, { accepted: false, reason: "CAPABILITY_MISMATCH" }),
  policyFixture("MC-ASSIGNMENT-REVOKED-REJECTED", { requiredRoute: "BOUNDED_EXECUTION", selectedRoute: "BOUNDED_EXECUTION", qualificationStatus: "PASS", revoked: true, hardConstraintsPass: true }, { action: "AUTHORIZE_ASSIGNMENT" }, { accepted: false, reason: "QUALIFICATION_REVOKED" }),
  policyFixture("MC-VERIFICATION-TOPOLOGY-EXACT", { requiredDimensions: ["ACTOR", "EXECUTION", "METHOD"], provenDimensions: ["ACTOR", "EXECUTION", "METHOD"] }, { action: "FINALIZE_REVIEW" }, { accepted: true, reason: "ACCEPTED" }),
  policyFixture("MC-VERIFICATION-TOPOLOGY-MISSING", { requiredDimensions: ["ACTOR", "EXECUTION", "METHOD"], provenDimensions: ["ACTOR", "METHOD"] }, { action: "FINALIZE_REVIEW" }, { accepted: false, reason: "INDEPENDENCE_NOT_PROVEN" }),
  policyFixture("MC-VARIANT-MATERIAL-DISAGREEMENT-ESCALATES", { classification: "MATERIAL_DISAGREEMENT", submittedCandidateIds: [ids.candidateA, ids.candidateB] }, { action: "ESCALATE" }, { accepted: true, reason: "ESCALATION_REQUIRED" }),
  policyFixture("MC-VARIANT-MATERIAL-DISAGREEMENT-NO-SELECTION", { classification: "MATERIAL_DISAGREEMENT", submittedCandidateIds: [ids.candidateA, ids.candidateB] }, { action: "SELECT", selectedCandidateId: ids.candidateA }, { accepted: false, reason: "MATERIAL_DISAGREEMENT" }),
  policyFixture("MC-VARIANT-UNSUBMITTED-CANDIDATE-REJECTED", { classification: "MATERIAL_AGREEMENT", submittedCandidateIds: [ids.candidateA, ids.candidateB] }, { action: "SELECT", selectedCandidateId: ids.execution }, { accepted: false, reason: "CANDIDATE_NOT_SUBMITTED" }),
);
modelFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(modelFixturesPath, modelFixtures);

const fixtureSchemaPath = "schemas/conformance-fixture.schema.json";
const fixtureSchema = readJson(`CONTRACTS/${contractIdentity}/${fixtureSchemaPath}`);
fixtureSchema.properties.kind.enum.push("MODEL_CAPABILITY_POLICY_MODEL");
fixtureSchema.properties.kind.enum.sort();
overwriteJson(fixtureSchemaPath, fixtureSchema);

const newCatalogueFixtureIds = catalogueFixtures.fixtures.filter((fixture) => /^CAT-0(37|38|39|40|41|42)-/.test(fixture.fixtureId)).map((fixture) => fixture.fixtureId).sort();
const newModelFixtureIds = modelFixtures.fixtures.filter((fixture) => fixture.fixtureId.startsWith("MC-")).map((fixture) => fixture.fixtureId).sort();
const invariantsPath = "invariants/invariants.json";
const invariants = readJson(`CONTRACTS/${contractIdentity}/${invariantsPath}`);
invariants.invariants.push(
  { invariantId: "INV-023-SCOPE-BOUND-WORK-PROFILE", description: "A live model assignment requires an exact non-stale work profile bound to task lifecycle and semantic scope; routine aggregate revisions neither stale nor refresh it.", decisionIds: ["MC-DEC-002", "MC-DEC-008"], negativeRequirementIds: [], fixtureIds: ["MC-SCOPE-BINDING-EXACT", "MC-SCOPE-BINDING-STALE", "MC-ROUTINE-REVISION-PRESERVES-PROFILE", "CAT-037-WORK-PROFILE-BIND-VALID", "CAT-037-WORK-PROFILE-BIND-INVALID"] },
  { invariantId: "INV-024-QUALIFIED-ASSIGNMENT-FAILS-CLOSED", description: "Live assignment is authorized only for an exact passing, non-revoked profile satisfying the required route and every hard constraint; no downgrade or unexplained eligibility Boolean has authority.", decisionIds: ["MC-DEC-001", "MC-DEC-006", "MC-DEC-007", "MC-DEC-008"], negativeRequirementIds: [], fixtureIds: ["MC-ASSIGNMENT-EXACT-QUALIFICATION", "MC-ASSIGNMENT-DOWNGRADE-REJECTED", "MC-ASSIGNMENT-REVOKED-REJECTED", "CAT-038-QUALIFIED-ASSIGNMENT-VALID", "CAT-038-QUALIFIED-ASSIGNMENT-INVALID"] },
  { invariantId: "INV-025-EXACT-VERIFICATION-TOPOLOGY", description: "Completion finalization proves the frozen candidate, work profile, gates, and every required independence dimension; missing topology is inconclusive rather than PASS.", decisionIds: ["MC-DEC-003", "MC-DEC-008"], negativeRequirementIds: [], fixtureIds: ["MC-VERIFICATION-TOPOLOGY-EXACT", "MC-VERIFICATION-TOPOLOGY-MISSING", "CAT-024-COMPLETION-REVIEW-OPEN-VALID", "CAT-025-COMPLETION-REVIEW-RECORD-RESULT-VALID", "CAT-027-COMPLETION-REVIEW-FINALIZE-VALID"] },
  { invariantId: "INV-026-VARIANT-ISOLATION-AND-SINGLE-SELECTION", description: "A frozen variant group accepts only immutable independent submissions and selects exactly one submitted candidate only after non-conflicting comparison; material disagreement escalates and patches are never automatically blended.", decisionIds: ["MC-DEC-005", "MC-DEC-008"], negativeRequirementIds: [], fixtureIds: ["MC-VARIANT-MATERIAL-DISAGREEMENT-ESCALATES", "MC-VARIANT-MATERIAL-DISAGREEMENT-NO-SELECTION", "MC-VARIANT-UNSUBMITTED-CANDIDATE-REJECTED", ...newCatalogueFixtureIds.filter((id) => /VARIANT/.test(id))] },
  { invariantId: "INV-027-EVIDENCE-DRIVEN-PROMOTION", description: "Promotion and escalation use the approved typed trigger vocabulary, exact evidence, finite budgets, and a new fenced execution or successor; confidence and provider outage cannot authorize downgrade.", decisionIds: ["MC-DEC-004", "MC-DEC-007", "MC-DEC-008"], negativeRequirementIds: [], fixtureIds: ["MC-ASSIGNMENT-DOWNGRADE-REJECTED", "MC-VARIANT-MATERIAL-DISAGREEMENT-ESCALATES"] },
);
invariants.invariants.sort((left, right) => left.invariantId.localeCompare(right.invariantId));
overwriteJson(invariantsPath, invariants);

const traceabilityPath = "traceability/traceability.json";
const traceability = readJson(`CONTRACTS/${contractIdentity}/${traceabilityPath}`);
const testsByDecision = {
  "MC-DEC-001": ["INV-024-QUALIFIED-ASSIGNMENT-FAILS-CLOSED", "MC-ASSIGNMENT-DOWNGRADE-REJECTED"],
  "MC-DEC-002": ["INV-023-SCOPE-BOUND-WORK-PROFILE", "MC-SCOPE-BINDING-EXACT", "MC-SCOPE-BINDING-STALE", "MC-ROUTINE-REVISION-PRESERVES-PROFILE", ...newCatalogueFixtureIds.filter((id) => /WORK-PROFILE/.test(id))],
  "MC-DEC-003": ["INV-025-EXACT-VERIFICATION-TOPOLOGY", "MC-VERIFICATION-TOPOLOGY-EXACT", "MC-VERIFICATION-TOPOLOGY-MISSING"],
  "MC-DEC-004": ["INV-027-EVIDENCE-DRIVEN-PROMOTION", "MC-VARIANT-MATERIAL-DISAGREEMENT-ESCALATES"],
  "MC-DEC-005": ["INV-026-VARIANT-ISOLATION-AND-SINGLE-SELECTION", "MC-VARIANT-MATERIAL-DISAGREEMENT-NO-SELECTION", "MC-VARIANT-UNSUBMITTED-CANDIDATE-REJECTED", ...newCatalogueFixtureIds.filter((id) => /VARIANT/.test(id))],
  "MC-DEC-006": ["INV-024-QUALIFIED-ASSIGNMENT-FAILS-CLOSED", "MC-ASSIGNMENT-EXACT-QUALIFICATION"],
  "MC-DEC-007": ["INV-024-QUALIFIED-ASSIGNMENT-FAILS-CLOSED", "INV-027-EVIDENCE-DRIVEN-PROMOTION", "MC-ASSIGNMENT-REVOKED-REJECTED", ...newCatalogueFixtureIds.filter((id) => /QUALIFIED-ASSIGNMENT/.test(id))],
  "MC-DEC-008": ["INV-023-SCOPE-BOUND-WORK-PROFILE", "INV-024-QUALIFIED-ASSIGNMENT-FAILS-CLOSED", "INV-025-EXACT-VERIFICATION-TOPOLOGY", "INV-026-VARIANT-ISOLATION-AND-SINGLE-SELECTION", "INV-027-EVIDENCE-DRIVEN-PROMOTION", ...newCatalogueFixtureIds],
  "MC-DEC-009": ["INV-024-QUALIFIED-ASSIGNMENT-FAILS-CLOSED", "INV-025-EXACT-VERIFICATION-TOPOLOGY", "INV-026-VARIANT-ISOLATION-AND-SINGLE-SELECTION"],
};
const decisionText = [
  "Use the provider-neutral decision-route ladder and prohibit unqualified downgrade.",
  "Bind every live model task to an evidence-backed work-risk profile and semantic scope.",
  "Require exact separation of duty and independently proven validation dimensions.",
  "Promote and escalate only from typed evidence under finite policy.",
  "Use risk-driven isolated N-version work with structured comparison and exactly one selected successor.",
  "Apply role defaults only beneath the exact work-risk profile and choose the least costly qualified compliant route.",
  "Qualify and revoke exact deployable profiles with attributable provenance and hard-filter-before-cost economics.",
  "Encode the remediation in immutable contract 0.6.0/schema 1.5.0 with explicit fail-closed compatibility.",
  "Implement and forward-requalify before separately preregistering and authorizing live OpenHands-Q1.",
];
for (let index = 1; index <= 9; index += 1) {
  const requirementId = `MC-DEC-00${index}`;
  traceability.requirements.push({ disposition: "BINDING", kind: "DECISION", requirementId, testIds: [...new Set(testsByDecision[requirementId])].sort(), text: decisionText[index - 1] });
}
traceability.requirements.sort((left, right) => left.requirementId.localeCompare(right.requirementId));
overwriteJson(traceabilityPath, traceability);

const predecessorManifestBytes = fs.readFileSync(path.join(predecessorRoot, "manifest.json"));
const additiveTypes = catalogue.entries.map((entry) => entry.typeId).filter((typeId) => !predecessorTypeIds.includes(typeId)).sort();
const extendedVocabularyTypes = ["tekroo.command.escalation.open", "tekroo.event.escalation.opened"];
const unchangedSemanticTypes = predecessorTypeIds.filter((typeId) => !changedCompletionTypes.includes(typeId)).sort();
writeJson("compatibility/from-0.5.0.json", {
  schemaVersion,
  contractName: "tekroo.kernel.contracts",
  contractVersion,
  predecessor: { contractVersion: predecessorVersion, contractIdentity: predecessorIdentity, manifestSha256: sha256(predecessorManifestBytes) },
  compatibilityClaim: "ADDITIVE_MODEL_CAPABILITY_POLICY_WITH_CONTEXT_REQUIRED_VERIFICATION_REVISION",
  directions: { backward: "COMPATIBLE_ADAPTER_REQUIRED", forward: "NOT_COMPATIBLE", replay: "VERSION_GATED", adapter: "REQUIRED" },
  unchangedSemanticTypes,
  changedSemanticTypes: changedCompletionTypes,
  extendedVocabularyTypes,
  additiveTypes,
  migrationRules: [
    { source: predecessorSchemaVersion, target: schemaVersion, mode: "IDENTITY", appliesTo: unchangedSemanticTypes },
    { source: predecessorSchemaVersion, target: schemaVersion, mode: "CONTEXT_REQUIRED", appliesTo: changedCompletionTypes, requiredContext: ["scope_revision", "work_profile", "candidate_artifact_digest", "verification_topology_digest", "independence_receipt"] },
    { source: schemaVersion, target: schemaVersion, mode: "IDENTITY" },
  ],
  aggregateStateMigration: { mode: "EXPLICIT_ACTIVE_TASK_MIGRATION", requiredContext: ["lifecycle_epoch", "scope_revision", "work_profile_binding"], historicalReplay: "RETAIN_0.5.0" },
  prohibitedInferences: [
    "A 0.5.0 aggregate revision is not a semantic scope revision.",
    "Missing work-profile, qualification, assignment, candidate, or independence evidence is never invented during upconversion.",
    "A historical completion PASS does not prove the 1.5.0 verification topology.",
    "An old eligibility Boolean does not prove exact-profile qualification.",
    "New escalation triggers are not inferred from historical narrative or aggregate state.",
  ],
  rollback: "Readers retain 0.5.0 for historical replay. New work-profile, qualified-assignment, variant-group, and verification-topology records are never down-converted.",
});

let referenceRunner = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/reference-runner.mjs"), "utf8"));
referenceRunner = referenceRunner.replace(
  "function runSuccessorSetModel(fixture) {",
  `function runModelCapabilityPolicyModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  const rank = { DETERMINISTIC: 0, BOUNDED_EXECUTION: 1, COMPLEX_REASONING: 2, NOVEL_REASONING: 3, HUMAN_REQUIRED: 4 };
  if (action.action === "BIND_PROFILE") {
    if (current.scopeRevision !== current.profileScopeRevision) return { accepted: false, reason: "STALE_WORK_PROFILE" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "AUTHORIZE_ASSIGNMENT") {
    if (current.revoked) return { accepted: false, reason: "QUALIFICATION_REVOKED" };
    if (current.qualificationStatus !== "PASS") return { accepted: false, reason: "QUALIFICATION_REQUIRED" };
    if (!current.hardConstraintsPass) return { accepted: false, reason: "HARD_CONSTRAINT_FAILED" };
    if (rank[current.selectedRoute] < rank[current.requiredRoute]) return { accepted: false, reason: "CAPABILITY_MISMATCH" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "FINALIZE_REVIEW") {
    const proven = new Set(current.provenDimensions);
    if (!current.requiredDimensions.every((dimension) => proven.has(dimension))) return { accepted: false, reason: "INDEPENDENCE_NOT_PROVEN" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "ESCALATE" && current.classification === "MATERIAL_DISAGREEMENT") return { accepted: true, reason: "ESCALATION_REQUIRED" };
  if (action.action === "SELECT") {
    if (current.classification === "MATERIAL_DISAGREEMENT") return { accepted: false, reason: "MATERIAL_DISAGREEMENT" };
    if (!current.submittedCandidateIds.includes(action.selectedCandidateId)) return { accepted: false, reason: "CANDIDATE_NOT_SUBMITTED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  return { accepted: false, reason: "INVALID_ACTION" };
}

function runSuccessorSetModel(fixture) {`,
);
referenceRunner = referenceRunner.replace(
  '  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
  '  } else if (fixture.kind === "MODEL_CAPABILITY_POLICY_MODEL") {\n    actual = runModelCapabilityPolicyModel(fixture);\n  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
);
fs.mkdirSync(path.join(packageRoot, "runner"), { recursive: true });
fs.writeFileSync(path.join(packageRoot, "runner/reference-runner.mjs"), referenceRunner, { flag: "wx" });

let validator = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/validate-package.mjs"), "utf8"))
  .replace('manifest.contract.version === "0.5.0"', 'manifest.contract.version === "0.6.0"')
  .replace("decisions.length === 13", "decisions.length === 22")
  .replaceAll("compatibility/from-0.4.0.json", "compatibility/from-0.5.0.json")
  .replace(
    'compatibility.predecessor?.contractVersion === "0.4.0" && compatibility.contractVersion === "0.5.0" && compatibility.directions?.adapter === "REQUIRED"',
    'compatibility.predecessor?.contractVersion === "0.5.0" && compatibility.contractVersion === "0.6.0" && compatibility.directions?.adapter === "REQUIRED"',
  );
fs.writeFileSync(path.join(packageRoot, "runner/validate-package.mjs"), `${validator.trimEnd()}\n`, { flag: "wx" });

function roleFor(relativePath) {
  if (relativePath.startsWith("catalogue/")) return "catalogue";
  if (relativePath.startsWith("schemas/")) return "schemas";
  if (relativePath.startsWith("fixtures/")) return "fixtures";
  if (relativePath.startsWith("invariants/")) return "invariants";
  if (relativePath.startsWith("traceability/")) return "traceability";
  if (relativePath.startsWith("compatibility/")) return "compatibility";
  if (relativePath.startsWith("runner/")) return relativePath.endsWith(".json") ? "runner-protocol" : "runner-tool";
  throw new Error(`unknown package role for ${relativePath}`);
}

function dependenciesFor(relativePath) {
  if (relativePath === "catalogue/kernel-catalogue.json") return ["schemas/payloads.schema.json"];
  if (relativePath.startsWith("fixtures/")) return ["catalogue/kernel-catalogue.json", "schemas/conformance-fixture.schema.json", "schemas/payloads.schema.json"];
  if (relativePath.startsWith("invariants/")) return ["fixtures/catalogue-coverage.json", "fixtures/model-and-invariant-scenarios.json"];
  if (relativePath.startsWith("traceability/")) return ["invariants/invariants.json", "fixtures/catalogue-coverage.json", "fixtures/model-and-invariant-scenarios.json"];
  if (relativePath === "runner/reference-runner.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "fixtures/catalogue-coverage.json", "fixtures/model-and-invariant-scenarios.json"];
  if (relativePath === "runner/validate-package.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "invariants/invariants.json", "traceability/traceability.json", "compatibility/from-0.5.0.json", "runner/protocol.json"];
  if (relativePath.startsWith("schemas/") && relativePath !== "schemas/core.schema.json") return ["schemas/core.schema.json"];
  return [];
}

const payloadPaths = walk(packageRoot).filter((relativePath) => !["manifest.json", "manifest.sha256"].includes(relativePath));
const fileInventory = payloadPaths.map((relativePath) => {
  const bytes = fs.readFileSync(path.join(packageRoot, relativePath));
  const isJson = relativePath.endsWith(".json");
  return { path: relativePath, role: roleFor(relativePath), mediaType: isJson ? "application/json" : "text/javascript", bytes: bytes.length, sha256: sha256(bytes), canonicalJsonSha256: isJson ? sha256(canonical(JSON.parse(bytes.toString("utf8")))) : null, dependencies: dependenciesFor(relativePath) };
});
const generatorPath = "scripts/generate_phase3_contract_0_6.mjs";
const manifest = {
  schemaVersion,
  contract: { name: "tekroo.kernel.contracts", version: contractVersion, status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-08-13T00:00:00Z",
  authority: { decisionAuthority: "Principal", implementationAuthority: "PENDING_CANDIDATE_ACCEPTANCE", productionAuthority: "NONE" },
  sourceLineage: [
    { path: "docs/architecture/020-model-capability-and-verification-policy.md", sha256: sha256(read("docs/architecture/020-model-capability-and-verification-policy.md")) },
    { path: "OUTPUT/phase-3/model-capability-remediation-impact.json", sha256: sha256(read("OUTPUT/phase-3/model-capability-remediation-impact.json")) },
    { path: `CONTRACTS/${predecessorIdentity}/manifest.json`, sha256: sha256(predecessorManifestBytes) },
    { path: generatorPath, sha256: sha256(read(generatorPath)) },
  ],
  canonicalization: { textEncoding: "UTF-8", byteOrderMark: false, lineEnding: "LF", jsonSemanticCanonicalization: "RFC-8785-compatible sorted-key canonical JSON", byteDigest: "SHA-256", manifestDigest: "detached manifest.sha256" },
  inventoryBoundary: "files lists every payload asset; manifest.json is identified by detached manifest.sha256 and manifest.sha256 is not self-listed.",
  counts: {
    catalogueEntries: catalogue.entries.length,
    commandTypes: catalogue.entries.filter((entry) => entry.kind === "COMMAND").length,
    eventTypes: catalogue.entries.filter((entry) => entry.kind === "EVENT").length,
    schemas: fileInventory.filter((item) => item.role === "schemas").length,
    fixtures: catalogueFixtures.fixtures.length + modelFixtures.fixtures.length,
    invariants: invariants.invariants.length,
    traceabilityRequirements: traceability.requirements.length,
  },
  profiles: [
    { name: "contract-structure", authorization: "MODEL_CAPABILITY_DECISION_9", required: true, currentStatus: "READY_TO_RUN" },
    { name: "core-hermetic", authorization: "AFTER_PRINCIPAL_CANDIDATE_ACCEPTANCE", required: true, currentStatus: "NOT_RUN" },
    { name: "mongo-integration", authorization: "AFTER_PRINCIPAL_CANDIDATE_ACCEPTANCE", required: true, currentStatus: "NOT_RUN" },
    { name: "synthesized-merge", authorization: "AFTER_PRINCIPAL_CANDIDATE_ACCEPTANCE", required: true, currentStatus: "NOT_RUN" },
    { name: "provider-e2e", authorization: "SEPARATE_PRINCIPAL_AUTHORIZATION", required: false, currentStatus: "NOT_RUN" },
  ],
  files: fileInventory,
  knownLimitations: [
    "Contract-structure PASS does not qualify the Go implementation, MongoDB integration, OpenHands, SMA, any provider, or production operation.",
    "Concrete model profiles, qualification corpora, prices, credentials, and provider execution remain outside the provider-neutral kernel package.",
    "Active 0.5.0 tasks require explicit semantic-scope and work-profile migration before live model execution; no automatic backfill is defined.",
    "OpenHands-Q1 has not been preregistered or executed, and live provider execution requires separate principal authority.",
    "Performance, reliability, and economic claims require later attributable measured evidence.",
  ],
};
fs.writeFileSync(manifestPath, pretty(manifest), { flag: "wx" });
const manifestDigest = sha256(fs.readFileSync(manifestPath));
fs.writeFileSync(checksumPath, `${manifestDigest}  manifest.json\n`, { flag: "wx" });
process.stdout.write(`GENERATED ${contractIdentity} ${manifestDigest} ${fileInventory.length}\n`);
