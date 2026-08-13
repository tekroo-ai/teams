#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const predecessorVersion = "0.6.0";
const contractVersion = "0.7.0";
const predecessorSchemaVersion = "1.5.0";
const schemaVersion = "1.6.0";
const predecessorIdentity = `tekroo.kernel.contracts/${predecessorVersion}`;
const contractIdentity = `tekroo.kernel.contracts/${contractVersion}`;
const predecessorRoot = path.join(repoRoot, `CONTRACTS/${predecessorIdentity}`);
const packageRoot = path.join(repoRoot, `CONTRACTS/${contractIdentity}`);
const manifestPath = path.join(packageRoot, "manifest.json");
const checksumPath = path.join(packageRoot, "manifest.sha256");

if (fs.existsSync(packageRoot)) throw new Error(`contract ${contractVersion} already exists; refusing in-place regeneration`);

const uuid7Pattern = "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$";
const shaPattern = "^[0-9a-f]{64}$";
const actorPattern = "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$";
const operatorActorPattern = "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::operator-[1-9][0-9]{0,19}$";

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
    .replaceAll("_1_5_0", "_1_6_0")
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
const boolean = (options = {}) => ({ type: "boolean", ...options });
const array = (items, options = {}) => ({ type: "array", items, ...options });
const object = (properties, required = Object.keys(properties)) => ({ type: "object", additionalProperties: false, properties, required });
const nullable = (schema) => ({ anyOf: [schema, { type: "null" }] });
const uuid = () => string({ pattern: uuid7Pattern });
const digest = () => string({ pattern: shaPattern });
const actor = () => string({ pattern: actorPattern });
const operatorActor = () => string({ pattern: operatorActorPattern });
const nonempty = (maxLength = 4096) => string({ minLength: 1, maxLength });
const uuidList = (options = {}) => array(uuid(), { maxItems: 1024, uniqueItems: true, ...options });
const stringList = (options = {}) => array(nonempty(), { maxItems: 128, uniqueItems: true, ...options });
const principal = () => object({ kind: string({ enum: ["ACTOR", "HUMAN", "SERVICE", "POLICY"] }), id: nonempty(256) });
const humanPrincipal = () => object({ kind: { const: "HUMAN" }, id: nonempty(256) });

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
  "compatibility/from-0.5.0.json",
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
for (const kind of ["human-interaction", "human-participant"]) {
  if (!core.$defs.AggregateRef.properties.kind.enum.includes(kind)) core.$defs.AggregateRef.properties.kind.enum.push(kind);
}
core.$defs.AggregateRef.properties.kind.enum.sort();
overwriteJson(corePath, core);

const capabilityIds = [
  "COORDINATE_DAG",
  "INSPECT_ORGANIZATION",
  "INSPECT_EXECUTION",
  "INVOKE_AUTHORIZED_COMMAND",
  "PREPARE_DECISION",
  "PROPOSE_ASSIGNMENT",
  "PROPOSE_DECOMPOSITION",
  "PROPOSE_ESCALATION",
  "ROUTE_HUMAN_REQUIRED",
  "SURFACE_OPERATIONAL_CONDITION",
];
const dispositionKinds = ["CANCEL_ON_SUSPEND", "COMPLETE_AND_QUARANTINE", "DETACH_AND_RECONCILE"];
const controlStates = ["ACTIVE", "QUIESCING", "SUSPENDED", "RECONCILING"];

const operatorRoleBinding = object({
  binding_id: uuid(),
  expected_system_revision: integer({ minimum: 0 }),
  role_id: { const: "operator" },
  operator_actor_fqn: operatorActor(),
  role_bundle_version: nonempty(128),
  role_bundle_digest: digest(),
  role_bundle_signature_evidence_ids: uuidList({ minItems: 1, maxItems: 16 }),
  role_definition_digest: digest(),
  capability_ids: array(string({ enum: capabilityIds }), { minItems: 1, maxItems: capabilityIds.length, uniqueItems: true }),
  human_selection_policy_revision: integer({ minimum: 1 }),
  human_selection_policy_digest: digest(),
  authority_policy_revision: integer({ minimum: 1 }),
  authority_policy_digest: digest(),
  replaces_binding_id: nullable(uuid()),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});

writeJson("schemas/operator-role-profile.schema.json", {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `https://contracts.tekroo.ai/${contractIdentity}/schemas/operator-role-profile.schema.json`,
  title: "Explicit operator actor role binding distinct from human principal and control surface",
  ...operatorRoleBinding,
});

const confidentialityClasses = ["PUBLIC", "INTERNAL", "CONFIDENTIAL", "RESTRICTED"];
const roleClasses = ["APPROVER", "CLIENT", "END_USER", "PRINCIPAL", "PROJECT_DEFINED", "SME"];
const interactionPurposes = ["ADVISORY_CONSULTATION", "AUTHORIZED_DECISION", "EVIDENTIARY_QUESTION", "REQUIREMENTS_CLARIFICATION", "ACCEPTANCE_FEEDBACK"];
const interactionEffects = ["ADVISORY_ONLY", "AUTHORITY_IF_AUTHORIZED", "EVIDENCE_ONLY"];

const humanRoleBinding = () => object({
  role_binding_id: uuid(),
  role_class: string({ enum: roleClasses }),
  role_id: nonempty(128),
  scope_kind: string({ enum: ["DOMAIN", "PROJECT", "SYSTEM", "WORK"] }),
  scope_id: nonempty(256),
  advisory_topic_ids: stringList({ maxItems: 64 }),
  authorized_command_types: array(string({ pattern: "^tekroo\\.command\\.[a-z0-9]+(?:[.-][a-z0-9]+)*$" }), { maxItems: 64, uniqueItems: true }),
  authority_policy_revision: integer({ minimum: 1 }),
  authority_policy_digest: digest(),
  valid_from: string({ format: "date-time" }),
  valid_until: nullable(string({ format: "date-time" })),
  active: { const: true },
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const authenticationBinding = () => object({
  authentication_binding_id: uuid(),
  method: string({ enum: ["API_CREDENTIAL", "CHANNEL_IDENTITY", "IN_PERSON_ATTESTATION", "MAGIC_LINK", "OIDC", "WEBAUTHN"] }),
  issuer_digest: digest(),
  subject_digest: digest(),
  assurance: string({ enum: ["HIGH", "STANDARD"] }),
  binding_revision: integer({ minimum: 1 }),
  active: { const: true },
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const deliveryBinding = () => object({
  delivery_binding_id: uuid(),
  channel: string({ enum: ["EMAIL", "OTHER", "SLACK", "SMS", "TEAMS", "WEB_PORTAL"] }),
  endpoint_digest: digest(),
  adapter_profile_digest: digest(),
  confidentiality_ceiling: string({ enum: confidentialityClasses }),
  authentication_binding_ids: uuidList({ minItems: 1, maxItems: 16 }),
  binding_revision: integer({ minimum: 1 }),
  active: { const: true },
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const humanParticipantProfile = object({
  participant: humanPrincipal(),
  expected_participant_revision: integer({ minimum: 0 }),
  profile_id: uuid(),
  profile_revision: integer({ minimum: 1 }),
  profile_digest: digest(),
  display_label: nonempty(256),
  privacy_classification: string({ enum: confidentialityClasses }),
  role_bindings: array(humanRoleBinding(), { minItems: 1, maxItems: 64 }),
  authentication_bindings: array(authenticationBinding(), { minItems: 1, maxItems: 32 }),
  delivery_bindings: array(deliveryBinding(), { minItems: 1, maxItems: 32 }),
  participant_policy_revision: integer({ minimum: 1 }),
  participant_policy_digest: digest(),
  supersedes_profile_id: nullable(uuid()),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const revokeHumanParticipant = object({
  participant: humanPrincipal(),
  expected_participant_revision: integer({ minimum: 1 }),
  expected_profile_id: uuid(),
  reason: nonempty(4096),
  revoked_at: string({ format: "date-time" }),
  revoked_by: principal(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});

writeJson("schemas/human-participant-profile.schema.json", {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `https://contracts.tekroo.ai/${contractIdentity}/schemas/human-participant-profile.schema.json`,
  title: "Versioned human participant with scoped roles and opaque authentication and delivery bindings",
  ...humanParticipantProfile,
});

const humanRecipient = () => object({
  principal: humanPrincipal(),
  participant_profile_id: uuid(),
  participant_profile_revision: integer({ minimum: 1 }),
  participant_profile_digest: digest(),
  role_binding_id: uuid(),
  delivery_binding_id: uuid(),
});
const responsePolicy = () => ({
  oneOf: [
    object({ kind: { const: "EXACT_ONE" } }),
    object({ kind: { const: "ANY_ONE" } }),
    object({ kind: { const: "ALL" } }),
    object({ kind: { const: "QUORUM" }, quorum: integer({ minimum: 1, maximum: 64 }) }),
  ],
});
const nullableActor = () => nullable(actor());
const nullableUuid = () => nullable(uuid());
const nullableEpoch = () => nullable(integer({ minimum: 1 }));
const openHumanInteraction = object({
  interaction_id: uuid(),
  subject_kind: string({ enum: ["escalation", "story", "task"] }),
  subject_id: uuid(),
  subject_lifecycle_epoch: integer({ minimum: 1 }),
  expected_subject_revision: integer({ minimum: 1 }),
  question_revision: { const: 1 },
  canonical_question_digest: digest(),
  response_specification_digest: digest(),
  origin_principal: principal(),
  origin_actor_fqn: nullableActor(),
  origin_execution_id: nullableUuid(),
  origin_execution_fencing_epoch: nullableEpoch(),
  recipients: array(humanRecipient(), { minItems: 1, maxItems: 64 }),
  purpose: string({ enum: interactionPurposes }),
  declared_effect: string({ enum: interactionEffects }),
  response_policy: responsePolicy(),
  deadline_at: string({ format: "date-time" }),
  timeout_policy: object({ kind: { const: "POLICY" }, id: nonempty(256) }),
  confidentiality: string({ enum: confidentialityClasses }),
  disclosure_scope_digest: digest(),
  interaction_policy_revision: integer({ minimum: 1 }),
  interaction_policy_digest: digest(),
  causal_path_event_ids: uuidList({ minItems: 1, maxItems: 64 }),
  predecessor_interaction_id: nullableUuid(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const recordHumanDelivery = object({
  interaction_id: uuid(),
  expected_interaction_revision: integer({ minimum: 1 }),
  question_revision: integer({ minimum: 1 }),
  delivery_id: uuid(),
  recipient: humanPrincipal(),
  delivery_binding_id: uuid(),
  adapter_profile_digest: digest(),
  canonical_question_digest: digest(),
  rendered_question_digest: digest(),
  presenter_actor_fqn: nullableActor(),
  presenter_execution_id: nullableUuid(),
  material_equivalence_evidence_id: nullableUuid(),
  outcome: string({ enum: ["DELIVERED", "FAILED", "UNKNOWN"] }),
  attempted_at: string({ format: "date-time" }),
  channel_provenance_digest: digest(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const recordHumanResponse = object({
  interaction_id: uuid(),
  expected_interaction_revision: integer({ minimum: 1 }),
  question_revision: integer({ minimum: 1 }),
  respondent: humanPrincipal(),
  participant_profile_id: uuid(),
  participant_profile_revision: integer({ minimum: 1 }),
  role_binding_id: uuid(),
  authentication_binding_id: uuid(),
  delivery_id: uuid(),
  delivery_event_id: uuid(),
  response_artifact_digest: digest(),
  response_specification_digest: digest(),
  response_classification: string({ enum: ["ANSWER", "DECLINE", "REQUEST_CLARIFICATION"] }),
  asserted_effect: string({ enum: interactionEffects }),
  credential_provenance_digest: digest(),
  channel_provenance_digest: digest(),
  responded_at: string({ format: "date-time" }),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const closeHumanInteraction = object({
  interaction_id: uuid(),
  expected_interaction_revision: integer({ minimum: 1 }),
  question_revision: integer({ minimum: 1 }),
  accepted_response_event_ids: uuidList({ minItems: 1, maxItems: 64 }),
  accepted_respondents: array(humanPrincipal(), { minItems: 1, maxItems: 64, uniqueItems: true }),
  response_policy_satisfied: { const: true },
  outcome: string({ enum: ["CONFLICT", "DECLINED", "SATISFIED", "UNAVAILABLE"] }),
  resolved_effect: string({ enum: ["ADVISORY_ONLY", "AUTHORIZED_EFFECT", "EVIDENCE_ONLY", "NO_EFFECT"] }),
  authorization_evidence_id: nullableUuid(),
  closed_at: string({ format: "date-time" }),
  closed_by: principal(),
  reasons: stringList({ minItems: 1, maxItems: 64 }),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const expireHumanInteraction = object({
  interaction_id: uuid(),
  expected_interaction_revision: integer({ minimum: 1 }),
  question_revision: integer({ minimum: 1 }),
  deadline_at: string({ format: "date-time" }),
  outcome: string({ enum: ["BLOCKED", "EXPIRED", "HUMAN_REQUIRED"] }),
  silence_is_consent: { const: false },
  expired_at: string({ format: "date-time" }),
  timeout_policy: object({ kind: { const: "POLICY" }, id: nonempty(256) }),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});

const humanParticipantState = object({
  id: uuid(),
  kind: { const: "human-participant" },
  revision: integer({ minimum: 1 }),
  principal: humanPrincipal(),
  status: string({ enum: ["ACTIVE", "REVOKED"] }),
  profile_id: uuid(),
  profile_revision: integer({ minimum: 1 }),
  profile_digest: digest(),
  role_binding_ids: uuidList({ minItems: 1, maxItems: 64 }),
  authentication_binding_ids: uuidList({ minItems: 1, maxItems: 32 }),
  delivery_binding_ids: uuidList({ minItems: 1, maxItems: 32 }),
});
const humanInteractionState = object({
  id: uuid(),
  kind: { const: "human-interaction" },
  revision: integer({ minimum: 1 }),
  phase: string({ enum: ["CLOSED", "COLLECTING", "EXPIRED", "OPEN", "SATISFIED"] }),
  question_revision: integer({ minimum: 1 }),
  canonical_question_digest: digest(),
  response_specification_digest: digest(),
  selected_human_ids: stringList({ minItems: 1, maxItems: 64 }),
  accepted_human_ids: stringList({ maxItems: 64 }),
  response_policy: responsePolicy(),
  declared_effect: string({ enum: interactionEffects }),
  confidentiality: string({ enum: confidentialityClasses }),
  deadline_at: string({ format: "date-time" }),
});
writeJson("schemas/human-interaction-state.schema.json", {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `https://contracts.tekroo.ai/${contractIdentity}/schemas/human-interaction-state.schema.json`,
  title: "Finite directed human question, delivery, response, and closure state",
  ...humanInteractionState,
});

const providerDisposition = () => object({
  provider_profile_digest: digest(),
  disposition: string({ enum: dispositionKinds }),
  quiesce_deadline_ms: integer({ minimum: 1, maximum: 3600000 }),
});
const configureContinuity = object({
  expected_system_revision: integer({ minimum: 0 }),
  initial_power_epoch: integer({ minimum: 1 }),
  operating_posture: string({ enum: ["CONTINUOUS", "PLANNED_SUSPEND_CAPABLE"] }),
  continuity_policy_revision: integer({ minimum: 1 }),
  continuity_policy_digest: digest(),
  health_requirement_digest: digest(),
  default_quiesce_timeout_ms: integer({ minimum: 1, maximum: 3600000 }),
  provider_dispositions: array(providerDisposition(), { minItems: 1, maxItems: 128 }),
  configured_by: principal(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const requestQuiescence = object({
  expected_system_revision: integer({ minimum: 1 }),
  expected_power_epoch: integer({ minimum: 1 }),
  next_power_epoch: integer({ minimum: 2 }),
  reason: string({ enum: ["HOST_MAINTENANCE", "LID_CLOSE", "OPERATOR_REQUEST", "POWER_EVENT", "ENERGY_POLICY"] }),
  requested_by: principal(),
  deadline_at: string({ format: "date-time" }),
  disposition_plan_digest: digest(),
  in_flight_execution_ids: uuidList(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const executionSuspension = () => object({
  execution_id: uuid(),
  actor_fqn: actor(),
  execution_fencing_epoch: integer({ minimum: 1 }),
  provider_profile_digest: digest(),
  disposition: string({ enum: dispositionKinds }),
  outcome: string({ enum: ["CANCELLED", "TERMINAL_CAPTURED", "DETACHED", "UNKNOWN"] }),
  result_evidence_id: nullable(uuid()),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const recordSuspended = object({
  expected_system_revision: integer({ minimum: 1 }),
  expected_power_epoch: integer({ minimum: 2 }),
  quiescence_request_event_id: uuid(),
  execution_records: array(executionSuspension(), { maxItems: 1024 }),
  unresolved_execution_ids: uuidList(),
  all_in_flight_executions_recorded: { const: true },
  recorded_by: principal(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const recordUnexpectedOutage = object({
  expected_system_revision: integer({ minimum: 1 }),
  observed_power_epoch: integer({ minimum: 1 }),
  next_power_epoch: integer({ minimum: 2 }),
  previous_control_state: string({ enum: controlStates }),
  detected_at: string({ format: "date-time" }),
  outage_kind: string({ enum: ["HOST_SLEEP", "HOST_RESTART", "NETWORK_PARTITION", "PROCESS_LOSS", "UNKNOWN"] }),
  known_in_flight_execution_ids: uuidList(),
  last_durable_event_id: nullable(uuid()),
  detected_by: principal(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const beginReconciliation = object({
  expected_system_revision: integer({ minimum: 1 }),
  expected_power_epoch: integer({ minimum: 1 }),
  trigger: string({ enum: ["HOST_RECOVERY", "OPERATOR_REQUEST", "WAKE"] }),
  unresolved_execution_ids: uuidList(),
  provider_snapshot_digest: digest(),
  initiated_by: principal(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const reconciliationRecord = () => object({
  execution_id: uuid(),
  outcome: string({ enum: ["BLOCKED", "CONFIRMED_CANCELLED", "HUMAN_REQUIRED", "REJECTED_LATE_RESULT", "SUPERSEDED", "TERMINAL_EVIDENCE_CAPTURED"] }),
  authoritative_state_digest: digest(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});
const resumeActive = object({
  expected_system_revision: integer({ minimum: 1 }),
  expected_power_epoch: integer({ minimum: 1 }),
  reconciliation_records: array(reconciliationRecord(), { maxItems: 1024 }),
  required_services_healthy: { const: true },
  outbox_reconciled: { const: true },
  change_stream_reconciled: { const: true },
  admission_reopen: { const: true },
  resumed_by: principal(),
  evidence_ids: uuidList({ minItems: 1, maxItems: 64 }),
});

const continuityState = object({
  id: uuid(),
  kind: { const: "system" },
  revision: integer({ minimum: 1 }),
  operating_posture: string({ enum: ["CONTINUOUS", "PLANNED_SUSPEND_CAPABLE"] }),
  control_state: string({ enum: controlStates }),
  power_epoch: integer({ minimum: 1 }),
  admission_open: boolean(),
  continuity_policy_revision: integer({ minimum: 1 }),
  continuity_policy_digest: digest(),
  health_requirement_digest: digest(),
  unresolved_execution_ids: uuidList(),
  last_transition_event_id: uuid(),
});
writeJson("schemas/team-continuity-state.schema.json", {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: `https://contracts.tekroo.ai/${contractIdentity}/schemas/team-continuity-state.schema.json`,
  title: "Team host-continuity control state",
  ...continuityState,
  allOf: [
    { if: { properties: { control_state: { const: "ACTIVE" } }, required: ["control_state"] }, then: { properties: { admission_open: { const: true }, unresolved_execution_ids: { maxItems: 0 } } } },
    { if: { properties: { control_state: { enum: ["QUIESCING", "SUSPENDED", "RECONCILING"] } }, required: ["control_state"] }, then: { properties: { admission_open: { const: false } } } },
  ],
});

const payloadPath = "schemas/payloads.schema.json";
const payloads = readJson(`CONTRACTS/${contractIdentity}/${payloadPath}`);
const payloadDefinitions = {
  tekroo_command_human_interaction_close_1_6_0: closeHumanInteraction,
  tekroo_event_human_interaction_closed_1_6_0: closeHumanInteraction,
  tekroo_command_human_interaction_expire_1_6_0: expireHumanInteraction,
  tekroo_event_human_interaction_expired_1_6_0: expireHumanInteraction,
  tekroo_command_human_interaction_open_1_6_0: openHumanInteraction,
  tekroo_event_human_interaction_opened_1_6_0: openHumanInteraction,
  tekroo_command_human_interaction_record_delivery_1_6_0: recordHumanDelivery,
  tekroo_event_human_interaction_delivery_recorded_1_6_0: recordHumanDelivery,
  tekroo_command_human_interaction_respond_1_6_0: recordHumanResponse,
  tekroo_event_human_interaction_response_recorded_1_6_0: recordHumanResponse,
  tekroo_command_human_participant_bind_profile_1_6_0: humanParticipantProfile,
  tekroo_event_human_participant_profile_bound_1_6_0: humanParticipantProfile,
  tekroo_command_human_participant_revoke_1_6_0: revokeHumanParticipant,
  tekroo_event_human_participant_revoked_1_6_0: revokeHumanParticipant,
  tekroo_command_system_bind_operator_role_1_6_0: operatorRoleBinding,
  tekroo_event_system_operator_role_bound_1_6_0: operatorRoleBinding,
  tekroo_command_system_configure_continuity_1_6_0: configureContinuity,
  tekroo_event_system_continuity_configured_1_6_0: configureContinuity,
  tekroo_command_system_request_quiescence_1_6_0: requestQuiescence,
  tekroo_event_system_quiescence_requested_1_6_0: requestQuiescence,
  tekroo_command_system_record_suspended_1_6_0: recordSuspended,
  tekroo_event_system_suspended_1_6_0: recordSuspended,
  tekroo_command_system_record_unexpected_outage_1_6_0: recordUnexpectedOutage,
  tekroo_event_system_unexpected_outage_recorded_1_6_0: recordUnexpectedOutage,
  tekroo_command_system_begin_reconciliation_1_6_0: beginReconciliation,
  tekroo_event_system_reconciliation_started_1_6_0: beginReconciliation,
  tekroo_command_system_resume_1_6_0: resumeActive,
  tekroo_event_system_resumed_1_6_0: resumeActive,
};
Object.assign(payloads.$defs, payloadDefinitions);
overwriteJson(payloadPath, payloads);

const aggregatePath = "schemas/aggregate-state.schema.json";
const aggregate = readJson(`CONTRACTS/${contractIdentity}/${aggregatePath}`);
aggregate.oneOf.push(continuityState, humanParticipantState, humanInteractionState);
overwriteJson(aggregatePath, aggregate);

const cataloguePath = "catalogue/kernel-catalogue.json";
const catalogue = readJson(`CONTRACTS/${contractIdentity}/${cataloguePath}`);
const predecessorCatalogue = readJson(`CONTRACTS/${predecessorIdentity}/${cataloguePath}`);
catalogue.revision = 7;
const predecessorTypeIds = catalogue.entries.map((entry) => entry.typeId).sort();
for (const entry of catalogue.entries) {
  const predecessorEntry = predecessorCatalogue.entries.find((candidate) => candidate.typeId === entry.typeId);
  if (!predecessorEntry) throw new Error(`predecessor catalogue entry missing for ${entry.typeId}`);
  const accepted = new Set(predecessorEntry.compatibility.acceptedSourceVersions);
  accepted.add(schemaVersion);
  entry.compatibility.acceptedSourceVersions = [...accepted].sort();
  const transformKey = `${predecessorSchemaVersion}:${schemaVersion}`;
  const transforms = new Map(predecessorEntry.compatibility.transforms.map((item) => [`${item.sourceVersion}:${item.targetVersion}`, item]));
  transforms.set(transformKey, { sourceVersion: predecessorSchemaVersion, targetVersion: schemaVersion, mode: "IDENTITY" });
  entry.compatibility.transforms = [...transforms.values()].sort((left, right) => `${left.sourceVersion}:${left.targetVersion}`.localeCompare(`${right.sourceVersion}:${right.targetVersion}`));
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
function addPair(commandType, eventType, targetKind, authorityKinds, commandSchema, eventSchema, rootAllowed = false) {
  catalogue.entries.push(
    { ...commonEntry, typeId: commandType, kind: "COMMAND", targetKinds: [targetKind], authorityKinds, routingMode: "KERNEL_DIRECT", payloadSchema: `schemas/payloads.schema.json#/$defs/${commandSchema}`, emits: [eventType], rootAllowed },
    { ...commonEntry, typeId: eventType, kind: "EVENT", targetKinds: [targetKind], authorityKinds: [], routingMode: "COMMITTED_EVENT", payloadSchema: `schemas/payloads.schema.json#/$defs/${eventSchema}`, acceptedCommandTypes: [commandType], rootAllowed },
  );
}
addPair("tekroo.command.human-participant.bind-profile", "tekroo.event.human-participant.profile-bound", "human-participant", ["HUMAN", "POLICY"], "tekroo_command_human_participant_bind_profile_1_6_0", "tekroo_event_human_participant_profile_bound_1_6_0", true);
addPair("tekroo.command.human-participant.revoke", "tekroo.event.human-participant.revoked", "human-participant", ["HUMAN", "POLICY"], "tekroo_command_human_participant_revoke_1_6_0", "tekroo_event_human_participant_revoked_1_6_0");
addPair("tekroo.command.human-interaction.open", "tekroo.event.human-interaction.opened", "human-interaction", ["ACTOR", "HUMAN", "POLICY", "SERVICE"], "tekroo_command_human_interaction_open_1_6_0", "tekroo_event_human_interaction_opened_1_6_0", true);
addPair("tekroo.command.human-interaction.record-delivery", "tekroo.event.human-interaction.delivery-recorded", "human-interaction", ["SERVICE"], "tekroo_command_human_interaction_record_delivery_1_6_0", "tekroo_event_human_interaction_delivery_recorded_1_6_0");
addPair("tekroo.command.human-interaction.respond", "tekroo.event.human-interaction.response-recorded", "human-interaction", ["HUMAN"], "tekroo_command_human_interaction_respond_1_6_0", "tekroo_event_human_interaction_response_recorded_1_6_0");
addPair("tekroo.command.human-interaction.close", "tekroo.event.human-interaction.closed", "human-interaction", ["HUMAN", "POLICY"], "tekroo_command_human_interaction_close_1_6_0", "tekroo_event_human_interaction_closed_1_6_0");
addPair("tekroo.command.human-interaction.expire", "tekroo.event.human-interaction.expired", "human-interaction", ["POLICY"], "tekroo_command_human_interaction_expire_1_6_0", "tekroo_event_human_interaction_expired_1_6_0");
addPair("tekroo.command.system.bind-operator-role", "tekroo.event.system.operator-role-bound", "system", ["HUMAN", "POLICY"], "tekroo_command_system_bind_operator_role_1_6_0", "tekroo_event_system_operator_role_bound_1_6_0", true);
addPair("tekroo.command.system.configure-continuity", "tekroo.event.system.continuity-configured", "system", ["HUMAN", "POLICY"], "tekroo_command_system_configure_continuity_1_6_0", "tekroo_event_system_continuity_configured_1_6_0", true);
addPair("tekroo.command.system.request-quiescence", "tekroo.event.system.quiescence-requested", "system", ["HUMAN", "POLICY", "SERVICE"], "tekroo_command_system_request_quiescence_1_6_0", "tekroo_event_system_quiescence_requested_1_6_0");
addPair("tekroo.command.system.record-suspended", "tekroo.event.system.suspended", "system", ["POLICY", "SERVICE"], "tekroo_command_system_record_suspended_1_6_0", "tekroo_event_system_suspended_1_6_0");
addPair("tekroo.command.system.record-unexpected-outage", "tekroo.event.system.unexpected-outage-recorded", "system", ["POLICY", "SERVICE"], "tekroo_command_system_record_unexpected_outage_1_6_0", "tekroo_event_system_unexpected_outage_recorded_1_6_0");
addPair("tekroo.command.system.begin-reconciliation", "tekroo.event.system.reconciliation-started", "system", ["HUMAN", "POLICY", "SERVICE"], "tekroo_command_system_begin_reconciliation_1_6_0", "tekroo_event_system_reconciliation_started_1_6_0");
addPair("tekroo.command.system.resume", "tekroo.event.system.resumed", "system", ["HUMAN", "POLICY"], "tekroo_command_system_resume_1_6_0", "tekroo_event_system_resumed_1_6_0");
catalogue.entries.sort((left, right) => left.typeId.localeCompare(right.typeId));
overwriteJson(cataloguePath, catalogue);

const ids = {
  system: "00000000-0000-7000-8000-000000000901",
  binding: "00000000-0000-7000-8000-000000000902",
  evidence: "00000000-0000-7000-8000-000000000903",
  executionA: "00000000-0000-7000-8000-000000000904",
  executionB: "00000000-0000-7000-8000-000000000905",
  quiescenceEvent: "00000000-0000-7000-8000-000000000906",
  participantAlice: "00000000-0000-7000-8000-000000000907",
  participantBob: "00000000-0000-7000-8000-000000000908",
  profileAlice: "00000000-0000-7000-8000-000000000909",
  roleAlice: "00000000-0000-7000-8000-000000000910",
  authenticationAlice: "00000000-0000-7000-8000-000000000911",
  deliveryAlice: "00000000-0000-7000-8000-000000000912",
  interaction: "00000000-0000-7000-8000-000000000913",
  deliveryAttempt: "00000000-0000-7000-8000-000000000914",
  deliveryEvent: "00000000-0000-7000-8000-000000000915",
  responseEvent: "00000000-0000-7000-8000-000000000916",
};
const d = (character) => character.repeat(64);
const operatorPayload = {
  binding_id: ids.binding,
  expected_system_revision: 0,
  role_id: "operator",
  operator_actor_fqn: "teams::operator-1",
  role_bundle_version: "4.0.0",
  role_bundle_digest: d("a"),
  role_bundle_signature_evidence_ids: [ids.evidence],
  role_definition_digest: d("b"),
  capability_ids: ["COORDINATE_DAG", "INSPECT_ORGANIZATION", "PREPARE_DECISION", "ROUTE_HUMAN_REQUIRED"],
  human_selection_policy_revision: 1,
  human_selection_policy_digest: d("5"),
  authority_policy_revision: 1,
  authority_policy_digest: d("c"),
  replaces_binding_id: null,
  evidence_ids: [ids.evidence],
};
const participantPayload = {
  participant: { kind: "HUMAN", id: "human:alice" },
  expected_participant_revision: 0,
  profile_id: ids.profileAlice,
  profile_revision: 1,
  profile_digest: d("6"),
  display_label: "Alice — payments SME",
  privacy_classification: "INTERNAL",
  role_bindings: [{
    role_binding_id: ids.roleAlice,
    role_class: "SME",
    role_id: "payments-sme",
    scope_kind: "PROJECT",
    scope_id: "tekroo-v4",
    advisory_topic_ids: ["payments"],
    authorized_command_types: [],
    authority_policy_revision: 1,
    authority_policy_digest: d("7"),
    valid_from: "2026-08-13T00:00:00Z",
    valid_until: null,
    active: true,
    evidence_ids: [ids.evidence],
  }],
  authentication_bindings: [{
    authentication_binding_id: ids.authenticationAlice,
    method: "OIDC",
    issuer_digest: d("8"),
    subject_digest: d("9"),
    assurance: "STANDARD",
    binding_revision: 1,
    active: true,
    evidence_ids: [ids.evidence],
  }],
  delivery_bindings: [{
    delivery_binding_id: ids.deliveryAlice,
    channel: "WEB_PORTAL",
    endpoint_digest: d("a"),
    adapter_profile_digest: d("b"),
    confidentiality_ceiling: "CONFIDENTIAL",
    authentication_binding_ids: [ids.authenticationAlice],
    binding_revision: 1,
    active: true,
    evidence_ids: [ids.evidence],
  }],
  participant_policy_revision: 1,
  participant_policy_digest: d("c"),
  supersedes_profile_id: null,
  evidence_ids: [ids.evidence],
};
const participantRevokePayload = {
  participant: { kind: "HUMAN", id: "human:alice" },
  expected_participant_revision: 1,
  expected_profile_id: ids.profileAlice,
  reason: "participant left project",
  revoked_at: "2026-08-14T00:00:00Z",
  revoked_by: { kind: "HUMAN", id: "principal" },
  evidence_ids: [ids.evidence],
};
const interactionOpenPayload = {
  interaction_id: ids.interaction,
  subject_kind: "task",
  subject_id: ids.system,
  subject_lifecycle_epoch: 1,
  expected_subject_revision: 2,
  question_revision: 1,
  canonical_question_digest: d("d"),
  response_specification_digest: d("e"),
  origin_principal: { kind: "ACTOR", id: "teams::coder-1" },
  origin_actor_fqn: "teams::coder-1",
  origin_execution_id: ids.executionA,
  origin_execution_fencing_epoch: 1,
  recipients: [{
    principal: { kind: "HUMAN", id: "human:alice" },
    participant_profile_id: ids.profileAlice,
    participant_profile_revision: 1,
    participant_profile_digest: d("6"),
    role_binding_id: ids.roleAlice,
    delivery_binding_id: ids.deliveryAlice,
  }],
  purpose: "ADVISORY_CONSULTATION",
  declared_effect: "ADVISORY_ONLY",
  response_policy: { kind: "EXACT_ONE" },
  deadline_at: "2026-08-14T00:00:00Z",
  timeout_policy: { kind: "POLICY", id: "human-interaction-timeout" },
  confidentiality: "INTERNAL",
  disclosure_scope_digest: d("f"),
  interaction_policy_revision: 1,
  interaction_policy_digest: d("1"),
  causal_path_event_ids: [ids.evidence],
  predecessor_interaction_id: null,
  evidence_ids: [ids.evidence],
};
const interactionDeliveryPayload = {
  interaction_id: ids.interaction,
  expected_interaction_revision: 1,
  question_revision: 1,
  delivery_id: ids.deliveryAttempt,
  recipient: { kind: "HUMAN", id: "human:alice" },
  delivery_binding_id: ids.deliveryAlice,
  adapter_profile_digest: d("b"),
  canonical_question_digest: d("d"),
  rendered_question_digest: d("2"),
  presenter_actor_fqn: "teams::operator-1",
  presenter_execution_id: ids.executionB,
  material_equivalence_evidence_id: ids.evidence,
  outcome: "DELIVERED",
  attempted_at: "2026-08-13T12:00:00Z",
  channel_provenance_digest: d("3"),
  evidence_ids: [ids.evidence],
};
const interactionResponsePayload = {
  interaction_id: ids.interaction,
  expected_interaction_revision: 2,
  question_revision: 1,
  respondent: { kind: "HUMAN", id: "human:alice" },
  participant_profile_id: ids.profileAlice,
  participant_profile_revision: 1,
  role_binding_id: ids.roleAlice,
  authentication_binding_id: ids.authenticationAlice,
  delivery_id: ids.deliveryAttempt,
  delivery_event_id: ids.deliveryEvent,
  response_artifact_digest: d("4"),
  response_specification_digest: d("e"),
  response_classification: "ANSWER",
  asserted_effect: "ADVISORY_ONLY",
  credential_provenance_digest: d("5"),
  channel_provenance_digest: d("3"),
  responded_at: "2026-08-13T12:05:00Z",
  evidence_ids: [ids.evidence],
};
const interactionClosePayload = {
  interaction_id: ids.interaction,
  expected_interaction_revision: 3,
  question_revision: 1,
  accepted_response_event_ids: [ids.responseEvent],
  accepted_respondents: [{ kind: "HUMAN", id: "human:alice" }],
  response_policy_satisfied: true,
  outcome: "SATISFIED",
  resolved_effect: "ADVISORY_ONLY",
  authorization_evidence_id: null,
  closed_at: "2026-08-13T12:06:00Z",
  closed_by: { kind: "POLICY", id: "human-interaction-policy" },
  reasons: ["exact selected respondent answered"],
  evidence_ids: [ids.evidence],
};
const interactionExpirePayload = {
  interaction_id: ids.interaction,
  expected_interaction_revision: 2,
  question_revision: 1,
  deadline_at: "2026-08-14T00:00:00Z",
  outcome: "EXPIRED",
  silence_is_consent: false,
  expired_at: "2026-08-14T00:00:01Z",
  timeout_policy: { kind: "POLICY", id: "human-interaction-timeout" },
  evidence_ids: [ids.evidence],
};
const continuityPayload = {
  expected_system_revision: 0,
  initial_power_epoch: 1,
  operating_posture: "CONTINUOUS",
  continuity_policy_revision: 1,
  continuity_policy_digest: d("d"),
  health_requirement_digest: d("e"),
  default_quiesce_timeout_ms: 30000,
  provider_dispositions: [{ provider_profile_digest: d("f"), disposition: "CANCEL_ON_SUSPEND", quiesce_deadline_ms: 10000 }],
  configured_by: { kind: "HUMAN", id: "principal" },
  evidence_ids: [ids.evidence],
};
const quiescencePayload = {
  expected_system_revision: 1,
  expected_power_epoch: 1,
  next_power_epoch: 2,
  reason: "LID_CLOSE",
  requested_by: { kind: "HUMAN", id: "principal" },
  deadline_at: "2026-08-13T23:59:00Z",
  disposition_plan_digest: d("1"),
  in_flight_execution_ids: [ids.executionA, ids.executionB],
  evidence_ids: [ids.evidence],
};
const suspendedPayload = {
  expected_system_revision: 2,
  expected_power_epoch: 2,
  quiescence_request_event_id: ids.quiescenceEvent,
  execution_records: [
    { execution_id: ids.executionA, actor_fqn: "teams::coder-1", execution_fencing_epoch: 1, provider_profile_digest: d("f"), disposition: "CANCEL_ON_SUSPEND", outcome: "CANCELLED", result_evidence_id: ids.evidence, evidence_ids: [ids.evidence] },
    { execution_id: ids.executionB, actor_fqn: "teams::reviewer-1", execution_fencing_epoch: 1, provider_profile_digest: d("2"), disposition: "DETACH_AND_RECONCILE", outcome: "DETACHED", result_evidence_id: null, evidence_ids: [ids.evidence] },
  ],
  unresolved_execution_ids: [ids.executionB],
  all_in_flight_executions_recorded: true,
  recorded_by: { kind: "SERVICE", id: "execution-coordinator" },
  evidence_ids: [ids.evidence],
};
const outagePayload = {
  expected_system_revision: 1,
  observed_power_epoch: 1,
  next_power_epoch: 2,
  previous_control_state: "ACTIVE",
  detected_at: "2026-08-13T12:00:00Z",
  outage_kind: "HOST_SLEEP",
  known_in_flight_execution_ids: [ids.executionA],
  last_durable_event_id: ids.quiescenceEvent,
  detected_by: { kind: "SERVICE", id: "execution-coordinator" },
  evidence_ids: [ids.evidence],
};
const reconciliationPayload = {
  expected_system_revision: 3,
  expected_power_epoch: 2,
  trigger: "WAKE",
  unresolved_execution_ids: [ids.executionB],
  provider_snapshot_digest: d("3"),
  initiated_by: { kind: "SERVICE", id: "execution-coordinator" },
  evidence_ids: [ids.evidence],
};
const resumePayload = {
  expected_system_revision: 4,
  expected_power_epoch: 2,
  reconciliation_records: [{ execution_id: ids.executionB, outcome: "TERMINAL_EVIDENCE_CAPTURED", authoritative_state_digest: d("4"), evidence_ids: [ids.evidence] }],
  required_services_healthy: true,
  outbox_reconciled: true,
  change_stream_reconciled: true,
  admission_reopen: true,
  resumed_by: { kind: "POLICY", id: "continuity-policy" },
  evidence_ids: [ids.evidence],
};

const catalogueFixturesPath = "fixtures/catalogue-coverage.json";
const catalogueFixtures = readJson(`CONTRACTS/${contractIdentity}/${catalogueFixturesPath}`);
function catalogueFixture(fixtureId, classification, commandType, payload, outcomeCode, eventTypes, sourceDecisionIds = ["OP-DEC-001", "HC-DEC-002"]) {
  return { classification, fixtureId, kind: "CATALOGUE_COMMAND", sourceDecisionIds, given: { contractManifest: contractIdentity }, when: { commandType, payload }, then: { expected: { outcomeCode, eventTypes } } };
}
catalogueFixtures.fixtures.push(
  catalogueFixture("CAT-043-OPERATOR-ROLE-BIND-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.system.bind-operator-role", operatorPayload, "APPLIED", ["tekroo.event.system.operator-role-bound"]),
  catalogueFixture("CAT-043-OPERATOR-ROLE-BIND-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.system.bind-operator-role", { ...operatorPayload, operator_actor_fqn: "teams::coder-1" }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-044-CONTINUITY-CONFIGURE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.system.configure-continuity", continuityPayload, "APPLIED", ["tekroo.event.system.continuity-configured"]),
  catalogueFixture("CAT-044-CONTINUITY-CONFIGURE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.system.configure-continuity", { ...continuityPayload, provider_dispositions: [] }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-045-QUIESCENCE-REQUEST-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.system.request-quiescence", quiescencePayload, "APPLIED", ["tekroo.event.system.quiescence-requested"]),
  catalogueFixture("CAT-045-QUIESCENCE-REQUEST-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.system.request-quiescence", { ...quiescencePayload, next_power_epoch: 1 }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-046-SUSPENDED-RECORD-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.system.record-suspended", suspendedPayload, "APPLIED", ["tekroo.event.system.suspended"]),
  catalogueFixture("CAT-046-SUSPENDED-RECORD-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.system.record-suspended", { ...suspendedPayload, all_in_flight_executions_recorded: false }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-047-UNEXPECTED-OUTAGE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.system.record-unexpected-outage", outagePayload, "APPLIED", ["tekroo.event.system.unexpected-outage-recorded"]),
  catalogueFixture("CAT-047-UNEXPECTED-OUTAGE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.system.record-unexpected-outage", { ...outagePayload, next_power_epoch: 1 }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-048-RECONCILIATION-BEGIN-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.system.begin-reconciliation", reconciliationPayload, "APPLIED", ["tekroo.event.system.reconciliation-started"]),
  catalogueFixture("CAT-048-RECONCILIATION-BEGIN-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.system.begin-reconciliation", { ...reconciliationPayload, provider_snapshot_digest: "invalid" }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-049-RESUME-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.system.resume", resumePayload, "APPLIED", ["tekroo.event.system.resumed"]),
  catalogueFixture("CAT-049-RESUME-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.system.resume", { ...resumePayload, required_services_healthy: false }, "REJECTED_INVALID", []),
  catalogueFixture("CAT-050-HUMAN-PARTICIPANT-BIND-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.human-participant.bind-profile", participantPayload, "APPLIED", ["tekroo.event.human-participant.profile-bound"], ["HP-DEC-001", "HP-DEC-002"]),
  catalogueFixture("CAT-050-HUMAN-PARTICIPANT-BIND-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.human-participant.bind-profile", { ...participantPayload, role_bindings: [] }, "REJECTED_INVALID", [], ["HP-DEC-001"]),
  catalogueFixture("CAT-051-HUMAN-PARTICIPANT-REVOKE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.human-participant.revoke", participantRevokePayload, "APPLIED", ["tekroo.event.human-participant.revoked"], ["HP-DEC-001", "HP-DEC-002"]),
  catalogueFixture("CAT-051-HUMAN-PARTICIPANT-REVOKE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.human-participant.revoke", { ...participantRevokePayload, participant: { kind: "ACTOR", id: "teams::operator-1" } }, "REJECTED_INVALID", [], ["HP-DEC-001"]),
  catalogueFixture("CAT-052-HUMAN-INTERACTION-OPEN-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.human-interaction.open", interactionOpenPayload, "APPLIED", ["tekroo.event.human-interaction.opened"], ["HP-DEC-003"]),
  catalogueFixture("CAT-052-HUMAN-INTERACTION-OPEN-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.human-interaction.open", { ...interactionOpenPayload, recipients: [] }, "REJECTED_INVALID", [], ["HP-DEC-003"]),
  catalogueFixture("CAT-053-HUMAN-DELIVERY-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.human-interaction.record-delivery", interactionDeliveryPayload, "APPLIED", ["tekroo.event.human-interaction.delivery-recorded"], ["HP-DEC-004"]),
  catalogueFixture("CAT-053-HUMAN-DELIVERY-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.human-interaction.record-delivery", { ...interactionDeliveryPayload, outcome: "READ" }, "REJECTED_INVALID", [], ["HP-DEC-004"]),
  catalogueFixture("CAT-054-HUMAN-RESPONSE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.human-interaction.respond", interactionResponsePayload, "APPLIED", ["tekroo.event.human-interaction.response-recorded"], ["HP-DEC-005"]),
  catalogueFixture("CAT-054-HUMAN-RESPONSE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.human-interaction.respond", { ...interactionResponsePayload, respondent: { kind: "ACTOR", id: "teams::operator-1" } }, "REJECTED_INVALID", [], ["HP-DEC-005"]),
  catalogueFixture("CAT-055-HUMAN-INTERACTION-CLOSE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.human-interaction.close", interactionClosePayload, "APPLIED", ["tekroo.event.human-interaction.closed"], ["HP-DEC-005", "HP-DEC-006"]),
  catalogueFixture("CAT-055-HUMAN-INTERACTION-CLOSE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.human-interaction.close", { ...interactionClosePayload, response_policy_satisfied: false }, "REJECTED_INVALID", [], ["HP-DEC-005"]),
  catalogueFixture("CAT-056-HUMAN-INTERACTION-EXPIRE-VALID", "NORMATIVE_EXAMPLE", "tekroo.command.human-interaction.expire", interactionExpirePayload, "APPLIED", ["tekroo.event.human-interaction.expired"], ["HP-DEC-006"]),
  catalogueFixture("CAT-056-HUMAN-INTERACTION-EXPIRE-INVALID", "BOUNDARY_NEGATIVE", "tekroo.command.human-interaction.expire", { ...interactionExpirePayload, silence_is_consent: true }, "REJECTED_INVALID", [], ["HP-DEC-006"]),
);
catalogueFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(catalogueFixturesPath, catalogueFixtures);

const modelFixturesPath = "fixtures/model-and-invariant-scenarios.json";
const modelFixtures = readJson(`CONTRACTS/${contractIdentity}/${modelFixturesPath}`);
function modelFixture(fixtureId, kind, sourceDecisionIds, given, when, expected) {
  return { classification: expected.accepted ? "NORMATIVE_EXAMPLE" : "COUNTEREXAMPLE", fixtureId, kind, sourceDecisionIds, given, when, then: { expected } };
}
modelFixtures.fixtures.push(
  modelFixture("OP-HUMAN-ATTRIBUTION-PRESERVED", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-001", "OP-DEC-003"], { sourceKind: "HUMAN", claimedKind: "HUMAN", explicitlyAuthorized: true }, { action: "PRESENT_AUTHORITY" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("OP-ACTOR-CANNOT-IMPERSONATE-HUMAN", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-001", "OP-DEC-003"], { sourceKind: "ACTOR", claimedKind: "HUMAN", explicitlyAuthorized: true }, { action: "PRESENT_AUTHORITY" }, { accepted: false, reason: "HUMAN_IMPERSONATION" }),
  modelFixture("OP-CONTROL-SURFACE-IS-NOT-PRINCIPAL", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-001"], { sourceKind: "CONTROL_SURFACE", claimedKind: "ACTOR", explicitlyAuthorized: true }, { action: "PRESENT_AUTHORITY" }, { accepted: false, reason: "ADAPTER_NOT_PRINCIPAL" }),
  modelFixture("OP-ACTOR-EXPLICIT-AUTHORITY", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-003", "OP-DEC-004"], { sourceKind: "ACTOR", claimedKind: "ACTOR", explicitlyAuthorized: true, currentExecution: true, commandAuthorityKinds: ["ACTOR"] }, { action: "PRESENT_AUTHORITY" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("OP-ROLE-NAME-NO-AUTHORITY", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-003"], { sourceKind: "ACTOR", claimedKind: "ACTOR", explicitlyAuthorized: false, currentExecution: true, commandAuthorityKinds: ["ACTOR"] }, { action: "PRESENT_AUTHORITY" }, { accepted: false, reason: "AUTHORITY_REQUIRED" }),
  modelFixture("OP-STALE-EXECUTION-FENCED", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-002"], { sourceKind: "ACTOR", claimedKind: "ACTOR", explicitlyAuthorized: true, currentExecution: false, commandAuthorityKinds: ["ACTOR"] }, { action: "PRESENT_AUTHORITY" }, { accepted: false, reason: "STALE_EXECUTION" }),
  modelFixture("OP-HUMAN-REQUIRED-EXACT-HUMAN", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-005"], { targetKind: "HUMAN" }, { action: "ROUTE_HUMAN_REQUIRED" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("OP-HUMAN-REQUIRED-NOT-OPERATOR-ACTOR", "OPERATOR_SEPARATION_MODEL", ["OP-DEC-005"], { targetKind: "ACTOR" }, { action: "ROUTE_HUMAN_REQUIRED" }, { accepted: false, reason: "HUMAN_PRINCIPAL_REQUIRED" }),
  modelFixture("HC-PLANNED-QUIESCENCE", "TEAM_CONTINUITY_MODEL", ["HC-DEC-002"], { state: "ACTIVE", powerEpoch: 7, admissionOpen: true }, { action: "REQUEST_QUIESCENCE", nextPowerEpoch: 8 }, { accepted: true, reason: "ACCEPTED", state: "QUIESCING", powerEpoch: 8, admissionOpen: false }),
  modelFixture("HC-QUIESCENCE-EPOCH-MUST-INCREMENT", "TEAM_CONTINUITY_MODEL", ["HC-DEC-002"], { state: "ACTIVE", powerEpoch: 7, admissionOpen: true }, { action: "REQUEST_QUIESCENCE", nextPowerEpoch: 7 }, { accepted: false, reason: "POWER_EPOCH_MISMATCH", state: "ACTIVE", powerEpoch: 7, admissionOpen: true }),
  modelFixture("HC-ADMISSION-CLOSED-DURING-QUIESCENCE", "TEAM_CONTINUITY_MODEL", ["HC-DEC-002"], { state: "QUIESCING", powerEpoch: 8, admissionOpen: false }, { action: "DISPATCH", powerEpoch: 8 }, { accepted: false, reason: "ADMISSION_CLOSED" }),
  modelFixture("HC-OLD-EPOCH-RESULT-QUARANTINED", "TEAM_CONTINUITY_MODEL", ["HC-DEC-002", "HC-DEC-003"], { state: "RECONCILING", powerEpoch: 8, admissionOpen: false }, { action: "ACCEPT_RESULT", powerEpoch: 7 }, { accepted: false, reason: "LATE_RESULT_QUARANTINED" }),
  modelFixture("HC-SUSPEND-REQUIRES-COMPLETE-INVENTORY", "TEAM_CONTINUITY_MODEL", ["HC-DEC-002", "HC-DEC-003"], { state: "QUIESCING", powerEpoch: 8, admissionOpen: false }, { action: "RECORD_SUSPENDED", allExecutionsRecorded: false, unresolvedExecutionIds: [ids.executionB] }, { accepted: false, reason: "EXECUTION_INVENTORY_INCOMPLETE", state: "QUIESCING" }),
  modelFixture("HC-SUSPEND-WITH-RECORDED-UNKNOWN", "TEAM_CONTINUITY_MODEL", ["HC-DEC-002", "HC-DEC-003"], { state: "QUIESCING", powerEpoch: 8, admissionOpen: false }, { action: "RECORD_SUSPENDED", allExecutionsRecorded: true, unresolvedExecutionIds: [ids.executionB] }, { accepted: true, reason: "ACCEPTED", state: "SUSPENDED", admissionOpen: false, unresolvedExecutionIds: [ids.executionB] }),
  modelFixture("HC-NO-DIRECT-SUSPENDED-TO-ACTIVE", "TEAM_CONTINUITY_MODEL", ["HC-DEC-004", "HC-DEC-005"], { state: "SUSPENDED", powerEpoch: 8, admissionOpen: false, unresolvedExecutionIds: [] }, { action: "RESUME", powerEpoch: 8, reconciledExecutionIds: [], servicesHealthy: true, outboxReconciled: true, changeStreamReconciled: true }, { accepted: false, reason: "RECONCILIATION_REQUIRED", state: "SUSPENDED" }),
  modelFixture("HC-BEGIN-RECONCILIATION", "TEAM_CONTINUITY_MODEL", ["HC-DEC-004"], { state: "SUSPENDED", powerEpoch: 8, admissionOpen: false, unresolvedExecutionIds: [ids.executionB] }, { action: "BEGIN_RECONCILIATION", powerEpoch: 8 }, { accepted: true, reason: "ACCEPTED", state: "RECONCILING", admissionOpen: false, unresolvedExecutionIds: [ids.executionB] }),
  modelFixture("HC-UNEXPECTED-OUTAGE-FENCES", "TEAM_CONTINUITY_MODEL", ["HC-DEC-004"], { state: "ACTIVE", powerEpoch: 7, admissionOpen: true, unresolvedExecutionIds: [] }, { action: "RECORD_UNEXPECTED_OUTAGE", nextPowerEpoch: 8, knownInFlightExecutionIds: [ids.executionA] }, { accepted: true, reason: "ACCEPTED", state: "RECONCILING", powerEpoch: 8, admissionOpen: false, unresolvedExecutionIds: [ids.executionA] }),
  modelFixture("HC-RESUME-REQUIRES-EXACT-RECONCILIATION", "TEAM_CONTINUITY_MODEL", ["HC-DEC-004", "HC-DEC-005"], { state: "RECONCILING", powerEpoch: 8, admissionOpen: false, unresolvedExecutionIds: [ids.executionA, ids.executionB] }, { action: "RESUME", powerEpoch: 8, reconciledExecutionIds: [ids.executionA], servicesHealthy: true, outboxReconciled: true, changeStreamReconciled: true }, { accepted: false, reason: "UNRESOLVED_EXECUTIONS", state: "RECONCILING" }),
  modelFixture("HC-RESUME-AFTER-EXACT-RECONCILIATION", "TEAM_CONTINUITY_MODEL", ["HC-DEC-004", "HC-DEC-005"], { state: "RECONCILING", powerEpoch: 8, admissionOpen: false, unresolvedExecutionIds: [ids.executionA, ids.executionB] }, { action: "RESUME", powerEpoch: 8, reconciledExecutionIds: [ids.executionA, ids.executionB], servicesHealthy: true, outboxReconciled: true, changeStreamReconciled: true }, { accepted: true, reason: "ACCEPTED", state: "ACTIVE", powerEpoch: 8, admissionOpen: true, unresolvedExecutionIds: [] }),
  modelFixture("HC-CONTINUOUS-POSTURE-REQUIRES-HEALTH", "TEAM_CONTINUITY_MODEL", ["HC-DEC-001"], { state: "ACTIVE", powerEpoch: 7, admissionOpen: true, operatingPosture: "CONTINUOUS" }, { action: "OBSERVE_CONTINUOUS", servicesHealthy: false }, { accepted: false, reason: "HOST_CONTINUITY_LOST" }),
  modelFixture("HP-DISTINCT-HUMANS-SAME-ROLE", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-001"], { participantIds: ["human:alice", "human:bob"], roleIds: ["payments-sme", "payments-sme"] }, { action: "RESOLVE_RECIPIENTS" }, { accepted: true, reason: "ACCEPTED", recipientIds: ["human:alice", "human:bob"] }),
  modelFixture("HP-ROLE-LABEL-NO-INTRINSIC-AUTHORITY", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-001"], { active: true, scopeMatches: true, commandAuthorized: false }, { action: "AUTHORIZE" }, { accepted: false, reason: "SCOPED_AUTHORITY_REQUIRED" }),
  modelFixture("HP-SCOPED-HUMAN-AUTHORITY", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-001"], { active: true, scopeMatches: true, commandAuthorized: true }, { action: "AUTHORIZE" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("HP-EXPIRED-ROLE-REJECTED", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-001"], { active: true, roleCurrent: false, scopeMatches: true, commandAuthorized: true }, { action: "AUTHORIZE" }, { accepted: false, reason: "ROLE_BINDING_EXPIRED" }),
  modelFixture("HP-REVOKED-HUMAN-REJECTED", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-001", "HP-DEC-002"], { active: false, scopeMatches: true, commandAuthorized: true }, { action: "AUTHORIZE" }, { accepted: false, reason: "PARTICIPANT_REVOKED" }),
  modelFixture("HP-AUTHENTICATION-BINDING-EXACT", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-002"], { bindingActive: true, subjectMatches: true, assuranceSufficient: true }, { action: "AUTHENTICATE" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("HP-CHANNEL-IDENTITY-NOT-HUMAN", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-002"], { bindingActive: true, subjectMatches: false, assuranceSufficient: true }, { action: "AUTHENTICATE" }, { accepted: false, reason: "AUTHENTICATION_SUBJECT_MISMATCH" }),
  modelFixture("HP-DELIVERY-BINDING-REFERENCES-ACTIVE-AUTH", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-002"], { activeAuthenticationBindingIds: [ids.authenticationAlice], deliveryAuthenticationBindingIds: [ids.authenticationAlice] }, { action: "VALIDATE_PROFILE_BINDINGS" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("HP-DELIVERY-BINDING-UNKNOWN-AUTH-REJECTED", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-002"], { activeAuthenticationBindingIds: [ids.authenticationAlice], deliveryAuthenticationBindingIds: [ids.participantBob] }, { action: "VALIDATE_PROFILE_BINDINGS" }, { accepted: false, reason: "AUTHENTICATION_BINDING_REFERENCE_INVALID" }),
  modelFixture("HP-CONFIDENTIALITY-CEILING-REJECTS", "HUMAN_PARTICIPANT_MODEL", ["HP-DEC-002", "HP-DEC-006"], { requestedConfidentiality: "RESTRICTED", routeCeiling: "CONFIDENTIAL" }, { action: "SELECT_DELIVERY_ROUTE" }, { accepted: false, reason: "CONFIDENTIALITY_CEILING_EXCEEDED" }),
  modelFixture("HI-HUMAN-ORIGIN-HAS-NO-ACTOR-EXECUTION", "HUMAN_INTERACTION_MODEL", ["HP-DEC-003"], { originKind: "HUMAN", originActorFqn: null, originExecutionId: null }, { action: "OPEN" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("HI-HUMAN-ORIGIN-CANNOT-BORROW-ACTOR", "HUMAN_INTERACTION_MODEL", ["HP-DEC-003"], { originKind: "HUMAN", originActorFqn: "teams::operator-1", originExecutionId: ids.executionB }, { action: "OPEN" }, { accepted: false, reason: "HUMAN_ACTOR_ATTRIBUTION" }),
  modelFixture("HI-SELECTED-HUMAN-RESPONSE", "HUMAN_INTERACTION_MODEL", ["HP-DEC-003", "HP-DEC-005"], { selectedHumanIds: ["human:alice"], questionRevision: 1, responseSpecificationMatches: true, participantActive: true, authenticationValid: true, duplicate: false }, { action: "RESPOND", respondentId: "human:alice", questionRevision: 1, sourceKind: "HUMAN" }, { accepted: true, reason: "ACCEPTED" }),
  modelFixture("HI-UNSELECTED-HUMAN-REJECTED", "HUMAN_INTERACTION_MODEL", ["HP-DEC-003", "HP-DEC-005"], { selectedHumanIds: ["human:alice"], questionRevision: 1, responseSpecificationMatches: true, participantActive: true, authenticationValid: true, duplicate: false }, { action: "RESPOND", respondentId: "human:bob", questionRevision: 1, sourceKind: "HUMAN" }, { accepted: false, reason: "RECIPIENT_MISMATCH" }),
  modelFixture("HI-STALE-QUESTION-REJECTED", "HUMAN_INTERACTION_MODEL", ["HP-DEC-003", "HP-DEC-005"], { selectedHumanIds: ["human:alice"], questionRevision: 2, responseSpecificationMatches: true, participantActive: true, authenticationValid: true, duplicate: false }, { action: "RESPOND", respondentId: "human:alice", questionRevision: 1, sourceKind: "HUMAN" }, { accepted: false, reason: "QUESTION_REVISION_MISMATCH" }),
  modelFixture("HI-OPERATOR-RELAY-NOT-HUMAN-RESPONSE", "HUMAN_INTERACTION_MODEL", ["HP-DEC-004", "HP-DEC-005", "HP-DEC-006"], { selectedHumanIds: ["human:alice"], questionRevision: 1, responseSpecificationMatches: true, participantActive: true, authenticationValid: true, duplicate: false }, { action: "RESPOND", respondentId: "human:alice", questionRevision: 1, sourceKind: "ACTOR" }, { accepted: false, reason: "HUMAN_AUTHORITY_REQUIRED" }),
  modelFixture("HI-HUMAN-RESPONSE-CANNOT-CARRY-ACTOR", "HUMAN_INTERACTION_MODEL", ["HP-DEC-005"], { selectedHumanIds: ["human:alice"], questionRevision: 1, responseSpecificationMatches: true, participantActive: true, authenticationValid: true, duplicate: false }, { action: "RESPOND", respondentId: "human:alice", questionRevision: 1, sourceKind: "HUMAN", actorFqn: "teams::operator-1" }, { accepted: false, reason: "HUMAN_ACTOR_ATTRIBUTION" }),
  modelFixture("HI-DELIVERY-IS-NOT-RESPONSE", "HUMAN_INTERACTION_MODEL", ["HP-DEC-004"], { selectedHumanIds: ["human:alice"], acceptedHumanIds: [], responsePolicy: { kind: "EXACT_ONE" } }, { action: "RECORD_DELIVERY", recipientId: "human:alice" }, { accepted: true, reason: "DELIVERY_RECORDED", responsePolicySatisfied: false }),
  modelFixture("HI-MATERIAL-RENDERING-CHANGE-REJECTED", "HUMAN_INTERACTION_MODEL", ["HP-DEC-004"], { renderedDiffers: true, materialEquivalenceProven: false }, { action: "RECORD_DELIVERY", recipientId: "human:alice" }, { accepted: false, reason: "PRESENTATION_EQUIVALENCE_REQUIRED" }),
  modelFixture("HI-RESPONSE-CAPABILITY-CONSUMED-AFTER-COMMIT", "HUMAN_INTERACTION_MODEL", ["HP-DEC-002", "HP-DEC-005"], { credentialVerified: true, commitSucceeded: true }, { action: "ACCEPT_RESPONSE_CAPABILITY" }, { accepted: true, reason: "ACCEPTED", capabilityConsumed: true }),
  modelFixture("HI-FAILED-COMMIT-PRESERVES-RETRY", "HUMAN_INTERACTION_MODEL", ["HP-DEC-002", "HP-DEC-005"], { credentialVerified: true, commitSucceeded: false }, { action: "ACCEPT_RESPONSE_CAPABILITY" }, { accepted: false, reason: "COMMIT_FAILED", capabilityConsumed: false }),
  modelFixture("HI-ALL-REQUIRES-EVERY-DISTINCT-HUMAN", "HUMAN_INTERACTION_MODEL", ["HP-DEC-003", "HP-DEC-005"], { selectedHumanIds: ["human:alice", "human:bob"], acceptedHumanIds: ["human:alice"], responsePolicy: { kind: "ALL" } }, { action: "EVALUATE_RESPONSE_POLICY" }, { accepted: false, reason: "RESPONSE_POLICY_UNSATISFIED" }),
  modelFixture("HI-QUORUM-SATISFIED-BY-DISTINCT-HUMANS", "HUMAN_INTERACTION_MODEL", ["HP-DEC-003", "HP-DEC-005"], { selectedHumanIds: ["human:alice", "human:bob", "human:carol"], acceptedHumanIds: ["human:alice", "human:bob"], responsePolicy: { kind: "QUORUM", quorum: 2 } }, { action: "EVALUATE_RESPONSE_POLICY" }, { accepted: true, reason: "RESPONSE_POLICY_SATISFIED" }),
  modelFixture("HI-ADVISORY-RESPONSE-CANNOT-AUTHORIZE", "HUMAN_INTERACTION_MODEL", ["HP-DEC-001", "HP-DEC-005"], { declaredEffect: "ADVISORY_ONLY", scopedCommandAuthority: true }, { action: "EVALUATE_EFFECT" }, { accepted: true, reason: "RECORDED_AS_ADVISORY", resolvedEffect: "ADVISORY_ONLY" }),
  modelFixture("HI-AUTHORIZED-EFFECT-REQUIRES-SCOPE", "HUMAN_INTERACTION_MODEL", ["HP-DEC-001", "HP-DEC-005"], { declaredEffect: "AUTHORITY_IF_AUTHORIZED", scopedCommandAuthority: false }, { action: "EVALUATE_EFFECT" }, { accepted: false, reason: "SCOPED_AUTHORITY_REQUIRED", resolvedEffect: "EVIDENCE_ONLY" }),
  modelFixture("HI-SILENCE-IS-NOT-CONSENT", "HUMAN_INTERACTION_MODEL", ["HP-DEC-006"], { deadlineExpired: true, acceptedHumanIds: [] }, { action: "EXPIRE", silenceIsConsent: false }, { accepted: true, reason: "EXPIRED_WITHOUT_CONSENT", state: "EXPIRED" }),
);
modelFixtures.fixtures.sort((left, right) => left.fixtureId.localeCompare(right.fixtureId));
overwriteJson(modelFixturesPath, modelFixtures);

const fixtureSchemaPath = "schemas/conformance-fixture.schema.json";
const fixtureSchema = readJson(`CONTRACTS/${contractIdentity}/${fixtureSchemaPath}`);
fixtureSchema.properties.kind.enum.push("HUMAN_INTERACTION_MODEL", "HUMAN_PARTICIPANT_MODEL", "OPERATOR_SEPARATION_MODEL", "TEAM_CONTINUITY_MODEL");
fixtureSchema.properties.kind.enum = [...new Set(fixtureSchema.properties.kind.enum)].sort();
overwriteJson(fixtureSchemaPath, fixtureSchema);

const invariantsPath = "invariants/invariants.json";
const invariants = readJson(`CONTRACTS/${contractIdentity}/${invariantsPath}`);
invariants.invariants.push(
  { invariantId: "INV-028-OPERATOR-IDENTITY-SEPARATION", description: "Human principal, operator actor, and control surface retain distinct identities; no adapter or actor may synthesize or impersonate another principal kind.", decisionIds: ["OP-DEC-001", "OP-DEC-003"], negativeRequirementIds: [], fixtureIds: ["OP-HUMAN-ATTRIBUTION-PRESERVED", "OP-ACTOR-CANNOT-IMPERSONATE-HUMAN", "OP-CONTROL-SURFACE-IS-NOT-PRINCIPAL", "CAT-043-OPERATOR-ROLE-BIND-VALID", "CAT-043-OPERATOR-ROLE-BIND-INVALID"] },
  { invariantId: "INV-029-OPERATOR-EXACT-AUTHORITY-AND-FENCING", description: "The operator role grants no intrinsic authority; only an explicitly authorized current actor execution may submit actor-admitted commands, and restart preserves FQN while fencing prior output.", decisionIds: ["OP-DEC-002", "OP-DEC-003", "OP-DEC-004"], negativeRequirementIds: [], fixtureIds: ["OP-ACTOR-EXPLICIT-AUTHORITY", "OP-ROLE-NAME-NO-AUTHORITY", "OP-STALE-EXECUTION-FENCED"] },
  { invariantId: "INV-030-HUMAN-REQUIRED-EXACT-HUMAN", description: "HUMAN_REQUIRED targets an exact authenticated human principal or authorized human-selection policy; operator delivery and presentation do not resolve it.", decisionIds: ["OP-DEC-005"], negativeRequirementIds: [], fixtureIds: ["OP-HUMAN-REQUIRED-EXACT-HUMAN", "OP-HUMAN-REQUIRED-NOT-OPERATOR-ACTOR"] },
  { invariantId: "INV-031-POWER-EPOCH-ADMISSION-FENCE", description: "Planned quiescence and unexpected outage advance a monotonic power epoch, close admission, and prevent prior-epoch dispatch or results from acquiring organizational authority.", decisionIds: ["HC-DEC-002", "HC-DEC-004"], negativeRequirementIds: [], fixtureIds: ["HC-PLANNED-QUIESCENCE", "HC-QUIESCENCE-EPOCH-MUST-INCREMENT", "HC-ADMISSION-CLOSED-DURING-QUIESCENCE", "HC-OLD-EPOCH-RESULT-QUARANTINED", "HC-UNEXPECTED-OUTAGE-FENCES", "CAT-045-QUIESCENCE-REQUEST-VALID", "CAT-045-QUIESCENCE-REQUEST-INVALID", "CAT-047-UNEXPECTED-OUTAGE-VALID", "CAT-047-UNEXPECTED-OUTAGE-INVALID"] },
  { invariantId: "INV-032-EXPLICIT-SUSPENSION-DISPOSITION", description: "Every in-flight execution receives a preregistered bounded suspension disposition and an observed outcome; cancellation is never inferred and detached or unknown work remains unresolved.", decisionIds: ["HC-DEC-002", "HC-DEC-003"], negativeRequirementIds: [], fixtureIds: ["HC-SUSPEND-REQUIRES-COMPLETE-INVENTORY", "HC-SUSPEND-WITH-RECORDED-UNKNOWN", "CAT-046-SUSPENDED-RECORD-VALID", "CAT-046-SUSPENDED-RECORD-INVALID"] },
  { invariantId: "INV-033-RECONCILE-BEFORE-RESUME", description: "Suspended or unexpectedly interrupted work enters RECONCILING with admission closed and returns ACTIVE only after exact execution, provider, effect, outbox, change-stream, service-health, authority, and power-epoch reconciliation.", decisionIds: ["HC-DEC-004", "HC-DEC-005"], negativeRequirementIds: [], fixtureIds: ["HC-NO-DIRECT-SUSPENDED-TO-ACTIVE", "HC-BEGIN-RECONCILIATION", "HC-RESUME-REQUIRES-EXACT-RECONCILIATION", "HC-RESUME-AFTER-EXACT-RECONCILIATION", "CAT-048-RECONCILIATION-BEGIN-VALID", "CAT-048-RECONCILIATION-BEGIN-INVALID", "CAT-049-RESUME-VALID", "CAT-049-RESUME-INVALID"] },
  { invariantId: "INV-034-CONTINUOUS-POSTURE-IS-OBSERVED", description: "CONTINUOUS is valid only while required local services and host leases are observably healthy; lid state, remote inference, or a power utility is not proof of local continuity.", decisionIds: ["HC-DEC-001"], negativeRequirementIds: [], fixtureIds: ["HC-CONTINUOUS-POSTURE-REQUIRES-HEALTH", "CAT-044-CONTINUITY-CONFIGURE-VALID", "CAT-044-CONTINUITY-CONFIGURE-INVALID"] },
  { invariantId: "INV-035-DISTINCT-HUMAN-IDENTITY-AND-SCOPED-ROLE", description: "Humans sharing a role remain distinct exact principals, and a role label grants only the command, subject, validity, and policy scope recorded in its active binding.", decisionIds: ["HP-DEC-001"], negativeRequirementIds: [], fixtureIds: ["HP-DISTINCT-HUMANS-SAME-ROLE", "HP-ROLE-LABEL-NO-INTRINSIC-AUTHORITY", "HP-SCOPED-HUMAN-AUTHORITY", "HP-EXPIRED-ROLE-REJECTED", "HP-REVOKED-HUMAN-REJECTED", "CAT-050-HUMAN-PARTICIPANT-BIND-VALID", "CAT-050-HUMAN-PARTICIPANT-BIND-INVALID", "CAT-051-HUMAN-PARTICIPANT-REVOKE-VALID", "CAT-051-HUMAN-PARTICIPANT-REVOKE-INVALID"] },
  { invariantId: "INV-036-AUTHENTICATED-HUMAN-BINDING", description: "A channel or credential identity becomes HUMAN authority only through an exact active participant authentication binding with sufficient assurance; delivery references active bindings and one-time capability consumption is atomic with accepted response commit.", decisionIds: ["HP-DEC-002"], negativeRequirementIds: [], fixtureIds: ["HP-AUTHENTICATION-BINDING-EXACT", "HP-CHANNEL-IDENTITY-NOT-HUMAN", "HP-DELIVERY-BINDING-REFERENCES-ACTIVE-AUTH", "HP-DELIVERY-BINDING-UNKNOWN-AUTH-REJECTED", "HI-RESPONSE-CAPABILITY-CONSUMED-AFTER-COMMIT", "HI-FAILED-COMMIT-PRESERVES-RETRY", "CAT-050-HUMAN-PARTICIPANT-BIND-VALID"] },
  { invariantId: "INV-037-DIRECTED-FINITE-HUMAN-INTERACTION", description: "Every human question binds an immutable question revision and response specification to exact work, origin identity, recipients, purpose, effect, response policy, deadline, confidentiality, and causal lineage without mixing human and actor execution identity.", decisionIds: ["HP-DEC-003"], negativeRequirementIds: [], fixtureIds: ["HI-HUMAN-ORIGIN-HAS-NO-ACTOR-EXECUTION", "HI-HUMAN-ORIGIN-CANNOT-BORROW-ACTOR", "HI-SELECTED-HUMAN-RESPONSE", "HI-UNSELECTED-HUMAN-REJECTED", "HI-STALE-QUESTION-REJECTED", "CAT-052-HUMAN-INTERACTION-OPEN-VALID", "CAT-052-HUMAN-INTERACTION-OPEN-INVALID"] },
  { invariantId: "INV-038-PRESENTATION-RETAINS-CANONICAL-QUESTION", description: "Operator or adapter presentation retains canonical and rendered identities plus presenter and delivery provenance; material divergence requires equivalence evidence or a new question revision, and delivery is never response or authorization.", decisionIds: ["HP-DEC-004"], negativeRequirementIds: [], fixtureIds: ["HI-DELIVERY-IS-NOT-RESPONSE", "HI-MATERIAL-RENDERING-CHANGE-REJECTED", "HI-OPERATOR-RELAY-NOT-HUMAN-RESPONSE", "CAT-053-HUMAN-DELIVERY-VALID", "CAT-053-HUMAN-DELIVERY-INVALID"] },
  { invariantId: "INV-039-EXACT-AUTHENTICATED-HUMAN-RESPONSE", description: "A response requires the exact selected active HUMAN with null actor execution, current question and profile, matching response specification, active authentication and delivery lineage, unique idempotent submission, and retained evidence.", decisionIds: ["HP-DEC-005"], negativeRequirementIds: [], fixtureIds: ["HI-SELECTED-HUMAN-RESPONSE", "HI-UNSELECTED-HUMAN-REJECTED", "HI-STALE-QUESTION-REJECTED", "HI-OPERATOR-RELAY-NOT-HUMAN-RESPONSE", "HI-HUMAN-RESPONSE-CANNOT-CARRY-ACTOR", "CAT-054-HUMAN-RESPONSE-VALID", "CAT-054-HUMAN-RESPONSE-INVALID"] },
  { invariantId: "INV-040-HUMAN-RESPONSE-POLICY-AND-EFFECT", description: "Response satisfaction counts distinct selected humans under EXACT_ONE, ANY_ONE, ALL, or QUORUM, while advice and evidence remain non-authoritative unless exact scoped command authorization independently admits the effect.", decisionIds: ["HP-DEC-001", "HP-DEC-003", "HP-DEC-005"], negativeRequirementIds: [], fixtureIds: ["HI-ALL-REQUIRES-EVERY-DISTINCT-HUMAN", "HI-QUORUM-SATISFIED-BY-DISTINCT-HUMANS", "HI-ADVISORY-RESPONSE-CANNOT-AUTHORIZE", "HI-AUTHORIZED-EFFECT-REQUIRES-SCOPE", "CAT-055-HUMAN-INTERACTION-CLOSE-VALID", "CAT-055-HUMAN-INTERACTION-CLOSE-INVALID"] },
  { invariantId: "INV-041-HUMAN-CONFIDENTIALITY-TIMEOUT-AND-RELAY", description: "Delivery never exceeds route confidentiality, silence is not consent, expiry is explicit, and operator-relayed testimony remains actor evidence until separately authenticated by the human.", decisionIds: ["HP-DEC-002", "HP-DEC-004", "HP-DEC-006"], negativeRequirementIds: [], fixtureIds: ["HP-CONFIDENTIALITY-CEILING-REJECTS", "HI-SILENCE-IS-NOT-CONSENT", "HI-OPERATOR-RELAY-NOT-HUMAN-RESPONSE", "CAT-056-HUMAN-INTERACTION-EXPIRE-VALID", "CAT-056-HUMAN-INTERACTION-EXPIRE-INVALID"] },
);
invariants.invariants.sort((left, right) => left.invariantId.localeCompare(right.invariantId));
overwriteJson(invariantsPath, invariants);

const traceabilityPath = "traceability/traceability.json";
const traceability = readJson(`CONTRACTS/${contractIdentity}/${traceabilityPath}`);
const decisionTests = {
  "OP-DEC-001": ["INV-028-OPERATOR-IDENTITY-SEPARATION", "OP-HUMAN-ATTRIBUTION-PRESERVED", "OP-ACTOR-CANNOT-IMPERSONATE-HUMAN", "OP-CONTROL-SURFACE-IS-NOT-PRINCIPAL", "CAT-043-OPERATOR-ROLE-BIND-VALID", "CAT-043-OPERATOR-ROLE-BIND-INVALID"],
  "OP-DEC-002": ["INV-029-OPERATOR-EXACT-AUTHORITY-AND-FENCING", "OP-STALE-EXECUTION-FENCED"],
  "OP-DEC-003": ["INV-028-OPERATOR-IDENTITY-SEPARATION", "INV-029-OPERATOR-EXACT-AUTHORITY-AND-FENCING", "OP-ACTOR-EXPLICIT-AUTHORITY", "OP-ROLE-NAME-NO-AUTHORITY"],
  "OP-DEC-004": ["INV-029-OPERATOR-EXACT-AUTHORITY-AND-FENCING", "OP-ACTOR-EXPLICIT-AUTHORITY"],
  "OP-DEC-005": ["INV-030-HUMAN-REQUIRED-EXACT-HUMAN", "OP-HUMAN-REQUIRED-EXACT-HUMAN", "OP-HUMAN-REQUIRED-NOT-OPERATOR-ACTOR"],
  "HP-DEC-001": ["INV-035-DISTINCT-HUMAN-IDENTITY-AND-SCOPED-ROLE", "INV-040-HUMAN-RESPONSE-POLICY-AND-EFFECT", "HP-DISTINCT-HUMANS-SAME-ROLE", "HP-ROLE-LABEL-NO-INTRINSIC-AUTHORITY", "HP-SCOPED-HUMAN-AUTHORITY", "HP-EXPIRED-ROLE-REJECTED", "HP-REVOKED-HUMAN-REJECTED", "HI-ADVISORY-RESPONSE-CANNOT-AUTHORIZE", "HI-AUTHORIZED-EFFECT-REQUIRES-SCOPE", "CAT-050-HUMAN-PARTICIPANT-BIND-VALID", "CAT-050-HUMAN-PARTICIPANT-BIND-INVALID", "CAT-051-HUMAN-PARTICIPANT-REVOKE-VALID", "CAT-051-HUMAN-PARTICIPANT-REVOKE-INVALID"],
  "HP-DEC-002": ["INV-036-AUTHENTICATED-HUMAN-BINDING", "INV-041-HUMAN-CONFIDENTIALITY-TIMEOUT-AND-RELAY", "HP-AUTHENTICATION-BINDING-EXACT", "HP-CHANNEL-IDENTITY-NOT-HUMAN", "HP-DELIVERY-BINDING-REFERENCES-ACTIVE-AUTH", "HP-DELIVERY-BINDING-UNKNOWN-AUTH-REJECTED", "HP-CONFIDENTIALITY-CEILING-REJECTS", "HI-RESPONSE-CAPABILITY-CONSUMED-AFTER-COMMIT", "HI-FAILED-COMMIT-PRESERVES-RETRY", "CAT-050-HUMAN-PARTICIPANT-BIND-VALID", "CAT-051-HUMAN-PARTICIPANT-REVOKE-VALID"],
  "HP-DEC-003": ["INV-037-DIRECTED-FINITE-HUMAN-INTERACTION", "INV-040-HUMAN-RESPONSE-POLICY-AND-EFFECT", "HI-HUMAN-ORIGIN-HAS-NO-ACTOR-EXECUTION", "HI-HUMAN-ORIGIN-CANNOT-BORROW-ACTOR", "HI-SELECTED-HUMAN-RESPONSE", "HI-UNSELECTED-HUMAN-REJECTED", "HI-STALE-QUESTION-REJECTED", "HI-ALL-REQUIRES-EVERY-DISTINCT-HUMAN", "HI-QUORUM-SATISFIED-BY-DISTINCT-HUMANS", "CAT-052-HUMAN-INTERACTION-OPEN-VALID", "CAT-052-HUMAN-INTERACTION-OPEN-INVALID"],
  "HP-DEC-004": ["INV-038-PRESENTATION-RETAINS-CANONICAL-QUESTION", "INV-041-HUMAN-CONFIDENTIALITY-TIMEOUT-AND-RELAY", "HI-DELIVERY-IS-NOT-RESPONSE", "HI-MATERIAL-RENDERING-CHANGE-REJECTED", "HI-OPERATOR-RELAY-NOT-HUMAN-RESPONSE", "CAT-053-HUMAN-DELIVERY-VALID", "CAT-053-HUMAN-DELIVERY-INVALID"],
  "HP-DEC-005": ["INV-039-EXACT-AUTHENTICATED-HUMAN-RESPONSE", "INV-040-HUMAN-RESPONSE-POLICY-AND-EFFECT", "HI-SELECTED-HUMAN-RESPONSE", "HI-UNSELECTED-HUMAN-REJECTED", "HI-STALE-QUESTION-REJECTED", "HI-OPERATOR-RELAY-NOT-HUMAN-RESPONSE", "HI-HUMAN-RESPONSE-CANNOT-CARRY-ACTOR", "HI-RESPONSE-CAPABILITY-CONSUMED-AFTER-COMMIT", "HI-FAILED-COMMIT-PRESERVES-RETRY", "HI-ALL-REQUIRES-EVERY-DISTINCT-HUMAN", "HI-QUORUM-SATISFIED-BY-DISTINCT-HUMANS", "HI-ADVISORY-RESPONSE-CANNOT-AUTHORIZE", "HI-AUTHORIZED-EFFECT-REQUIRES-SCOPE", "CAT-054-HUMAN-RESPONSE-VALID", "CAT-054-HUMAN-RESPONSE-INVALID", "CAT-055-HUMAN-INTERACTION-CLOSE-VALID", "CAT-055-HUMAN-INTERACTION-CLOSE-INVALID"],
  "HP-DEC-006": ["INV-041-HUMAN-CONFIDENTIALITY-TIMEOUT-AND-RELAY", "HP-CONFIDENTIALITY-CEILING-REJECTS", "HI-SILENCE-IS-NOT-CONSENT", "HI-OPERATOR-RELAY-NOT-HUMAN-RESPONSE", "CAT-056-HUMAN-INTERACTION-EXPIRE-VALID", "CAT-056-HUMAN-INTERACTION-EXPIRE-INVALID"],
  "HC-DEC-001": ["INV-034-CONTINUOUS-POSTURE-IS-OBSERVED", "HC-CONTINUOUS-POSTURE-REQUIRES-HEALTH", "CAT-044-CONTINUITY-CONFIGURE-VALID", "CAT-044-CONTINUITY-CONFIGURE-INVALID"],
  "HC-DEC-002": ["INV-031-POWER-EPOCH-ADMISSION-FENCE", "INV-032-EXPLICIT-SUSPENSION-DISPOSITION", "HC-PLANNED-QUIESCENCE", "HC-QUIESCENCE-EPOCH-MUST-INCREMENT", "HC-ADMISSION-CLOSED-DURING-QUIESCENCE", "HC-SUSPEND-REQUIRES-COMPLETE-INVENTORY", "HC-SUSPEND-WITH-RECORDED-UNKNOWN", "CAT-045-QUIESCENCE-REQUEST-VALID", "CAT-045-QUIESCENCE-REQUEST-INVALID", "CAT-046-SUSPENDED-RECORD-VALID", "CAT-046-SUSPENDED-RECORD-INVALID"],
  "HC-DEC-003": ["INV-032-EXPLICIT-SUSPENSION-DISPOSITION", "HC-OLD-EPOCH-RESULT-QUARANTINED", "HC-SUSPEND-WITH-RECORDED-UNKNOWN"],
  "HC-DEC-004": ["INV-031-POWER-EPOCH-ADMISSION-FENCE", "INV-033-RECONCILE-BEFORE-RESUME", "HC-UNEXPECTED-OUTAGE-FENCES", "HC-BEGIN-RECONCILIATION", "HC-NO-DIRECT-SUSPENDED-TO-ACTIVE", "CAT-047-UNEXPECTED-OUTAGE-VALID", "CAT-047-UNEXPECTED-OUTAGE-INVALID", "CAT-048-RECONCILIATION-BEGIN-VALID", "CAT-048-RECONCILIATION-BEGIN-INVALID"],
  "HC-DEC-005": ["INV-033-RECONCILE-BEFORE-RESUME", "HC-RESUME-REQUIRES-EXACT-RECONCILIATION", "HC-RESUME-AFTER-EXACT-RECONCILIATION", "CAT-049-RESUME-VALID", "CAT-049-RESUME-INVALID"],
};
const decisionText = {
  "OP-DEC-001": "Keep the authenticated human principal, durable operator actor, and operator control surface as three non-interchangeable identities.",
  "OP-DEC-002": "Preserve the operator actor FQN across process restart while replacing ExecutionId and increasing fencing; semantic role replacement is explicit.",
  "OP-DEC-003": "Grant no authority from the operator name, prompt, role bundle, provider, model, or interface; require exact principal kind, policy, actor, and execution.",
  "OP-DEC-004": "Define the starter operator as a bounded coordinator, observer, proposal, escalation, and authorized-command role rather than a direct store writer or final authority.",
  "OP-DEC-005": "Resolve HUMAN_REQUIRED through an exact authenticated selected human participant or participant set; operator presentation or delivery does not resolve the decision.",
  "HP-DEC-001": "Represent multiple exact human principals with versioned scoped roles; role labels select eligible participants but grant no authority outside explicit current bindings.",
  "HP-DEC-002": "Bind authentication and delivery through opaque content-addressed active records; a channel identity is not a human principal and confidentiality ceilings fail closed.",
  "HP-DEC-003": "Use a finite directed human-interaction aggregate for ordinary consultation and decisions with exact recipients, question, response specification, policy, deadline, effect, confidentiality, and lineage.",
  "HP-DEC-004": "Permit operator presentation only with canonical/rendered identity and delivery provenance; delivery never counts as response, agreement, or authorization.",
  "HP-DEC-005": "Accept responses only from exact selected authenticated humans against current bindings and classify testimony separately from any independently authorized organizational effect.",
  "HP-DEC-006": "Close or expire interactions explicitly; silence is not consent, manual operator relay is actor evidence, and confidentiality follows delivery and downstream use.",
  "HC-DEC-001": "Prefer observably healthy continuous local operation while keeping closed-lid and always-on host mechanisms outside kernel semantics.",
  "HC-DEC-002": "Use ACTIVE to QUIESCING to SUSPENDED to RECONCILING to ACTIVE with monotonic power epoch and closed admission outside ACTIVE.",
  "HC-DEC-003": "Predeclare CANCEL_ON_SUSPEND, COMPLETE_AND_QUARANTINE, or DETACH_AND_RECONCILE per provider execution and never infer cancellation or rollback.",
  "HC-DEC-004": "Record unexpected outages, fence prior epochs, quarantine late results, and reconcile provider and local effects idempotently before resumption.",
  "HC-DEC-005": "Reopen admission only through an authorized resume transition after exact execution, service, outbox, change-stream, and power-epoch reconciliation.",
};
for (const requirementId of Object.keys(decisionText)) {
  traceability.requirements.push({ disposition: "BINDING", kind: "DECISION", requirementId, testIds: [...new Set(decisionTests[requirementId])].sort(), text: decisionText[requirementId] });
}
traceability.requirements.sort((left, right) => left.requirementId.localeCompare(right.requirementId));
overwriteJson(traceabilityPath, traceability);

const predecessorManifestBytes = fs.readFileSync(path.join(predecessorRoot, "manifest.json"));
const additiveTypes = catalogue.entries.map((entry) => entry.typeId).filter((typeId) => !predecessorTypeIds.includes(typeId)).sort();
writeJson("compatibility/from-0.6.0.json", {
  schemaVersion,
  contractName: "tekroo.kernel.contracts",
  contractVersion,
  predecessor: { contractVersion: predecessorVersion, contractIdentity: predecessorIdentity, manifestSha256: sha256(predecessorManifestBytes) },
  compatibilityClaim: "ADDITIVE_OPERATOR_MULTI_HUMAN_INTERACTION_AND_HOST_CONTINUITY_CONTROL",
  directions: { backward: "COMPATIBLE_ADAPTER_REQUIRED", forward: "NOT_COMPATIBLE", replay: "VERSION_GATED", adapter: "REQUIRED" },
  unchangedSemanticTypes: predecessorTypeIds,
  changedSemanticTypes: [],
  extendedVocabularyTypes: [],
  additiveTypes,
  migrationRules: [
    { source: predecessorSchemaVersion, target: schemaVersion, mode: "IDENTITY", appliesTo: predecessorTypeIds },
    { source: schemaVersion, target: schemaVersion, mode: "IDENTITY" },
  ],
  aggregateStateMigration: { mode: "INITIALIZE_SYSTEM_CONTINUITY_AND_HUMAN_BINDINGS_EXPLICITLY", requiredContext: ["operating_posture", "power_epoch", "continuity_policy_digest", "health_requirement_digest", "provider_dispositions", "human_participant_profiles", "human_selection_policy_digest"], historicalReplay: "RETAIN_0.6.0" },
  prohibitedInferences: [
    "A historical human action attributed to an operator actor is not silently rewritten as authenticated HUMAN authority.",
    "The role name operator does not prove authorization, ownership, acceptance, completion, or human identity.",
    "A human role label, shared channel, display label, email address, or unverified operator relay does not identify or authorize a HUMAN principal.",
    "Delivery, read status, silence, response text, or response-count equality does not prove consent, authority, or response-policy satisfaction.",
    "Historical messages are not silently converted into authenticated human-interaction responses or participant bindings.",
    "An absent continuity record does not prove that the host remained awake or that no remote work continued.",
    "Provider completion, lid opening, restored network, or process health does not prove organizational resumption.",
    "Cancellation request or transport closure does not prove cancellation or rollback of external effects.",
    "A late result from an earlier power epoch is evidence only and is never silently upconverted into an authoritative current result.",
  ],
  rollback: "Readers retain 0.6.0 for historical replay. Operator-role, human-participant, human-interaction, and system-continuity records are not down-converted or inferred into 0.6.0 state.",
});

let referenceRunner = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/reference-runner.mjs"), "utf8"));
referenceRunner = referenceRunner.replace(
  "function runSuccessorSetModel(fixture) {",
  `function runOperatorSeparationModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (action.action === "PRESENT_AUTHORITY") {
    if (current.sourceKind === "CONTROL_SURFACE") return { accepted: false, reason: "ADAPTER_NOT_PRINCIPAL" };
    if (current.sourceKind === "ACTOR" && current.claimedKind === "HUMAN") return { accepted: false, reason: "HUMAN_IMPERSONATION" };
    if (current.sourceKind !== current.claimedKind) return { accepted: false, reason: "PRINCIPAL_KIND_MISMATCH" };
    if (current.sourceKind === "ACTOR") {
      if (!current.currentExecution) return { accepted: false, reason: "STALE_EXECUTION" };
      if (!current.commandAuthorityKinds?.includes("ACTOR") || !current.explicitlyAuthorized) return { accepted: false, reason: "AUTHORITY_REQUIRED" };
    }
    if (!current.explicitlyAuthorized) return { accepted: false, reason: "AUTHORITY_REQUIRED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "ROUTE_HUMAN_REQUIRED") {
    if (current.targetKind !== "HUMAN") return { accepted: false, reason: "HUMAN_PRINCIPAL_REQUIRED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  return { accepted: false, reason: "INVALID_ACTION" };
}

function sameSet(left, right) {
  return left.length === right.length && [...left].sort().every((value, index) => value === [...right].sort()[index]);
}

function runTeamContinuityModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (action.action === "REQUEST_QUIESCENCE") {
    if (current.state !== "ACTIVE") return { accepted: false, reason: "INVALID_CONTROL_STATE", state: current.state, powerEpoch: current.powerEpoch, admissionOpen: current.admissionOpen };
    if (action.nextPowerEpoch !== current.powerEpoch + 1) return { accepted: false, reason: "POWER_EPOCH_MISMATCH", state: current.state, powerEpoch: current.powerEpoch, admissionOpen: current.admissionOpen };
    return { accepted: true, reason: "ACCEPTED", state: "QUIESCING", powerEpoch: action.nextPowerEpoch, admissionOpen: false };
  }
  if (action.action === "DISPATCH") {
    if (action.powerEpoch !== current.powerEpoch) return { accepted: false, reason: "STALE_POWER_EPOCH" };
    if (!current.admissionOpen) return { accepted: false, reason: "ADMISSION_CLOSED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "ACCEPT_RESULT") {
    if (action.powerEpoch !== current.powerEpoch) return { accepted: false, reason: "LATE_RESULT_QUARANTINED" };
    if (!current.admissionOpen) return { accepted: false, reason: "ADMISSION_CLOSED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "RECORD_SUSPENDED") {
    if (current.state !== "QUIESCING") return { accepted: false, reason: "INVALID_CONTROL_STATE", state: current.state };
    if (!action.allExecutionsRecorded) return { accepted: false, reason: "EXECUTION_INVENTORY_INCOMPLETE", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "SUSPENDED", admissionOpen: false, unresolvedExecutionIds: action.unresolvedExecutionIds };
  }
  if (action.action === "BEGIN_RECONCILIATION") {
    if (current.state !== "SUSPENDED" || action.powerEpoch !== current.powerEpoch) return { accepted: false, reason: "RECONCILIATION_PRECONDITION_FAILED", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "RECONCILING", admissionOpen: false, unresolvedExecutionIds: current.unresolvedExecutionIds };
  }
  if (action.action === "RECORD_UNEXPECTED_OUTAGE") {
    if (action.nextPowerEpoch !== current.powerEpoch + 1) return { accepted: false, reason: "POWER_EPOCH_MISMATCH", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "RECONCILING", powerEpoch: action.nextPowerEpoch, admissionOpen: false, unresolvedExecutionIds: action.knownInFlightExecutionIds };
  }
  if (action.action === "RESUME") {
    if (current.state !== "RECONCILING") return { accepted: false, reason: "RECONCILIATION_REQUIRED", state: current.state };
    if (action.powerEpoch !== current.powerEpoch) return { accepted: false, reason: "POWER_EPOCH_MISMATCH", state: current.state };
    if (!sameSet(current.unresolvedExecutionIds, action.reconciledExecutionIds)) return { accepted: false, reason: "UNRESOLVED_EXECUTIONS", state: current.state };
    if (!action.servicesHealthy || !action.outboxReconciled || !action.changeStreamReconciled) return { accepted: false, reason: "RESUME_PRECONDITION_FAILED", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "ACTIVE", powerEpoch: current.powerEpoch, admissionOpen: true, unresolvedExecutionIds: [] };
  }
  if (action.action === "OBSERVE_CONTINUOUS") {
    if (!action.servicesHealthy) return { accepted: false, reason: "HOST_CONTINUITY_LOST" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  return { accepted: false, reason: "INVALID_ACTION" };
}

function runHumanParticipantModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (action.action === "RESOLVE_RECIPIENTS") {
    const recipientIds = [...new Set(current.participantIds)];
    if (recipientIds.length !== current.participantIds.length) return { accepted: false, reason: "DUPLICATE_HUMAN_IDENTITY" };
    return { accepted: true, reason: "ACCEPTED", recipientIds };
  }
  if (action.action === "AUTHORIZE") {
    if (!current.active) return { accepted: false, reason: "PARTICIPANT_REVOKED" };
    if (current.roleCurrent === false) return { accepted: false, reason: "ROLE_BINDING_EXPIRED" };
    if (!current.scopeMatches) return { accepted: false, reason: "ROLE_SCOPE_MISMATCH" };
    if (!current.commandAuthorized) return { accepted: false, reason: "SCOPED_AUTHORITY_REQUIRED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "AUTHENTICATE") {
    if (!current.bindingActive) return { accepted: false, reason: "AUTHENTICATION_BINDING_INACTIVE" };
    if (!current.subjectMatches) return { accepted: false, reason: "AUTHENTICATION_SUBJECT_MISMATCH" };
    if (!current.assuranceSufficient) return { accepted: false, reason: "AUTHENTICATION_ASSURANCE_INSUFFICIENT" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "VALIDATE_PROFILE_BINDINGS") {
    if (!current.deliveryAuthenticationBindingIds.every((id) => current.activeAuthenticationBindingIds.includes(id))) {
      return { accepted: false, reason: "AUTHENTICATION_BINDING_REFERENCE_INVALID" };
    }
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "SELECT_DELIVERY_ROUTE") {
    const rank = { PUBLIC: 0, INTERNAL: 1, CONFIDENTIAL: 2, RESTRICTED: 3 };
    if (rank[current.requestedConfidentiality] > rank[current.routeCeiling]) return { accepted: false, reason: "CONFIDENTIALITY_CEILING_EXCEEDED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  return { accepted: false, reason: "INVALID_ACTION" };
}

function runHumanInteractionModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (action.action === "OPEN") {
    if (current.originKind === "HUMAN" && (current.originActorFqn !== null || current.originExecutionId !== null)) return { accepted: false, reason: "HUMAN_ACTOR_ATTRIBUTION" };
    if (current.originKind === "ACTOR" && (!current.originActorFqn || !current.originExecutionId)) return { accepted: false, reason: "ACTOR_EXECUTION_REQUIRED" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "RESPOND") {
    if (action.sourceKind !== "HUMAN") return { accepted: false, reason: "HUMAN_AUTHORITY_REQUIRED" };
    if (action.actorFqn !== undefined && action.actorFqn !== null) return { accepted: false, reason: "HUMAN_ACTOR_ATTRIBUTION" };
    if (!current.selectedHumanIds.includes(action.respondentId)) return { accepted: false, reason: "RECIPIENT_MISMATCH" };
    if (action.questionRevision !== current.questionRevision) return { accepted: false, reason: "QUESTION_REVISION_MISMATCH" };
    if (!current.participantActive) return { accepted: false, reason: "PARTICIPANT_REVOKED" };
    if (!current.authenticationValid) return { accepted: false, reason: "AUTHENTICATION_REQUIRED" };
    if (!current.responseSpecificationMatches) return { accepted: false, reason: "RESPONSE_SPECIFICATION_MISMATCH" };
    if (current.duplicate) return { accepted: false, reason: "DUPLICATE_RESPONSE" };
    return { accepted: true, reason: "ACCEPTED" };
  }
  if (action.action === "RECORD_DELIVERY") {
    if (current.renderedDiffers && !current.materialEquivalenceProven) return { accepted: false, reason: "PRESENTATION_EQUIVALENCE_REQUIRED" };
    return { accepted: true, reason: "DELIVERY_RECORDED", responsePolicySatisfied: false };
  }
  if (action.action === "ACCEPT_RESPONSE_CAPABILITY") {
    if (!current.credentialVerified) return { accepted: false, reason: "AUTHENTICATION_REQUIRED", capabilityConsumed: false };
    if (!current.commitSucceeded) return { accepted: false, reason: "COMMIT_FAILED", capabilityConsumed: false };
    return { accepted: true, reason: "ACCEPTED", capabilityConsumed: true };
  }
  if (action.action === "EVALUATE_RESPONSE_POLICY") {
    const accepted = [...new Set(current.acceptedHumanIds)];
    if (!accepted.every((id) => current.selectedHumanIds.includes(id))) return { accepted: false, reason: "RECIPIENT_MISMATCH" };
    let satisfied = false;
    if (current.responsePolicy.kind === "EXACT_ONE") satisfied = current.selectedHumanIds.length === 1 && accepted.length === 1;
    else if (current.responsePolicy.kind === "ANY_ONE") satisfied = accepted.length >= 1;
    else if (current.responsePolicy.kind === "ALL") satisfied = sameSet(current.selectedHumanIds, accepted);
    else if (current.responsePolicy.kind === "QUORUM") satisfied = accepted.length >= current.responsePolicy.quorum;
    return satisfied
      ? { accepted: true, reason: "RESPONSE_POLICY_SATISFIED" }
      : { accepted: false, reason: "RESPONSE_POLICY_UNSATISFIED" };
  }
  if (action.action === "EVALUATE_EFFECT") {
    if (current.declaredEffect === "ADVISORY_ONLY") return { accepted: true, reason: "RECORDED_AS_ADVISORY", resolvedEffect: "ADVISORY_ONLY" };
    if (current.declaredEffect === "EVIDENCE_ONLY") return { accepted: true, reason: "RECORDED_AS_EVIDENCE", resolvedEffect: "EVIDENCE_ONLY" };
    if (!current.scopedCommandAuthority) return { accepted: false, reason: "SCOPED_AUTHORITY_REQUIRED", resolvedEffect: "EVIDENCE_ONLY" };
    return { accepted: true, reason: "AUTHORIZED_EFFECT", resolvedEffect: "AUTHORIZED_EFFECT" };
  }
  if (action.action === "EXPIRE") {
    if (!current.deadlineExpired) return { accepted: false, reason: "DEADLINE_NOT_EXPIRED" };
    if (action.silenceIsConsent) return { accepted: false, reason: "SILENCE_CANNOT_AUTHORIZE" };
    return { accepted: true, reason: "EXPIRED_WITHOUT_CONSENT", state: "EXPIRED" };
  }
  return { accepted: false, reason: "INVALID_ACTION" };
}

function runSuccessorSetModel(fixture) {`,
);
referenceRunner = referenceRunner.replace(
  '  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
  '  } else if (fixture.kind === "HUMAN_INTERACTION_MODEL") {\n    actual = runHumanInteractionModel(fixture);\n  } else if (fixture.kind === "HUMAN_PARTICIPANT_MODEL") {\n    actual = runHumanParticipantModel(fixture);\n  } else if (fixture.kind === "OPERATOR_SEPARATION_MODEL") {\n    actual = runOperatorSeparationModel(fixture);\n  } else if (fixture.kind === "TEAM_CONTINUITY_MODEL") {\n    actual = runTeamContinuityModel(fixture);\n  } else if (fixture.kind === "SUCCESSOR_SET_MODEL") {',
);
fs.mkdirSync(path.join(packageRoot, "runner"), { recursive: true });
fs.writeFileSync(path.join(packageRoot, "runner/reference-runner.mjs"), referenceRunner, { flag: "wx" });

let validator = advanceString(fs.readFileSync(path.join(predecessorRoot, "runner/validate-package.mjs"), "utf8"))
  .replace('manifest.contract.version === "0.6.0"', 'manifest.contract.version === "0.7.0"')
  .replace("decisions.length === 22", "decisions.length === 38")
  .replaceAll("compatibility/from-0.5.0.json", "compatibility/from-0.6.0.json")
  .replace(
    'compatibility.predecessor?.contractVersion === "0.5.0" && compatibility.contractVersion === "0.6.0" && compatibility.directions?.adapter === "REQUIRED"',
    'compatibility.predecessor?.contractVersion === "0.6.0" && compatibility.contractVersion === "0.7.0" && compatibility.directions?.adapter === "REQUIRED"',
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
  if (relativePath === "runner/validate-package.mjs") return ["catalogue/kernel-catalogue.json", "schemas/payloads.schema.json", "invariants/invariants.json", "traceability/traceability.json", "compatibility/from-0.6.0.json", "runner/protocol.json"];
  if (relativePath.startsWith("schemas/") && relativePath !== "schemas/core.schema.json") return ["schemas/core.schema.json"];
  return [];
}

const payloadPaths = walk(packageRoot).filter((relativePath) => !["manifest.json", "manifest.sha256"].includes(relativePath));
const fileInventory = payloadPaths.map((relativePath) => {
  const bytes = fs.readFileSync(path.join(packageRoot, relativePath));
  const isJson = relativePath.endsWith(".json");
  return { path: relativePath, role: roleFor(relativePath), mediaType: isJson ? "application/json" : "text/javascript", bytes: bytes.length, sha256: sha256(bytes), canonicalJsonSha256: isJson ? sha256(canonical(JSON.parse(bytes.toString("utf8")))) : null, dependencies: dependenciesFor(relativePath) };
});
const generatorPath = "scripts/generate_phase3_contract_0_7.mjs";
const manifest = {
  schemaVersion,
  contract: { name: "tekroo.kernel.contracts", version: contractVersion, status: "FROZEN_CANDIDATE" },
  generatedAt: "2026-08-13T00:00:00Z",
  authority: { decisionAuthority: "Principal", implementationAuthority: "PENDING_CANDIDATE_ACCEPTANCE", productionAuthority: "NONE" },
  sourceLineage: [
    { path: "docs/architecture/022-operator-and-host-continuity-control.md", sha256: sha256(read("docs/architecture/022-operator-and-host-continuity-control.md")) },
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
    { name: "contract-structure", authorization: "OPERATOR_AND_CONTINUITY_DESIGN_APPROVAL", required: true, currentStatus: "READY_TO_RUN" },
    { name: "core-hermetic", authorization: "AFTER_PRINCIPAL_CANDIDATE_ACCEPTANCE", required: true, currentStatus: "NOT_RUN" },
    { name: "mongo-integration", authorization: "AFTER_PRINCIPAL_CANDIDATE_ACCEPTANCE", required: true, currentStatus: "NOT_RUN" },
    { name: "synthesized-merge", authorization: "AFTER_PRINCIPAL_CANDIDATE_ACCEPTANCE", required: true, currentStatus: "NOT_RUN" },
    { name: "provider-e2e", authorization: "SEPARATE_PRINCIPAL_AUTHORIZATION", required: false, currentStatus: "NOT_RUN" },
  ],
  files: fileInventory,
  knownLimitations: [
    "Contract-structure and reference PASS do not qualify the Go implementation, MongoDB integration, OpenHands, SMA, a model provider, host power behavior, or production operation.",
    "The operator role bundle has not been implemented or signed; this package defines its identity and authority boundary only.",
    "No human participant directory, authentication provider, channel adapter, delivery route, response portal, or confidentiality enforcement has been implemented or empirically qualified.",
    "The package uses opaque digests for credentials, endpoints, canonical questions, renderings, and responses; concrete secure artifact storage and access policy remain implementation responsibilities.",
    "In-person attestation is only a profile vocabulary option and requires separate protocol design and qualification before use.",
    "No macOS closed-display, sleep/wake, dedicated-host, remote-job, or provider-cancellation path has been empirically qualified.",
    "Automatic planned suspension depends on a separately implemented and qualified host power-event adapter; unexpected outage reconciliation remains mandatory.",
    "Existing live 0.6.0 systems require explicit continuity and human-participant initialization; no host state, participant identity, channel binding, response, consent, or historical human attribution is inferred.",
  ],
};
fs.writeFileSync(manifestPath, pretty(manifest), { flag: "wx" });
const manifestDigest = sha256(fs.readFileSync(manifestPath));
fs.writeFileSync(checksumPath, `${manifestDigest}  manifest.json\n`, { flag: "wx" });
process.stdout.write(`GENERATED ${contractIdentity} ${manifestDigest} ${fileInventory.length}\n`);
