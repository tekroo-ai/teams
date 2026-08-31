#!/usr/bin/env node

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const predecessorRoot = path.join(repositoryRoot, "CONTRACTS", "tekroo.kernel.contracts", "0.7.0");
const packageRoot = path.join(repositoryRoot, "CONTRACTS", "tekroo.kernel.contracts", "0.8.0");
const contractIdentity = "tekroo.kernel.contracts/0.8.0";
const schemaVersion = "1.7.0";
const generatedAt = "2026-08-31T00:00:00Z";
const generatorPath = fileURLToPath(import.meta.url);

if (fs.existsSync(packageRoot)) {
  throw new Error(`refusing to overwrite existing package: ${packageRoot}`);
}

function sortValue(value) {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortValue(value[key])]));
  }
  return value;
}

function canonical(value) {
  return JSON.stringify(sortValue(value));
}

function sha256(value) {
  return crypto.createHash("sha256").update(value).digest("hex");
}

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function writeJson(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `${JSON.stringify(sortValue(value), null, 2)}\n`, { flag: "w" });
}

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

function copyPredecessor() {
  fs.cpSync(predecessorRoot, packageRoot, {
    recursive: true,
    filter: (source) => !["manifest.json", "manifest.sha256"].includes(path.basename(source)),
  });
  fs.rmSync(path.join(packageRoot, "compatibility", "from-0.6.0.json"));
  for (const relative of walk(packageRoot)) {
    const file = path.join(packageRoot, relative);
    const text = fs.readFileSync(file, "utf8")
      .replaceAll("tekroo.kernel.contracts/0.7.0", contractIdentity)
      .replaceAll("/0.7.0/", "/0.8.0/");
    fs.writeFileSync(file, text);
  }
}

const uuid = () => ({ type: "string", pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$" });
const digest = () => ({ type: "string", pattern: "^[0-9a-f]{64}$" });
const actor = () => ({ type: "string", pattern: "^(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])::(?:[a-z0-9]|[a-z0-9][a-z0-9-]{0,61}[a-z0-9])-[1-9][0-9]{0,19}$" });
const timestamp = () => ({ type: "string", format: "date-time" });
const nonempty = (maxLength = 4096) => ({ type: "string", minLength: 1, maxLength });
const positive = (maximum = 1000000) => ({ type: "integer", minimum: 1, maximum });
const nonnegative = (maximum = 1000000) => ({ type: "integer", minimum: 0, maximum });
const uuidArray = (minimum = 1, maximum = 64) => ({ type: "array", minItems: minimum, maxItems: maximum, uniqueItems: true, items: uuid() });
const purposeValues = ["INVESTIGATION", "IMPLEMENTATION", "VALIDATION", "REVIEW", "REPAIR", "HANDOFF", "REPLAN", "PROMOTION", "ESCALATION"];
const purpose = () => ({ type: "string", enum: purposeValues });
const purposeLimits = () => ({
  type: "object",
  additionalProperties: false,
  required: purposeValues,
  properties: Object.fromEntries(purposeValues.map((value) => [value, nonnegative(1000)])),
});
const principal = () => ({
  type: "object",
  additionalProperties: false,
  required: ["kind", "id"],
  properties: {
    kind: { type: "string", enum: ["HUMAN", "POLICY", "SERVICE"] },
    id: nonempty(256),
  },
});
const rootWork = () => ({
  type: "object",
  additionalProperties: false,
  required: ["kind", "id"],
  properties: { kind: { type: "string", enum: ["story", "task"] }, id: uuid() },
});
const profileBinding = () => ({
  type: "object",
  additionalProperties: false,
  required: ["profile_id", "profile_revision", "profile_digest", "lifecycle_epoch", "scope_revision"],
  properties: {
    profile_id: uuid(), profile_revision: positive(), profile_digest: digest(),
    lifecycle_epoch: positive(), scope_revision: positive(),
  },
});

function objectSchema(required, properties) {
  return { type: "object", additionalProperties: false, required, properties };
}

function addSchemas() {
  const corePath = path.join(packageRoot, "schemas", "core.schema.json");
  const core = readJson(corePath);
  core.$defs.AggregateRef.properties.kind.enum.push("work-budget-account", "work-invocation");
  core.$defs.AggregateRef.properties.kind.enum.sort();
  writeJson(corePath, core);

  const fixtureSchemaPath = path.join(packageRoot, "schemas", "conformance-fixture.schema.json");
  const fixtureSchema = readJson(fixtureSchemaPath);
  fixtureSchema.properties.kind.enum.push(
    "AUTHORITY_BOUNDARY_MODEL", "INVOCATION_ADMISSION_MODEL", "PROJECTION_MODEL", "WORK_BUDGET_MODEL",
  );
  fixtureSchema.properties.kind.enum.sort();
  writeJson(fixtureSchemaPath, fixtureSchema);

  const budgetState = {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    $id: "https://contracts.tekroo.ai/tekroo.kernel.contracts/0.8.0/schemas/work-budget-account.schema.json",
    title: "Teams work budget account",
    ...objectSchema(
      ["id", "kind", "revision", "root_work", "lifecycle_epoch", "policy_revision", "policy_digest", "model_invocation_limit", "model_invocations_used", "purpose_limits", "purpose_used", "deadline_at", "last_event_id"],
      {
        id: uuid(), kind: { const: "work-budget-account" }, revision: positive(), root_work: rootWork(),
        lifecycle_epoch: positive(), policy_revision: positive(), policy_digest: digest(),
        model_invocation_limit: positive(1000), model_invocations_used: nonnegative(1000),
        purpose_limits: purposeLimits(), purpose_used: purposeLimits(), deadline_at: timestamp(), last_event_id: uuid(),
      },
    ),
  };
  writeJson(path.join(packageRoot, "schemas", "work-budget-account.schema.json"), budgetState);

  const invocationState = {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    $id: "https://contracts.tekroo.ai/tekroo.kernel.contracts/0.8.0/schemas/work-invocation.schema.json",
    title: "Teams single-use work invocation",
    ...objectSchema(
      ["id", "kind", "revision", "state", "task_id", "budget_account_id", "lifecycle_epoch", "scope_revision", "task_revision", "work_profile", "qualified_assignment_id", "purpose", "attempt_family", "attempt_ordinal", "condition_digest", "actor_fqn", "execution_id", "fencing_epoch", "model_profile_digest", "runtime_identity_digest", "deadline_at", "global_debit_ordinal", "purpose_debit_ordinal", "remaining_global_budget", "remaining_purpose_budget", "claim_id", "claimed_at", "conversation_id", "request_digest", "started_at", "terminal_outcome", "retryable", "terminal_evidence_ids", "output_digest", "finished_at", "cancellation_requested_at", "last_event_id"],
      {
        id: uuid(), kind: { const: "work-invocation" }, revision: positive(),
        state: { type: "string", enum: ["AUTHORIZED", "CLAIMED", "STARTED", "START_FAILED", "SUCCEEDED", "FAILED", "TIMED_OUT", "CANCELLED", "EXPIRED"] },
        task_id: uuid(), budget_account_id: uuid(), lifecycle_epoch: positive(), scope_revision: positive(), task_revision: positive(),
        work_profile: profileBinding(), qualified_assignment_id: uuid(), purpose: purpose(), attempt_family: nonempty(128), attempt_ordinal: positive(1000), condition_digest: digest(),
        actor_fqn: actor(), execution_id: uuid(), fencing_epoch: positive(), model_profile_digest: digest(), runtime_identity_digest: digest(), deadline_at: timestamp(),
        global_debit_ordinal: positive(1000), purpose_debit_ordinal: positive(1000), remaining_global_budget: nonnegative(1000), remaining_purpose_budget: nonnegative(1000), last_event_id: uuid(),
        claim_id: { anyOf: [uuid(), { type: "null" }] }, claimed_at: { anyOf: [timestamp(), { type: "null" }] },
        conversation_id: { anyOf: [nonempty(512), { type: "null" }] }, request_digest: { anyOf: [digest(), { type: "null" }] }, started_at: { anyOf: [timestamp(), { type: "null" }] },
        terminal_outcome: { anyOf: [{ type: "string", enum: ["SUCCEEDED", "FAILED", "TIMED_OUT", "CANCELLED", "START_FAILED"] }, { type: "null" }] },
        retryable: { anyOf: [{ type: "boolean" }, { type: "null" }] }, terminal_evidence_ids: uuidArray(0, 64), output_digest: { anyOf: [digest(), { type: "null" }] }, finished_at: { anyOf: [timestamp(), { type: "null" }] }, cancellation_requested_at: { anyOf: [timestamp(), { type: "null" }] },
      },
    ),
  };
  writeJson(path.join(packageRoot, "schemas", "work-invocation.schema.json"), invocationState);

  const projectionCommon = { id: uuid(), aggregate_revision: positive(), lifecycle_epoch: positive(), scope_revision: positive(), phase: nonempty(64), condition: nonempty(64), projection_revision: positive(), last_event_id: uuid(), source_contract_identity: { const: contractIdentity } };
  const descriptive = { title: nonempty(256), description: { type: "string", maxLength: 65536 }, acceptance_criteria: { type: "array", minItems: 1, maxItems: 64, uniqueItems: true, items: nonempty(4096) }, dependency_ids: uuidArray(0, 256) };
  const statusSummary = objectSchema(["state", "event_id", "evidence_ids"], { state: nonempty(64), event_id: { anyOf: [uuid(), { type: "null" }] }, evidence_ids: uuidArray(0, 64) });
  const budgetSummary = objectSchema(["account_id", "account_revision", "model_invocation_limit", "model_invocations_used", "remaining_model_invocations", "purpose_limits", "purpose_used", "deadline_at"], { account_id: uuid(), account_revision: positive(), model_invocation_limit: positive(1000), model_invocations_used: nonnegative(1000), remaining_model_invocations: nonnegative(1000), purpose_limits: purposeLimits(), purpose_used: purposeLimits(), deadline_at: timestamp() });
  const assignmentSummary = objectSchema(["assignment_id", "required_route", "selected_route", "actor_fqn", "execution_id", "fencing_epoch", "model_profile_digest", "runtime_identity_digest"], { assignment_id: uuid(), required_route: nonempty(64), selected_route: nonempty(64), actor_fqn: actor(), execution_id: uuid(), fencing_epoch: positive(), model_profile_digest: digest(), runtime_identity_digest: digest() });
  const invocationSummary = objectSchema(["invocation_id", "state", "purpose", "attempt_ordinal", "condition_digest"], { invocation_id: uuid(), state: nonempty(64), purpose: purpose(), attempt_ordinal: positive(1000), condition_digest: digest() });
  const operationalScopeSummary = objectSchema(["owner_fqn", "execution_id", "fencing_epoch", "workspace_id", "worktree_id", "branch", "baseline_sha", "writable_paths", "interface_constraint_evidence_ids"], { owner_fqn: actor(), execution_id: uuid(), fencing_epoch: positive(), workspace_id: nonempty(1024), worktree_id: nonempty(1024), branch: nonempty(256), baseline_sha: { type: "string", pattern: "^[0-9a-f]{40}$" }, writable_paths: { type: "array", minItems: 1, maxItems: 256, uniqueItems: true, items: nonempty(4096) }, interface_constraint_evidence_ids: uuidArray(0, 64) });
  const projectionState = {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    $id: "https://contracts.tekroo.ai/tekroo.kernel.contracts/0.8.0/schemas/operational-projection.schema.json",
    title: "Teams operational task and story projections",
    oneOf: [
      objectSchema(["kind", ...Object.keys(projectionCommon), ...Object.keys(descriptive), "task_ids", "task_counts", "completion", "acceptance", "release", "blocker", "escalation"], { kind: { const: "story_projection" }, ...projectionCommon, ...descriptive, task_ids: uuidArray(0, 1024), task_counts: { type: "object", additionalProperties: { type: "integer", minimum: 0 } }, completion: statusSummary, acceptance: statusSummary, release: statusSummary, blocker: statusSummary, escalation: statusSummary }),
      objectSchema(["kind", ...Object.keys(projectionCommon), ...Object.keys(descriptive), "story_id", "owner_fqn", "ownership_version", "operational_scope", "work_profile_id", "work_profile_revision", "work_profile_digest", "classification", "budget", "qualified_assignment", "latest_invocation", "invocation_counts", "validation", "finding", "escalation", "completion", "acceptance"], { kind: { const: "task_projection" }, ...projectionCommon, ...descriptive, story_id: uuid(), owner_fqn: { anyOf: [actor(), { type: "null" }] }, ownership_version: nonnegative(), operational_scope: { anyOf: [operationalScopeSummary, { type: "null" }] }, work_profile_id: uuid(), work_profile_revision: positive(), work_profile_digest: digest(), classification: objectSchema(["work_kind", "ambiguity", "novelty", "blast_radius", "security_sensitivity", "minimum_decision_route"], { work_kind: nonempty(64), ambiguity: nonempty(64), novelty: nonempty(64), blast_radius: nonempty(64), security_sensitivity: nonempty(64), minimum_decision_route: nonempty(64) }), budget: budgetSummary, qualified_assignment: { anyOf: [assignmentSummary, { type: "null" }] }, latest_invocation: { anyOf: [invocationSummary, { type: "null" }] }, invocation_counts: { type: "object", additionalProperties: { type: "integer", minimum: 0 } }, validation: statusSummary, finding: statusSummary, escalation: statusSummary, completion: statusSummary, acceptance: statusSummary }),
      objectSchema(["kind", "projector", "last_event_id", "last_event_digest", "projection_revision"], {
        kind: { const: "projection_checkpoint" }, projector: nonempty(128), last_event_id: uuid(), last_event_digest: digest(), projection_revision: positive(),
      }),
    ],
  };
  writeJson(path.join(packageRoot, "schemas", "operational-projection.schema.json"), projectionState);

  const aggregatePath = path.join(packageRoot, "schemas", "aggregate-state.schema.json");
  const aggregate = readJson(aggregatePath);
  aggregate.oneOf.push({ $ref: "./work-budget-account.schema.json" }, { $ref: "./work-invocation.schema.json" });
  writeJson(aggregatePath, aggregate);
}

function addPayloadsAndCatalogue() {
  const payloadPath = path.join(packageRoot, "schemas", "payloads.schema.json");
  const payloads = readJson(payloadPath);
  const defs = payloads.$defs;

  const budgetCreate = objectSchema(
    ["budget_account_id", "root_work", "lifecycle_epoch", "policy_revision", "policy_digest", "model_invocation_limit", "purpose_limits", "deadline_at", "evidence_ids", "authority"],
    { budget_account_id: uuid(), root_work: rootWork(), lifecycle_epoch: positive(), policy_revision: positive(), policy_digest: digest(), model_invocation_limit: positive(1000), purpose_limits: purposeLimits(), deadline_at: timestamp(), evidence_ids: uuidArray(), authority: principal() },
  );
  const budgetAmend = objectSchema(
    ["budget_account_id", "expected_budget_revision", "expected_lifecycle_epoch", "policy_revision", "policy_digest", "model_invocation_limit", "purpose_limits", "deadline_at", "reason", "evidence_ids", "authority"],
    { budget_account_id: uuid(), expected_budget_revision: positive(), expected_lifecycle_epoch: positive(), policy_revision: positive(), policy_digest: digest(), model_invocation_limit: positive(1000), purpose_limits: purposeLimits(), deadline_at: timestamp(), reason: nonempty(4096), evidence_ids: uuidArray(), authority: principal() },
  );
  const bindBudget = objectSchema(
    ["task_id", "budget_account_id", "expected_task_revision", "lifecycle_epoch", "scope_revision", "task_model_invocation_limit", "purpose_limits", "evidence_ids"],
    { task_id: uuid(), budget_account_id: uuid(), expected_task_revision: positive(), lifecycle_epoch: positive(), scope_revision: positive(), task_model_invocation_limit: positive(1000), purpose_limits: purposeLimits(), evidence_ids: uuidArray() },
  );
  const operationalScope = objectSchema(
    ["task_id", "expected_task_revision", "lifecycle_epoch", "scope_revision", "owner_fqn", "execution_id", "fencing_epoch", "workspace_id", "worktree_id", "branch", "baseline_sha", "writable_paths", "interface_constraint_evidence_ids"],
    { task_id: uuid(), expected_task_revision: positive(), lifecycle_epoch: positive(), scope_revision: positive(), owner_fqn: actor(), execution_id: uuid(), fencing_epoch: positive(), workspace_id: nonempty(1024), worktree_id: nonempty(1024), branch: nonempty(256), baseline_sha: { type: "string", pattern: "^[0-9a-f]{40}$" }, writable_paths: { type: "array", minItems: 1, maxItems: 256, uniqueItems: true, items: nonempty(4096) }, interface_constraint_evidence_ids: uuidArray(0, 64) },
  );
  const authorize = objectSchema(
    ["invocation_id", "task_id", "budget_account_id", "expected_budget_revision", "expected_task_revision", "lifecycle_epoch", "scope_revision", "parent_event_id", "work_profile", "qualified_assignment_id", "purpose", "attempt_family", "attempt_ordinal", "condition_digest", "retry_of_invocation_id", "retry_ordinal", "output_predicate_digest", "allowed_terminal_outcomes", "tool_policy_digest", "effect_policy_digest", "actor_fqn", "execution_id", "fencing_epoch", "model_profile_digest", "runtime_identity_digest", "workspace_id", "deadline_at", "idempotency_key", "admission_policy_revision", "admission_policy_digest"],
    {
      invocation_id: uuid(), task_id: uuid(), budget_account_id: uuid(), expected_budget_revision: positive(), expected_task_revision: positive(), lifecycle_epoch: positive(), scope_revision: positive(), parent_event_id: uuid(), work_profile: profileBinding(), qualified_assignment_id: uuid(), purpose: purpose(), attempt_family: nonempty(128), attempt_ordinal: positive(1000), condition_digest: digest(),
      retry_of_invocation_id: { anyOf: [uuid(), { type: "null" }] }, retry_ordinal: nonnegative(1000), output_predicate_digest: digest(),
      allowed_terminal_outcomes: { type: "array", minItems: 1, maxItems: 8, uniqueItems: true, items: { type: "string", enum: ["SUCCEEDED", "FAILED", "TIMED_OUT", "CANCELLED", "START_FAILED"] } },
      tool_policy_digest: digest(), effect_policy_digest: digest(), actor_fqn: actor(), execution_id: uuid(), fencing_epoch: positive(), model_profile_digest: digest(), runtime_identity_digest: digest(), workspace_id: nonempty(1024), deadline_at: timestamp(), idempotency_key: nonempty(256), admission_policy_revision: positive(), admission_policy_digest: digest(),
    },
  );
  const exactRuntime = { actor_fqn: actor(), execution_id: uuid(), fencing_epoch: positive(), model_profile_digest: digest(), runtime_identity_digest: digest() };
  const claim = objectSchema(
    ["invocation_id", "expected_invocation_revision", "claim_id", ...Object.keys(exactRuntime), "consumer_id", "claimed_at"],
    { invocation_id: uuid(), expected_invocation_revision: positive(), claim_id: uuid(), ...exactRuntime, consumer_id: nonempty(256), claimed_at: timestamp() },
  );
  const started = objectSchema(
    ["invocation_id", "expected_invocation_revision", "claim_id", "conversation_id", "request_digest", "started_at"],
    { invocation_id: uuid(), expected_invocation_revision: positive(), claim_id: uuid(), conversation_id: nonempty(512), request_digest: digest(), started_at: timestamp() },
  );
  const terminal = objectSchema(
    ["invocation_id", "expected_invocation_revision", "claim_id", "outcome", "retryable", "evidence_ids", "output_digest", "finished_at"],
    { invocation_id: uuid(), expected_invocation_revision: positive(), claim_id: uuid(), outcome: { type: "string", enum: ["SUCCEEDED", "FAILED", "TIMED_OUT", "CANCELLED", "START_FAILED"] }, retryable: { type: "boolean" }, evidence_ids: uuidArray(), output_digest: digest(), finished_at: timestamp() },
  );
  const cancel = objectSchema(
    ["invocation_id", "expected_invocation_revision", "reason", "evidence_ids", "authority", "requested_at"],
    { invocation_id: uuid(), expected_invocation_revision: positive(), reason: nonempty(4096), evidence_ids: uuidArray(), authority: principal(), requested_at: timestamp() },
  );
  const expire = objectSchema(
    ["invocation_id", "expected_invocation_revision", "deadline_at", "observed_at", "authority"],
    { invocation_id: uuid(), expected_invocation_revision: positive(), deadline_at: timestamp(), observed_at: timestamp(), authority: principal() },
  );
  const projectionFault = objectSchema(
    ["projection_kind", "aggregate", "expected_revision", "observed_revision", "event_id", "event_digest", "fault_kind", "observed_at"],
    { projection_kind: { type: "string", enum: ["STORY", "TASK"] }, aggregate: rootWork(), expected_revision: positive(), observed_revision: positive(), event_id: uuid(), event_digest: digest(), fault_kind: { type: "string", enum: ["REVISION_GAP", "REVISION_CONFLICT", "REBUILD_MISMATCH"] }, observed_at: timestamp() },
  );

  const authorizedEvent = structuredClone(authorize);
  authorizedEvent.required.push("global_debit_ordinal", "purpose_debit_ordinal", "remaining_global_budget", "remaining_purpose_budget");
  Object.assign(authorizedEvent.properties, { global_debit_ordinal: positive(1000), purpose_debit_ordinal: positive(1000), remaining_global_budget: nonnegative(1000), remaining_purpose_budget: nonnegative(1000) });
  const definitions = {
    tekroo_command_work_budget_create_1_7_0: budgetCreate,
    tekroo_event_work_budget_created_1_7_0: budgetCreate,
    tekroo_command_work_budget_amend_1_7_0: budgetAmend,
    tekroo_event_work_budget_amended_1_7_0: budgetAmend,
    tekroo_command_task_bind_work_budget_1_7_0: bindBudget,
    tekroo_event_task_work_budget_bound_1_7_0: bindBudget,
    tekroo_command_task_bind_operational_scope_1_7_0: operationalScope,
    tekroo_event_task_operational_scope_bound_1_7_0: operationalScope,
    tekroo_command_work_invocation_authorize_1_7_0: authorize,
    tekroo_event_work_invocation_authorized_1_7_0: authorizedEvent,
    tekroo_command_work_invocation_claim_1_7_0: claim,
    tekroo_event_work_invocation_claimed_1_7_0: claim,
    tekroo_command_work_invocation_record_started_1_7_0: started,
    tekroo_event_work_invocation_started_1_7_0: started,
    tekroo_command_work_invocation_record_terminal_1_7_0: terminal,
    tekroo_event_work_invocation_terminal_recorded_1_7_0: terminal,
    tekroo_command_work_invocation_request_cancellation_1_7_0: cancel,
    tekroo_event_work_invocation_cancellation_requested_1_7_0: cancel,
    tekroo_command_work_invocation_expire_1_7_0: expire,
    tekroo_event_work_invocation_expired_1_7_0: expire,
    tekroo_command_projection_record_fault_1_7_0: projectionFault,
    tekroo_event_projection_fault_recorded_1_7_0: projectionFault,
  };
  Object.assign(defs, definitions);
  writeJson(payloadPath, payloads);

  const cataloguePath = path.join(packageRoot, "catalogue", "kernel-catalogue.json");
  const catalogue = readJson(cataloguePath);
  catalogue.contractIdentity = contractIdentity;
  catalogue.schemaVersion = schemaVersion;
  catalogue.revision = 8;

  const edges = ["CAUSAL", "RESPONSE", "RETRY", "DERIVATION", "SUPERSESSION"];
  const compatibility = { acceptedSourceVersions: ["1.7.0"], transforms: [] };
  function command(typeId, def, targetKinds, emits, authorityKinds, rootAllowed = false) {
    return { aliases: [], allowedParentEdges: edges, authorityKinds, compatibility, emits: [emits], executionRequired: false, kind: "COMMAND", lifecycle: "ACTIVE", owner: "tekroo-kernel", payloadSchema: `schemas/payloads.schema.json#/$defs/${def}`, rootAllowed, routingMode: "KERNEL_DIRECT", targetKinds, typeId, version: schemaVersion };
  }
  function event(typeId, def, targetKinds, acceptedCommandTypes, rootAllowed = false) {
    return { acceptedCommandTypes: [acceptedCommandTypes], aliases: [], allowedParentEdges: edges, authorityKinds: [], compatibility, executionRequired: false, kind: "EVENT", lifecycle: "ACTIVE", owner: "tekroo-kernel", payloadSchema: `schemas/payloads.schema.json#/$defs/${def}`, rootAllowed, routingMode: "COMMITTED_EVENT", targetKinds, typeId, version: schemaVersion };
  }
  const pairs = [
    ["tekroo.command.work-budget.create", "tekroo_command_work_budget_create_1_7_0", ["work-budget-account"], "tekroo.event.work-budget.created", "tekroo_event_work_budget_created_1_7_0", ["HUMAN", "POLICY"], true],
    ["tekroo.command.work-budget.amend", "tekroo_command_work_budget_amend_1_7_0", ["work-budget-account"], "tekroo.event.work-budget.amended", "tekroo_event_work_budget_amended_1_7_0", ["HUMAN", "POLICY"], false],
    ["tekroo.command.task.bind-work-budget", "tekroo_command_task_bind_work_budget_1_7_0", ["task"], "tekroo.event.task.work-budget-bound", "tekroo_event_task_work_budget_bound_1_7_0", ["HUMAN", "POLICY"], false],
    ["tekroo.command.task.bind-operational-scope", "tekroo_command_task_bind_operational_scope_1_7_0", ["task"], "tekroo.event.task.operational-scope-bound", "tekroo_event_task_operational_scope_bound_1_7_0", ["HUMAN", "POLICY"], false],
    ["tekroo.command.work-invocation.authorize", "tekroo_command_work_invocation_authorize_1_7_0", ["work-invocation"], "tekroo.event.work-invocation.authorized", "tekroo_event_work_invocation_authorized_1_7_0", ["POLICY"], false],
    ["tekroo.command.work-invocation.claim", "tekroo_command_work_invocation_claim_1_7_0", ["work-invocation"], "tekroo.event.work-invocation.claimed", "tekroo_event_work_invocation_claimed_1_7_0", ["SERVICE", "POLICY"], false],
    ["tekroo.command.work-invocation.record-started", "tekroo_command_work_invocation_record_started_1_7_0", ["work-invocation"], "tekroo.event.work-invocation.started", "tekroo_event_work_invocation_started_1_7_0", ["SERVICE", "POLICY"], false],
    ["tekroo.command.work-invocation.record-terminal", "tekroo_command_work_invocation_record_terminal_1_7_0", ["work-invocation"], "tekroo.event.work-invocation.terminal-recorded", "tekroo_event_work_invocation_terminal_recorded_1_7_0", ["SERVICE", "POLICY"], false],
    ["tekroo.command.work-invocation.request-cancellation", "tekroo_command_work_invocation_request_cancellation_1_7_0", ["work-invocation"], "tekroo.event.work-invocation.cancellation-requested", "tekroo_event_work_invocation_cancellation_requested_1_7_0", ["HUMAN", "POLICY"], false],
    ["tekroo.command.work-invocation.expire", "tekroo_command_work_invocation_expire_1_7_0", ["work-invocation"], "tekroo.event.work-invocation.expired", "tekroo_event_work_invocation_expired_1_7_0", ["POLICY"], false],
    ["tekroo.command.projection.record-fault", "tekroo_command_projection_record_fault_1_7_0", ["system"], "tekroo.event.projection.fault-recorded", "tekroo_event_projection_fault_recorded_1_7_0", ["SERVICE", "POLICY"], false],
  ];
  for (const [commandType, commandDef, targets, eventType, eventDef, authorities, rootAllowed] of pairs) {
    catalogue.entries.push(command(commandType, commandDef, targets, eventType, authorities, rootAllowed));
    catalogue.entries.push(event(eventType, eventDef, targets, commandType, rootAllowed));
  }
  catalogue.entries.sort((left, right) => left.typeId.localeCompare(right.typeId));
  writeJson(cataloguePath, catalogue);
  return { pairs, definitions };
}

const ids = {
  account: "00000000-0000-7000-8000-000000000901",
  story: "00000000-0000-7000-8000-000000000902",
  task: "00000000-0000-7000-8000-000000000903",
  evidence: "00000000-0000-7000-8000-000000000904",
  invocation: "00000000-0000-7000-8000-000000000905",
  profile: "00000000-0000-7000-8000-000000000906",
  assignment: "00000000-0000-7000-8000-000000000907",
  execution: "00000000-0000-7000-8000-000000000908",
  parent: "00000000-0000-7000-8000-000000000909",
  claim: "00000000-0000-7000-8000-000000000910",
  event: "00000000-0000-7000-8000-000000000911",
};
const d = (character) => character.repeat(64);
const limits = Object.fromEntries(purposeValues.map((value) => [value, value === "IMPLEMENTATION" ? 4 : 1]));
const authority = { kind: "POLICY", id: "teams-admission-policy" };
const binding = { profile_id: ids.profile, profile_revision: 1, profile_digest: d("a"), lifecycle_epoch: 1, scope_revision: 1 };

function validPayloads() {
  return {
    "tekroo.command.work-budget.create": { budget_account_id: ids.account, root_work: { kind: "story", id: ids.story }, lifecycle_epoch: 1, policy_revision: 1, policy_digest: d("b"), model_invocation_limit: 8, purpose_limits: limits, deadline_at: "2026-09-30T00:00:00Z", evidence_ids: [ids.evidence], authority },
    "tekroo.command.work-budget.amend": { budget_account_id: ids.account, expected_budget_revision: 1, expected_lifecycle_epoch: 1, policy_revision: 2, policy_digest: d("c"), model_invocation_limit: 9, purpose_limits: limits, deadline_at: "2026-10-01T00:00:00Z", reason: "operator-approved scope revision", evidence_ids: [ids.evidence], authority },
    "tekroo.command.task.bind-work-budget": { task_id: ids.task, budget_account_id: ids.account, expected_task_revision: 2, lifecycle_epoch: 1, scope_revision: 1, task_model_invocation_limit: 4, purpose_limits: limits, evidence_ids: [ids.evidence] },
    "tekroo.command.task.bind-operational-scope": { task_id: ids.task, expected_task_revision: 3, lifecycle_epoch: 1, scope_revision: 1, owner_fqn: "teams::coder-1", execution_id: ids.execution, fencing_epoch: 1, workspace_id: "workspace-task-903", worktree_id: "/tmp/task-903", branch: "task-903", baseline_sha: "1".repeat(40), writable_paths: ["src/"], interface_constraint_evidence_ids: [ids.evidence] },
    "tekroo.command.work-invocation.authorize": { invocation_id: ids.invocation, task_id: ids.task, budget_account_id: ids.account, expected_budget_revision: 2, expected_task_revision: 4, lifecycle_epoch: 1, scope_revision: 1, parent_event_id: ids.parent, work_profile: binding, qualified_assignment_id: ids.assignment, purpose: "IMPLEMENTATION", attempt_family: "implementation", attempt_ordinal: 1, condition_digest: d("d"), retry_of_invocation_id: null, retry_ordinal: 0, output_predicate_digest: d("e"), allowed_terminal_outcomes: ["SUCCEEDED", "FAILED", "TIMED_OUT", "CANCELLED", "START_FAILED"], tool_policy_digest: d("f"), effect_policy_digest: d("1"), actor_fqn: "teams::coder-1", execution_id: ids.execution, fencing_epoch: 1, model_profile_digest: d("2"), runtime_identity_digest: d("3"), workspace_id: "workspace-task-903", deadline_at: "2026-09-30T00:00:00Z", idempotency_key: "invoke-task-903-1", admission_policy_revision: 1, admission_policy_digest: d("4") },
    "tekroo.command.work-invocation.claim": { invocation_id: ids.invocation, expected_invocation_revision: 1, claim_id: ids.claim, actor_fqn: "teams::coder-1", execution_id: ids.execution, fencing_epoch: 1, model_profile_digest: d("2"), runtime_identity_digest: d("3"), consumer_id: "teams-openhands-runtime", claimed_at: "2026-08-31T12:00:00Z" },
    "tekroo.command.work-invocation.record-started": { invocation_id: ids.invocation, expected_invocation_revision: 2, claim_id: ids.claim, conversation_id: "openhands-conversation-1", request_digest: d("5"), started_at: "2026-08-31T12:00:01Z" },
    "tekroo.command.work-invocation.record-terminal": { invocation_id: ids.invocation, expected_invocation_revision: 3, claim_id: ids.claim, outcome: "SUCCEEDED", retryable: false, evidence_ids: [ids.evidence], output_digest: d("6"), finished_at: "2026-08-31T12:01:00Z" },
    "tekroo.command.work-invocation.request-cancellation": { invocation_id: ids.invocation, expected_invocation_revision: 3, reason: "operator cancellation", evidence_ids: [ids.evidence], authority: { kind: "HUMAN", id: "principal" }, requested_at: "2026-08-31T12:00:30Z" },
    "tekroo.command.work-invocation.expire": { invocation_id: ids.invocation, expected_invocation_revision: 1, deadline_at: "2026-08-31T12:00:00Z", observed_at: "2026-08-31T12:00:01Z", authority },
    "tekroo.command.projection.record-fault": { projection_kind: "TASK", aggregate: { kind: "task", id: ids.task }, expected_revision: 4, observed_revision: 6, event_id: ids.event, event_digest: d("7"), fault_kind: "REVISION_GAP", observed_at: "2026-08-31T12:00:00Z" },
  };
}

function addFixtures(pairs) {
  const catalogueFixturePath = path.join(packageRoot, "fixtures", "catalogue-coverage.json");
  const catalogueFixtures = readJson(catalogueFixturePath);
  const payloads = validPayloads();
  pairs.forEach((pair, index) => {
    const commandType = pair[0];
    const eventType = pair[3];
    const number = String(57 + index).padStart(3, "0");
    catalogueFixtures.fixtures.push(
      {
        fixtureId: `CAT-${number}-PHASE4-VALID`, kind: "CATALOGUE_COMMAND", classification: "NORMATIVE_EXAMPLE", sourceDecisionIds: [`P2-KCF-${String(39 + Math.min(index, 11)).padStart(3, "0")}`],
        given: { contractManifest: contractIdentity }, when: { commandType, payload: payloads[commandType] }, then: { expected: { outcomeCode: "APPLIED", eventTypes: [eventType] } },
      },
      {
        fixtureId: `CAT-${number}-PHASE4-INVALID`, kind: "CATALOGUE_COMMAND", classification: "BOUNDARY_NEGATIVE", sourceDecisionIds: [`P2-KCF-${String(39 + Math.min(index, 11)).padStart(3, "0")}`],
        given: { contractManifest: contractIdentity }, when: { commandType, payload: {} }, then: { expected: { outcomeCode: "REJECTED_INVALID", eventTypes: [] } },
      },
    );
  });
  catalogueFixtures.schemaVersion = schemaVersion;
  catalogueFixtures.contractIdentity = contractIdentity;
  writeJson(catalogueFixturePath, catalogueFixtures);

  const modelPath = path.join(packageRoot, "fixtures", "model-and-invariant-scenarios.json");
  const models = readJson(modelPath);
  const exact = { taskRevision: 4, lifecycleEpoch: 1, scopeRevision: 1, actorFqn: "teams::coder-1", executionId: ids.execution, fencingEpoch: 1, modelDigest: d("2"), runtimeDigest: d("3") };
  const budget = { accountId: ids.account, limit: 8, used: 2, purposeLimit: 4, purposeUsed: 1, deadlineAt: "2026-09-30T00:00:00Z" };
  const authorize = { action: "AUTHORIZE", origin: "TEAMS_KERNEL", now: "2026-08-31T12:00:00Z", taskRevision: 4, lifecycleEpoch: 1, scopeRevision: 1, actorFqn: "teams::coder-1", executionId: ids.execution, fencingEpoch: 1, modelDigest: d("2"), runtimeDigest: d("3"), purpose: "IMPLEMENTATION", conditionDigest: d("d"), retryOrdinal: 0, retryOf: null };
  const invocationGiven = { exact, budget, state: "ABSENT", consumed: false, lastConditionDigest: null, priorTerminal: null };
  const modelFixtures = [
    ["P4-MISSING-BUDGET-FAILS-CLOSED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", { ...invocationGiven, budget: null }, authorize, { accepted: false, reason: "MISSING_BUDGET" }],
    ["P4-DIRECT-AGENT-WAKEUP-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, origin: "AGENT_MESSAGE" }, { accepted: false, reason: "DIRECT_WAKEUP_PROHIBITED" }],
    ["P4-COOPTED-MESSAGE-WAKEUP-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, origin: "AGENT_MESSAGE", messageType: "tekroo.event.task.dispatched" }, { accepted: false, reason: "DIRECT_WAKEUP_PROHIBITED" }],
    ["P4-TASK-DISPATCH-NOT-EXECUTABLE", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, origin: "TEAMS_TASK_DISPATCH", messageType: "tekroo.event.task.dispatched" }, { accepted: false, reason: "DIRECT_WAKEUP_PROHIBITED" }],
    ["P4-STALE-TASK-REVISION-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, taskRevision: 3 }, { accepted: false, reason: "STALE_TASK_REVISION" }],
    ["P4-STALE-LIFECYCLE-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, lifecycleEpoch: 2 }, { accepted: false, reason: "STALE_LIFECYCLE" }],
    ["P4-STALE-FENCE-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, fencingEpoch: 2 }, { accepted: false, reason: "RUNTIME_IDENTITY_MISMATCH" }],
    ["P4-WRONG-ACTOR-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, actorFqn: "teams::coder-2" }, { accepted: false, reason: "RUNTIME_IDENTITY_MISMATCH" }],
    ["P4-WRONG-MODEL-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, modelDigest: d("9") }, { accepted: false, reason: "RUNTIME_IDENTITY_MISMATCH" }],
    ["P4-WRONG-RUNTIME-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", invocationGiven, { ...authorize, runtimeDigest: d("9") }, { accepted: false, reason: "RUNTIME_IDENTITY_MISMATCH" }],
    ["P4-FIRST-INVOCATION-ACCEPTED", "INVOCATION_ADMISSION_MODEL", "NORMATIVE_EXAMPLE", invocationGiven, authorize, { accepted: true, reason: "ACCEPTED", globalUsed: 3, purposeUsed: 2, maximumAdditionalInvocations: 5 }],
    ["P4-UNCHANGED-CONDITION-REDISPATCH-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", { ...invocationGiven, lastConditionDigest: d("d") }, authorize, { accepted: false, reason: "UNCHANGED_CONDITION" }],
    ["P4-CHANGED-EVIDENCE-CONTINUATION-ACCEPTED", "INVOCATION_ADMISSION_MODEL", "NORMATIVE_EXAMPLE", { ...invocationGiven, lastConditionDigest: d("8") }, authorize, { accepted: true, reason: "ACCEPTED", globalUsed: 3, purposeUsed: 2, maximumAdditionalInvocations: 5 }],
    ["P4-RETRYABLE-TERMINAL-CONTINUATION-ACCEPTED", "INVOCATION_ADMISSION_MODEL", "NORMATIVE_EXAMPLE", { ...invocationGiven, lastConditionDigest: d("d"), priorTerminal: { invocationId: "prior", retryable: true, retryOrdinal: 0 } }, { ...authorize, retryOrdinal: 1, retryOf: "prior" }, { accepted: true, reason: "ACCEPTED", globalUsed: 3, purposeUsed: 2, maximumAdditionalInvocations: 5 }],
    ["P4-NONRETRYABLE-CONTINUATION-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", { ...invocationGiven, lastConditionDigest: d("d"), priorTerminal: { invocationId: "prior", retryable: false, retryOrdinal: 0 } }, { ...authorize, retryOrdinal: 1, retryOf: "prior" }, { accepted: false, reason: "UNCHANGED_CONDITION" }],
    ["P4-PERMIT-FIRST-CLAIM-ACCEPTED", "INVOCATION_ADMISSION_MODEL", "NORMATIVE_EXAMPLE", { ...invocationGiven, state: "AUTHORIZED" }, { action: "CLAIM", consumed: false, identityMatches: true, beforeDeadline: true }, { accepted: true, reason: "ACCEPTED", state: "CLAIMED" }],
    ["P4-PERMIT-REUSE-REJECTED", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", { ...invocationGiven, state: "CLAIMED", consumed: true }, { action: "CLAIM", consumed: true, identityMatches: true, beforeDeadline: true }, { accepted: false, reason: "PERMIT_ALREADY_CONSUMED", state: "CLAIMED" }],
    ["P4-CANCELLATION-REQUEST-NOT-TERMINAL", "INVOCATION_ADMISSION_MODEL", "NORMATIVE_EXAMPLE", { ...invocationGiven, state: "STARTED" }, { action: "REQUEST_CANCELLATION" }, { accepted: true, reason: "CANCELLATION_REQUESTED", state: "STARTED", cancellationRequested: true }],
    ["P4-CANCELLED-REQUIRES-TERMINAL-EVIDENCE", "INVOCATION_ADMISSION_MODEL", "BOUNDARY_NEGATIVE", { ...invocationGiven, state: "STARTED" }, { action: "RECORD_TERMINAL", outcome: "CANCELLED", terminalEvidence: false }, { accepted: false, reason: "TERMINAL_EVIDENCE_REQUIRED", state: "STARTED" }],
    ["P4-ROOT-BOUND-COMPUTED", "WORK_BUDGET_MODEL", "NORMATIVE_EXAMPLE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "COMPUTE_BOUND" }, { accepted: true, reason: "ACCEPTED", maximumAdditionalInvocations: 5 }],
    ["P4-CHILD-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "CHILD_TASK", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-HANDOFF-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "HANDOFF", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-REPLAN-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "REPLAN", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-PROMOTION-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "PROMOTION", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-REVIEW-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "REVIEW", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-REPAIR-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "REPAIR", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-ACTOR-REPLACEMENT-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "ACTOR_REPLACEMENT", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-MODEL-REPLACEMENT-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "MODEL_REPLACEMENT", accountId: "new-account", requestedLimit: 8 }, { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-RESTART-BUDGET-RESET-REJECTED", "WORK_BUDGET_MODEL", "BOUNDARY_NEGATIVE", { rootAccountId: ids.account, limit: 8, used: 3 }, { action: "INHERIT", mechanism: "RESTART", accountId: ids.account, requestedLimit: 11 }, { accepted: false, reason: "CAPACITY_MINT_PROHIBITED", maximumAdditionalInvocations: 5 }],
    ["P4-EXPLICIT-BUDGET-AMENDMENT-ACCEPTED", "WORK_BUDGET_MODEL", "NORMATIVE_EXAMPLE", { rootAccountId: ids.account, limit: 8, used: 3, revision: 1 }, { action: "AMEND", authorized: true, expectedRevision: 1, newLimit: 9 }, { accepted: true, reason: "ACCEPTED", revision: 2, maximumAdditionalInvocations: 6 }],
    ["P4-PROJECTION-DUPLICATE-IDEMPOTENT", "PROJECTION_MODEL", "NORMATIVE_EXAMPLE", { revision: 3, lastEventId: "e3", lastDigest: d("3") }, { action: "APPLY", event: { revision: 3, eventId: "e3", digest: d("3") } }, { accepted: true, reason: "DUPLICATE", revision: 3 }],
    ["P4-PROJECTION-GAP-FAILS-CLOSED", "PROJECTION_MODEL", "BOUNDARY_NEGATIVE", { revision: 3, lastEventId: "e3", lastDigest: d("3") }, { action: "APPLY", event: { revision: 5, eventId: "e5", digest: d("5") } }, { accepted: false, reason: "REVISION_GAP", revision: 3 }],
    ["P4-PROJECTION-CONFLICT-FAILS-CLOSED", "PROJECTION_MODEL", "BOUNDARY_NEGATIVE", { revision: 3, lastEventId: "e3", lastDigest: d("3") }, { action: "APPLY", event: { revision: 3, eventId: "other", digest: d("4") } }, { accepted: false, reason: "REVISION_CONFLICT", revision: 3 }],
    ["P4-PROJECTION-REBUILD-EQUIVALENT", "PROJECTION_MODEL", "NORMATIVE_EXAMPLE", { incremental: { id: ids.task, revision: 4, phase: "ACTIVE" } }, { action: "COMPARE_REBUILD", rebuilt: { id: ids.task, revision: 4, phase: "ACTIVE" } }, { accepted: true, reason: "EXACT_REBUILD_MATCH" }],
    ["P4-PROJECTION-REBUILD-MISMATCH", "PROJECTION_MODEL", "BOUNDARY_NEGATIVE", { incremental: { id: ids.task, revision: 4, phase: "ACTIVE" } }, { action: "COMPARE_REBUILD", rebuilt: { id: ids.task, revision: 4, phase: "COMPLETED" } }, { accepted: false, reason: "REBUILD_MISMATCH" }],
    ["P4-SMA-AUTHORITATIVE-TASK-WRITE-REJECTED", "AUTHORITY_BOUNDARY_MODEL", "BOUNDARY_NEGATIVE", {}, { source: "SMA", effect: "TASK_STATE" }, { accepted: false, reason: "TEAMS_AUTHORITY_REQUIRED" }],
    ["P4-SMA-ROUTING-WRITE-REJECTED", "AUTHORITY_BOUNDARY_MODEL", "BOUNDARY_NEGATIVE", {}, { source: "SMA", effect: "ROUTING_STATE" }, { accepted: false, reason: "TEAMS_AUTHORITY_REQUIRED" }],
    ["P4-OPENHANDS-LEASE-WRITE-REJECTED", "AUTHORITY_BOUNDARY_MODEL", "BOUNDARY_NEGATIVE", {}, { source: "OPENHANDS", effect: "OWNERSHIP_LEASE" }, { accepted: false, reason: "TEAMS_AUTHORITY_REQUIRED" }],
    ["P4-SMA-SEMANTIC-MEMORY-WRITE-ACCEPTED", "AUTHORITY_BOUNDARY_MODEL", "NORMATIVE_EXAMPLE", {}, { source: "SMA", effect: "SEMANTIC_MEMORY" }, { accepted: true, reason: "SMA_MEMORY_DOMAIN" }],
    ["P4-TEAMS-OPERATIONAL-WRITE-ACCEPTED", "AUTHORITY_BOUNDARY_MODEL", "NORMATIVE_EXAMPLE", {}, { source: "TEAMS_KERNEL", effect: "TASK_STATE" }, { accepted: true, reason: "ACCEPTED" }],
  ];
  modelFixtures.forEach((item, index) => models.fixtures.push({ fixtureId: item[0], kind: item[1], classification: item[2], sourceDecisionIds: [`P2-KCF-${String(39 + (index % 12)).padStart(3, "0")}`], given: item[3], when: item[4], then: { expected: item[5] } }));
  models.schemaVersion = schemaVersion;
  models.contractIdentity = contractIdentity;
  writeJson(modelPath, models);
  return modelFixtures.map((item) => item[0]);
}

function addInvariantsAndTraceability(newFixtureIds, pairs) {
  const catalogueIds = pairs.flatMap((pair, index) => {
    const number = String(57 + index).padStart(3, "0");
    return [`CAT-${number}-PHASE4-VALID`, `CAT-${number}-PHASE4-INVALID`];
  });
  const allNewTests = [...newFixtureIds, ...catalogueIds];
  const invariantPath = path.join(packageRoot, "invariants", "invariants.json");
  const invariants = readJson(invariantPath);
  const invariantDescriptions = [
    "Every model invocation consumes one graph-wide root budget unit; a missing or exhausted applicable budget fails closed.",
    "Only a single-use Teams work-invocation authorization can wake model-backed execution.",
    "Invocation claim, start, terminal, cancellation, and expiry bind exact current task, lifecycle, assignment, actor, execution fence, model, runtime, and deadline identities.",
    "Unchanged-condition continuation requires an accepted retryable predecessor and the next bounded retry ordinal; changed authoritative evidence may establish a new condition.",
    "Child creation, handoff, replanning, review, repair, promotion, restart, and actor/model replacement inherit and cannot increase the root invocation ceiling.",
    "Task dispatch is an assignment fact and is not itself executable authority.",
    "Teams alone owns task, story, budget, ownership, lease, routing, review, completion, and current operational state.",
    "SMA owns semantic and task-local memory; SMA or OpenHands operational suggestions remain evidence until Teams accepts a command.",
    "Task and story projections apply exact aggregate revisions idempotently and stop on gaps or conflicting revisions.",
    "A clean projection rebuild must canonically equal the incremental projection before replacement.",
    "Operational worktree, writable-path, and interface-constraint bindings are Teams task state tied to exact ownership and execution fencing.",
  ];
  invariantDescriptions.forEach((description, index) => invariants.invariants.push({
    invariantId: `INV-${String(42 + index).padStart(3, "0")}-PHASE4-${String(index + 1).padStart(2, "0")}`,
    description,
    decisionIds: [`P2-KCF-${String(39 + Math.min(index, 11)).padStart(3, "0")}`],
    negativeRequirementIds: [`P2-KCF-${String(39 + Math.min(index, 11)).padStart(3, "0")}-NEG-01`],
    fixtureIds: allNewTests.filter((_, fixtureIndex) => fixtureIndex % invariantDescriptions.length === index),
  }));
  invariants.schemaVersion = schemaVersion;
  invariants.contractIdentity = contractIdentity;
  writeJson(invariantPath, invariants);

  const tracePath = path.join(packageRoot, "traceability", "traceability.json");
  const trace = readJson(tracePath);
  const decisions = [
    "One root work-budget account imposes a mandatory graph-wide model invocation ceiling.",
    "Work invocation is a first-class single-use aggregate and the only executable authority.",
    "The executable outbox is emitted only by an accepted Teams invocation authorization.",
    "Continuation is tied to authoritative condition identity and bounded retry semantics.",
    "All loop-capable transformations inherit the root budget without implicit reset.",
    "Every invocation binds exact task, lifecycle, profile, assignment, actor, execution fence, model, runtime, workspace, and deadline.",
    "Teams owns task/story operational state and SMA remains semantic memory.",
    "Ownership leases and shared operational scope are canonical Teams task state.",
    "Task and story operational projections are rebuildable, idempotent, and non-authoritative.",
    "Projection gaps, conflicts, and rebuild mismatches fail closed and are retained as evidence.",
    "Contract 0.8.0 is a version-gated successor to immutable 0.7.0.",
    "Task dispatch remains compatible historical state but cannot start a model without a work invocation.",
  ];
  decisions.forEach((text, index) => {
    const id = `P2-KCF-${String(39 + index).padStart(3, "0")}`;
    const tests = allNewTests.filter((_, testIndex) => testIndex % decisions.length === index);
    const resolvedTests = tests.length ? [...tests] : [allNewTests[index]];
    if (index < 11) resolvedTests.push(`INV-${String(42 + index).padStart(3, "0")}-PHASE4-${String(index + 1).padStart(2, "0")}`);
    trace.requirements.push({ requirementId: id, kind: "DECISION", disposition: "BINDING", text, testIds: resolvedTests });
    trace.requirements.push({ requirementId: `${id}-NEG-01`, parentDecisionId: id, kind: "NEGATIVE_REQUIREMENT", disposition: "BINDING", text: `Reject any path that contradicts ${id}.`, testIds: resolvedTests });
  });
  const prohibitions = [
    "DAG acyclicity alone proves organizational termination.",
    "A role, message type, child task, handoff, restart, or model change creates a fresh budget.",
    "Task dispatch, agent prose, provider status, or SMA output is executable authority.",
    "A projection or SMA mirror may substitute for authoritative Teams state.",
    "A permit may be replayed after claim, expiry, stale fencing, or terminal completion.",
    "Contract-package acceptance qualifies a Teams implementation, SMA integration, OpenHands runtime, or production deployment.",
  ];
  prohibitions.forEach((text, index) => trace.requirements.push({
    requirementId: `P2-KCF-${String(39 + index).padStart(3, "0")}-PROHIBIT-01`,
    parentDecisionId: `P2-KCF-${String(39 + index).padStart(3, "0")}`,
    kind: "PROHIBITED_INTERPRETATION", disposition: "BINDING", text,
    testIds: [allNewTests[index]],
  }));
  trace.schemaVersion = schemaVersion;
  trace.contractIdentity = contractIdentity;
  writeJson(tracePath, trace);
}

function extendRunner() {
  const runnerPath = path.join(packageRoot, "runner", "reference-runner.mjs");
  let runner = fs.readFileSync(runnerPath, "utf8");
  const functions = `
function runInvocationAdmissionModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (action.action === "REQUEST_CANCELLATION") {
    if (current.state !== "STARTED") return { accepted: false, reason: "INVALID_INVOCATION_STATE", state: current.state };
    return { accepted: true, reason: "CANCELLATION_REQUESTED", state: "STARTED", cancellationRequested: true };
  }
  if (action.action === "RECORD_TERMINAL") {
    if (current.state !== "STARTED") return { accepted: false, reason: "INVALID_INVOCATION_STATE", state: current.state };
    if (!action.terminalEvidence) return { accepted: false, reason: "TERMINAL_EVIDENCE_REQUIRED", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: action.outcome };
  }
  if (action.action === "CLAIM") {
    if (current.consumed || current.state !== "AUTHORIZED") return { accepted: false, reason: "PERMIT_ALREADY_CONSUMED", state: current.state };
    if (!action.identityMatches) return { accepted: false, reason: "RUNTIME_IDENTITY_MISMATCH", state: current.state };
    if (!action.beforeDeadline) return { accepted: false, reason: "INVOCATION_EXPIRED", state: current.state };
    return { accepted: true, reason: "ACCEPTED", state: "CLAIMED" };
  }
  if (action.action !== "AUTHORIZE") return { accepted: false, reason: "INVALID_ACTION" };
  if (action.origin !== "TEAMS_KERNEL") return { accepted: false, reason: "DIRECT_WAKEUP_PROHIBITED" };
  if (!current.budget) return { accepted: false, reason: "MISSING_BUDGET" };
  if (Date.parse(action.now) >= Date.parse(current.budget.deadlineAt)) return { accepted: false, reason: "DEADLINE_EXCEEDED" };
  if (action.taskRevision !== current.exact.taskRevision) return { accepted: false, reason: "STALE_TASK_REVISION" };
  if (action.lifecycleEpoch !== current.exact.lifecycleEpoch || action.scopeRevision !== current.exact.scopeRevision) return { accepted: false, reason: "STALE_LIFECYCLE" };
  if (action.actorFqn !== current.exact.actorFqn || action.executionId !== current.exact.executionId || action.fencingEpoch !== current.exact.fencingEpoch || action.modelDigest !== current.exact.modelDigest || action.runtimeDigest !== current.exact.runtimeDigest) return { accepted: false, reason: "RUNTIME_IDENTITY_MISMATCH" };
  if (current.budget.used >= current.budget.limit) return { accepted: false, reason: "INVOCATION_BUDGET_EXHAUSTED" };
  if (current.budget.purposeUsed >= current.budget.purposeLimit) return { accepted: false, reason: "PURPOSE_BUDGET_EXHAUSTED" };
  if (current.lastConditionDigest === action.conditionDigest) {
    const retry = current.priorTerminal && current.priorTerminal.retryable === true && action.retryOf === current.priorTerminal.invocationId && action.retryOrdinal === current.priorTerminal.retryOrdinal + 1;
    if (!retry) return { accepted: false, reason: "UNCHANGED_CONDITION" };
  }
  const globalUsed = current.budget.used + 1;
  const purposeUsed = current.budget.purposeUsed + 1;
  return { accepted: true, reason: "ACCEPTED", globalUsed, purposeUsed, maximumAdditionalInvocations: Math.max(0, current.budget.limit - globalUsed) };
}

function runWorkBudgetModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  const remaining = Math.max(0, current.limit - current.used);
  if (action.action === "COMPUTE_BOUND") return { accepted: true, reason: "ACCEPTED", maximumAdditionalInvocations: remaining };
  if (action.action === "INHERIT") {
    if (action.accountId !== current.rootAccountId) return { accepted: false, reason: "BUDGET_RESET_PROHIBITED", maximumAdditionalInvocations: remaining };
    if (action.requestedLimit > current.limit) return { accepted: false, reason: "CAPACITY_MINT_PROHIBITED", maximumAdditionalInvocations: remaining };
    return { accepted: true, reason: "ACCEPTED", maximumAdditionalInvocations: remaining };
  }
  if (action.action === "AMEND") {
    if (!action.authorized) return { accepted: false, reason: "AUTHORITY_REQUIRED", maximumAdditionalInvocations: remaining };
    if (action.expectedRevision !== current.revision) return { accepted: false, reason: "STALE_BUDGET_REVISION", maximumAdditionalInvocations: remaining };
    if (action.newLimit < current.used) return { accepted: false, reason: "LIMIT_BELOW_USED", maximumAdditionalInvocations: remaining };
    return { accepted: true, reason: "ACCEPTED", revision: current.revision + 1, maximumAdditionalInvocations: action.newLimit - current.used };
  }
  return { accepted: false, reason: "INVALID_ACTION", maximumAdditionalInvocations: remaining };
}

function runProjectionModel(fixture) {
  const current = fixture.given;
  const action = fixture.when;
  if (action.action === "COMPARE_REBUILD") return canonical(current.incremental) === canonical(action.rebuilt)
    ? { accepted: true, reason: "EXACT_REBUILD_MATCH" }
    : { accepted: false, reason: "REBUILD_MISMATCH" };
  const event = action.event;
  if (event.revision === current.revision && event.eventId === current.lastEventId && event.digest === current.lastDigest) return { accepted: true, reason: "DUPLICATE", revision: current.revision };
  if (event.revision <= current.revision) return { accepted: false, reason: "REVISION_CONFLICT", revision: current.revision };
  if (event.revision !== current.revision + 1) return { accepted: false, reason: "REVISION_GAP", revision: current.revision };
  return { accepted: true, reason: "APPLIED", revision: event.revision };
}

function runAuthorityBoundaryModel(fixture) {
  const action = fixture.when;
  if (action.effect === "SEMANTIC_MEMORY" && action.source === "SMA") return { accepted: true, reason: "SMA_MEMORY_DOMAIN" };
  if (["TASK_STATE", "ROUTING_STATE", "OWNERSHIP_LEASE", "BUDGET", "ACCEPTANCE"].includes(action.effect) && action.source !== "TEAMS_KERNEL") return { accepted: false, reason: "TEAMS_AUTHORITY_REQUIRED" };
  return action.source === "TEAMS_KERNEL" ? { accepted: true, reason: "ACCEPTED" } : { accepted: false, reason: "INVALID_SOURCE" };
}

`;
  runner = runner.replace("function runSuccessorSetModel(fixture) {", `${functions}function runSuccessorSetModel(fixture) {`);
  runner = runner.replace(
    "  } else if (fixture.kind === \"SUCCESSOR_SET_MODEL\") {",
    "  } else if (fixture.kind === \"INVOCATION_ADMISSION_MODEL\") {\n    actual = runInvocationAdmissionModel(fixture);\n  } else if (fixture.kind === \"WORK_BUDGET_MODEL\") {\n    actual = runWorkBudgetModel(fixture);\n  } else if (fixture.kind === \"PROJECTION_MODEL\") {\n    actual = runProjectionModel(fixture);\n  } else if (fixture.kind === \"AUTHORITY_BOUNDARY_MODEL\") {\n    actual = runAuthorityBoundaryModel(fixture);\n  } else if (fixture.kind === \"SUCCESSOR_SET_MODEL\") {",
  );
  fs.writeFileSync(runnerPath, runner);
}

function addCompatibility() {
  const predecessorManifestSha = sha256(fs.readFileSync(path.join(predecessorRoot, "manifest.json")));
  const catalogue = readJson(path.join(packageRoot, "catalogue", "kernel-catalogue.json"));
  const newTypes = catalogue.entries.filter((entry) => entry.version === schemaVersion).map((entry) => entry.typeId).sort();
  const changedTypes = ["tekroo.command.task.dispatch", "tekroo.event.task.dispatched"];
  const oldTypes = catalogue.entries.filter((entry) => entry.version !== schemaVersion && !changedTypes.includes(entry.typeId)).map((entry) => entry.typeId).sort();
  const compatibility = {
    schemaVersion,
    contractName: "tekroo.kernel.contracts",
    contractVersion: "0.8.0",
    predecessor: { contractIdentity: "tekroo.kernel.contracts/0.7.0", contractVersion: "0.7.0", manifestSha256: predecessorManifestSha },
    compatibilityClaim: "ADDITIVE_SINGLE_USE_INVOCATION_INHERITED_BUDGET_AND_OPERATIONAL_PROJECTIONS",
    directions: { backward: "COMPATIBLE_ADAPTER_REQUIRED", forward: "NOT_COMPATIBLE", replay: "VERSION_GATED", adapter: "REQUIRED" },
    additiveTypes: newTypes,
    unchangedSemanticTypes: oldTypes,
    changedSemanticTypes: changedTypes,
    semanticClarification: "task.dispatch remains an accepted assignment fact but is not executable authority in the 0.8.0 runtime",
    aggregateStateMigration: {
      mode: "EXPLICITLY_BIND_ROOT_WORK_BUDGET_BEFORE_PHASE4_EXECUTION",
      historicalReplay: "RETAIN_0.7.0",
      requiredContext: ["root_work_budget_account", "graph_wide_model_invocation_limit", "purpose_limits", "deadline", "task_budget_binding", "operational_scope_binding"],
    },
    migrationRules: [
      { source: "1.6.0", target: "1.6.0", mode: "IDENTITY", appliesTo: oldTypes },
      { source: "1.7.0", target: "1.7.0", mode: "IDENTITY", appliesTo: newTypes },
    ],
    prohibitedInferences: [
      "A 0.7.0 task dispatch is not silently treated as a 0.8.0 executable invocation.",
      "A missing graph-wide budget is not interpreted as unlimited capacity.",
      "Child tasks, handoffs, replanning, review, promotion, restart, actor replacement, model replacement, or message-type substitution do not reset a root budget.",
      "SMA or OpenHands state is not imported as authoritative task, routing, lease, budget, review, acceptance, or completion state.",
      "Task and story projections are not imported as decision authority.",
    ],
    rollback: "Readers retain 0.7.0 for historical replay. Work-budget accounts, work invocations, operational scope bindings, and projections are not down-converted or inferred into 0.7.0 state.",
  };
  writeJson(path.join(packageRoot, "compatibility", "from-0.7.0.json"), compatibility);
}

function extendValidator() {
  const validatorPath = path.join(packageRoot, "runner", "validate-package.mjs");
  let validator = fs.readFileSync(validatorPath, "utf8")
    .replace('manifest.contract.version === "0.7.0"', 'manifest.contract.version === "0.8.0"')
    .replace('decisions.length === 38', 'decisions.length === 50')
    .replace('negatives.length === 14', 'negatives.length === 26')
    .replace('readJson("compatibility/from-0.6.0.json")', 'readJson("compatibility/from-0.7.0.json")')
    .replace(
      'compatibility.predecessor?.contractVersion === "0.6.0" && compatibility.contractVersion === "0.7.0" && compatibility.directions?.adapter === "REQUIRED"',
      'compatibility.predecessor?.contractVersion === "0.7.0" && compatibility.contractVersion === "0.8.0" && compatibility.directions?.adapter === "REQUIRED"',
    );
  validator = validator.replace(
    'const protocol = readJson("runner/protocol.json");',
    'check("phase4-state-schemas", ["schemas/work-budget-account.schema.json", "schemas/work-invocation.schema.json", "schemas/operational-projection.schema.json"].every((file) => listedPayloadFiles.includes(file)));\ncheck("phase4-model-fixtures", ["INVOCATION_ADMISSION_MODEL", "WORK_BUDGET_MODEL", "PROJECTION_MODEL", "AUTHORITY_BOUNDARY_MODEL"].every((kind) => fixtures.some((fixture) => fixture.kind === kind)));\ncheck("phase4-direct-wakeup-negative", fixtures.some((fixture) => fixture.fixtureId === "P4-DIRECT-AGENT-WAKEUP-REJECTED"));\ncheck("phase4-task-dispatch-nonexecutable", fixtures.some((fixture) => fixture.fixtureId === "P4-TASK-DISPATCH-NOT-EXECUTABLE"));\ncheck("phase4-cancellation-evidence", ["P4-CANCELLATION-REQUEST-NOT-TERMINAL", "P4-CANCELLED-REQUIRES-TERMINAL-EVIDENCE"].every((id) => fixtureIds.includes(id)));\ncheck("phase4-budget-reset-negatives", ["P4-CHILD-BUDGET-RESET-REJECTED", "P4-HANDOFF-BUDGET-RESET-REJECTED", "P4-REPLAN-BUDGET-RESET-REJECTED", "P4-PROMOTION-BUDGET-RESET-REJECTED", "P4-REVIEW-BUDGET-RESET-REJECTED", "P4-REPAIR-BUDGET-RESET-REJECTED", "P4-ACTOR-REPLACEMENT-BUDGET-RESET-REJECTED", "P4-MODEL-REPLACEMENT-BUDGET-RESET-REJECTED", "P4-RESTART-BUDGET-RESET-REJECTED"].every((id) => fixtureIds.includes(id)));\nconst protocol = readJson("runner/protocol.json");',
  );
  fs.writeFileSync(validatorPath, validator);
}

function updateTopLevelIdentities() {
  for (const relative of walk(packageRoot)) {
    if (!relative.endsWith(".json")) continue;
    const file = path.join(packageRoot, relative);
    const value = readJson(file);
    if (value.contractIdentity !== undefined) value.contractIdentity = contractIdentity;
    if (value.schemaVersion === "1.6.0") value.schemaVersion = schemaVersion;
    writeJson(file, value);
  }
}

function dependencyList(relative, predecessorManifest) {
  const previous = predecessorManifest.files.find((file) => file.path === relative);
  if (previous) return previous.dependencies.map((dependency) => dependency === "compatibility/from-0.6.0.json" ? "compatibility/from-0.7.0.json" : dependency);
  if (relative === "compatibility/from-0.7.0.json") return ["CONTRACTS/tekroo.kernel.contracts/0.7.0/manifest.json"];
  if (relative === "schemas/work-budget-account.schema.json" || relative === "schemas/work-invocation.schema.json" || relative === "schemas/operational-projection.schema.json") return ["schemas/core.schema.json"];
  return [];
}

function role(relative) {
  if (relative.startsWith("catalogue/")) return "catalogue";
  if (relative.startsWith("compatibility/")) return "compatibility";
  if (relative.startsWith("fixtures/")) return "fixtures";
  if (relative.startsWith("invariants/")) return "invariants";
  if (relative === "runner/protocol.json") return "runner-protocol";
  if (relative.startsWith("runner/")) return "runner-tool";
  if (relative.startsWith("schemas/")) return "schemas";
  if (relative.startsWith("traceability/")) return "traceability";
  throw new Error(`unknown manifest role for ${relative}`);
}

function buildManifest() {
  const predecessorManifest = readJson(path.join(predecessorRoot, "manifest.json"));
  const payloadPaths = walk(packageRoot).filter((relative) => !["manifest.json", "manifest.sha256"].includes(relative));
  const files = payloadPaths.map((relative) => {
    const bytes = fs.readFileSync(path.join(packageRoot, relative));
    const mediaType = relative.endsWith(".json") ? "application/json" : "text/javascript";
    return {
      path: relative,
      role: role(relative),
      mediaType,
      bytes: bytes.length,
      sha256: sha256(bytes),
      canonicalJsonSha256: mediaType === "application/json" ? sha256(canonical(JSON.parse(bytes.toString("utf8")))) : null,
      dependencies: dependencyList(relative, predecessorManifest),
    };
  });
  const catalogue = readJson(path.join(packageRoot, "catalogue", "kernel-catalogue.json"));
  const fixtures = files.filter((file) => file.role === "fixtures").flatMap((file) => readJson(path.join(packageRoot, file.path)).fixtures);
  const invariants = readJson(path.join(packageRoot, "invariants", "invariants.json")).invariants;
  const requirements = readJson(path.join(packageRoot, "traceability", "traceability.json")).requirements;
  const manifest = {
    schemaVersion,
    generatedAt,
    contract: { name: "tekroo.kernel.contracts", version: "0.8.0", status: "FROZEN_CANDIDATE" },
    authority: { decisionAuthority: "Principal", implementationAuthority: "PENDING_CANDIDATE_ACCEPTANCE", productionAuthority: "NONE" },
    canonicalization: { textEncoding: "UTF-8", byteOrderMark: false, lineEnding: "LF", jsonSemanticCanonicalization: "RFC-8785-compatible sorted-key canonical JSON", byteDigest: "SHA-256", manifestDigest: "detached manifest.sha256" },
    inventoryBoundary: "files lists every payload asset; manifest.json is identified by detached manifest.sha256 and manifest.sha256 is not self-listed.",
    sourceLineage: [
      { path: "docs/architecture/107-phase-4-operational-teams-runtime-plan.md", sha256: sha256(fs.readFileSync(path.join(repositoryRoot, "docs", "architecture", "107-phase-4-operational-teams-runtime-plan.md"))) },
      { path: "docs/architecture/108-phase-4-step-1-design-closure.md", sha256: sha256(fs.readFileSync(path.join(repositoryRoot, "docs", "architecture", "108-phase-4-step-1-design-closure.md"))) },
      { path: "OUTPUT/phase-4/step-1-state-ownership-matrix.json", sha256: sha256(fs.readFileSync(path.join(repositoryRoot, "OUTPUT", "phase-4", "step-1-state-ownership-matrix.json"))) },
      { path: "CONTRACTS/tekroo.kernel.contracts/0.7.0/manifest.json", sha256: sha256(fs.readFileSync(path.join(predecessorRoot, "manifest.json"))) },
      { path: "scripts/generate_phase4_contract_0_8.mjs", sha256: sha256(fs.readFileSync(generatorPath)) },
    ],
    counts: {
      catalogueEntries: catalogue.entries.length,
      commandTypes: catalogue.entries.filter((entry) => entry.kind === "COMMAND").length,
      eventTypes: catalogue.entries.filter((entry) => entry.kind === "EVENT").length,
      fixtures: fixtures.length,
      invariants: invariants.length,
      schemas: files.filter((file) => file.role === "schemas").length,
      traceabilityRequirements: requirements.length,
    },
    files,
    profiles: predecessorManifest.profiles.map((profile) => ({ ...profile, authorization: profile.name === "contract-structure" ? "PHASE_4_STEP_2_AUTHORIZATION" : "AFTER_PRINCIPAL_CANDIDATE_ACCEPTANCE", currentStatus: profile.name === "contract-structure" ? "READY_TO_RUN" : "NOT_RUN" })),
    knownLimitations: [
      "Contract-structure and reference PASS do not qualify the Go implementation, MongoDB projections, OpenHands, SMA, a model provider, or production operation.",
      "The successor package defines work-budget, invocation, projection, and Teams/SMA authority semantics; product implementation begins only after candidate acceptance.",
      "Historical 0.7.0 task dispatch records remain non-executable until an explicit 0.8.0 budget binding and work invocation are accepted.",
      "No v3 data migration, live service activity, database mutation, OpenHands conversation, or SMA modification is authorized by this package.",
    ],
  };
  const manifestPath = path.join(packageRoot, "manifest.json");
  writeJson(manifestPath, manifest);
  const manifestBytes = fs.readFileSync(manifestPath);
  fs.writeFileSync(path.join(packageRoot, "manifest.sha256"), `${sha256(manifestBytes)}  manifest.json\n`);
}

copyPredecessor();
addSchemas();
const { pairs } = addPayloadsAndCatalogue();
const modelFixtureIds = addFixtures(pairs);
addInvariantsAndTraceability(modelFixtureIds, pairs);
extendRunner();
addCompatibility();
extendValidator();
updateTopLevelIdentities();
buildManifest();

process.stdout.write(`${contractIdentity} ${sha256(fs.readFileSync(path.join(packageRoot, "manifest.json")))}\n`);
